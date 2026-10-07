package friend

import (
	"cmp"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// A reworked card's brief opens with its fix (the night of 2026-10-05: lint-pkg-tlc-tbb came
// back five times, sec-rocketnet-server-dos-zhi four and presence-from-session-only five, each
// with the same finding: the server puts a rework's fix on a 'The coordinator asks:' line
// under the start, over a long card whose own STOP is the whole card's, and the next lane
// read the card and never reached the fix). The daemon writes such a brief (ReworkedBrief)
// with THE ONE THING LEFT, the fix, as the first line after STATUS and the reader's finding
// as the second, then the work it carries (the head of the attempt before, onto this
// attempt's branch) and how its report is checked, before RULES and the task; the card's
// STOP becomes the fix alone. A LAND whose report does not carry the fix's key words
// (FixAddressed) is finished as a HOLD by the outbox pass, so the same finding is never read
// twice (docs/SPEC-FRIEND.md, a reworked brief opens with the fix).

// The labels of a reworked brief's first lines, and of the server's lines it is made from.
const (
	OneThingLeft     = "THE ONE THING LEFT: "
	ReaderFoundLabel = "The reader found: "
	carriedLabel     = "The carried work: "
	checkedLabel     = "How it is checked: "
	asksLabel        = "The coordinator asks: "
	foundLabel       = "A reader found: "
	whyLabel         = "This attempt exists because: "
	startLabel       = "This attempt starts from "
	workInLabel      = "Work in "
)

// preludeLabels start the lines of a brief's prelude (STATUS to its first blank line); a
// prelude line that starts with none continues the one before it.
var preludeLabels = []string{OneThingLeft, ReaderFoundLabel, carriedLabel, checkedLabel, asksLabel, foundLabel, whyLabel, startLabel, workInLabel}

// MaxFixKeyWords bounds the key words a report is grepped for: a long fix is checked by its
// first ones.
const MaxFixKeyWords = 8

// preludeLine is one line of a brief's prelude, its continuation lines joined to it.
type preludeLine struct{ label, text string }

// splitBrief is a brief's STATUS line, its prelude (the lines after STATUS up to the first
// blank line) and the rest after that blank line (the card as the server hands it).
func splitBrief(brief string) (status string, prelude []preludeLine, body string, ok bool) {
	status, rest, _ := strings.Cut(brief, "\n")
	if !strings.HasPrefix(status, "STATUS: nova-sprint card ") {
		return "", nil, "", false
	}
	head, body, _ := strings.Cut(rest, "\n\n")
	for _, l := range strings.Split(head, "\n") {
		label := ""
		for _, p := range preludeLabels {
			if strings.HasPrefix(l, p) {
				label = p
				break
			}
		}
		if label == "" && len(prelude) > 0 {
			prelude[len(prelude)-1].text += "\n" + l
			continue
		}
		prelude = append(prelude, preludeLine{label, strings.TrimPrefix(l, label)})
	}
	return status, prelude, body, true
}

// BriefFix is the fix a brief's prelude asks of its attempt: its THE ONE THING LEFT line, else
// the server's 'The coordinator asks:' line, on one line; "" for a brief that asks none.
func BriefFix(brief string) string {
	_, prelude, _, ok := splitBrief(brief)
	if !ok {
		return ""
	}
	fix := ""
	for _, p := range prelude {
		switch p.label {
		case OneThingLeft:
			return oneLine(p.text, len(p.text))
		case asksLabel:
			if fix == "" {
				fix = oneLine(p.text, len(p.text))
			}
		}
	}
	return fix
}

// ReworkedBrief is a reworked card's brief with its fix first: STATUS; THE ONE THING LEFT,
// the fix; The reader found, the finding (or why the attempt exists when no reader found
// anything); the carried head and the branch it is carried onto; how the report is checked;
// then the rest of the prelude as the server wrote it, and the card with its STOP the fix
// alone. A brief that asks no fix, and one already reworked, are answered as they are.
func ReworkedBrief(brief string) string {
	status, prelude, body, ok := splitBrief(brief)
	if !ok {
		return brief
	}
	var fix, finding, why, start string
	var kept []preludeLine
	for _, p := range prelude {
		switch p.label {
		case OneThingLeft:
			return brief
		case asksLabel:
			fix = oneLine(p.text, len(p.text))
		case foundLabel:
			finding = oneLine(p.text, len(p.text))
		default:
			switch p.label {
			case whyLabel:
				why = oneLine(p.text, len(p.text))
			case startLabel:
				start = p.text
			}
			kept = append(kept, p)
		}
	}
	if fix == "" {
		return brief
	}
	if finding == "" {
		finding = "no reader's finding; " + cmp.Or(why, "the attempt before was sent back")
	}
	branch := ""
	if m := statusBranchRE.FindStringSubmatch(status); m != nil {
		branch = m[1]
	}
	var b strings.Builder
	b.WriteString(status + "\n")
	b.WriteString(OneThingLeft + fix + "\n")
	b.WriteString(ReaderFoundLabel + finding + "\n")
	b.WriteString(carriedLabel + carriedText(start, branch) + "\n")
	b.WriteString(checkedLabel + checkedText(fix) + "\n")
	for _, p := range kept {
		b.WriteString(p.label + p.text + "\n")
	}
	b.WriteString("\n" + stopIsTheFix(body, fix))
	return b.String()
}

// carriedText says the work this attempt carries: the head of the attempt before (off the
// start line, its 'Carry the work of attempt N onto it yourself: its head, <sha>,'), carried
// onto this attempt's branch, or that nothing is carried.
func carriedText(start, branch string) string {
	onto := cmp.Or(branch, "your branch")
	const carry, its = "Carry the work of attempt ", " onto it yourself: its head, "
	if _, after, ok := strings.Cut(start, carry); ok {
		if n, rest, ok := strings.Cut(after, its); ok {
			if sha, _, ok := strings.Cut(rest, ","); ok && fullSha.MatchString(sha) {
				return fmt.Sprintf("attempt %s's head %s, carried onto %s from the tip of its base; the Head you report is on %s and holds that work and the fix.", n, sha, onto, onto)
			}
		}
	}
	return fmt.Sprintf("nothing carried: no attempt before this one pushed work; start %s from the tip of its base.", onto)
}

// checkedText says how a report on the fix is read.
func checkedText(fix string) string {
	words := FixKeyWords(fix)
	if len(words) == 0 {
		return "your REPORT.md says how THE ONE THING LEFT is done, first."
	}
	return fmt.Sprintf("your REPORT.md says how THE ONE THING LEFT is done, first; a LAND whose report does not name at least %d of its key words (%s) is a HOLD by the daemon.", keyWordsNeeded(len(words)), strings.Join(words, ", "))
}

// stopIsTheFix is the card with its first STOP line the fix alone (a card with none is given
// one, first).
func stopIsTheFix(body, fix string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "STOP:") {
			lines[i] = "STOP: " + fix
			return strings.Join(lines, "\n")
		}
	}
	return "STOP: " + fix + "\n" + body
}

