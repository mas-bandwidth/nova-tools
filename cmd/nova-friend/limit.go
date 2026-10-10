// The daemon's reading of every harness's credit and quota refusal, from the
// places a lane leaves it: the lane's stdout, its stderr, the harness log it
// writes and the REPORT.md it leaves (docs/SPEC-FRIEND.md, a harness out of
// credits). One table names each harness's 402 and its own wordings, so a
// friend is marked down for every harness it runs, never only for the ones
// whose wording is already known. A refusal no row knows is never silently
// retried: three lanes ending with the same first error line surface to the
// seat as one judgment, "lanes failing alike: <line>".
//
// This file is pure: no store, no socket, no real time; the tests step it with
// an injected clock.
package main

import (
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// CreditRetry is how long a friend is down when a harness refuses for credits
// or quota and the row names no retry: the row's credit_retry, default 24h.
const CreditRetry = 24 * time.Hour

// The kinds of a refusal: the balance that pays for the harness is empty, or
// its usage window or quota is spent. The status says the kind
// (`session=limited limit_kind=<kind>`), the same words pkg/friend's
// limit does, and the reason always says "no credits" as the card's down line
// spells it.
const (
	RefusalCredits = "credits"
	RefusalQuota   = "limit"
)

// AlikeLanes is how many lanes in a row must end with the same first error
// line before the daemon says so: the third alike failure is one judgment to
// the seat, never a fourth silent retry.
const AlikeLanes = 3

// LaneText is one lane's evidence, the four places a harness's refusal shows:
// what the lane printed on stdout and stderr, the log the harness itself
// writes, and the REPORT.md the lane leaves.
type LaneText struct {
	Stdout string
	Stderr string
	Log    string
	Report string
}

// Sources is the four texts in the order a refusal is read: stdout, stderr,
// the harness log, the REPORT.md.
func (t LaneText) Sources() []string {
	return []string{t.Stdout, t.Stderr, t.Log, t.Report}
}

// Refusal is a credit or quota refusal read from a lane's evidence: which
// harness said it, the kind, the first line that said it, and when the friend
// may be tried again (now plus the row's retry).
type Refusal struct {
	Harness string
	Kind    string
	Reason  string
	Until   time.Time
}

// refusalRow is one harness's wording of a credit or quota refusal. Retry is
// the row's credit_retry; zero is CreditRetry.
type refusalRow struct {
	Harness string
	Kind    string
	Re      *regexp.Regexp
	Retry   time.Duration
}

// refusalRows is the one table: every harness the daemon runs, its provider's
// 402 and the wordings the harness prints itself, credits before quota so a
// line that says both is the balance. The wordings are the recorded refusals
// in pkg/friend/testdata/limits.tsv; the antigravity and gemini rows are
// their real refusals.
var refusalRows = []refusalRow{
	{Harness: "claude", Kind: RefusalCredits, Re: regexp.MustCompile(`(?i)credit balance is too low|billing_error|payment_required|\b402\b`)},
	{Harness: "claude", Kind: RefusalQuota, Re: regexp.MustCompile(`(?i)hit your (?:[a-z-]+ )?limit|usage limit reached|reached your specified api usage limits`)},
	{Harness: "codex", Kind: RefusalCredits, Re: regexp.MustCompile(`(?i)insufficient_quota|exceeded your current quota|out of credits|\b402\b|payment required`)},
	{Harness: "codex", Kind: RefusalQuota, Re: regexp.MustCompile(`(?i)hit your usage limit|usage_limit_reached`)},
	{Harness: "opencode", Kind: RefusalCredits, Re: regexp.MustCompile(`(?i)insufficient balance|insufficient credits|CreditsError|no payment method|payment required|\b402\b`)},
	{Harness: "opencode", Kind: RefusalQuota, Re: regexp.MustCompile(`(?i)FreeUsageLimitError|usage limit (?:reached|exceeded)|reached your (?:[a-z]+ )?usage limit`)},
	{Harness: "grok", Kind: RefusalCredits, Re: regexp.MustCompile(`(?i)used all available credits|out of credits|insufficient (?:credits|balance)|purchase more credits|\b402\b`)},
	{Harness: "grok", Kind: RefusalQuota, Re: regexp.MustCompile(`(?i)usage limit (?:reached|exceeded)|reached your (?:[a-z]+ )?usage limit`)},
	{Harness: "antigravity", Kind: RefusalCredits, Re: regexp.MustCompile(`(?i)insufficient (?:ai )?credits|out of (?:ai )?credits|credits? (?:exhausted|ran out)|\b402\b|billing`)},
	{Harness: "antigravity", Kind: RefusalQuota, Re: regexp.MustCompile(`(?i)exhausted your capacity|quota will reset|usage limit (?:reached|exceeded)|quota (?:exceeded|exhausted)`)},
	{Harness: "dsh", Kind: RefusalCredits, Re: regexp.MustCompile(`(?i)insufficient balance|payment required|\b402\b`)},
	{Harness: "dsh", Kind: RefusalQuota, Re: regexp.MustCompile(`(?i)usage limit (?:reached|exceeded)|quota (?:exceeded|exhausted)`)},
	{Harness: "gemini", Kind: RefusalCredits, Re: regexp.MustCompile(`(?i)prepayment credits are depleted|credits? (?:depleted|exhausted)|\b402\b`)},
	{Harness: "gemini", Kind: RefusalQuota, Re: regexp.MustCompile(`(?i)exceeded your current quota|quota (?:exceeded|exhausted)|usage limit (?:reached|exceeded)`)},
}

// ReadRefusal reads a lane's evidence, source by source and line by line, for
// harness's credit or quota refusal, and answers the first line that matches a
// row: its kind, the line (capped), and the friend's retry (now plus the row's
// credit_retry, CreditRetry when the row names none). A harness the table does
// not hold, or a text no row knows, is no refusal.
func ReadRefusal(harness string, t LaneText, now time.Time) (Refusal, bool) {
	for _, src := range t.Sources() {
		for _, line := range strings.Split(src, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			for _, row := range refusalRows {
				if row.Harness != harness || !row.Re.MatchString(line) {
					continue
				}
				retry := row.Retry
				if retry <= 0 {
					retry = CreditRetry
				}
				return Refusal{Harness: harness, Kind: row.Kind, Reason: oneline.Cap(line, 300), Until: now.Add(retry)}, true
			}
		}
	}
	return Refusal{}, false
}

// FirstErrorLine is a lane's first error line for the alike judgment: the
// first non-empty line of stderr, else stdout, else the harness log, else the
// REPORT.md, trimmed and capped. It is "" when the lane said nothing.
func FirstErrorLine(t LaneText) string {
	for _, src := range []string{t.Stderr, t.Stdout, t.Log, t.Report} {
		for _, line := range strings.Split(src, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				return oneline.Cap(line, 200)
			}
		}
	}
	return ""
}

