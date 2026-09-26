package ci

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/release"
)

// docs/ONBOARDING.md, asserted for EVERY directory under cmd/ — by walking it, not
// by listing the tools. The point of the walk is the binary nobody has written
// yet: a sixth command joins the standard on the day it appears, rather than on
// the day somebody remembers to add it to a table here.
//
// Two of the five points are repo-wide facts and are checked here:
//
//	(a) `<tool> help` prints usage ending in an `example:` block, and a bare
//	    command refuses in ONE line that names that door
//	(c) docs/TESTS.md carries a `### First run` inside that tool's `## <tool>`
//	    section -- the transcript document the tests execute, which is the file
//	    this test reads. It is NOT README.md: the comments here said README for
//	    long enough to be believed, while the code below has always opened
//	    docs/TESTS.md (TESTS.md before this move).
//
// The rest are per-binary and live in each command's own firstrun_test.go,
// because only that package knows its fixture: the example lines are EXECUTED
// there, the refusal sentences are asserted there, and the transcript is
// compared against real output there.

// notYetInTheFleetBuild names the tools whose design work is still open and
// that ship in no fleet build, with the issues named. A docs/TESTS.md section
// for one of these may still carry a `### First run` transcript, but the
// transcript must sit behind a note saying the tool ships in no fleet build: a
// reader must not be invited to run a binary no bench has (#1510). An entry
// added here names issues that are genuinely open, and it comes off the list
// the day the tool ships.
//
// It is EMPTY today, and an empty list is the honest state rather than an
// oversight: nova-play was the last entry and it ships (#1510). The mechanism
// stays because the next tool with open design work will want it.
var notYetInTheFleetBuild = map[string]string{}

// shipsInNoFleetBuild is the note a not-yet-shipped tool's docs/TESTS.md
// section must carry beside its transcript.
const shipsInNoFleetBuild = "ships in no fleet build"

// toolSections returns the body of every `## nova-<tool>` section of the
// document, keyed by tool name.
func toolSections(md string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split("\n"+md, "\n") {
		rest, ok := strings.CutPrefix(line, "## ")
		if !ok {
			continue
		}
		name := strings.Fields(rest)[0]
		if !strings.HasPrefix(name, "nova-") {
			continue
		}
		if body, found := onboarding.Section(md, name); found {
			out[name] = body
		}
	}
	return out
}

// TestTESTSmdFirstRunSectionsNameToolsTheFleetBuildInstalls walks every
// `## nova-<tool>` section of docs/TESTS.md and holds a section with a runnable
// `### First run` transcript to the fleet build: the tool it names must be one
// the build installs. A tool whose design work is open and that ships in no
// fleet build is the exception, and its transcript must sit behind the note
// that says so (#1510).
func TestTESTSmdFirstRunSectionsNameToolsTheFleetBuildInstalls(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	transcripts := readFile(t, filepath.Join(root, "docs", "TESTS.md"))

	installed := map[string]bool{}
	tools, err := release.Tools(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		installed[tool] = true
	}

	for tool, body := range toolSections(transcripts) {
		if !strings.Contains(body, onboarding.FirstRunHeading+"\n") {
			continue
		}
		if issues, open := notYetInTheFleetBuild[tool]; open {
			if !strings.Contains(body, shipsInNoFleetBuild) {
				t.Errorf("docs/TESTS.md gives %s a runnable %s transcript, but %s is open design work (%s) and ships in no fleet build; hold the transcript behind a note naming that, or remove the section until it ships", tool, onboarding.FirstRunHeading, tool, issues)
			}
			continue
		}
		if !installed[tool] {
			t.Errorf("docs/TESTS.md gives %s a runnable %s transcript, but the fleet build installs no %s; remove the section until the tool ships", tool, onboarding.FirstRunHeading, tool)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestNoToolIsWrittenTwiceInTheTranscripts is the guard the two-bench dogfood run
// bought. docs/TESTS.md carried `## nova-work` twice: the first section is the one
// every test reads, because onboarding.Section cuts to the first match, and the
// second was executed by nothing. It drifted, unwatched, into two sentences the
// binary no longer prints -- a bare-command refusal in the old spelling, and an
// `events` line carrying `--repo`, which switches ON the gh forge fallback that
// the section's own prose says is off -- and both reproduced as DEFECT on space
// and on hulk while every test in this repository was green.
//
// The reading a second section gets is nobody's. So the document names each tool
// ONCE, and a tool that needs two things said about it says them in two `###`
// subsections of its one section, where FirstRun and Transcript can both find
// them.
func TestNoToolIsWrittenTwiceInTheTranscripts(t *testing.T) {
	t.Parallel()

	path := filepath.Join(repoRoot(t), "docs", "TESTS.md")
	repeated := onboarding.RepeatedSections(readFile(t, path))
	if len(repeated) == 0 {
		return
	}
	t.Errorf("docs/TESTS.md heads more than one `## ` section with each of these names: %s\n"+
		"Only the FIRST is read -- by onboarding.Section, by every firstrun_test.go, and by a\n"+
		"person looking for the one place to change. Fold each repeat into that tool's one\n"+
		"section, as `### ` subsections if it has more than one thing to say.", strings.Join(repeated, ", "))
}