// fixStopWords are the words of a fix that say nothing of it.
var fixStopWords = map[string]bool{
	"about": true, "after": true, "again": true, "also": true, "before": true, "card": true, "does": true,
	"done": true, "each": true, "every": true, "from": true, "have": true, "into": true, "just": true,
	"make": true, "must": true, "only": true, "should": true, "sure": true, "that": true, "them": true,
	"then": true, "there": true, "their": true, "these": true, "they": true, "this": true, "those": true,
	"what": true, "when": true, "where": true, "which": true, "will": true, "with": true, "without": true,
	"your": true,
}

// FixKeyWords is what a report on fix is grepped for: its distinct words of four or more
// letters, digits or underscores, lower case, the empty ones (fixStopWords) left out, the
// first MaxFixKeyWords of them.
func FixKeyWords(fix string) []string {
	var words []string
	seen := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(fix), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}) {
		if len([]rune(w)) < 4 || fixStopWords[w] || seen[w] {
			continue
		}
		seen[w] = true
		if words = append(words, w); len(words) == MaxFixKeyWords {
			break
		}
	}
	return words
}

// keyWordsNeeded is how many of n key words a report names to address its fix: half, rounded up.
func keyWordsNeeded(n int) int { return (n + 1) / 2 }

// FixAddressed says a report addresses fix: it names (case-insensitively, anywhere) at least
// half of the fix's key words, rounded up; a fix with none is addressed. missing is the key
// words it does not name.
func FixAddressed(report, fix string) (missing []string, ok bool) {
	words := FixKeyWords(fix)
	low := strings.ToLower(report)
	for _, w := range words {
		if !strings.Contains(low, w) {
			missing = append(missing, w)
		}
	}
	return missing, len(words)-len(missing) >= keyWordsNeeded(len(words))
}

// UnaddressedText is the first words of the finish the holder by (the daemon, or friend sync)
// sends for a LAND whose report does not address THE ONE THING LEFT.
func UnaddressedText(by, fix string, missing []string) string {
	return fmt.Sprintf("held by %s: the report says LAND and does not address THE ONE THING LEFT (%s); the key words it does not name: %s.", by, oneLine(fix, 300), strings.Join(missing, ", "))
}

// The holders of an unaddressed LAND, as UnaddressedText names them.
const (
	HeldByDaemon     = "the daemon"
	HeldByFriendSync = "friend sync"
)

// UnaddressedLand is the one fix check of a LAND, the daemon's outbox pass's and friend
// sync's (cmd/nova-sprint, friendFinish, the finish of friend sync, friend reconcile and
// collect): held is UnaddressedText when report does not address the fix brief asks (BriefFix,
// in either form), "" when it does or when brief asks none. A held LAND is finished as a
// HOLD, its head kept.
func UnaddressedLand(by, report, brief string) (held string) {
	return unaddressed(by, report, BriefFix(brief))
}

// unaddressed is UnaddressedLand of a fix already read.
func unaddressed(by, report, fix string) string {
	if fix == "" {
		return ""
	}
	if missing, ok := FixAddressed(report, fix); !ok {
		return UnaddressedText(by, fix, missing)
	}
	return ""
}

// jobFix is the fix a job's brief asks: the BRIEF.md in her inbox, the brief the lane read,
// else the server's brief.
func jobFix(path, server string) string {
	if raw, err := os.ReadFile(path); err == nil {
		if fix := BriefFix(string(raw)); fix != "" {
			return fix
		}
	}
	return BriefFix(server)
}
