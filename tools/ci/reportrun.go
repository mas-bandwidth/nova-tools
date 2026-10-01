package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	register(verb{
		name:    "report-run",
		summary: "report this CI run to Redis from the runner, under the bench seat's secret",
		help: `usage: go run ./tools/ci report-run --repo R --sha S --run-id ID --pr N --workflow W --conclusion C

The runner is the event source: the run reports itself at its end, as one
ev:github row of the workflow_run shape, written by this tree's
` + "`go run ./cmd/nova-ci github receipt --from-runner`" + `, so the writer is versioned with
the commit under test and no runner's installed build matters. It runs under
nova-secrets exec as the bench seat: the password never touches a flag or a
file, and this verb never prints a secret value.

The bench's environment file $HOME/nova-bench/launch/card.env (KEY=VALUE lines, an
optional "export ", optional quotes, # comments) must name NOVA_BENCH_SEAT,
NOVA_BENCH_SOPS and NOVA_CARD_REDIS. A runner with no card.env, or one that
lacks a name, is refused and the verb exits 1: a receipt that silently did not
happen must never read as one that did.

The flags are the run's own context (--pr may be empty outside a pull request).
The exit code is the writer's.

exit 0  the receipt was written
exit 1  the bench file is absent or incomplete, or the writer failed
exit 2  usage
`,
		do: func(e env, args []string) int { return reportRun(e, osCmdRunner{}, args) },
	})
}

// receiptFlags are the run's own context the receipt writer takes, in the
// order it is given them.
var receiptFlags = []string{"repo", "sha", "run-id", "pr", "workflow", "conclusion"}

// reportRun is the verb over a runner.
func reportRun(e env, r cmdRunner, args []string) int {
	ctx := map[string]string{}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		name := strings.TrimPrefix(args[i], "--")
		known := false
		for _, f := range receiptFlags {
			known = known || f == name
		}
		if !strings.HasPrefix(args[i], "--") || !known || i+1 >= len(args) {
			fmt.Fprintf(e.stderr, "report-run: bad argument %q; usage: go run ./tools/ci help report-run\n", args[i])
			return 2
		}
		ctx[name] = args[i+1]
		seen[name] = true
		i++
	}
	for _, f := range receiptFlags {
		if !seen[f] {
			fmt.Fprintf(e.stderr, "report-run: --%s is required; usage: go run ./tools/ci help report-run\n", f)
			return 2
		}
	}

	home := e.getenv("HOME")
	if home == "" {
		fmt.Fprintln(e.stderr, "report-run: HOME is not set")
		return 1
	}
	envFile := filepath.Join(home, "nova-bench", "launch", "card.env")
	vals, err := readCardEnv(envFile, e.getenv)
	if err != nil {
		host, _ := os.Hostname()
		if os.IsNotExist(err) {
			fmt.Fprintf(e.stderr, "report-run: no %s on this runner: run the bench play on %s (it writes the bench seat and store address)\n", envFile, host)
		} else {
			fmt.Fprintf(e.stderr, "report-run: %v\n", err)
		}
		return 1
	}
	for _, k := range []string{"NOVA_BENCH_SEAT", "NOVA_BENCH_SOPS", "NOVA_CARD_REDIS"} {
		if vals[k] == "" {
			fmt.Fprintf(e.stderr, "report-run: card.env names no %s\n", k)
			return 1
		}
	}
	seat := vals["NOVA_BENCH_SEAT"]

	argv := []string{
		"exec", "--store", filepath.Join(home, "nova-bench", "secrets"), "--as", seat,
		"--key", filepath.Join(home, ".config", "nova-secrets", seat+".key"), "--sops", vals["NOVA_BENCH_SOPS"],
		"--only", "NOVA_REDIS_BENCH_PASSWORD", "--require", "NOVA_REDIS_BENCH_PASSWORD", "--",
		"/usr/bin/env", "NOVA_SPRINT_REDIS_USER=bench", "NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_BENCH_PASSWORD",
		"go", "run", "./cmd/nova-ci", "github", "receipt", "--from-runner", "--redis", vals["NOVA_CARD_REDIS"],
	}
	for _, f := range receiptFlags {
		argv = append(argv, "--"+f, ctx[f])
	}
	code, err := r.Run(cmdSpec{Name: filepath.Join(home, ".local", "bin", "nova-secrets"), Args: argv, Dir: e.dir, Stdout: e.stdout, Stderr: e.stderr})
	if err != nil {
		fmt.Fprintf(e.stderr, "report-run: nova-secrets: %v\n", err)
		return 127
	}
	return code
}

// readCardEnv reads a bench environment file: KEY=VALUE lines, blank lines and
// # comments skipped, an optional leading "export ", a value that may be
// wrapped in single quotes (taken literally) or double quotes or bare ($VAR and
// ${VAR} expand from getenv).
func readCardEnv(path string, getenv func(string) string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch {
		case len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'':
			v = v[1 : len(v)-1]
		case len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"':
			v = os.Expand(v[1:len(v)-1], getenv)
		default:
			v = os.Expand(v, getenv)
		}
		out[k] = v
	}
	return out, sc.Err()
}
