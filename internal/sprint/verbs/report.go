package verbs

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// Report prints a verb's result in the command's shape (cmd/nova-sprint's
// report): a MOVED line for each id moved, a REFUSED line for each id
// refused ("<id>: <code> <why>") and a NOTWRITTEN line for each id of a
// refused step or part that was not written, at most max of each kind before
// a MORE line (0 prints all), then the verb's line, "<VERB> OK|FAIL moved=<n>
// refused=<n> notes=<n>" with notwritten=, op=, replay=yes and changed= when
// they apply, then the error's line. The counts are the Result's totals over
// the parts. It returns the exit code: 0 done, 1 refused (nothing of the
// refused step was changed), 2 unknown or failed.
func Report(stdout, stderr io.Writer, res Result, err error, max int) int {
	code := 0
	if len(res.Refused) > 0 || len(res.NotWritten) > 0 {
		code = 1
	}
	var rf *Refused
	switch {
	case err == nil:
	case errors.As(err, &rf):
		code = 1
	default:
		code = 2
	}
	unknown := errors.Is(err, tset.ErrOutcomeUnknown)
	var u *Unknown
	if errors.As(err, &u) {
		unknown = true
	}
	name := res.Verb
	if name == "" && rf != nil {
		name = rf.Verb
	}
	ReportIDs(stdout, stderr, res, max, name)
	status := "OK"
	if code != 0 {
		status = "FAIL"
	}
	fields := fmt.Sprintf("moved=%d refused=%d notes=%d", len(res.Moved), len(res.Refused), res.Notes)
	if len(res.NotWritten) > 0 {
		fields += fmt.Sprintf(" notwritten=%d", len(res.NotWritten))
	}
	if res.Op != "" {
		fields += " op=" + oneline.Escape(res.Op)
	}
	if res.Replay {
		fields += " replay=yes"
	}
	if err != nil {
		changed := "no"
		if unknown {
			changed = "unknown"
		} else if res.Parts > 0 {
			changed = "some"
		}
		fields += " changed=" + changed
	}
	out := stdout
	if code != 0 {
		out = stderr
	}
	fmt.Fprintf(out, "%s %s %s\n", token(name), status, fields)
	if err != nil {
		fmt.Fprintf(stderr, "nova-sprint %s: %s\n", name, oneline.Escape(err.Error()))
	}
	return code
}

// ReportIDs prints a result's id lines as Report does, and nothing else: a
// MOVED line on stdout for each id moved, a REFUSED line ("<id>: <code>
// <why>") and a NOTWRITTEN line on stderr for each id refused and each id of
// a refused step or part not written, at most max of each kind before a MORE
// line naming verb (0 prints all). A command that prints its own verb line
// prints these before it.
func ReportIDs(stdout, stderr io.Writer, res Result, max int, verb string) {
	listed(stdout, "MOVED", res.Moved, max, verb)
	why := make([]string, 0, len(res.Refused))
	for _, r := range res.Refused {
		why = append(why, r.ID+": "+strings.TrimSpace(r.Code+" "+r.Why))
	}
	listed(stderr, "REFUSED", why, max, verb)
	listed(stderr, "NOTWRITTEN", res.NotWritten, max, verb)
}

// token is a verb's name as the first word of its line.
func token(verb string) string {
	return strings.ToUpper(strings.ReplaceAll(verb, " ", "-"))
}

// listed prints at most max lines of a kind, then a MORE line.
func listed(w io.Writer, kind string, lines []string, max int, verb string) {
	for i, l := range lines {
		if max > 0 && i == max {
			fmt.Fprintf(w, "MORE kind=%s shown=%d total=%d run: nova-sprint %s ... --max 0\n", strings.ToLower(kind), max, len(lines), verb)
			return
		}
		fmt.Fprintf(w, "%s %s\n", kind, oneline.Escape(l))
	}
}
