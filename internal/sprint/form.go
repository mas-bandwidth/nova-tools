package sprint

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// A REPORT'S FORM IS CHECKED BY THE MACHINE, NEVER BY A READER (docs/SPEC-SPRINT.md, the
// report's form). The FORM: block's grammar, its parse and its check live in
// internal/swarm (swarm.ReadForm, swarm.CheckForm, swarm.DecideForm), the package the card
// runner already reads a brief's header with, so a worker's binary renders a skeleton
// without reaching a sprint's store. A run's finish reads the block with that grammar and
// refuses a report that misses it (cmd/nova-sprint's finish); this file is the sprint-side
// part: the work-card count the refusal stamps, the note a read's brief carries, and the
// refusal a broken-for-form verdict meets.

// FormRefusalsField is the work card field that counts the form refusals of one attempt:
// the third miss finishes the attempt FAIL.
const FormRefusalsField = "form_refusals"

// formPath is the report file a brief's FORM: line names, "" when the brief carries none.
// The full grammar is swarm.ReadForm's; this reads only the one header line the read-side
// helpers need, so internal/sprint does not depend on a worker's package.
func formPath(brief string) string {
	for _, l := range strings.Split(brief, "\n") {
		if k, v, ok := cardhdr.KeyValue(strings.TrimRight(l, "\r")); ok && k == "FORM" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// FormReadNote is the line a read's brief carries when the card under review has a FORM:
// block: the form passed the lint at the finish, so the reader judges the substance, and
// a broken-for-form verdict is not a verdict the ask accepts (the finish refusal is where
// form lives). "" when the card carries no form.
func FormReadNote(brief string) string {
	if formPath(brief) == "" {
		return ""
	}
	return "The form passed the lint; judge the substance. A broken-for-form verdict is not a verdict this read accepts: the finish refusal is where form lives."
}

// FormFinding is why a broken read's finding may not be taken: the card under review
// carries a FORM: block and the finding names the form's own report file as the defect,
// with nothing else to read. "" when the finding is about the work.
func FormFinding(brief, finding string) string {
	path := formPath(brief)
	if path == "" || !strings.Contains(finding, path) {
		return ""
	}
	// The form file named is the form's: a finding whose every file:line is that file
	// has nothing about the work to send back.
	lower, p := strings.ToLower(finding), strings.ToLower(path)
	rest := strings.ReplaceAll(lower, p, "")
	if strings.Contains(rest, ".md:") || strings.Contains(rest, ".go:") {
		return ""
	}
	return "the finding is about the report's form (" + path + "), which the lint checked at the finish: judge the substance, or name the work's own file:line"
}

// FormRefusalReq names the work cards one form miss refused.
type FormRefusalReq struct {
	IDs []string
}

// FormRefusal stamps one form refusal on each named working card and changes nothing else:
// the finish was refused, so the attempt is not spent and no read is asked, and the next
// finish reads the count to know when a third miss finishes the attempt FAIL
// (swarm.DecideForm). A card that is not working is refused and changes nothing.
func FormRefusal(s *Snapshot, r FormRefusalReq) Plan {
	var p Plan
	for _, id := range r.IDs {
		c := s.Fleet.Card(id)
		if c == nil || !c.Placed() || c.Col != Working {
			p.refuse(id, "no working card to hold the form refusal")
			continue
		}
		n := c.Int(FormRefusalsField) + 1
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"),
			Changes: []Change{change(Fleet, setEntry(c, map[string]string{FormRefusalsField: itoa(n)}))},
			Moved:   c.ID + " form refusal " + itoa(n) + " (no attempt spent, no read asked)"})
	}
	return p
}

// HeadFileMiss is why the named report was not the blob at the finish's head.
// A file missing from the process working directory is not the report, so the
// text names the commit, or that the finish named none.
func HeadFileMiss(head string, err error) string {
	msg := ""
	if err != nil {
		msg = strings.TrimSpace(err.Error())
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = strings.TrimSpace(msg[:i])
		}
	}
	if msg == "" {
		msg = "the blob was not read"
	}
	if head == "" {
		return "not at a head (" + msg + "); the finish names no commit (--head), and a file in the process directory is not the report"
	}
	return "not in " + head + " (" + msg + "); a file in the process directory is not the report"
}
