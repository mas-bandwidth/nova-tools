package pulse

// FAILURE HAS A CAUSE, AND A CAUSE HAS EXACTLY ONE ACTION. Pit stop 3, class I (#828).
//
// Measured on 2026-09-16: 834 launches, 307 of them requeues of a card that had just
// failed, and 174 cards that failed twice with the same text. `attempt=1` reran the card
// blindly, because nothing read WHY it had failed. A blind rerun is the most expensive
// thing this estate does: it pays the full card twice for the same refusal.
//
// The rule that retires the class: the harness log is read, its signature names the cause,
// and the cause chooses ONE action. Four signatures, four actions:
//
//	fence=rejected path=<p>                            fence      re-cut, naming the fence
//	toolchain not available | command not found: go |  signature  probe the bench, NOT the card
//	  cannot find package
//	no RESULT.md and the harness ended rc=0            nosha      re-cut, "RESULT.md last"
//	rc=143, or the deadline                            orphan     re-cut, a shorter step list
//	anything else                                      unknown    a triage packet (#849)
//
// And one rule over all of them: A SECOND FAILURE OF THE SAME KIND IS NOT RE-CUT. The card
// is failed and a triage packet goes out on the text route, because a cause that survived
// its own remedy is a decision, not a retry.
//
// THE SAME CARD TEXT IS NEVER LAUNCHED TWICE. A re-cut takes a new number, carries the
// cause line, and writes the superseded body's sha8 into <queue>/RECUT.tsv; `launch`
// refuses any card whose body sha8 stands in that file and names the card that replaced it
// (admission.go). So the old text cannot be relaunched by a stale cards.tsv, a re-run of an
// old pulse, or a hand.
//
// Where the evidence lives: <queue>/harness/<card>.log is the harness's own output for that
// card and <queue>/harness/<card>.RESULT.md is the result it wrote, if it wrote one. The
// swarm's `--then` step copies both there; a bench that has copied neither reads as
// `unknown`, which is a triage packet and not a rerun.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The four causes, and the fifth that is the absence of one.
const (
	CauseFence     = "fence"
	CauseSignature = "signature"
	CauseNoSHA     = "nosha"
	CauseOrphan    = "orphan"
	CauseUnknown   = "unknown"
)

// The actions a cause may choose. Exactly one per cause, and no cause chooses two.
const (
	ActionRecut  = "recut"  // cut the card again under a new number with the cause named
	ActionProbe  = "probe"  // the bench or the route is at fault; the card is untouched
	ActionTriage = "triage" // no rule reads this signature: one packet on the text route
	ActionFail   = "fail"   // this cause already had its remedy: fail, and a packet with it
)

// fencePath pulls the rejected path off the harness's fence line.
var fencePath = regexp.MustCompile(`fence=rejected\s+path=(\S+)`)

// benchSignatures are the harness lines that say the BENCH is wrong, not the card. Re-cutting
// a card because go was not on the worker's PATH is how 174 cards failed twice.
var benchSignatures = []string{
	"toolchain not available",
	"command not found: go",
	"cannot find package",
}

// orphanSignatures are the harness lines of a card killed rather than finished.
var orphanSignatures = []string{
	"rc=143",
	"deadline exceeded",
	"deadline hit",
	"past the deadline",
}

// Signature is one reading of a harness log: the cause, the one action it chooses, the
// detail the cause line needs (a fence's path), and the cause line itself.
type Signature struct {
	Kind   string
	Action string
	Detail string
	Line   string // the `Prior attempt: ...` line a re-cut carries; empty when nothing is re-cut
}

// Cause reads a harness log and names the cause and the one action it chooses. It is the
// whole of class I's rule table, and it is pure: no file, no clock, no queue.
func Cause(harnessLog []byte, resultPresent bool) (kind string, action string) {
	s := Read(harnessLog, resultPresent)
	return s.Kind, s.Action
}

// Read is Cause with the detail and the cause line kept. The order of the arms is the order
// of certainty: a fence names itself, a bench signature names itself, a kill names itself,
// and only then is a missing RESULT read as the card's own fault.
func Read(harnessLog []byte, resultPresent bool) Signature {
	log := string(harnessLog)
	if m := fencePath.FindStringSubmatch(log); m != nil {
		return signatureOf(CauseFence, ActionRecut, m[1])
	}
	if containsAny(log, benchSignatures) {
		// NOT the card's fault, so NOT the card that is changed: the bench or the route is
		// probed and the card waits. This arm is the 174.
		return signatureOf(CauseSignature, ActionProbe, "")
	}
	if containsAny(log, orphanSignatures) {
		return signatureOf(CauseOrphan, ActionRecut, "")
	}
	if !resultPresent && strings.Contains(log, "rc=0") {
		return signatureOf(CauseNoSHA, ActionRecut, "")
	}
	return signatureOf(CauseUnknown, ActionTriage, "")
}

