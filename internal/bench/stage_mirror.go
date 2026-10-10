package bench

// THE MIRROR STAGE (docs/SPEC-SPRINT.md section 7, how a gate reaches a bench).
//
// THE HURT. Until 2026-10-07 the lander's tree gate on a bench was staged by Copy: the
// lander's clone, .git and all, as a tar stream on ssh's stdin, about 180 MB a batch and
// 60 to 90 s a copy over the tailnet when it completed. When it did not (WriteTree refusing
// a socket, the bench's tar exiting), the run directory appeared and vanished on the bench
// every 15 to 30 s and the loop's line said only "step=gate": nothing said what refused the
// copy, and the gate was asked again from the top.
//
// THE STAGE. The bench already keeps the repository's bare mirror (nova-worker member's
// NOVA_SWARM_MIRRORS, swarm.MirrorPath: ~/nova-bench/mirror/<repo>.git, gc.auto=0, only
// ever fetched into). The lander pushes the gated tip to a temporary ref on the remote
// (refs/nova-gate/<batch>, WithGateRef), the bench fetches that one ref into its mirror
// (skipped when the mirror holds the commit already) and the run's tree is a clone of the
// mirror that borrows its objects (clone --shared) checked out detached at the sha. Only the
// sha crosses the tailnet. A shared clone rather than `git worktree add`: the run directory
// is then the whole of what the stage made, so the run's one remove (RemoveLine) leaves
// nothing behind in the mirror (a worktree's record under the mirror's worktrees/ would
// outlive it). The temporary ref is deleted after the gate, whatever the gate did.
//
// THE LINE. Every stage says what it did, one clause the lander puts on its LAND line and
// on the loop's idle line: "copy <host> <n>MB <t>s via mirror|tar", or "copy refused:
// <why>", the why naming the host, the wall time, the step that refused (WriteTree's own
// refusal, the bench's tar or git exit) and the tail of its stderr. A bench whose stage
// fails twice in a land pass is passed over by the ring for the rest of that pass, with
// the reason (StageSkips); never a bare retry.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

// MirrorDir is the bench's mirror of the repository named name, relative to the login's
// home: the directory swarm.MirrorPath names under the bench's home.
func MirrorDir(name string) string {
	return "nova-bench/mirror/" + strings.TrimSuffix(name, ".git") + ".git"
}

// GateRefPrefix is where the lander's temporary refs live on the remote.
const GateRefPrefix = "refs/nova-gate/"

// GateRef is the temporary ref of one gate: key (the batch's stream) made a ref name
// component, and the sha's first twelve, so two gates never share one.
func GateRef(key, sha string) string {
	k := refUnsafe.ReplaceAllString(key, "-")
	k = strings.Trim(k, ".-")
	if k == "" {
		k = "gate"
	}
	if len(sha) > 12 {
		sha = sha[:12]
	}
	return GateRefPrefix + k + "-" + sha
}

var (
	refUnsafe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)
	shaRe     = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	gateRefRe = regexp.MustCompile(`^refs/nova-gate/[A-Za-z0-9_][A-Za-z0-9_-]*$`)
)

// MirrorStage is a run's tree staged from the bench's mirror in place of a copy.
type MirrorStage struct {
	// Mirror is the bare mirror on the bench, relative to the login's home or absolute
	// (MirrorDir).
	Mirror string
	// Remote is the URL the ref is fetched from when the mirror names no origin of its
	// own; the mirror's origin is preferred, since that is the URL the bench can read.
	Remote string
	// Ref is the temporary ref the lander pushed (GateRef); Sha the commit it names, the
	// tree the run's command runs in.
	Ref, Sha string
}

// Validate refuses a stage the remote line could read as more than it says.
func (s *MirrorStage) Validate() error {
	var bad []string
	if err := CheckPath("mirror", s.Mirror); err != nil {
		bad = append(bad, err.Error())
	}
	if s.Remote == "" || strings.HasPrefix(s.Remote, "-") || strings.ContainsAny(s.Remote, "\n\r") {
		bad = append(bad, fmt.Sprintf("the stage's remote %q is not a URL git can fetch from", s.Remote))
	}
	if !gateRefRe.MatchString(s.Ref) {
		bad = append(bad, fmt.Sprintf("the stage's ref %q is not under %s", s.Ref, GateRefPrefix))
	}
	if !shaRe.MatchString(s.Sha) {
		bad = append(bad, fmt.Sprintf("the stage's sha %q is not a full commit id", s.Sha))
	}
	if len(bad) > 0 {
		return errors.New(strings.Join(bad, "; "))
	}
	return nil
}

// NoMirror is the stage line's own exit when the bench keeps no mirror at the path.
const NoMirror = 3

