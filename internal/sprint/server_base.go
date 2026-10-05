package sprint

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// The server is built from the sprint base and nothing else (docs/SPEC-SPRINT.md section 14,
// "server-from-base-only.w1"). On 2026-10-04 the live server ran a binary built from a side
// branch for a day, 1,052 commits off the base one way and 24 the other: two lander designs at
// once. The owner, 2026-10-05: "Prevention is better than cure". So the build commit a binary's
// version line names (internal/buildinfo, Fields.Commit) must be an ancestor of origin's sprint
// base, fetched, by git merge-base --is-ancestor: server switch refuses a binary whose commit is
// not, and the tick keeps one judgment while the running server's commit is not.

// NServerOffBase is the judgment that the running server's build commit is not on origin's
// sprint base, one for the sprint, closed when it is back on.
const NServerOffBase = "the server runs off the sprint base"

// VersionWait bounds `<binary> version`: a version verb answers at once or not at all.
const VersionWait = 10 * time.Second

// ServerBase is one binary's build commit checked against origin's sprint base.
type ServerBase struct {
	Line   string // the binary's version line, as it printed it
	Commit string // the full commit the line names; "" when it names none
	Why    string // with no Commit: why the line names none (buildinfo.Fields.Commit)
	Base   string // the sprint base, a branch on origin
	Tip    string // origin/<Base> as fetched
	On     bool   // Commit is an ancestor of Tip
}

// BinaryLine is the version line `<binary> version` prints, its first line.
func BinaryLine(ctx context.Context, binary string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, VersionWait)
	defer cancel()
	var out, errs bytes.Buffer
	cmd := exec.CommandContext(ctx, binary, "version")
	cmd.Stdout, cmd.Stderr = &out, &errs
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s version failed: %v: %s", binary, err, strings.TrimSpace(errs.String()))
	}
	line, _, _ := strings.Cut(out.String(), "\n")
	return strings.TrimSpace(line), nil
}

// CheckServerBinary is CheckServerBase over the line `<binary> version` prints: the check
// every verb that installs the server binary makes before anything on disk changes. A binary
// whose version verb fails is a check that could not be made.
func CheckServerBinary(ctx context.Context, binary, repo, base string) (ServerBase, error) {
	if err := baseNamed(repo, base); err != nil {
		return ServerBase{Base: base}, err
	}
	line, err := BinaryLine(ctx, binary)
	if err != nil {
		return ServerBase{Base: base}, err
	}
	return CheckServerBase(ctx, line, repo, base)
}

// CheckServerBase reads the build commit from line, fetches origin's base into repo and asks
// git whether the commit is an ancestor of its tip. An error is a check that could not be
// made (no base named, no clone, a fetch that failed); a line naming no commit, or a commit
// the clone does not hold, is a check made, and not On.
func CheckServerBase(ctx context.Context, line, repo, base string) (ServerBase, error) {
	b := ServerBase{Line: line, Base: base}
	if err := baseNamed(repo, base); err != nil {
		return b, err
	}
	f, ok := buildinfo.Parse(line)
	if !ok {
		b.Why = "its version line " + strings.TrimSpace(line) + " is not a version line"
	} else {
		b.Commit, b.Why = f.Commit()
	}
	ref := "refs/remotes/origin/" + base
	if _, err := gitIn(ctx, repo, "fetch", "--quiet", "origin", "+refs/heads/"+base+":"+ref); err != nil {
		return b, fmt.Errorf("git fetch origin %s in %s failed: %w", base, repo, err)
	}
	tip, err := gitIn(ctx, repo, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return b, fmt.Errorf("origin/%s has no tip in %s: %w", base, repo, err)
	}
	b.Tip = tip
	if b.Commit == "" {
		return b, nil
	}
	full, err := gitIn(ctx, repo, "rev-parse", "--verify", "--quiet", "--end-of-options", b.Commit+"^{commit}")
	if err != nil {
		b.Why = "its commit " + b.Commit + " is in no branch fetched from origin"
		return b, nil
	}
	b.Commit = full
	_, err = gitIn(ctx, repo, "merge-base", "--is-ancestor", "--end-of-options", full, tip)
	var ee *exec.ExitError
	switch {
	case err == nil:
		b.On = true
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		b.Why = "its commit " + shortSHA(full) + " is not an ancestor of origin/" + base
	default:
		return b, fmt.Errorf("git merge-base --is-ancestor %s origin/%s in %s failed: %w", shortSHA(full), base, repo, err)
	}
	return b, nil
}

// baseNamed refuses a check with no base, or no clone, to make it in.
func baseNamed(repo, base string) error {
	if base == "" || strings.HasPrefix(base, "-") || strings.ContainsAny(base, " \t:") {
		return fmt.Errorf("the sprint base %q is not a branch name", base)
	}
	if repo == "" {
		return errors.New("no clone of the repository named to fetch the sprint base into")
	}
	return nil
}

// Refusal is what server switch says of a binary not On the base: the commit (or why there
// is none), the base and its tip, and the remedy.
func (b ServerBase) Refusal(binary string) string {
	commit := "no source commit"
	if b.Commit != "" {
		commit = "commit " + shortSHA(b.Commit)
	}
	return fmt.Sprintf("%s was built from %s, not from the sprint base origin/%s (tip %s): %s; the server is built from the base and nothing else; remedy: build nova-sprint from origin/%s at its tip, then run: nova-sprint server switch <that binary>",
		binary, commit, b.Base, shortSHA(b.Tip), b.Why, b.Base)
}

// What is the judgment's line for a running server not On the base. It names the commit and
// the base and never the tip, so the base moving on is the same episode (notify keys this
// judgment by its line).
func (b ServerBase) What() string {
	commit := "no source commit (" + b.Why + ")"
	if b.Commit != "" {
		commit = "commit " + shortSHA(b.Commit)
	}
	return fmt.Sprintf("the running server was built from %s, not an ancestor of origin/%s; remedy: build nova-sprint from origin/%s at its tip, then run: nova-sprint server switch <that binary>",
		commit, b.Base, b.Base)
}

// TickServerBase is the tick's server-base judgment (docs/SPEC-SPRINT.md section 14,
// "server-from-base-only.w1"): one judgment while the running server's commit is not On the
// base, none while it stands, and closed once it is. A nil check is no fact this tick (none
// made, or one that could not be made): nothing is raised and nothing closed.
func TickServerBase(s *Snapshot, r TickReq, b *ServerBase) (Plan, int) {
	var p Plan
	if b == nil {
		return p, 0
	}
	var conds []cond
	if !b.On {
		conds = append(conds, cond{typ: NServerOffBase, streamLevel: true, what: b.What(),
			decisions: []string{"server switch <a binary built from origin/" + b.Base + ">", "wait 30m"}})
	}
	due := notify(&p, s, conds, []string{NServerOffBase}, r)
	return p, due
}

func gitIn(ctx context.Context, repo string, args ...string) (string, error) {
	var out, errs bytes.Buffer
	cmd := subproc.Context(ctx, "git", append([]string{"-C", repo}, args...)...)
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errs.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	if sha == "" {
		return "-"
	}
	return sha
}
