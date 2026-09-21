package pulse

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// Attempt3WayApply attempts `git apply --3way` of diffPath in repoDir,
// applying the patch to repoDir so the job starts from an applied tree.
// It checks output and repo state for conflict markers/messages rather than relying
// solely on exit code. It returns "clean" if the patch applies without conflict,
// or "conflict" otherwise.
func Attempt3WayApply(repoDir, diffPath string) string {
	absDiff, err := filepath.Abs(diffPath)
	if err != nil {
		absDiff = diffPath
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", repoDir, "apply", "--3way", absDiff)
	out, err := cmd.CombinedOutput()
	outStr := strings.ToLower(string(out))

	hasConflict := strings.Contains(outStr, "conflict") ||
		strings.Contains(outStr, "<<<<<<<") ||
		strings.Contains(outStr, "=======") ||
		strings.Contains(outStr, ">>>>>>>") ||
		strings.Contains(outStr, "leftover conflict marker")

	if !hasConflict && err == nil {
		chkCmd := exec.CommandContext(ctx, "git", "-C", repoDir, "diff", "--check")
		chkOut, chkErr := chkCmd.CombinedOutput()
		if chkErr != nil || strings.Contains(strings.ToLower(string(chkOut)), "conflict marker") {
			hasConflict = true
		}
		statusCmd := exec.CommandContext(ctx, "git", "-C", repoDir, "status", "--porcelain")
		statusOut, _ := statusCmd.CombinedOutput()
		for _, line := range strings.Split(string(statusOut), "\n") {
			if strings.HasPrefix(line, "UU ") || strings.HasPrefix(line, "AA ") || strings.HasPrefix(line, "UD ") || strings.HasPrefix(line, "DU ") {
				hasConflict = true
				break
			}
		}
	}

	if err == nil && !hasConflict {
		return "clean"
	}
	return "conflict"
}

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
	if in.Paths == "" {
		in.Paths = h.Paths
	}
	if in.TestName == "" {
		in.TestName = h.Test
	}
	in.HoldLine = h.Line
	if in.Remains == "" {
		in.Remains = h.Remains
	}
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

// recutInstruction writes instructions for recutting work.
// If a prior diff was applied, it inlines instructions for applied: clean vs conflict
// and includes the diff if within size cap (64 KB).
func recutInstruction(in CutKindInput, diffContent string) string {
	var b strings.Builder
	if in.Applied == "clean" {
		b.WriteString("The prior diff applied cleanly onto the tip (applied: clean).\n")
		b.WriteString("Run the tests, and fix only what is red.\n")
	} else if in.Applied == "conflict" {
		b.WriteString("The prior diff had conflicts when applied onto the tip (applied: conflict).\n")
		b.WriteString("Resolve every conflict keeping both sides, and make the tests green.\n")
	}

	if in.HoldLine != "" || in.Base != "" || in.BaseSHA != "" || in.Paths != "" || in.TestName != "" || in.Remains != "" {
		testLine := "Write the remaining work red first: the named test must fail on the base, then the fix, then green."
		if t := strings.TrimSpace(in.TestName); t != "" {
			pkg, name, ok := strings.Cut(t, " ")
			if ok && pkg != "" && name != "" {
				testLine = fmt.Sprintf("Write the named test red first. Run: go test %s -run %s — it must fail on the base, then the fix, then green.", pkg, name)
			} else {
				testLine = fmt.Sprintf("Write the named test red first: %s — it must fail on the base, then the fix, then green.", t)
			}
		}
		if in.Applied != "" {
			b.WriteString("\n")
		}
		remainsLine := ""
		if r := strings.TrimSpace(in.Remains); r != "" {
			remainsLine = fmt.Sprintf("Remains: %s\n", r)
		}
		fmt.Fprintf(&b, `Recut the remaining work named in the HOLD onto BASE %s at base-sha %s.
PATHS and the failing test come from that HOLD; they are the declared scope.
%s%s
`, oneline.Field(in.Base), oneline.Field(in.BaseSHA), remainsLine, testLine)
	}

	dbytes := len(diffContent)
	if dbytes > 0 && dbytes <= 64000 {
		fmt.Fprintf(&b, "\nTHE PRIOR DIFF, inline and complete (%d bytes):\n```diff\n%s\n```\n", dbytes, strings.TrimRight(diffContent, "\n"))
	} else if dbytes > 64000 {
		fmt.Fprintf(&b, "\nThe prior diff is %d bytes, over the 64000-byte inline cap; omitted from the card body.\n", dbytes)
	}

	fmt.Fprintf(&b, "Write RESULT.md: line 1 exactly the line 1 of this card, line 2 DONE or ABSTAIN <why>, then BRANCH <name> and REPO %s.\n", in.Repo)
	return b.String()
}
