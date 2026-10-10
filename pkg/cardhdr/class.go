package cardhdr

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The classes a card's CLASS line may carry (docs/SPEC-SPRINT.md, the script read): a model
// card is worked, and read, by models; a script card is mechanical, made by a program its
// SCRIPT line names, so its read may be asked of a script reader before any model.
const (
	ClassModel  = "model"
	ClassScript = "script"
)

// Class is what a card's brief says of its class: `CLASS: model|script`, and for a script
// card `SCRIPT: <command>` (run from the repository root at the start commit) and the
// brief's `DEADLINE:` line, which bounds that run. A brief with no CLASS line is a model
// card. ReadClass is the one parser of these lines; the member's script reader reads the
// brief through it only.
type Class struct {
	Name     string        // ClassModel or ClassScript
	Script   string        // the program's command line, "" for a model card
	Deadline time.Duration // the card's deadline, 0 when its DEADLINE line names none
}

// IsScript says the card is a script card with a program to run.
func (c Class) IsScript() bool { return c.Name == ClassScript && c.Script != "" }

// deadlineRE is a DEADLINE value: `finish within <n> <unit>` or a bare `<n> <unit>`.
var deadlineRE = regexp.MustCompile(`(?i)^(?:finish within\s+)?(\d+)\s*(seconds?|secs?|s|minutes?|mins?|m|hours?|h)$`)

// ReadClass reads a brief's class lines from its header: the `KEY: value` lines at column 0
// before the first `STEP` line, outside a fenced block (a step's own `SCRIPT: regex|go|lisp`
// is the card tree's and is never read here). The first CLASS, SCRIPT and DEADLINE lines
// win. why is "" or the one line naming what is wrong and what to write; with a why, c is
// what could be read, and a card whose class is unreadable is a model card.
func ReadClass(brief string) (c Class, why string) {
	c.Name = ClassModel
	var class, script, deadline string
	fenced := false
	for _, l := range strings.Split(brief, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if strings.HasPrefix(l, "STEP ") {
			break
		}
		k, v, ok := KeyValue(l)
		if !ok {
			continue
		}
		switch strings.ToUpper(k) {
		case "CLASS":
			if class == "" {
				class = strings.ToLower(v)
			}
		case "SCRIPT":
			if script == "" {
				script = v
			}
		case "DEADLINE":
			if deadline == "" {
				deadline = v
			}
		}
	}
	var problems []string
	switch class {
	case "", ClassModel:
		if script != "" {
			problems = append(problems, "SCRIPT: "+script+" names a program on a card whose class is model; write CLASS: script or drop the SCRIPT line")
		}
		return c, strings.Join(problems, "; ")
	case ClassScript:
	default:
		return c, "CLASS: " + class + " is not " + ClassModel + " or " + ClassScript
	}
	if script == "" {
		return c, "CLASS: script names no program; write SCRIPT: <command> (run from the repository root at the start commit)"
	}
	c.Name, c.Script = ClassScript, script
	if m := deadlineRE.FindStringSubmatch(deadline); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := time.Second
		switch strings.ToLower(m[2])[0] {
		case 'm':
			unit = time.Minute
		case 'h':
			unit = time.Hour
		}
		c.Deadline = time.Duration(n) * unit
	}
	return c, ""
}
