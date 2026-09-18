package pulse

// The install verb (nova-tools #1142): build nova-tools once on a build bench,
// cache the binaries by the commit's full sha, install the same cache on every
// named Linux bench by an atomic per-binary rename, and verify each bench's
// nova-swarm reports that commit. It replaces the coordinator's
// scripts/coordination/fleet-install-tools.sh, whose failed cp/mv a review
// (Stella, #1263 F13) found it ignored: here every copy, install and verify is
// checked, and a failure names the bench, the step and the reason.
//
// Every remote command is one script handed to a runner that runs
// `ssh <target> bash -s` with the script on stdin, so nothing is ever
// interpolated into a command line and a test drives a fake runner that records
// what was sent and answers canned output. Ageing cached builds are removed only
// through internal/safepath, below the one build root.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// installCacheKeep is how many cached builds survive a prune: the newest five,
// the way the coordinator's shell replacement kept them.
const installCacheKeep = 5

// installBuildRoot is the directory, under a bench's home, where builds are
// cached by full sha. It is a literal, and the only root a cached build is
// removed under.
const installBuildRoot = "nova-bench/build"

// InstallRunner runs one script on one bench. The real runner is
// `ssh <target> bash -s` with the script on stdin; a test supplies a fake that
// records the call and answers canned output.
type InstallRunner interface {
	Run(ctx context.Context, target, script string) (string, error)
}

// SSHInstallRunner is the real transport: one ssh child per call, bounded by a
// timeout, with the script on the child's stdin and never on its command line.
type SSHInstallRunner struct {
	Program string
	Timeout time.Duration
}

// Run runs one remote script on target over ssh. The ssh program is the verb's
// --ssh; an empty one is `ssh` on PATH.
func (r SSHInstallRunner) Run(ctx context.Context, target, script string) (string, error) {
	program := r.Program
	if program == "" {
		program = "ssh"
	}
	bound := r.Timeout
	if bound <= 0 {
		bound = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", target, "bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	raw, err := cmd.CombinedOutput()
	return string(raw), err
}

// InstallInput is everything the verb needs apart from flag parsing.
type InstallInput struct {
	SHA        string
	BuildBench string
	Benches    []string
	Home       string // the local home; the build cache is under <Home>/nova-bench/build
	Timeout    time.Duration
	Runner     InstallRunner
	Stdout     io.Writer
	Stderr     io.Writer
}

// ValidInstallSHA reports whether s is the 7 to 40 hex commit the verb accepts.
func ValidInstallSHA(s string) bool {
	if len(s) < 7 || len(s) > 40 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// ValidInstallBench reports whether s is one safe bench name: letters, digits,
// dot, underscore and dash. Every name is checked before the first ssh.
func ValidInstallBench(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// Install builds once, caches by commit, installs on every named bench and
// verifies each. It returns 0 when every bench is at the sha, 1 when any bench
// failed, and 2 on an input it cannot act on.
func Install(in InstallInput) int {
	if in.Stdout == nil {
		in.Stdout = io.Discard
	}
	if in.Stderr == nil {
		in.Stderr = io.Discard
	}
	if in.Home == "" {
		in.Home, _ = os.UserHomeDir()
	}
	if in.Runner == nil {
		fmt.Fprintln(in.Stderr, "nova-pulse install: no runner wired; refusing to guess")
		return 2
	}
	// Defence in depth: the command layer has already refused these before a
	// runner existed, but Install is exported and may be driven directly.
	if !ValidInstallSHA(in.SHA) {
		fmt.Fprintf(in.Stderr, "nova-pulse install: --sha wants 7 to 40 hex characters, got %q\n", in.SHA)
		return 2
	}
	if !ValidInstallBench(in.BuildBench) {
		fmt.Fprintf(in.Stderr, "nova-pulse install: --build-bench wants letters, digits, dot, underscore and dash, got %q\n", in.BuildBench)
		return 2
	}
	for _, b := range in.Benches {
		if !ValidInstallBench(b) {
			fmt.Fprintf(in.Stderr, "nova-pulse install: --benches wants letters, digits, dot, underscore and dash, got %q\n", b)
			return 2
		}
	}

	full, tools, cached, err := installBuild(in)
	if err != nil {
		fmt.Fprintf(in.Stdout, "INSTALL FAIL build bench=%s %s\n",
			oneline.Field(in.BuildBench), oneline.Escape(err.Error()))
		fmt.Fprintf(in.Stdout, "INSTALL DONE benches=%d ok=0 skipped=0 failed=%d\n", len(in.Benches), len(in.Benches))
		return 1
	}
	fmt.Fprintf(in.Stdout, "INSTALL BUILD bench=%s sha=%s tools=%d cached=%s\n",
		oneline.Field(in.BuildBench), full, tools, cached)

	// The cached builds beyond the newest five go, through safepath only.
	pruneInstallCaches(filepath.Join(in.Home, installBuildRoot), installCacheKeep)

	ok, skipped, failed := 0, 0, 0
	for _, bench := range in.Benches {
		r := installOn(in, bench, full)
		switch r.kind {
		case "ok":
			ok++
			fmt.Fprintf(in.Stdout, "INSTALL OK bench=%s tools=%d sha=%s\n", oneline.Field(bench), r.tools, shortInstallSHA(r.sha))
		case "skip":
			skipped++
			fmt.Fprintf(in.Stdout, "INSTALL SKIP bench=%s already at %s\n", oneline.Field(bench), shortInstallSHA(r.sha))
		default:
			failed++
			fmt.Fprintf(in.Stdout, "INSTALL FAIL bench=%s step=%s %s\n",
				oneline.Field(bench), oneline.Field(r.step), oneline.Escape(r.reason))
		}
	}
	fmt.Fprintf(in.Stdout, "INSTALL DONE benches=%d ok=%d skipped=%d failed=%d\n",
		len(in.Benches), ok, skipped, failed)
	if failed > 0 {
		return 1
	}
	return 0
}

// installBuild runs the build script on the build bench and folds its one
// marker into the full sha, the tool count and whether the cache already held
// it.
func installBuild(in InstallInput) (full string, tools int, cached string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout(in.Timeout))
	defer cancel()
	out, runErr := in.Runner.Run(ctx, in.BuildBench, installBuildScript(in.SHA))
	if full, tools, cached, ok := parseInstallBuild(out); ok {
		return full, tools, cached, nil
	}
	return "", 0, "", fmt.Errorf("%s", installReason(out, runErr))
}

// installBenchResult is one bench's answer.
type installBenchResult struct {
	kind   string // ok, skip, fail
	tools  int
	sha    string
	step   string
	reason string
}

// installOn runs one bench's script and folds its one marker.
func installOn(in InstallInput, bench, full string) installBenchResult {
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout(in.Timeout))
	defer cancel()
	out, runErr := in.Runner.Run(ctx, bench, installBenchScript(in.BuildBench, bench, full))
	if r, ok := parseInstallBench(out); ok {
		return r
	}
	return installBenchResult{kind: "fail", step: "install", reason: installReason(out, runErr)}
}

// installTimeout bounds one runner call: the input's, or fifteen minutes.
func installTimeout(d time.Duration) time.Duration {
	if d <= 0 {
		return 15 * time.Minute
	}
	return d
}

// shortInstallSHA is the commit as the per-bench lines spell it: the first twelve.
func shortInstallSHA(full string) string {
	if len(full) > 12 {
		return full[:12]
	}
	return full
}

// installReason is the last non-empty line of a failed runner call, or its error.
func installReason(out string, err error) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	if err != nil {
		return err.Error()
	}
	return "no answer"
}

