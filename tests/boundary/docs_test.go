package boundary

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readDoc(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root(t), rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// section is the text of the "## <heading>" section of a guide, up to the next "## ".
func section(t *testing.T, doc, heading string) string {
	t.Helper()
	_, rest, ok := strings.Cut(doc, "\n## "+heading+"\n")
	if !ok {
		t.Fatalf("no section %q", heading)
	}
	body, _, _ := strings.Cut(rest, "\n## ")
	return body
}

func TestGuideKeepsRedisUpForItsHealthChecks(t *testing.T) {
	t.Parallel()
	guide := readDoc(t, "docs/README.md")
	before, _, _ := strings.Cut(guide, "\n## Is it working?\n")
	if strings.Contains(before, "shutdown nosave") {
		t.Errorf("the guide shuts Redis down before the health checks, which need `nova-bus who`")
	}
	if !strings.Contains(section(t, guide, "Clean up"), "shutdown nosave") {
		t.Errorf("the clean-up block no longer shuts Redis down")
	}
}

func TestGuideHealthChecksKeepTheirExitStatus(t *testing.T) {
	t.Parallel()
	for _, line := range strings.Split(section(t, readDoc(t, "docs/README.md"), "Is it working?"), "\n") {
		if strings.Contains(line, "| head") {
			t.Errorf("a health check pipes into head, which hides its exit status: %q", line)
		}
	}
}

func TestGuideBuildsAndInstallsOneTag(t *testing.T) {
	t.Parallel()
	guide := readDoc(t, "docs/README.md")
	if n := len(regexp.MustCompile(`(?m)^NOVA_TOOLS_VERSION=v\d+\.\d+\.\d+$`).FindAllString(guide, -1)); n != 1 {
		t.Errorf("NOVA_TOOLS_VERSION is set %d times, want once", n)
	}
	if !strings.Contains(guide, `git clone -q --depth 1 --branch "$NOVA_TOOLS_VERSION" https://github.com/mas-bandwidth/nova-tools`) {
		t.Errorf("the guide does not clone the release tag")
	}
	for _, m := range regexp.MustCompile(`--tools (\S+)`).FindAllStringSubmatch(guide, -1) {
		if m[1] != `"$NOVA_TOOLS_VERSION"` && m[1] != "<TAG|DIR>" {
			t.Errorf("the guide installs %s, not the tag it built", m[1])
		}
	}
}

func TestNextCommandsHaveNoEscapedBlanks(t *testing.T) {
	t.Parallel()
	for _, rel := range []string{"docs/README.md", "cmd/nova-cairn/README.md", "cmd/nova-secrets/README.md", "cmd/nova-sandbox/README.md", "cmd/nova-config/README.md", "cmd/nova-bus/README.md"} {
		if strings.Contains(readDoc(t, rel), `\x20`) {
			t.Errorf("%s shows a next command with \\x20 for its blanks; it is a `next: <command>` line with real spaces", rel)
		}
	}
}
