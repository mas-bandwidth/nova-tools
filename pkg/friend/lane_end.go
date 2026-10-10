package friend

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
)

// A lane's end is a finish (the owner, 2026-10-05: "Now let's look at friends. Are they
// actually doing work?"; six one-shot runs ended overnight with no REPORT.md, killed at a
// cap or exited early, and their cards stayed working on the friend's row for up to 14
// hours). When a lane is done with a card (its RESULT.md or REPORT.md is there, or the card
// is set aside after CardTurns), and when a daemon starting up finds a card its lanes
// marked started, the card is finished: by the friend's own REPORT.md when she wrote one,
// which friend sync reads; else the lane writes a REPORT.md naming how the run ended and
// sends the failed finish to the sprint server (docs/SPEC-FRIEND.md, a lane's end). The
// model is pkg/friend/tla/LaneEnd.tla: NoOrphan, HersStands and Finished hold, and its
// reversed witness (MCLaneEndBrokenNoWrite.cfg, a lane that writes nothing) breaks NoOrphan.

// Started is a card a lane began, kept in the lane state until the lane is done with it:
// a daemon that starts up and finds one finishes it, for the run that held it is gone.
type Started struct {
	Lane int       `json:"lane"`
	Card Card      `json:"card"`
	At   time.Time `json:"at"`
}

// LaneEnd is how a card's last run in a lane ended, as its finish says it.
type LaneEnd struct {
	Exit     int
	Err      string        // the harness's error, when the run did not end with an exit
	Wall     time.Duration // the run's wall, its start to its end
	Cap      string        // the cap that stopped it, when one did
	Rejected string        // a permission the harness refused
	Turns    int           // the turns the card had in the lane
	Restart  time.Time     // not zero: the run was found gone by a daemon starting up at this time
	Started  time.Time     // when the lane began the card
	NoReport bool          // the run wrote RESULT.md and no REPORT.md
	// Capped is the card's wall cap by its tier when the lane ended the card at it
	// (lane_cap.go), Tier that tier, Overrun the card's wall past the cap at its end, and
	// Tail the last CapTailLines lines of the lane's output.
	Capped  time.Duration
	Tier    string
	Overrun time.Duration
	Tail    string
}

// How is the run's end on one line: the restart that found it gone, else the cap, the
// refused permission, the error or the exit, and the wall.
func (e LaneEnd) How() string {
	var b strings.Builder
	switch {
	case !e.Restart.IsZero():
		fmt.Fprintf(&b, "the run is gone: the lane daemon started up at %s and found the card begun at %s with no REPORT.md (the daemon that ran it exited, was killed or crashed)",
			e.Restart.UTC().Format(time.RFC3339), e.Started.UTC().Format(time.RFC3339))
		return b.String()
	case e.Capped > 0:
		fmt.Fprintf(&b, "%s: the card's wall reached its tier's cap and the daemon ended the lane, exit %d", CappedWords(e.Capped, e.Tier, e.Overrun), e.Exit)
	case e.Cap != "":
		fmt.Fprintf(&b, "the run was stopped at the cap (%s), exit %d", e.Cap, e.Exit)
	case e.Rejected != "":
		fmt.Fprintf(&b, "the harness refused a permission (%s), exit %d", e.Rejected, e.Exit)
	case e.Err != "":
		fmt.Fprintf(&b, "the run ended with an error (%s), exit %d", e.Err, e.Exit)
	case e.NoReport:
		fmt.Fprintf(&b, "the run exited %d with RESULT.md and no REPORT.md", e.Exit)
	default:
		fmt.Fprintf(&b, "the run exited %d with no REPORT.md", e.Exit)
	}
	fmt.Fprintf(&b, ", wall %s", e.Wall.Round(time.Second))
	if e.Turns > 1 {
		fmt.Fprintf(&b, ", after %d turns", e.Turns)
	}
	return b.String()
}

