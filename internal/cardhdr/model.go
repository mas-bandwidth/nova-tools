package cardhdr

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Model is what a card's brief says of the model it runs on: the tier its line 1
// names (`... tier: flash|pro|frontier`), and a pin, the header lines under line 1
// `model: <provider>/<model>` with its `tokens: <n>|unmetered` and
// `deadline: <seconds>|<duration>` (both required with a pin, read only beside it). A pin bypasses the deal's draw among the tier's
// routes (docs/SPEC-SPRINT.md, the deal's route); the frame and the deal read the
// brief through ReadModel only, the one parser of these lines.
type Model struct {
	Tier     string // "" when line 1 names none
	Pin      string // provider/model, "" when the card pins none
	Tokens   string // a count or "unmetered", "" when the pin names none
	Deadline int    // seconds, 0 when the pin names none
}

// tierRE is the tier word on line 1.
var tierRE = regexp.MustCompile(`\btier:\s*([A-Za-z0-9_-]+)`)

// ReadModel reads a brief's model lines: the tier from line 1, the pin from the header
// block under it (the `key: value` lines up to the first blank line or line of prose,
// keys in any case). why is "" or the one line naming what is wrong and what to write;
// with a why, m is what could be read (its Tier as line 1 names it, known or not).
func ReadModel(brief string) (m Model, why string) {
	first, rest, _ := strings.Cut(brief, "\n")
	if t := tierRE.FindStringSubmatch(first); t != nil {
		m.Tier = t[1]
	}
	var problems []string
	if m.Tier != "" && !IsRoute(m.Tier) {
		problems = append(problems, "line 1 names tier "+m.Tier+"; want "+RouteList)
	}
	var tokens, deadline string
	for rest != "" {
		var l string
		l, rest, _ = strings.Cut(rest, "\n")
		k, v, ok := KeyValue(l)
		if !ok {
			break
		}
		switch strings.ToLower(k) {
		case "model":
			m.Pin = v
		case "tokens":
			tokens = v
		case "deadline":
			deadline = v
		}
	}
	if m.Pin == "" {
		// tokens: and deadline: belong to a pin; without one they are the brief's own
		// words (a card's `deadline:` line is the lint's), never read here
		tokens, deadline = "", ""
	} else if !IsModelID(m.Pin) {
		problems = append(problems, "model: "+m.Pin+" is not <provider>/<model>")
	}
	if tokens != "" {
		if n, err := strconv.Atoi(tokens); tokens != "unmetered" && (err != nil || n < 1) {
			problems = append(problems, "tokens: "+tokens+" is not a count of at least 1 or the word unmetered")
		}
		m.Tokens = tokens
	}
	if deadline != "" {
		n, err := strconv.Atoi(deadline)
		if err != nil {
			d, derr := time.ParseDuration(deadline)
			n, err = int(d/time.Second), derr
		}
		if err != nil || n < 1 {
			problems = append(problems, "deadline: "+deadline+" is not seconds or a duration of at least 1s")
		}
		m.Deadline = n
	}
	if m.Pin != "" && (tokens == "" || deadline == "") {
		// a pin is the whole route: a member with no override could not launch it
		var missing []string
		if tokens == "" {
			missing = append(missing, "tokens: <n>|unmetered")
		}
		if deadline == "" {
			missing = append(missing, "deadline: <seconds>")
		}
		problems = append(problems, "model: "+m.Pin+" pins the card without "+strings.Join(missing, " and ")+"; a pin carries its budget and deadline on the lines under it")
	}
	if len(problems) > 0 {
		return m, strings.Join(problems, "; ")
	}
	return m, ""
}

// EndProvider is how a member's failed finish begins when the provider failed the
// run: the member writes it (member.Judge) and the sprint's route stats count it.
const EndProvider = "provider failure"

// EndNoResult is how a member's failed finish begins when its child ended, by itself and
// within its budget and deadline, having written no result at all (no RESULT.md shape):
// no work came back, so the sprint deals the card again on another route, within the
// redeal bound, as it does a take the provider failed, and judges nothing (the owner,
// 2026-10-01, on six such judgments in one pass: "that's fine with me."). Work that came
// back and is wrong is still the coordinator's to judge.
const EndNoResult = "no result"

// IsModelID says id is `<provider>/<model>`: a provider word with no slash, then a
// model name that may hold slashes of its own, neither empty, no blank or tab.
func IsModelID(id string) bool {
	p, rest, ok := strings.Cut(id, "/")
	return ok && p != "" && rest != "" && !strings.ContainsAny(id, " \t")
}

// EndStaging is how a member's failed finish begins when its machine refused the launch at
// staging, before any child ran (no bench mirror, the pushed head missing): the member's
// failure, never the card's; the sprint deals the card to another member
// (tla/CardContract.tla, StageRefused).
const EndStaging = "staging refused"

// EndLaunch is how a member's failed finish begins when it could not launch a taken card
// (no model, budget or deadline from its packet or its override): the member's failure,
// never the card's, like a staging refusal.
const EndLaunch = "launch refused"

// EndPreExisting is how a member's failed finish begins when its child's gate was red only on
// failures the gate decision classed pre-existing (docs/SPEC-SPRINT.md section 5, the gate
// verdict): `pre-existing: <test>, ...`. The test fails without the card's change, at the base
// or on the machine, so the failure is the base's or the member's and never the card's: it has
// no failure class and is never the second identical failure (sprint.FailureClass).
const EndPreExisting = "pre-existing"

// EndNothing and EndNoCommit are how a member's failed finish begins when its child found
// nothing to do (verdict nothing) or committed nothing: no new work. The sprint returns
// such a rework to review at the head an earlier attempt pushed when a reader passed that
// head (docs/SPEC-SPRINT.md section 6), else it is failed work for the coordinator.
const (
	EndNothing  = "nothing to do"
	EndNoCommit = "no commit"
)

// RemainderKey begins the report of a tree card that finished ok at the step before its
// failed step: `remainder=<id>-r<n> step <n> <verdict>: <why>`, the card the coordinator adds
// for the rest (docs/SPEC-SPRINT.md, a card is a tree of steps). The sprint carries such a
// report on its "work came back ok" note.
const RemainderKey = "remainder="
