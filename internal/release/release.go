// Package release is the last mile: a green commit on main becomes a version, a
// set of binaries, and the same binaries answering for themselves on every bench
// in the fleet.
//
// It exists because that mile was a shell script. `fleet-install-tools.sh` built
// the tools once on hulk, cached them by sha, tarred them to each bench and
// installed them by rename -- 30 lines of nested ssh quoting with a build, a
// cache, a copy, an install and a verify all in one `for` loop, and no test of
// any of it. It was written after the pit stop of 2026-09-17, where four Linux
// benches ran six-hour-old tools while the coordinator believed they were
// current, and the reason that could happen is that nothing in the loop could
// SAY what it had done: the install printed one line per bench and the line was
// composed from the same ssh it was reporting on.
//
// So the four steps are four verbs, each of which can refuse:
//
//	cut      main is green, here is the version, here is what changed
//	build    every cmd/nova-* for one platform, stamped, with a checksum file
//	install  verify the checksums, put them in place atomically, skip what is current
//	adopt    do the install on every machine in a file, one receipt line each
//
// The three edges to the world outside this process -- the forge, ssh, and the
// Go toolchain -- are interfaces, so that a test can watch every argument this
// tool would hand a subprocess and so that the production implementations are
// short enough to read line by line. No unit test here reaches the network or a
// real machine.
//
// NOTHING HERE TOUCHES A SECRET. gh carries its own credential, ssh carries
// its own key, and neither is read, logged or passed by this package.
package release