// EndReport is the REPORT.md a lane writes for a card whose run ended without one: HOLD
// with the pushed head when the friend's branch has one (friend sync keeps a HOLD's head
// when it is origin's tip), else FAIL; one paragraph naming the lane, how the run ended and
// the head. A card the lane ended at its cap is a HOLD, head or none, and its report quotes
// the last lines of the lane's output after the paragraph, each indented four spaces
// (lane_cap.go).
func EndReport(friend string, lane int, c Card, end LaneEnd, head, branch string) string {
	verdict, pushed := "FAIL", "no pushed head found"
	if head != "" {
		verdict, pushed = "HOLD", "pushed head "+head+" on "+branch
	}
	if end.Capped > 0 {
		verdict = "HOLD"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Verdict: %s\n", verdict)
	if head != "" {
		fmt.Fprintf(&b, "Head: %s\n", head)
	}
	fmt.Fprintf(&b, "\nnova-friend lane %d of %s finished card %s: %s; %s.\n", lane, friend, c.ID, oneLine(end.How(), 600), pushed)
	if end.Capped > 0 {
		fmt.Fprintf(&b, "\nThe last %d lines of the lane's output:\n\n", CapTailLines)
		tail := cmp.Or(end.Tail, "(the lane printed nothing)")
		for l := range strings.SplitSeq(tail, "\n") {
			b.WriteString("    " + l + "\n")
		}
	}
	return b.String()
}

// FinishArgv is the sprint server's failed finish of a card whose run ended without the
// friend's report: `finish --as friend.<name> <card>@<gen> --epoch <n> --failed [--head
// <sha>] [--branch <b>] --report <text>`, the report as friend sync words it ("friend
// <name> <verdict>: <paragraph>").
func FinishArgv(friend string, c Card, report, head, branch string) []string {
	argv := []string{"finish", "--as", "friend." + friend, c.ID + "@" + strconv.Itoa(c.Gen()), "--epoch", c.Epoch(), "--failed"}
	if head != "" {
		argv = append(argv, "--head", head)
	}
	if branch != "" {
		argv = append(argv, "--branch", branch)
	}
	verdict, _ := reportLine(report, "Verdict")
	return append(argv, "--report", "friend "+friend+" "+verdict+": "+reportParagraph(report))
}

func reportLine(report, key string) (string, bool) {
	for _, l := range strings.Split(report, "\n") {
		if v, ok := strings.CutPrefix(l, key+": "); ok {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}

// reportParagraph is the report's first line that is no key line, as friend sync reads it.
func reportParagraph(report string) string {
	for _, l := range strings.Split(report, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "Verdict:") && !strings.HasPrefix(l, "Head:") {
			return l
		}
	}
	return ""
}

// statusBranch is the branch a friend's brief tells her to push to, off its STATUS line.
var statusBranch = regexp.MustCompile(`push your work to the branch ([^\s;]+)`)

// PushedHead is the head the friend pushed for card c: the branch its brief's STATUS line
// names, read as origin's remote-tracking ref in a clone under dir's jobs/<job>/ (a push
// writes it; no network is asked). Empty when the brief names no branch or no clone holds
// the ref; branch is the brief's either way.
func PushedHead(dir string, c Card) (head, branch string) {
	brief, err := os.ReadFile(c.Brief)
	if err != nil {
		return "", ""
	}
	m := statusBranch.FindSubmatch(brief)
	if m == nil {
		return "", ""
	}
	branch = string(m[1])
	jobs := filepath.Join(dir, "jobs", filepath.Base(c.Outbox))
	// ignored: the walk's callback swallows every error itself; a job it cannot read has no head
	_ = filepath.WalkDir(jobs, func(path string, e fs.DirEntry, err error) error {
		switch {
		case head != "":
			return filepath.SkipAll
		case err != nil:
			return filepath.SkipDir // ignored: an unreadable directory holds no head this walk can read
		}
		if rel, _ := filepath.Rel(jobs, path); strings.Count(rel, string(filepath.Separator)) > 3 {
			return filepath.SkipDir
		}
		if e.Name() != ".git" {
			return nil
		}
		head = remoteRef(gitDir(path), "refs/remotes/origin/"+branch)
		if e.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	return head, branch
}

var fullSha = regexp.MustCompile(`^[0-9a-f]{40}$`)

// gitDir is the git directory a .git names: itself, or the common directory of the
// worktree a .git file points to.
func gitDir(dotGit string) string {
	fi, err := os.Stat(dotGit)
	if err != nil || fi.IsDir() {
		return dotGit
	}
	raw, err := os.ReadFile(dotGit)
	if err != nil {
		return dotGit
	}
	dir, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir: ")
	if !ok {
		return dotGit
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(filepath.Dir(dotGit), dir)
	}
	if common, err := os.ReadFile(filepath.Join(dir, "commondir")); err == nil {
		c := strings.TrimSpace(string(common))
		if !filepath.IsAbs(c) {
			c = filepath.Join(dir, c)
		}
		return c
	}
	return dir
}

// remoteRef is ref's commit in the git directory gd: its loose file, else its packed-refs
// line; empty when neither holds a full sha.
func remoteRef(gd, ref string) string {
	if raw, err := os.ReadFile(filepath.Join(gd, filepath.FromSlash(ref))); err == nil {
		if sha := strings.TrimSpace(string(raw)); fullSha.MatchString(sha) {
			return sha
		}
	}
	f, err := os.Open(filepath.Join(gd, "packed-refs"))
	if err != nil {
		return ""
	}
	defer f.Close() // ignored: packed-refs is only read; its close can lose nothing the scan returned
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if sha, name, ok := strings.Cut(sc.Text(), " "); ok && name == ref && fullSha.MatchString(sha) {
			return sha
		}
	}
	return ""
}

// endCard is the lane done with card: the friend's REPORT.md, when there, is the finish
// (friend sync reads it); else the lane writes one naming how the run ended and sends the
// failed finish to the sprint server. Either way the card is no longer started. It answers
// the record's words.
func (l *loop) endCard(lane int, card Card, end LaneEnd, now time.Time) string {
	d, s := l.d, l.lanes
	delete(s.state.Started, filepath.Base(card.Outbox))
	defer l.saveLanes(now)
	mark := ""
	if err := endLaneMark(d.Dir, filepath.Base(card.Outbox), l.laneWho(lane)); err != nil {
		mark = fmt.Sprintf(" mark_error=%q", oneLine(err.Error(), 300)) // its other lanes end by her row alone
	}
	if exists(card.Report()) {
		return "finish=report" + mark
	}
	head, branch := PushedHead(d.Dir, card)
	report := EndReport(d.Friend, lane, card, end, head, branch)
	words := "finish=failed"
	if head != "" {
		words += " head=" + head
	}
	words += mark
	if err := os.MkdirAll(card.Outbox, 0o755); err != nil {
		return words + fmt.Sprintf(" report_error=%q", err.Error())
	}
	if err := atomicfile.WriteFile(card.Report(), []byte(report), 0o644); err != nil {
		return words + fmt.Sprintf(" report_error=%q", err.Error())
	}
	if d.Finish == nil {
		return words + " sent=sync" // friend sync finishes it from the report just written
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), FinishWait)
	defer cancel()
	if err := d.Finish(ctx, FinishArgv(d.Friend, card, report, head, branch)); err != nil {
		return words + fmt.Sprintf(" sent=sync finish_error=%q", oneLine(err.Error(), 300))
	}
	return words + " sent=server"
}

// FinishWait bounds a lane's finish to the sprint server; one not answered is left to
// friend sync, which reads the REPORT.md the lane wrote.
const FinishWait = 10 * time.Second

// endStarted is a daemon starting up: every card its lanes marked started is ended, for the
// run that held it is gone with the daemon that ran it.
func (l *loop) endStarted(now time.Time) {
	s := l.lanes
	for job, st := range s.state.Started {
		words := l.endCard(st.Lane, st.Card, LaneEnd{Restart: now, Started: st.At}, now)
		if !s.given[job] {
			s.given[job] = true
			s.state.GivenUp = append(s.state.GivenUp, job)
		}
		l.d.Record(fmt.Sprintf("%s lane %d: card %s was begun at %s and its run is gone: %s", now.UTC().Format(time.RFC3339), st.Lane, st.Card.ID, st.At.UTC().Format(time.RFC3339), words))
	}
	l.saveLanes(now)
}
