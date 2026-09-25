package pulse

// `fleet mirror` is scripts/bench-mirror.sh as a verb: the bare mirror a card clones from
// (`git clone --reference`), so a bench pulls only the delta from GitHub instead of a whole
// repository per card. It creates the mirror when it is missing and fetches it when it is
// there, and it deletes nothing -- a mirror verb that could remove a directory is one
// mistake away from removing the wrong one (Glenn 2026-09-17, "tools must be safe").
//
// Every path comes from a flag: the script hard-coded $HOME/nova-bench/mirror and three
// repository names, so a bench that kept its mirrors elsewhere could not be served by it.

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// FleetMirrorInput is everything `fleet mirror` needs apart from flag parsing.
type FleetMirrorInput struct {
	Benches  string // the fleet file
	Machines string // the machines registry; a machine whose roles lack `bench` is refused
	Name     string // the one bench
	SSH      string // the ssh program; empty is "ssh"
	Repo     string // the https remote to mirror
	Path     string // the absolute path of the bare mirror on the bench
	Timeout  time.Duration
	Runner   FleetRunner
	Stdout   io.Writer
	Stderr   io.Writer
}

// FleetMirror creates or refreshes one bare mirror on one bench and prints one line:
// `FLEET <bench> MIRROR <path> created|refreshed head=<sha> size=<n>K`. Exit 0 when the
// mirror is there, 2 when the invocation was refused, 3 when the bench could not be
// reached or git failed on it.
func FleetMirror(in FleetMirrorInput) int {
	bench, code := fleetOneBench(in.Benches, in.Machines, in.Name, in.Stdout, in.Stderr, "mirror")
	if code != 0 {
		return code
	}
	if err := checkMirrorRemote(in.Repo); err != nil {
		return refusal(in.Stderr, "FLEET", err)
	}
	if err := checkMirrorPath(in.Path); err != nil {
		return refusal(in.Stderr, "FLEET", err)
	}
	run := in.Runner
	if run == nil {
		run = SSHRunner{Program: in.SSH}
	}
	start := time.Now()
	fmt.Fprintf(in.Stderr, "MIRROR WALK bench=%s repo=%s\n", oneline.Field(bench.Name), oneline.Field(in.Repo))

	ctx, cancel := context.WithTimeout(context.Background(), fleetPowerTimeout(in.Timeout))
	defer cancel()
	out, err := run.Run(ctx, bench.SSH, fleetMirrorScript(bench.Home, in.Repo, in.Path))
	if what, ok := fleetMarker(out, "FLEETFAIL"); ok {
		fmt.Fprintf(in.Stdout, "FLEET %s MIRROR FAILED %s\n", oneline.Field(bench.Name), oneline.Escape(what))
		return 3
	}
	if err != nil {
		return fleetUnreachable(in.Stdout, bench.Name, fleetReason(out, err))
	}
	what, ok := fleetMarker(out, "FLEETMIRROR")
	if !ok {
		return fleetUnreachable(in.Stdout, bench.Name, "no answer")
	}
	fields := strings.Split(what, "\t")
	for len(fields) < 3 {
		fields = append(fields, "")
	}
	fmt.Fprintf(in.Stdout, "FLEET %s MIRROR %s %s head=%s size=%sK\n",
		oneline.Field(bench.Name), oneline.Field(in.Path),
		oneline.Field(fields[0]), oneline.Field(fields[1]), oneline.Field(fields[2]))
	fmt.Fprintf(in.Stderr, "MIRROR DONE bench=%s elapsed=%s\n",
		oneline.Field(bench.Name), time.Since(start).Round(time.Millisecond))
	return 0
}

// checkMirrorRemote holds --repo to an https remote with no shell metacharacter in it: the
// value is pasted into a remote command line, and a guessed one is a bench cloning
// something nobody named.
func checkMirrorRemote(repo string) error {
	r := strings.TrimSpace(repo)
	if r == "" {
		return fmt.Errorf("missing --repo; refusing to guess (run: nova-pulse fleet mirror --repo https://github.com/<owner>/<name>.git)")
	}
	if !strings.HasPrefix(r, "https://") {
		return fmt.Errorf("--repo %s is not an https remote; a mirror is cloned unauthenticated (run: nova-pulse fleet mirror --repo https://github.com/<owner>/<name>.git)", oneline.Field(r))
	}
	if strings.ContainsAny(r, " \t'\"`$\\;&|<>()\n") {
		return fmt.Errorf("--repo %s holds a character a remote command line must not carry; name the plain https url", oneline.Field(r))
	}
	return nil
}

// checkMirrorPath holds --path to an absolute remote path with no "..", so the verb names
// the directory it will write and nothing above it.
func checkMirrorPath(p string) error {
	t := strings.TrimSpace(p)
	if t == "" {
		return fmt.Errorf("missing --path; refusing to guess (run: nova-pulse fleet mirror --path /home/<user>/nova-bench/mirror/<name>.git)")
	}
	if !strings.HasPrefix(t, "/") {
		return fmt.Errorf("--path %s is not absolute; the mirror's path on the bench is named whole (run: nova-pulse fleet mirror --path /home/<user>/nova-bench/mirror/<name>.git)", oneline.Field(t))
	}
	if t != path.Clean(t) || strings.Contains(t, "..") {
		return fmt.Errorf("--path %s is not a clean absolute path; name it without \"..\" or a trailing slash", oneline.Field(t))
	}
	if strings.ContainsAny(t, " \t'\"`$\\;&|<>()\n") {
		return fmt.Errorf("--path %s holds a character a remote command line must not carry", oneline.Field(t))
	}
	return nil
}

// fleetMirrorScript is the remote side: fetch what is there, clone what is not, and print
// one tab-separated marker. It never removes anything.
func fleetMirrorScript(home, repo, mirror string) string {
	return strings.Join([]string{
		"HOME=" + fleetQuote(home),
		"export HOME",
		"GIT_TERMINAL_PROMPT=0",
		"export GIT_TERMINAL_PROMPT",
		"p=" + fleetQuote(mirror),
		"what=refreshed",
		`if [ -d "$p" ]; then`,
		`  git -C "$p" fetch -q --prune origin '+refs/heads/*:refs/heads/*' || { printf 'FLEETFAIL\tfetch failed\n'; exit 0; }`,
		`else`,
		`  mkdir -p "$(dirname "$p")" || { printf 'FLEETFAIL\tcannot make the mirror directory\n'; exit 0; }`,
		`  git clone -q --mirror ` + fleetQuote(repo) + ` "$p" || { printf 'FLEETFAIL\tclone failed\n'; exit 0; }`,
		`  what=created`,
		`fi`,
		`head=$(git -C "$p" rev-parse --short refs/heads/dev 2>/dev/null || git -C "$p" rev-parse --short refs/heads/main 2>/dev/null || echo -)`,
		`size=$(du -sk "$p" 2>/dev/null | awk '{print $1}')`,
		`printf 'FLEETMIRROR\t%s\t%s\t%s\n' "$what" "$head" "${size:-0}"`,
	}, "\n")
}
