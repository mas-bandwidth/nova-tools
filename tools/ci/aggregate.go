package main

import (
	"fmt"
	"strings"
)

func init() {
	register(verb{
		name:    "aggregate",
		summary: "state one verdict over the results of the jobs or steps a gate waits on",
		help: `usage: go run ./tools/ci aggregate [--event E] [--skip-ok NAMES] [--skip-ok-on EVENT=NAMES]
                                   [--what TEXT] NAME=RESULT...

The verdict step of an aggregate job (ci-ok, certification-ok) or of a job whose
steps all carry if: !cancelled() (the smoke gate). Each NAME=RESULT pair is one
job's result (needs.<job>.result) or one step's outcome (steps.<id>.outcome); it
prints "RESULT<TAB>NAME" for every pair, then a verdict.

A pair is good when its result is "success". A result of "skipped" is also good
for a name in --skip-ok (comma separated; "*" is every name) and, when --event
names the triggering event, for a name in the --skip-ok-on list of that event
(--skip-ok-on may repeat). Any other result, an empty one (a job that never
existed), and an unlisted skip are bad: the verdict fails closed.

With any bad pair it prints "N <TEXT>" (--what, default "job(s) did not succeed")
and exits 1. No pairs at all is refused: a verdict over nothing must not pass.

exit 0  every pair is good
exit 1  at least one pair is bad
exit 2  usage
`,
		do: aggregate,
	})
}

func aggregate(e env, args []string) int {
	event := ""
	what := "job(s) did not succeed"
	skipOK := map[string]bool{}
	skipOKOn := map[string]map[string]bool{}
	var pairs []string
	usage := func(format string, a ...any) int {
		fmt.Fprintf(e.stderr, "aggregate: "+format+"; run: go run ./tools/ci help aggregate\n", a...)
		return 2
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		need := func() (string, bool) {
			if i+1 >= len(args) {
				return "", false
			}
			i++
			return args[i], true
		}
		switch a {
		case "--event", "--skip-ok", "--skip-ok-on", "--what":
			v, ok := need()
			if !ok {
				return usage("%s needs a value", a)
			}
			switch a {
			case "--event":
				event = v
			case "--what":
				what = v
			case "--skip-ok":
				for _, n := range strings.Split(v, ",") {
					skipOK[strings.TrimSpace(n)] = true
				}
			case "--skip-ok-on":
				ev, names, ok := strings.Cut(v, "=")
				if !ok || ev == "" {
					return usage("--skip-ok-on wants EVENT=NAMES, got %q", v)
				}
				if skipOKOn[ev] == nil {
					skipOKOn[ev] = map[string]bool{}
				}
				for _, n := range strings.Split(names, ",") {
					skipOKOn[ev][strings.TrimSpace(n)] = true
				}
			}
		default:
			if strings.HasPrefix(a, "--") {
				return usage("unknown flag %s", a)
			}
			pairs = append(pairs, a)
		}
	}
	if len(pairs) == 0 {
		return usage("no NAME=RESULT pairs: a verdict over nothing must not pass")
	}
	bad := 0
	for _, p := range pairs {
		i := strings.LastIndex(p, "=")
		if i <= 0 {
			return usage("%q is not NAME=RESULT", p)
		}
		name, result := p[:i], p[i+1:]
		fmt.Fprintf(e.stdout, "%s\t%s\n", result, name)
		switch {
		case result == "success":
		case result == "skipped" && (skipOK["*"] || skipOK[name] || (event != "" && skipOKOn[event][name])):
		default:
			bad++
		}
	}
	if bad > 0 {
		fmt.Fprintf(e.stdout, "%d %s\n", bad, what)
		return 1
	}
	return 0
}
