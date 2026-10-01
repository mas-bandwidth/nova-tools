package cardhdr

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Model is what a card's brief says of the model it runs on: the tier its line 1
// names (`... tier: flash|pro|frontier`), and a pin, the header lines under line 1
// `model: <provider>/<model>` with an optional `tokens: <n>|unmetered` and
// `deadline: <seconds>|<duration>` (read only beside a model: line). A pin bypasses the deal's draw among the tier's
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
// keys in any case). why is "" or the one line naming what is wrong and what to write.
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
	if len(problems) > 0 {
		return Model{}, strings.Join(problems, "; ")
	}
	return m, ""
}

// EndProvider is how a member's failed finish begins when the provider failed the
// run: the member writes it (member.Judge) and the sprint's route stats count it.
const EndProvider = "provider failure"

// IsModelID says id is `<provider>/<model>`: a provider word with no slash, then a
// model name that may hold slashes of its own, neither empty, no blank or tab.
func IsModelID(id string) bool {
	p, rest, ok := strings.Cut(id, "/")
	return ok && p != "" && rest != "" && !strings.ContainsAny(id, " \t")
}