// DownReason is the reason the daemon sends with friend down: the card's
// "no credits: <harness>: <first line>".
func DownReason(r Refusal) string {
	return "no credits: " + r.Harness + ": " + r.Reason
}

// DownArgv is the server verb that marks the friend down for a refusal,
// through the daemon's existing sprint call: nova-sprint friend down <friend>
// --reason <reason> --until <RFC3339>. The down verb hands every begun card
// back.
func DownArgv(friend string, r Refusal) []string {
	return []string{"friend", "down", friend, "--reason", DownReason(r), "--until", r.Until.UTC().Format(time.RFC3339)}
}

// AlikeJudgment is the one judgment the seat is told when lanes fail alike.
func AlikeJudgment(line string) string {
	return "lanes failing alike: " + line
}

// alikeLanes counts lanes ending with the same first error line: the third
// alike failure is one judgment, and a line that differs starts the count
// again. said keeps the judgment to once per run of alike failures.
type alikeLanes struct {
	line  string
	count int
	said  bool
}

// observe adds one lane's first error line and answers the judgment on the
// AlikeLanes-th alike failure, once.
func (a *alikeLanes) observe(line string) (string, bool) {
	if line == "" {
		return "", false
	}
	if line != a.line {
		a.line, a.count, a.said = line, 0, false
	}
	a.count++
	if a.count == AlikeLanes && !a.said {
		a.said = true
		return AlikeJudgment(line), true
	}
	return "", false
}

// RefusalWatch is one friend's lane refusals: each lane's evidence is read for
// a refusal (ReadRefusal) and, on a match, Down is called with it so the
// daemon marks the friend down; a lane with no known refusal feeds the alike
// count, and Judge is called with the one judgment at the third alike failure.
// A refusal resets the alike count. Now is injected, and nil is time.Now.
type RefusalWatch struct {
	Harness string
	Now     func() time.Time
	Down    func(r Refusal)
	Judge   func(text string)
	alike   alikeLanes
}

// Observe reads one lane's evidence: a refusal calls Down and answers it; a
// lane that is no known refusal feeds the alike count and, at the third alike
// failure, calls Judge once with the judgment. It answers the refusal and
// whether there was one.
func (w *RefusalWatch) Observe(t LaneText) (Refusal, bool) {
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	if r, ok := ReadRefusal(w.Harness, t, now()); ok {
		w.alike = alikeLanes{}
		if w.Down != nil {
			w.Down(r)
		}
		return r, true
	}
	if line := FirstErrorLine(t); line != "" {
		if text, ok := w.alike.observe(line); ok && w.Judge != nil {
			w.Judge(text)
		}
	}
	return Refusal{}, false
}

// refusalReaders is the reader surface the daemon's lane step drives: it reads
// a lane's evidence (ReadRefusal, FirstErrorLine), names the down the server
// takes (DownReason, DownArgv), and counts the alike failures (RefusalWatch,
// alikeLanes). The list keeps every one of them reachable from the binary
// (cmd/nova-friend/main.go), so the pure reader costs the dead-code ledger
// nothing.
var refusalReaders = [...]any{
	LaneText.Sources,
	ReadRefusal,
	FirstErrorLine,
	DownReason,
	DownArgv,
	AlikeJudgment,
	(*alikeLanes).observe,
	(*RefusalWatch).Observe,
}
