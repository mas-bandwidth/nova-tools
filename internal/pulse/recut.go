package pulse

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// holdCard is the typed HOLD a recut is cut from: the DISPOSITION line, the
// named remains, and the BASE / base-sha the card header carries.
type holdCard struct {
	Line    string
	Who     string
	Head    string
	Verdict string
	Paths   string
	Test    string
	Base    string
	BaseSHA string
	Remains string
}

// parseHoldFile reads a typed HOLD. A quoted or fenced DISPOSITION is not a
// HOLD. A HOLD without a named remains (PATHS:, TEST:, or REMAINS:) cannot
// cut a recut card: there is no remaining work to name.
func parseHoldFile(raw string) (holdCard, string) {
	clean := merge.StripQuotedAndCode(raw)
	var h holdCard
	for _, line := range strings.Split(strings.ReplaceAll(clean, "\r\n", "\n"), "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if who, head, verdict, _, ok := merge.ParseDispositionLine(trim); ok && h.Line == "" {
			h.Line = trim
			h.Who, h.Head, h.Verdict = who, head, verdict
			continue
		}
		if v, ok := headerValue(trim, "PATHS"); ok {
			h.Paths = v
			continue
		}
		if v, ok := headerValue(trim, "TEST"); ok {
			h.Test = v
			continue
		}
		if v, ok := headerValue(trim, "BASE"); ok {
			b, s := parseBaseValue(v)
			h.Base = b
			if s != "" && h.BaseSHA == "" {
				h.BaseSHA = s
			}
			continue
		}
		if v, ok := headerValue(trim, "base-sha"); ok {
			h.BaseSHA = v
			continue
		}
		if v, ok := headerValue(trim, "REMAINS"); ok {
			h.Remains = v
		}
	}
	switch {
	case h.Line == "":
		return holdCard{}, "a recut is cut from a typed HOLD; --hold-file has no DISPOSITION line (pass a file whose unquoted body has DISPOSITION who=<name> head=<sha> verdict=HOLD)"
	case h.Verdict != "HOLD":
		return holdCard{}, fmt.Sprintf("a recut is cut from a typed HOLD; --hold-file verdict=%s (pass verdict=HOLD)", oneline.Field(h.Verdict))
	case strings.TrimSpace(h.Who) == "" || strings.TrimSpace(h.Head) == "":
		return holdCard{}, "a recut is cut from a typed HOLD; --hold-file DISPOSITION is missing who= or head= (pass DISPOSITION who=<name> head=<sha> verdict=HOLD)"
	case strings.TrimSpace(h.Paths) == "" && strings.TrimSpace(h.Test) == "" && strings.TrimSpace(h.Remains) == "":
		return holdCard{}, "HOLD has no named remains (name PATHS: and TEST: on the HOLD, the remaining work this recut is measured by)"
	}
	return h, ""
}

func applyHold(in CutKindInput, h holdCard) CutKindInput {
	if strings.TrimSpace(in.Head) == "" {
		in.Head = h.Head
	}
	if strings.TrimSpace(in.Base) == "" {
		in.Base = h.Base
	}
	if strings.TrimSpace(in.BaseSHA) == "" {
		in.BaseSHA = h.BaseSHA
	}
	in.Paths = h.Paths
	in.TestName = h.Test
	in.HoldLine = h.Line
	in.Remains = h.Remains
	return in
}

func headerValue(line, key string) (string, bool) {
	trim := strings.TrimSpace(line)
	if len(trim) < len(key)+1 {
		return "", false
	}
	if !strings.EqualFold(trim[:len(key)], key) || trim[len(key)] != ':' {
		return "", false
	}
	return strings.TrimSpace(trim[len(key)+1:]), true
}

// parseBaseValue reads BASE: <branch> or BASE: <branch>@<sha>, dropping a
// trailing parenthetical so `dev@abc (every anchor…)` still splits.
func parseBaseValue(v string) (base, sha string) {
	v = strings.TrimSpace(v)
	if i := strings.Index(v, " ("); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	if i := strings.LastIndex(v, "@"); i > 0 {
		maybe := v[i+1:]
		if isHoldSHA(maybe) {
			return v[:i], maybe
		}
	}
	return v, ""
}

func isHoldSHA(s string) bool {
	if len(s) < 7 {
		return false
	}
	for _, r := range s {
		if !unicode.Is(unicode.ASCII_Hex_Digit, r) {
			return false
		}
	}
	return true
}

// recutInstruction is the one paragraph a recut always carries: remaining work
// from the HOLD, red first, onto the named BASE. It does not apply a prior diff.
func recutInstruction(in CutKindInput) string {
	testLine := "Write the remaining work red first: the named test must fail on the base, then the fix, then green."
	if t := strings.TrimSpace(in.TestName); t != "" {
		pkg, name, ok := strings.Cut(t, " ")
		if ok && pkg != "" && name != "" {
			testLine = fmt.Sprintf("Write the named test red first. Run: go test %s -run %s — it must fail on the base, then the fix, then green.", pkg, name)
		} else {
			testLine = fmt.Sprintf("Write the named test red first: %s — it must fail on the base, then the fix, then green.", t)
		}
	}
	return fmt.Sprintf(`Recut the remaining work named in the HOLD onto BASE %s at base-sha %s.
PATHS and the failing test come from that HOLD; they are the declared scope.
%s
Write RESULT.md: line 1 exactly the line 1 of this card, line 2 DONE or ABSTAIN <why>, then BRANCH <name> and REPO %s.
`, oneline.Field(in.Base), oneline.Field(in.BaseSHA), testLine, in.Repo)
}