import (
	"context"
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// CheckRun is one CI check on a commit, as the forge reports it: the name a
// person reads in the checks list, the run's status, and its conclusion once it
// has one. A run that has not completed has an empty Conclusion, which is why
// both fields are here -- a pending run is not a green one, and reading only the
// conclusion would make it look like a neutral one.
type CheckRun struct {
	Name       string
	Status     string // queued, in_progress, completed
	Conclusion string // success, failure, cancelled, timed_out, skipped, neutral
}

// Commit is one commit in a range, with the whole message: the subject carries
// the pull request number a squash merge writes as `(#123)`, and the body of an
// integration batch carries the members it rolled up. Both are read from this
// one field, so the whole changelog is one call to the forge rather than one
// call per pull request.
type Commit struct {
	SHA     string
	Message string
}

// PR is one merged pull request as the changelog prints it. Members is the list
// of pull requests an integration batch rolled up, read from the batch's own
// body: a batch line that named only the batch would hide ten pieces of work
// behind one number.
type PR struct {
	Number  int
	Title   string
	Members []int
}

// Forge is the edge between this tool and GitHub. Seven questions, one gh
// invocation each -- Tag is two, for the reason it gives -- and every one of
// them is a read except Tag.
type Forge interface {
	// HeadSHA resolves a branch to the commit it points at.
	HeadSHA(ctx context.Context, repo, branch string) (string, error)
	// CheckRuns reads every check run recorded against one commit.
	CheckRuns(ctx context.Context, repo, sha string) ([]CheckRun, error)
	// Tags lists the repository's tag names, unordered; the caller picks.
	Tags(ctx context.Context, repo string) ([]string, error)
	// Compare lists the commits in base..head, oldest first.
	Compare(ctx context.Context, repo, base, head string) ([]Commit, error)
	// Files lists the paths base..head touched. It is a separate question
	// from Compare because it is asked for a separate reason -- the
	// classification `cut` makes against SensitivePaths -- and because its
	// answer carries a ceiling of its own (CompareFileCap) that the commit
	// list does not.
	Files(ctx context.Context, repo, base, head string) ([]string, error)
	// Tag creates an ANNOTATED tag at sha: a tag OBJECT carrying message,
	// then the ref pointing at that object. It is the one mutation on this
	// interface and the only one `cut` performs.
	Tag(ctx context.Context, repo, tag, sha, message string) error
	// TagMessage reads an existing annotated tag's message back. It is how
	// `adopt` learns the digest a release was cut with without anybody
	// retyping it: a tag object is a git object, so its message reached the
	// adopting host through the repository rather than through the machine
	// whose bits are being checked against it.
	TagMessage(ctx context.Context, repo, tag string) (string, error)
}

// SSH is the edge to another machine: run a command there, or put a directory
// there. Both take argv rather than a command line, because the thing this
// package replaces built its remote commands by pasting shell into shell and
// the quoting was the part nobody could read.
type SSH interface {
	// Run executes argv on machine and returns its combined output.
	Run(ctx context.Context, machine string, argv []string) (string, error)
	// Send copies the local directory tree at dir to dest on machine.
	Send(ctx context.Context, machine, dir, dest string) (string, error)
	// Fetch copies the directory dir ON machine into the local directory
	// dest. It is how the host that has the trust reads a release built on
	// the host that has the cores, without anybody copying it by hand.
	Fetch(ctx context.Context, machine, dir, dest string) (string, error)
}

// Machine is one line of the --machines file: which machine, and optionally
// where ITS tools go. The fleet has three different home directories, so a
// single --bin is right for most machines and wrong for one; the columns are
// how that one is said in the file rather than by a second run with different
// flags -- which is a second chance to get the version wrong.
type Machine struct {
	Name string
	// Bin and Dest override --bin and --dest for this machine. Empty means
	// the flag's value, which is the ordinary case.
	Bin, Dest string
}

// MachinesShape is the one sentence that says what the --machines file holds.
// The help prints it and docs/SPEC-UPDATE.md carries it, because a file format
// discoverable only from a refusal is a format nobody can write correctly the
// first time: the dogfood pass of 2026-09-18 had to read the source for it.
const MachinesShape = "one machine per line: <name>[TAB<bin>[TAB<dest>]]; blank lines and #-comments skipped; user@host allowed; the optional columns override --bin and --dest for that machine"

// RemotePathsNote says the one thing about --bin and --dest that is easy to get
// wrong and silent when you do. They are paths ON THE MACHINE: the remote shell
// expands a leading ~, so `--bin '~/.local/bin'` is how three different home
// directories are named at once -- and the quotes are load-bearing, because an
// unquoted ~ is expanded by the LOCAL shell into the adopting host's home,
// which is a path the machine has probably never heard of.
const RemotePathsNote = "--bin and --dest are paths on each machine; the remote shell expands a leading ~, so quote it ('~/.local/bin') or the local shell expands it here instead"

// Toolchain is the edge to `go build`. The arguments are handed over whole, so
// that a test asserting -trimpath and the -ldflags stamp is asserting the exact
// strings the compiler is given rather than a summary of them.
type Toolchain interface {
	Build(ctx context.Context, source, pkg, out, goos, goarch string, args []string) (string, error)
}

// field is SPEC.md's field law: one token, never empty, never able to end a line.
func field(s string) string {
	if s == "" {
		return "-"
	}
	return oneline.Field(s)
}

// refusalErr wraps a reason and the one thing to do about it, so that every
// refusal this package prints carries a remedy without each call site
// remembering to add one.
type refusalErr struct {
	reason, remedy string
}

func (r *refusalErr) Error() string { return r.reason + " (" + r.remedy + ")" }

func refuse(remedy, format string, a ...any) error {
	return &refusalErr{reason: fmt.Sprintf(format, a...), remedy: remedy}
}

// ValidVersion holds a release version to the same shape .github/scripts/release-ldflags.sh
// refuses at, and for the same reasons: the string travels into `-X main.version=`,
// into a printf format, and into a one-line field. Whitespace splits the linker
// flag, `%` reads a directive that was never supplied, and `=` is what both the
// stamp assertion and internal/oneline treat as a separator -- so a version
// carrying any of them is a version no later step could check. Refused here,
// before anything is built or tagged.
func ValidVersion(v string) error {
	remedy := "pass --version vX.Y.Z"
	if strings.TrimSpace(v) == "" {
		return refuse(remedy, "the version is empty")
	}
	if strings.ContainsAny(v, " \t\n\r") {
		return refuse(remedy, "the version %q carries whitespace; it would split the linker flag", v)
	}
	if strings.Contains(v, "%") {
		return refuse(remedy, "the version %q contains %%; it is substituted into a printf format further down the release", v)
	}
	if strings.Contains(v, "=") {
		return refuse(remedy, "the version %q contains =, which the stamp assertion and internal/oneline both read as a separator", v)
	}
	if !strings.HasPrefix(v, "v") {
		return refuse(remedy, "the version %q is not v-prefixed", v)
	}
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(parts) != 3 {
		return refuse(remedy, "the version %q is not three dotted numbers after the v", v)
	}
	for i, p := range parts {
		n := p
		if i == 2 {
			// The patch may carry a prerelease or build suffix; the number
			// in front of it still has to be a number.
			if c := strings.IndexAny(p, "-+"); c >= 0 {
				n = p[:c]
			}
		}
		if n == "" {
			return refuse(remedy, "the version %q has an empty number", v)
		}
		for _, r := range n {
			if r < '0' || r > '9' {
				return refuse(remedy, "the version %q is not three dotted numbers after the v", v)
			}
		}
	}
	return nil
}

// Ldflags is the stamp a release build carries, the same string
// .github/scripts/release-ldflags.sh composes: one place, so the -X cannot be
// dropped by an edit to a long build line nobody rereads.
func Ldflags(version string) string { return "-s -w -X main.version=" + version }
