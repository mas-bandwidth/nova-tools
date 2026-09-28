//go:build unix

package update

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// The red tests nova-tools #2288 demands (docs/SPEC-VERSION.md, "nova-version
// moved and nova-update apply --sha", rules 2, 3 and 12; the demands list's
// items 1, 5, 6 and 7). `git`, `go` and every built binary are fakes on PATH,
// the clock is injected, and nothing here reaches a network: the fakes stand
// where the spec says a bench, a network or a clock stands.

// movedBench is the fake bench `moved` runs against. fx holds the fixtures the
// fakes read: revs/<rev>/tools (the cmd/* directory names one revision builds),
// revs/<rev>/<tool>.help (what that revision's build answers to `<tool> help`),
// revs/<rev>/<tool>.slow (make that build's help sleep past any bound), and the
// optional msg.txt (the commit messages between the revisions) and MOVED.txt
// (the MOVED file at --to).
type movedBench struct {
	fx, fakeDir, repo string
}

// movedFakeGit answers the six subcommands `moved` runs and nothing else; any
// other subcommand -- a `git fetch`, say -- is recorded in git-unexpected so a
// test can prove the verb never reached for the network itself.
const movedFakeGit = `FX='@FX@'
if [ "$1" = "-C" ]; then shift 2; fi
cmd="$1"; shift
case "$cmd" in
rev-parse)
	rev=${2%%\^*}
	if [ -f "$FX/revs/$rev/tools" ]; then
		echo "$rev"
	else
		echo "fatal: not a commit" >&2
		exit 1
	fi
	;;
log)
	if [ -f "$FX/msg.txt" ]; then cat "$FX/msg.txt"; fi
	;;
show)
	if [ -f "$FX/MOVED.txt" ]; then cat "$FX/MOVED.txt"; else exit 1; fi
	;;
ls-tree)
	eval "rev=\${$#}"
	rev=${rev%:cmd}
	cat "$FX/revs/$rev/tools"
	;;
worktree)
	case "$1" in
	add)
		dir="$3"; rev="$4"
		mkdir -p "$dir/cmd"
		while IFS= read -r tool; do
			[ -n "$tool" ] && mkdir -p "$dir/cmd/$tool"
		done < "$FX/revs/$rev/tools"
		;;
	remove)
		rm -rf "$3"
		;;
	esac
	;;
*)
	echo "git subcommand $cmd" > "$FX/git-unexpected"
	echo "fatal: unsupported" >&2
	exit 1
	;;
esac`

// movedFakeGo writes each built binary as a script that answers `help` with the
// fixture for that revision and tool, and marks go-ran so a test can prove a
// refusal started no build. The revision is read off the -o directory the verb
// names for the build, which the production code names after the revision.
const movedFakeGo = `FX='@FX@'
: > "$FX/go-ran"
wt=""
out=""
shift
while [ $# -gt 0 ]; do
	case "$1" in
	-C) wt="$2"; shift 2 ;;
	-o) out="$2"; shift 2 ;;
	*) shift ;;
	esac
done
rev=$(basename "$out")
mkdir -p "$out"
for d in "$wt"/cmd/*/; do
	[ -d "$d" ] || continue
	tool=$(basename "$d")
	bin="$out/$tool"
	{
		echo '#!/bin/sh'
		echo "if [ -f \"$FX/revs/$rev/$tool.slow\" ]; then sleep 5; fi"
		echo "cat \"$FX/revs/$rev/$tool.help\""
	} > "$bin"
	chmod 755 "$bin"
done`