// signatureOf binds a cause to its action and its line in one place, so a cause can never
// be given two actions by two callers.
func signatureOf(kind, action, detail string) Signature {
	return Signature{Kind: kind, Action: action, Detail: detail, Line: CauseLine(kind, detail)}
}

// CauseLine is the one line a re-cut card carries above its steps: what the last attempt
// did, and the one thing to do differently. A worker that is told only "this failed once"
// repeats it; a worker told which fence rejected which path does not.
func CauseLine(kind, detail string) string {
	switch kind {
	case CauseFence:
		return fmt.Sprintf("Prior attempt: fence rejected %s; write only under ./repo and ./scratch", oneline.Field(detail))
	case CauseNoSHA:
		return "Prior attempt: the harness ended rc=0 with no RESULT.md; write RESULT.md as the last step"
	case CauseOrphan:
		return "Prior attempt: killed at the deadline; this cut carries a shorter step list -- do the steps below and stop"
	}
	return ""
}

// Decide folds one reading and the card's prior causes into the action actually taken. It
// is the second half of the rule: a cause whose remedy has already been spent is a decision
// for the text route, never another launch.
func Decide(sig Signature, prior []string) (action string, why string) {
	for _, p := range prior {
		if p == sig.Kind && sig.Kind != CauseUnknown {
			return ActionFail, fmt.Sprintf("a second %s failure: the re-cut that names the cause has already been spent", sig.Kind)
		}
	}
	if sig.Action == ActionTriage {
		return ActionTriage, "no rule reads this harness signature"
	}
	return sig.Action, ""
}

// TriageCase is the case a packet is cut under for a cause. fence, nosha and orphan are
// cases of their own; an unreadable signature is the `signature` case, which is exactly the
// question "what does this harness line mean".
func TriageCase(kind string) string {
	if KnownTriageKind(kind) {
		return kind
	}
	return "signature"
}

// AttemptRow is one prior attempt of one card: when it failed, the cause it failed as, and
// the cause line that was written on its re-cut.
type AttemptRow struct {
	At   string
	Kind string
	Line string
}

// attemptsDir is where a card's attempt history lives. One file per card name, appended.
const attemptsDir = "attempts"

// ReadAttempts reads a card's attempt history. No file is no attempts and not an error: a
// card's first failure has nothing behind it.
func ReadAttempts(queue, card string) []AttemptRow {
	var out []AttemptRow
	for _, l := range readLines(filepath.Join(queue, attemptsDir, attemptFile(card))) {
		if strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Split(l, "\t")
		if len(f) < 2 {
			continue
		}
		r := AttemptRow{At: strings.TrimSpace(f[0]), Kind: strings.TrimSpace(f[1])}
		if len(f) >= 3 {
			r.Line = strings.TrimSpace(f[2])
		}
		out = append(out, r)
	}
	return out
}

// PriorKinds is the causes a card has already failed as, oldest first.
func PriorKinds(queue, card string) []string {
	rows := ReadAttempts(queue, card)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Kind)
	}
	return out
}

// PriorLine is the `--prior` text a refill or a hand cut passes to `cut --kind`: every cause
// line this card has already been given, in order, as ONE line. It is the prior-attempts
// line of the card the refiller cuts next, and it is why a refill never hands a worker a
// card that hides what the last worker hit.
func PriorLine(queue, card string) string {
	var parts []string
	for _, r := range ReadAttempts(queue, card) {
		if l := strings.TrimSpace(r.Line); l != "" {
			parts = append(parts, l)
			continue
		}
		parts = append(parts, r.Kind)
	}
	return strings.Join(parts, "; ")
}

// AppendAttempt records one failure against a card.
func AppendAttempt(queue, card string, r AttemptRow) error {
	dir := filepath.Join(queue, attemptsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, attemptFile(card)), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s\t%s\t%s\n", oneline.Field(r.At), oneline.Field(r.Kind), oneline.Escape(r.Line))
	return err
}

// carryAttempts copies a card's history onto the card that supersedes it, so the second
// failure of a cause is seen even though the card wears a new number.
func carryAttempts(queue, from, to string) error {
	rows := ReadAttempts(queue, from)
	for _, r := range rows {
		if err := AppendAttempt(queue, to, r); err != nil {
			return err
		}
	}
	return nil
}

func attemptFile(card string) string { return strings.TrimSuffix(card, ".md") + ".tsv" }

// recutFile is the index of superseded card bodies: one row per re-cut, holding the sha8 of
// the text that may never launch again.
const recutFile = "RECUT.tsv"

// RecutRow is one superseded body: its sha8, the card it was, the card that replaced it and
// the cause that forced the re-cut.
type RecutRow struct {
	SHA8 string
	From string
	To   string
	Kind string
}