// parseInstallBuild reads the build script's marker:
// `INSTALLBUILT<TAB><full><TAB><tools><TAB><yes|no>`.
func parseInstallBuild(out string) (full string, tools int, cached string, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "INSTALLBUILT\t") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 4 {
			continue
		}
		n, err := strconv.Atoi(f[2])
		if err != nil {
			continue
		}
		return f[1], n, f[3], true
	}
	return "", 0, "", false
}

// parseInstallBench reads one per-bench marker: INSTALLED, INSTALLSKIP or
// INSTALLFAIL.
func parseInstallBench(out string) (installBenchResult, bool) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "INSTALLSKIP\t"):
			return installBenchResult{kind: "skip", sha: strings.TrimPrefix(line, "INSTALLSKIP\t")}, true
		case strings.HasPrefix(line, "INSTALLED\t"):
			r := installBenchResult{kind: "ok"}
			for _, tok := range strings.Fields(strings.TrimPrefix(line, "INSTALLED\t")) {
				if v, cut := strings.CutPrefix(tok, "tools="); cut {
					r.tools, _ = strconv.Atoi(v)
				}
				if v, cut := strings.CutPrefix(tok, "sha="); cut {
					r.sha = v
				}
			}
			return r, true
		case strings.HasPrefix(line, "INSTALLFAIL\t"):
			f := strings.SplitN(line, "\t", 3)
			if len(f) == 3 {
				return installBenchResult{kind: "fail", step: f[1], reason: f[2]}, true
			}
		}
	}
	return installBenchResult{}, false
}

// pruneInstallCaches removes every cached build under root beyond the newest
// keep, and only through safepath: a name that is not one safe path element is
// never touched, and neither is anything that resolves outside root.
func pruneInstallCaches(root string, keep int) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	type cached struct {
		name string
		mod  time.Time
	}
	var dirs []cached
	for _, e := range entries {
		if !e.IsDir() || !safepath.NameOK(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		dirs = append(dirs, cached{name: e.Name(), mod: info.ModTime()})
	}
	if len(dirs) <= keep {
		return
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].mod.After(dirs[j].mod) })
	for _, d := range dirs[keep:] {
		_ = safepath.RemoveUnder(root, filepath.Join(root, d.name))
	}
}