func movedBenchSetup(t *testing.T) *movedBench {
	t.Helper()
	b := &movedBench{fx: t.TempDir(), fakeDir: t.TempDir(), repo: filepath.Join(t.TempDir(), "repo")}
	if err := os.MkdirAll(b.repo, 0o755); err != nil {
		t.Fatal(err)
	}
	specScript(t, b.fakeDir, "git", strings.ReplaceAll(movedFakeGit, "@FX@", b.fx))
	specScript(t, b.fakeDir, "go", strings.ReplaceAll(movedFakeGo, "@FX@", b.fx))
	t.Setenv("PATH", b.fakeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return b
}

// rev writes one revision's build: the cmd/* names it holds and what each one
// answers to `help`.
func (b *movedBench) rev(t *testing.T, rev string, tools map[string]string) {
	t.Helper()
	dir := filepath.Join(b.fx, "revs", rev)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	var list strings.Builder
	for _, name := range names {
		if strings.ContainsAny(name, "/\n") {
			t.Fatalf("fixture tool name %q is not one cmd/* directory", name)
		}
		fmt.Fprintln(&list, name)
		if err := os.WriteFile(filepath.Join(dir, name+".help"), []byte(tools[name]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "tools"), []byte(list.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// message states the commit messages git log answers for the range.
func (b *movedBench) message(t *testing.T, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(b.fx, "msg.txt"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(filepath.Join(b.fx, "msg.txt")) })
}

// movedFile states what the MOVED file at --to answers.
func (b *movedBench) movedFile(t *testing.T, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(b.fx, "MOVED.txt"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(filepath.Join(b.fx, "MOVED.txt")) })
}

func (b *movedBench) run(t *testing.T, env Environment, args ...string) (int, string, string) {
	t.Helper()
	var out, errs strings.Builder
	c := Run("nova-version", args, "test", &out, &errs, env)
	return c, out.String(), errs.String()
}

func (b *movedBench) goRan() bool {
	_, err := os.Stat(filepath.Join(b.fx, "go-ran"))
	return err == nil
}

func (b *movedBench) resetGoRan() {
	os.Remove(filepath.Join(b.fx, "go-ran"))
}

func (b *movedBench) gitUnexpected() bool {
	_, err := os.Stat(filepath.Join(b.fx, "git-unexpected"))
	return err == nil
}

// TestIssue2288 is the anchor: `nova-version moved` exists, reads each
// revision's own build, and never a hand-written list. One tool gains --decide
// between the two revisions and another is replaced by a differently named one,
// and every word the note announces was parsed off a help a built binary
// printed -- the flag a hand list would name is announced nowhere.
func TestIssue2288(t *testing.T) {
	b := movedBenchSetup(t)
	b.rev(t, "aaaa1", map[string]string{
		"nova-secrets": "nova-secrets seat --file <path> [--max <n>]\nnova-secrets version (or --version)\nnova-secrets help\n",
		"nova-old":     "nova-old fetch --file <path>\nnova-old version (or --version)\nnova-old help\n",
	})
	b.rev(t, "bbbb2", map[string]string{
		"nova-secrets": "nova-secrets seat --file <path> [--max <n>] [--decide <name>]\nnova-secrets version (or --version)\nnova-secrets help\n",
		"nova-new":     "nova-new fetch --file <path>\nnova-new version (or --version)\nnova-new help\n",
	})
	out := filepath.Join(t.TempDir(), "moved.txt")
	env := Environment{Now: func() time.Time { return time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC) }}
	code, stdout, stderr := b.run(t, env, "moved", "--from", "aaaa1", "--to", "bbbb2", "--repo", b.repo, "--out", out)
	if code != 0 {
		t.Fatalf("moved refused a healthy bench: exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	need(t, stdout, "MOVED OK", "from=aaaa1", "to=bbbb2", "added=1", "deleted=1", "renamed=0", "verbs=6", "file="+field(out))
	note := string(readFileOrFail(t, out))
	// READING THE BUILD: --decide is announced because the --to build's own
	// help prints it, and for no other reason.
	need(t, note, "added=--decide tool=nova-secrets verb=seat")
	// NEVER A HAND-WRITTEN LIST: --adopt is the kind of flag the ADOPT
	// EVERYTHING note named off a list (#1141); neither revision's help prints
	// it, so it is announced nowhere. This is the mutation that matters.
	if strings.Contains(stdout+note, "--adopt") {
		t.Errorf("a flag no help printed was announced:\nstdout:\n%s\nnote:\n%s", stdout, note)
	}
	// A vanished tool and a differently named appeared tool, no rename stated:
	// deleted and added, never a rename inferred from help text.
	need(t, note, "added=nova-new", "deleted=nova-old")
	if strings.Contains(note, "renamed=") {
		t.Errorf("a rename was inferred from help text alone:\n%s", note)
	}
	// The verb is dispatched and the help banner names it.
	var banner bytes.Buffer
	help("nova-version", &banner)
	if !strings.Contains(banner.String(), "nova-version moved --from <sha> --to <sha> --repo <dir> --out <path>") {
		t.Errorf("nova-version help omits the moved verb:\n%s", banner.String())
	}
}

// 1. TestMovedReadsTheBuildNeverAList: the note announces the flags the
// builds' own helps print -- --decide appears at --to and is announced; --pin,
// the kind of flag a hand-written list would name, appears in neither help and
// is announced nowhere.
func TestMovedReadsTheBuildNeverAList(t *testing.T) {
	b := movedBenchSetup(t)
	b.rev(t, "aaaa1", map[string]string{
		"nova-secrets": "nova-secrets seat --file <path> [--max <n>]\nnova-secrets version (or --version)\nnova-secrets help\n",
	})
	b.rev(t, "bbbb2", map[string]string{
		"nova-secrets": "nova-secrets seat --file <path> [--max <n>] [--decide <name>]\nnova-secrets version (or --version)\nnova-secrets help\n",
	})
	out := filepath.Join(t.TempDir(), "moved.txt")
	code, stdout, stderr := b.run(t, Environment{}, "moved", "--from", "aaaa1", "--to", "bbbb2", "--repo", b.repo, "--out", out)
	if code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	need(t, stdout, "MOVED OK", "added=0", "deleted=0", "renamed=0")
	note := string(readFileOrFail(t, out))
	need(t, note, "added=--decide tool=nova-secrets verb=seat")
	if strings.Contains(note+stdout, "--pin") {
		t.Errorf("a flag neither help prints was announced:\nstdout:\n%s\nnote:\n%s", stdout, note)
	}
}

// 5. TestMovedNeverInfersARename: help text alone never makes a rename.
// nova-old is built only at --from and nova-new only at --to, with the same
// verbs and flags under each name; unstated, the pair is one deleted and one
// added; only a statement -- in a commit message or a MOVED file -- makes it a
// rename.
func TestMovedNeverInfersARename(t *testing.T) {
	b := movedBenchSetup(t)
	b.rev(t, "aaaa1", map[string]string{
		"nova-old": "nova-old fetch --file <path>\nnova-old version (or --version)\nnova-old help\n",
	})
	b.rev(t, "bbbb2", map[string]string{
		"nova-new": "nova-new fetch --file <path>\nnova-new version (or --version)\nnova-new help\n",
	})
	out := filepath.Join(t.TempDir(), "moved.txt")
	args := []string{"moved", "--from", "aaaa1", "--to", "bbbb2", "--repo", b.repo, "--out", out}
	t.Run("unstated is deleted and added, never a rename", func(t *testing.T) {
		code, stdout, stderr := b.run(t, Environment{}, args...)
		if code != 0 {
			t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		need(t, stdout, "added=1", "deleted=1", "renamed=0")
		note := string(readFileOrFail(t, out))
		need(t, note, "added=nova-new", "deleted=nova-old")
		if strings.Contains(note, "renamed=") {
			t.Errorf("a rename was inferred from help text alone:\n%s", note)
		}
	})
	t.Run("a rename stated in the commit message", func(t *testing.T) {
		b.message(t, "routine churn between the two revisions\n\nrenamed nova-old to nova-new\n")
		code, stdout, stderr := b.run(t, Environment{}, args...)
		if code != 0 {
			t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		need(t, stdout, "added=0", "deleted=0", "renamed=1")
		note := string(readFileOrFail(t, out))
		need(t, note, "renamed=nova-old->nova-new")
		if strings.Contains(note, "added=") || strings.Contains(note, "deleted=") {
			t.Errorf("a stated rename double-counted the pair:\n%s", note)
		}
	})
	t.Run("a rename stated in a MOVED file", func(t *testing.T) {
		b.movedFile(t, "renamed nova-old to nova-new\n")
		code, stdout, stderr := b.run(t, Environment{}, args...)
		if code != 0 {
			t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		need(t, stdout, "added=0", "deleted=0", "renamed=1")
		note := string(readFileOrFail(t, out))
		need(t, note, "renamed=nova-old->nova-new")
		if strings.Contains(note, "added=") || strings.Contains(note, "deleted=") {
			t.Errorf("a stated rename double-counted the pair:\n%s", note)
		}
	})
}

// 6. TestMovedEmptyDiffIsNotARefusal: an empty diff is a note with three
// zeros, exit 0 -- a refusal would send a reader to repair a build that moved
// nothing.
func TestMovedEmptyDiffIsNotARefusal(t *testing.T) {
	b := movedBenchSetup(t)
	seatHelp := "nova-bus seat --file <path>\nnova-bus version (or --version)\nnova-bus help\n"
	b.rev(t, "aaaa1", map[string]string{"nova-bus": seatHelp})
	b.rev(t, "bbbb2", map[string]string{"nova-bus": seatHelp})
	out := filepath.Join(t.TempDir(), "moved.txt")
	code, stdout, stderr := b.run(t, Environment{}, "moved", "--from", "aaaa1", "--to", "bbbb2", "--repo", b.repo, "--out", out)
	if code != 0 {
		t.Fatalf("an empty diff was refused: exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	need(t, stdout, "MOVED OK", "added=0", "deleted=0", "renamed=0")
	if strings.Contains(stdout+stderr, "REFUSED") {
		t.Errorf("an empty diff printed a refusal:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	note := string(readFileOrFail(t, out))
	need(t, note, "MOVED from=aaaa1 to=bbbb2")
	for _, absent := range []string{"added=", "deleted=", "renamed="} {
		if strings.Contains(note, absent) {
			t.Errorf("an empty diff announced an entry:\n%s", note)
		}
	}
}

// 7. TestMovedIsBoundedByTheClock: every child is capped, the clock comes from
// the injected seam, and the verb never reaches for the network itself -- a
// missing revision is refused with the git fetch that would bring it, and no
// build is started.
func TestMovedIsBoundedByTheClock(t *testing.T) {
	// SLEEPS: the first subtest uses the injected clock, but the second runs a child
	// whose help sleeps past --timeout and waits for the real deadline to bite; it
	// failed on the 2026-09-25 darwin shard of PR #4215. Skipped 2026-09-25 by Glenn's
	// rule ("unit tests must not have real sleeps or waits"): the deadline subtest
	// becomes a mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")
	b := movedBenchSetup(t)
	b.rev(t, "aaaa1", map[string]string{
		"nova-bus": "nova-bus seat --file <path>\nnova-bus version (or --version)\nnova-bus help\n",
	})
	b.rev(t, "bbbb2", map[string]string{
		"nova-bus": "nova-bus seat --file <path> [--decide <name>]\nnova-bus version (or --version)\nnova-bus help\n",
	})
	out := filepath.Join(t.TempDir(), "moved.txt")
	args := []string{"moved", "--from", "aaaa1", "--to", "bbbb2", "--repo", b.repo, "--out", out}

	t.Run("the clock comes from the injected seam", func(t *testing.T) {
		env := Environment{Now: func() time.Time { return time.Date(2026, 9, 23, 4, 5, 6, 0, time.UTC) }}
		code, stdout, stderr := b.run(t, env, args...)
		if code != 0 {
			t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		need(t, string(readFileOrFail(t, out)), "MOVED from=aaaa1 to=bbbb2 at=2026-09-23T04:05:06Z")
	})
	t.Run("a child past its deadline is refused and no note is written", func(t *testing.T) {
		// A revision pair whose only tool sleeps past --timeout on its help,
		// so the deadline the caller named is the one that bites.
		b.rev(t, "dddd4", map[string]string{
			"nova-slow": "nova-slow seat --file <path>\nnova-slow help\n",
		})
		b.rev(t, "eeee5", map[string]string{
			"nova-slow": "nova-slow seat --file <path>\nnova-slow help\n",
		})
		if err := os.WriteFile(filepath.Join(b.fx, "revs", "dddd4", "nova-slow.slow"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		slowOut := filepath.Join(t.TempDir(), "slow.txt")
		code, _, stderr := b.run(t, Environment{}, "moved", "--from", "dddd4", "--to", "eeee5", "--repo", b.repo, "--out", slowOut, "--timeout", "200ms")
		if code != 2 {
			t.Fatalf("exit %d, want 2; stderr=%s", code, stderr)
		}
		need(t, stderr, "MOVED REFUSED", "nova-slow", "200ms")
		if _, err := os.Stat(slowOut); err == nil {
			t.Fatal("a refused run wrote its note")
		}
	})
	t.Run("a missing revision names the fetch and starts no build", func(t *testing.T) {
		b.resetGoRan()
		missOut := filepath.Join(t.TempDir(), "missing.txt")
		code, _, stderr := b.run(t, Environment{}, "moved", "--from", "cccc3", "--to", "bbbb2", "--repo", b.repo, "--out", missOut)
		if code != 2 {
			t.Fatalf("exit %d, want 2; stderr=%s", code, stderr)
		}
		need(t, stderr, "MOVED REFUSED", "cccc3", "git fetch")
		if _, err := os.Stat(missOut); err == nil {
			t.Fatal("a refused run wrote its note")
		}
		if b.goRan() {
			t.Error("an unresolved revision started a build")
		}
		if b.gitUnexpected() {
			t.Error("moved ran a git subcommand beyond its six")
		}
	})
}