// BodySHA8 is the first eight hex of the SHA-256 of a card's body -- the card with any
// trailing CUT stamp removed, so a stamp does not make one text look like two.
func BodySHA8(card string) string {
	sum := sha256.Sum256([]byte(StampBody(card)))
	return hex.EncodeToString(sum[:])[:8]
}

// ReadRecuts reads the superseded index of a queue.
func ReadRecuts(queue string) []RecutRow {
	var out []RecutRow
	for _, l := range readLines(filepath.Join(queue, recutFile)) {
		if strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Split(l, "\t")
		if len(f) < 3 {
			continue
		}
		r := RecutRow{SHA8: strings.TrimSpace(f[0]), From: strings.TrimSpace(f[1]), To: strings.TrimSpace(f[2])}
		if len(f) >= 4 {
			r.Kind = strings.TrimSpace(f[3])
		}
		out = append(out, r)
	}
	return out
}

// AppendRecut writes one superseded body into the index.
func AppendRecut(queue string, r RecutRow) error {
	if err := os.MkdirAll(queue, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(queue, recutFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s\t%s\t%s\t%s\n",
		oneline.Field(r.SHA8), oneline.Field(r.From), oneline.Field(r.To), oneline.Field(r.Kind))
	return err
}

// Superseded says whether this card text has already been re-cut, and as what.
func Superseded(recuts []RecutRow, card string) (RecutRow, bool) {
	sha := BodySHA8(card)
	for _, r := range recuts {
		if r.SHA8 == sha {
			return r, true
		}
	}
	return RecutRow{}, false
}

// RecutInput is one re-cut: the card that failed, the reading that failed it, and the queue
// whose state file hands out the next number.
type RecutInput struct {
	Queue    string
	CardPath string
	Sig      Signature
	Version  string
	Now      func() time.Time
}

// Recut writes the card again under a NEW number, carrying the cause line, and closes the
// old text: its sha8 goes into RECUT.tsv and its history is carried onto the new card. The
// old file is not deleted here -- the reaper moves it -- because a re-cut that loses the
// text it replaced loses the evidence for the next reading.
func Recut(in RecutInput) (newPath string, err error) {
	raw, err := os.ReadFile(in.CardPath)
	if err != nil {
		return "", fmt.Errorf("the failed card could not be read: %w", err)
	}
	old := string(raw)
	oldName := filepath.Base(in.CardPath)
	n, err := NextCardNumber(in.Queue)
	if err != nil {
		return "", err
	}
	newName := fmt.Sprintf("card-%d.md", n)
	body := recutBody(old, in.Sig.Line, n)
	out := filepath.Join(in.Queue, "pending", newName)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(out, []byte(Stamp(body, in.Version)), 0o644); err != nil {
		return "", err
	}
	if err := AppendRecut(in.Queue, RecutRow{SHA8: BodySHA8(old), From: oldName, To: newName, Kind: in.Sig.Kind}); err != nil {
		return "", err
	}
	if err := carryAttempts(in.Queue, oldName, newName); err != nil {
		return "", err
	}
	now := in.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	row := AttemptRow{At: now().UTC().Format(time.RFC3339), Kind: in.Sig.Kind, Line: in.Sig.Line}
	if err := AppendAttempt(in.Queue, oldName, row); err != nil {
		return "", err
	}
	if err := AppendAttempt(in.Queue, newName, row); err != nil {
		return "", err
	}
	return out, nil
}

// recutBody renumbers line 1 and puts the cause line directly under the card's SOURCE line,
// above the steps, where a worker reads it before it reads anything else.
func recutBody(old, cause string, n int) string {
	body := StampBody(old)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) == 0 {
		return body
	}
	lines[0] = cardNumberPattern.ReplaceAllString(lines[0], fmt.Sprintf("CARD-%d", n))
	at := 1
	if len(lines) > 1 && strings.HasPrefix(lines[1], "SOURCE:") {
		at = 2
	}
	if strings.TrimSpace(cause) != "" {
		rest := append([]string{cause}, lines[at:]...)
		lines = append(lines[:at:at], rest...)
	}
	return strings.Join(lines, "\n") + "\n"
}

// cardNumberPattern is line 1's own number, the only number a re-cut rewrites.
var cardNumberPattern = regexp.MustCompile(`CARD-\d+`)

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// HarnessEvidence reads what a bench left behind for one card: the harness log, and whether
// a RESULT.md was written. A bench that left nothing reads as nothing, which is `unknown`.
func HarnessEvidence(queue, card string) (log []byte, resultPresent bool) {
	base := strings.TrimSuffix(card, ".md")
	log, _ = os.ReadFile(filepath.Join(queue, "harness", base+".log"))
	if _, err := os.Stat(filepath.Join(queue, "harness", base+".RESULT.md")); err == nil {
		resultPresent = true
	}
	return log, resultPresent
}
