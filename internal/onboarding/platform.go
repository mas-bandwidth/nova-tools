package onboarding

import (
	"fmt"
	"strings"
)

// A transcript is real output pasted whole, so a section whose block carries a
// value only one platform produces cannot reproduce anywhere else. #1509 is the
// case: `nova-sandbox probe` prints `backend=sandbox-exec` and an empty `abi=` on
// macOS, and `backend=landlock` with `hosts=`, `gpu=`, `used=` and `ancestors=`
// on Linux -- fields the macOS transcript has no slot for. A reader on the wrong
// bench was invited to type it anyway.
//
// The section says which platforms it is for in ONE typed line, and the line is a
// GOOS LIST rather than a sentence, for three reasons a sentence cannot serve: a
// test can SKIP on it, by name; a class test can ask whether every platform named
// is a leg CI runs, so a skipped transcript is still executed somewhere; and a
// reader is told something they can check against their own `go env GOOS`.

// PlatformKey opens the one line a `## <tool>` section carries when its
// transcript does not reproduce everywhere.
const PlatformKey = "Platform:"

// platformNote is the dash that separates the GOOS list from the sentence a
// reader on the wrong bench is shown. The list is what a test reads; the note is
// what a person reads, and a bare list says which benches and never why.
const platformNote = "—"

// PlatformLine is a `Platform:` line read off a `## <tool>` section.
type PlatformLine struct {
	// GOOS is the platforms the section's transcript reproduces on, in the order
	// the line writes them. It is never empty in a line that parsed.
	GOOS []string
	// Note is the sentence after the dash: what the other benches print instead.
	Note string
}

// SectionPlatforms reads a tool's `Platform:` line out of its `## <tool>`
// section. A section with no such line reproduces everywhere, which is the
// ordinary case and is not an error; a line that IS there and does not parse is
// an error, because a line a test cannot act on is worse than none -- it reads
// like a promise and holds nothing.
//
// The whole section is read, not only `### First run`: nova-swarm's
// platform-specific transcript lives under a heading of its own, and the line
// belongs in the section's prose where a stranger meets it before the fence.
func SectionPlatforms(md, tool string) (PlatformLine, bool, error) {
	section, ok := Section(md, tool)
	if !ok {
		return PlatformLine{}, false, fmt.Errorf("the document has no `## %s` section", tool)
	}
	for _, line := range strings.Split(section, "\n") {
		text, found := strings.CutPrefix(strings.TrimSpace(line), PlatformKey)
		if !found {
			continue
		}
		p, err := ParsePlatformLine(text)
		if err != nil {
			return PlatformLine{}, true, fmt.Errorf("the `## %s` section's platform line %w", tool, err)
		}
		return p, true, nil
	}
	return PlatformLine{}, false, nil
}

// ParsePlatformLine parses the text after `Platform:`: a comma-separated GOOS
// list, then optionally the dash and the sentence that says what the other
// benches print instead.
func ParsePlatformLine(text string) (PlatformLine, error) {
	list, note, _ := strings.Cut(text, platformNote)
	p := PlatformLine{Note: strings.TrimSpace(note)}
	for _, field := range strings.Split(list, ",") {
		name := strings.TrimSpace(field)
		if name == "" {
			continue
		}
		if !isGOOS(name) {
			return PlatformLine{}, fmt.Errorf(
				"names %q, which is not a GOOS. The line is `%s <goos>[,<goos>]`, optionally then `%s` and the sentence that says what the other benches print -- a list a test can skip on and a class test can check against the legs CI runs, rather than a sentence only a person can read",
				name, PlatformKey, platformNote)
		}
		p.GOOS = append(p.GOOS, name)
	}
	if len(p.GOOS) == 0 {
		return PlatformLine{}, fmt.Errorf(
			"names no platform. The line is `%s <goos>[,<goos>]`: a section that cannot say which benches reproduce it is a transcript nobody can run and nobody can skip",
			PlatformKey)
	}
	return p, nil
}

// isGOOS reports whether a word is spelled the way `go env GOOS` spells one:
// lower-case letters and digits and nothing else. It holds no list of the
// platforms Go knows, because the platforms that matter here are the ones CI
// runs, and that is the class test's question rather than this parser's.
func isGOOS(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// Names returns the GOOS list as the line writes it, for a message.
func (p PlatformLine) Names() string { return strings.Join(p.GOOS, ", ") }

// Runs reports whether the section's transcript is this platform's to run.
func (p PlatformLine) Runs(goos string) bool {
	for _, name := range p.GOOS {
		if name == goos {
			return true
		}
	}
	return false
}

// SectionSkipReason is the sentence a transcript test skips with when this bench
// is not one the section names, and "" when the transcript is this bench's to
// run. A skip names the tool, the platforms the section is for and the platform
// this bench is, because a skip nobody can read is how a transcript stops being
// executed anywhere without anybody deciding that.
func SectionSkipReason(tool string, p PlatformLine, goos string) string {
	if len(p.GOOS) == 0 || p.Runs(goos) {
		return ""
	}
	reason := fmt.Sprintf(
		"docs/TESTS.md's `## %s` section is recorded for %s and this bench is %s, so its transcript is not run here",
		tool, p.Names(), goos)
	if p.Note != "" {
		return reason + ": " + p.Note
	}
	return reason
}