// installBuildScript is the remote script that clones, fetches, checks out and
// builds every tool into the sha's cache, printing one INSTALLBUILT marker. A
// cache hit prints the same marker with cached=yes and builds nothing.
func installBuildScript(sha string) string {
	return strings.Join([]string{
		"set -eu",
		"export PATH=$HOME/go/bin:/usr/local/go/bin:$HOME/.local/bin:$PATH",
		"SRC=$HOME/nova-bench/src/nova-tools",
		"M=$HOME/nova-bench/mirror/nova-tools.git",
		`[ -d "$SRC/.git" ] || git clone -q $([ -d "$M" ] && echo "--reference $M") https://github.com/mas-bandwidth/nova-tools.git "$SRC"`,
		`cd "$SRC"`,
		"git fetch -q origin dev",
		"SHA=" + fleetQuote(sha),
		`full=$(git rev-parse --verify -q "$SHA^{commit}") || { printf 'INSTALLBUILDFAIL\t%s\n' "unknown sha $SHA"; exit 0; }`,
		`OUT=$HOME/nova-bench/build/$full`,
		`if [ -f "$OUT/.complete" ]; then`,
		`  n=$(cat "$OUT/.complete")`,
		`  printf 'INSTALLBUILT\t%s\t%s\tyes\n' "$full" "$n"`,
		`  exit 0`,
		`fi`,
		`rm -rf -- "$HOME/nova-bench/build/.partial-$full"`,
		`mkdir -p "$HOME/nova-bench/build/.partial-$full"`,
		`n=0`,
		`for d in cmd/*/; do`,
		`  t=$(basename "$d")`,
		"  CGO_ENABLED=0 go build -trimpath -o \"$HOME/nova-bench/build/.partial-$full/$t\" \"./cmd/$t\" || { printf 'INSTALLBUILDFAIL\\t%s\\n' \"build $t failed\"; exit 0; }",
		`  n=$((n+1))`,
		`done`,
		`"$HOME/nova-bench/build/.partial-$full/nova-swarm" version | grep -q "${full:0:12}" || { printf 'INSTALLBUILDFAIL\t%s\n' "the built nova-swarm does not carry the sha"; exit 0; }`,
		`echo "$n" > "$HOME/nova-bench/build/.partial-$full/.complete"`,
		`rm -rf -- "$OUT"`,
		`mv "$HOME/nova-bench/build/.partial-$full" "$OUT"`,
		`printf 'INSTALLBUILT\t%s\t%s\tno\n' "$full" "$n"`,
	}, "\n")
}

// installBenchScript is the remote script that installs one bench: it skips a
// bench already at the sha, copies the build bench's cache over (unless this is
// the build bench), installs every binary by an atomic dotfile-then-mv rename,
// and verifies nova-swarm carries the sha. Every failure prints an INSTALLFAIL
// marker naming the step and the binary or destination that failed.
func installBenchScript(buildBench, bench, full string) string {
	return strings.Join([]string{
		"set -u",
		"BUILD=" + fleetQuote(buildBench),
		"BENCH=" + fleetQuote(bench),
		"FULL=" + fleetQuote(full),
		"SHA12=${FULL:0:12}",
		"DEST=$HOME/nova-bench/build/$FULL",
		`if [ -x "$HOME/.local/bin/nova-swarm" ] && "$HOME/.local/bin/nova-swarm" version 2>/dev/null | grep -q "$SHA12"; then`,
		`  printf 'INSTALLSKIP\t%s\n' "$FULL"`,
		`  exit 0`,
		`fi`,
		`if [ "$BENCH" != "$BUILD" ]; then`,
		`  rm -rf -- "$DEST"`,
		`  mkdir -p "$DEST"`,
		`  if ! ssh -o BatchMode=yes -o ConnectTimeout=10 "$BUILD" "tar -C ~/nova-bench/build/$FULL -cf - ." | tar -C "$DEST" -xf -; then`,
		`    printf 'INSTALLFAIL\tcopy\t%s\n' "copying the cache from $BUILD failed"`,
		`    exit 0`,
		`  fi`,
		`fi`,
		`n=0`,
		`for f in "$DEST"/nova-*; do`,
		`  [ -e "$f" ] || continue`,
		`  t=$(basename "$f")`,
		`  for dir in "$HOME/.local/bin" "$HOME/go/bin"; do`,
		`    [ -d "$dir" ] || continue`,
		`    [ "$dir" = "$HOME/go/bin" ] && [ ! -e "$dir/$t" ] && continue`,
		`    if ! cp -p "$f" "$dir/.$t.new" || ! mv -f "$dir/.$t.new" "$dir/$t"; then`,
		`      printf 'INSTALLFAIL\tinstall\t%s\n' "$t -> $dir failed"`,
		`      exit 0`,
		`    fi`,
		`  done`,
		`  n=$((n+1))`,
		`done`,
		`got=$("$HOME/.local/bin/nova-swarm" version 2>/dev/null | head -n 1)`,
		`case "$got" in`,
		`  *"$SHA12"*) printf 'INSTALLED\ttools=%s\tsha=%s\n' "$n" "$FULL" ;;`,
		`  *) printf 'INSTALLFAIL\tverify\t%s\n' "nova-swarm reports: $got" ;;`,
		`esac`,
	}, "\n")
}
