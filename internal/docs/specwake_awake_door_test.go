package docs

import (
	"os"
	"strings"
	"testing"
)

// docs/SPEC-WAKE.md is the normative document; cmd/nova-wake is its
// implementation. #1451 put the door `; run: nova-wake help` on the awake
// world-refusals, but the prose that teaches a reader that shape was not
// amended, so the spec still described the pre-#1451 line. The door literal
// is READ OUT OF THE SOURCE, so the pin follows the code if the wording
// changes. The control exists because `refused()` -- `watch`'s printer --
// still has no door, and a spec sentence that over-claimed would be worse
// than the stale one.
func TestSpecWakeSaysTheAwakeWorldRefusalsCarryTheDoor(t *testing.T) {
	src, err := os.ReadFile("../../cmd/nova-wake/main.go")
	if err != nil {
		t.Fatalf("read cmd/nova-wake/main.go: %v", err)
	}
	door := awakeDoorLiteral(t, string(src))

	spec, err := os.ReadFile("../../docs/SPEC-WAKE.md")
	if err != nil {
		t.Fatalf("read docs/SPEC-WAKE.md: %v", err)
	}
	text := string(spec)

	para := ""
	for _, p := range strings.Split(text, "\n\n") {
		if strings.Contains(p, "`AWAKE REFUSED` the shape for the") {
			para = p
			break
		}
	}
	if para == "" {
		t.Fatal("no SPEC-WAKE paragraph contains \"`AWAKE REFUSED` the shape for the\"; the pin is looking in the wrong place")
	}

	if !strings.Contains(para, door) {
		t.Errorf("the AWAKE REFUSED paragraph does not name the door %q: awakeRefused appends it to every AWAKE REFUSED line, this paragraph is where a reader learns that shape, and a spec that omits it invites the next reader to remove the door from the code", door)
	}

	bare := strings.ReplaceAll(para, "AWAKE REFUSED", "")
	if strings.Contains(bare, "WAKE REFUSED") {
		t.Errorf("the AWAKE REFUSED paragraph claims the door for watch's WAKE REFUSED shape, but refused() (cmd/nova-wake/main.go) does not carry it")
	}

	found := false
	for _, line := range strings.Split(text, "\n") {
		if line == "AWAKE REFUSED <reason>" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("docs/SPEC-WAKE.md no longer carries the grammar line exactly \"AWAKE REFUSED <reason>\"")
	}
}

// awakeDoorLiteral reads the door literal out of `func awakeRefused`: from
// `; run: nova-wake ` up to the `\n` escape inside the format string.
func awakeDoorLiteral(t *testing.T, src string) string {
	t.Helper()
	start := strings.Index(src, "func awakeRefused(")
	if start < 0 {
		t.Fatal("cmd/nova-wake/main.go has no func awakeRefused(; the pin is looking in the wrong place")
	}
	body := src[start:]
	if nl := strings.Index(body, "\n}\n"); nl >= 0 {
		body = body[:nl]
	}
	door := strings.Index(body, "; run: nova-wake ")
	if door < 0 {
		t.Fatal("func awakeRefused names no \"; run: nova-wake \" door; a scan that finds nothing must not pass by asking nothing")
	}
	rest := body[door:]
	end := strings.Index(rest, `\n`)
	if end < 0 {
		t.Fatal("the awakeRefused door has no trailing \\n; the pin could not find the literal's end")
	}
	return rest[:end]
}