// StageLine is the remote line of the mirror stage into dst: the mirror must exist; the
// commit is fetched from the mirror's origin (else Remote) by the temporary ref unless the
// mirror holds it already, no ref or FETCH_HEAD written; then dst is a clone of the mirror
// borrowing its objects, checked out detached at the sha.
func StageLine(s MirrorStage, dst string) string {
	m, d := Quote(s.Mirror), Quote(dst)
	commit := Quote(s.Sha + "^{commit}")
	return "test -f " + m + "/HEAD || { echo " + Quote("no mirror at "+s.Mirror) + " >&2; exit " + fmt.Sprint(NoMirror) + "; }" +
		" && { git -C " + m + " cat-file -e " + commit + " 2>/dev/null" +
		" || { src=$(git -C " + m + " config --get remote.origin.url) || src=" + Quote(s.Remote) +
		"; GIT_TERMINAL_PROMPT=0 git -C " + m + " fetch --quiet --no-tags --no-write-fetch-head \"$src\" " + Quote(s.Ref) +
		" && git -C " + m + " cat-file -e " + commit + "; }; }" +
		" && git clone --quiet --shared --no-checkout " + m + " " + d +
		" && git -C " + d + " checkout --quiet --detach " + Quote(s.Sha)
}

// RefGit is the lander's side of a mirror stage: the gated commit pushed to a temporary
// ref on the remote the bench fetches from, and that ref deleted after.
type RefGit interface {
	Push(ctx context.Context, sha, ref string) error
	Delete(ctx context.Context, ref string) error
}

// WithGateRef pushes sha to ref, runs gate, and deletes ref whatever gate did, under a
// context the caller's cancellation does not end (bounded by StepBudget), so an
// interrupted gate still removes its ref. A push that fails is gate not run, the error
// saying so; a delete that fails is joined to gate's error.
func WithGateRef(ctx context.Context, g RefGit, sha, ref string, gate func() error) error {
	if err := g.Push(ctx, sha, ref); err != nil {
		return &StageError{Step: "push " + ref, Err: err}
	}
	err := gate()
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), StepBudget)
	defer cancel()
	if derr := g.Delete(dctx, ref); derr != nil {
		err = errors.Join(err, fmt.Errorf("deleting the gate's ref %s: %w", ref, derr))
	}
	return err
}

// Stage is what a run's stage did, for the lander's lines.
type Stage struct {
	// Host is the bench; Via how the tree got there: "mirror" or "tar".
	Host, Via string
	// Bytes is what crossed the wire from this machine (the tar stream; nothing but the
	// line for a mirror stage); Wall the stage's time on the run's clock.
	Bytes int64
	Wall  time.Duration
	// Err is the stage's refusal, nil when the tree is staged.
	Err *StageError
}

// Line is the stage as the lander's lines say it: "copy <host> <n>MB <t>s via <via>", or
// "copy refused: <why>". A run that staged nothing is "".
func (s Stage) Line() string {
	switch {
	case s.Err != nil:
		return s.Err.Error()
	case s.Host == "":
		return ""
	}
	return fmt.Sprintf("copy %s %dMB %.1fs via %s", s.Host, s.Bytes>>20, s.Wall.Seconds(), s.Via)
}

// StageError is a stage that did not put the tree on the bench: the step that refused,
// its exit (0 when it has none), the tail of its stderr and the wall time it took.
type StageError struct {
	Host, Step string
	Code       int
	Tail       string
	Wall       time.Duration
	Err        error
}

// Error is the refusal's one line: "copy refused: <host> after <t>s: <step>[ exit <n>][:
// <err>][: <stderr tail>]".
func (e *StageError) Error() string {
	var b strings.Builder
	b.WriteString("copy refused: ")
	if e.Host != "" {
		fmt.Fprintf(&b, "%s after %.1fs: ", e.Host, e.Wall.Seconds())
	}
	b.WriteString(e.Step)
	if e.Code != 0 {
		fmt.Fprintf(&b, " exit %d", e.Code)
	}
	if e.Err != nil {
		b.WriteString(": " + strings.Join(strings.Fields(e.Err.Error()), " "))
	}
	if e.Tail != "" {
		b.WriteString(": " + e.Tail)
	}
	return b.String()
}

func (e *StageError) Unwrap() error { return e.Err }

// tailLines is the last three non-empty lines of s joined by " | ", capped at 400 bytes
// from the end: the stderr a refusal carries.
func tailLines(s string) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	t := strings.Join(lines, " | ")
	if len(t) > 400 {
		t = "..." + t[len(t)-400:]
	}
	return t
}

// SkipAfter is how many stage failures in one land pass pass a bench over for the rest
// of it.
const SkipAfter = 2

// StageSkips is one land pass's record of the benches whose stage failed. Safe for the
// pass's streams at once.
type StageSkips struct {
	mu    sync.Mutex
	fails map[string]int
	why   map[string]string
}

// Fail records one failed stage on host, why its refusal; skipped says host is now passed
// over for the rest of the pass.
func (s *StageSkips) Fail(host, why string) (skipped bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fails == nil {
		s.fails, s.why = map[string]int{}, map[string]string{}
	}
	s.fails[host]++
	s.why[host] = why
	return s.fails[host] >= SkipAfter
}

// Skipped is the reason host is passed over this pass, and whether it is.
func (s *StageSkips) Skipped(host string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fails[host] < SkipAfter {
		return "", false
	}
	return fmt.Sprintf("skip %s: its stage failed %d times this pass, last: %s", host, s.fails[host], s.why[host]), true
}

// Ring is ring without the hosts passed over this pass, in its order, and one line for
// each host left out.
func (s *StageSkips) Ring(ring []string) (keep, notes []string) {
	for _, h := range ring {
		if why, skip := s.Skipped(h); skip {
			notes = append(notes, why)
			continue
		}
		keep = append(keep, h)
	}
	return keep, notes
}

// countingWriter counts what passes through it to w.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
