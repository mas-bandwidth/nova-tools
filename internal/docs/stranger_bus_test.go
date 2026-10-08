package docs

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// strangerBusPath is the cold stranger run of nova-bus between two names.
const strangerBusPath = "../../docs/stranger/bus-two-names.md"

// strangerBusSections are the four sections a cold stranger run is recorded under.
var strangerBusSections = []string{"Setup", "Transcript", "Stumbles", "Verdict"}

// strangerBusCardRE is a stumble's proposed card line: `card: <id>`, as a list item or not.
var strangerBusCardRE = regexp.MustCompile(`^(?:- )?card: [A-Za-z0-9][A-Za-z0-9._-]*\s*$`)

// strangerBusSectionBodies splits a run record into its `## ` sections, by heading.
func strangerBusSectionBodies(text string) map[string][]string {
	bodies := map[string][]string{}
	cur := ""
	for _, line := range strings.Split(text, "\n") {
		if h, ok := strings.CutPrefix(line, "## "); ok {
			cur = strings.TrimSpace(h)
			bodies[cur] = []string{}
			continue
		}
		if cur != "" {
			bodies[cur] = append(bodies[cur], line)
		}
	}
	return bodies
}

// strangerBusProblems is every way the bus run record falls short: a missing
// section, a transcript with no command line (`$ <command>`), and a stumble (a
// `### ` heading under Stumbles) with no proposed card line `card: <id>`.
func strangerBusProblems(text string) []string {
	var problems []string
	bodies := strangerBusSectionBodies(text)
	for _, s := range strangerBusSections {
		if _, ok := bodies[s]; !ok {
			problems = append(problems, "no ## "+s+" section")
		}
	}
	commands := 0
	for _, line := range bodies["Transcript"] {
		if strings.HasPrefix(line, "$ ") && strings.TrimSpace(line) != "$" {
			commands++
		}
	}
	if _, ok := bodies["Transcript"]; ok && commands == 0 {
		problems = append(problems, "the transcript has no command line")
	}
	stumble, carded := "", false
	closeStumble := func() {
		if stumble != "" && !carded {
			problems = append(problems, "stumble "+quoteBusStumble(stumble)+" has no proposed card line `card: <id>`")
		}
	}
	for _, line := range bodies["Stumbles"] {
		if h, ok := strings.CutPrefix(line, "### "); ok {
			closeStumble()
			stumble, carded = strings.TrimSpace(h), false
			continue
		}
		if strangerBusCardRE.MatchString(strings.TrimSpace(line)) {
			carded = true
		}
	}
	closeStumble()
	return problems
}

// quoteBusStumble quotes a stumble heading for a problem line.
func quoteBusStumble(s string) string { return `"` + s + `"` }

// strangerBusHostCommands are the bench-side commands every bus run transcript
// must show. The transcript is the run's whole command record, so the host work
// before the container -- starting it and fetching the modules into it -- is
// part of the transcript, not only of Setup.
var strangerBusHostCommands = []string{
	"podman --cgroup-manager=cgroupfs run -d",
	"go mod download",
}

// strangerBusHostProblems is every host-side command the transcript does not
// show.
func strangerBusHostProblems(transcript string) []string {
	var problems []string
	for _, cmd := range strangerBusHostCommands {
		if !strings.Contains(transcript, cmd) {
			problems = append(problems, "the transcript does not show the host-side command `"+cmd+"`")
		}
	}
	return problems
}

// strangerBusReadLogRecords are the earlier records under docs/stranger/ a run's
// Setup read log may speak of. The run reads README and help first, and anything
// else only after a stumble, named with the stumble it answered, so a record the
// Setup claims to have read must be named there (the reader's finding: "the two
// earlier records in docs/stranger/" named no file).
var strangerBusReadLogRecords = []string{
	"docs/stranger/friend-fake-harness.md",
	"docs/stranger/three-card-sprint.md",
}

// strangerBusReadLogProblems is every earlier record the Setup read log speaks
// of without naming. A Setup that names no earlier record has no problem; one
// that does must name each record and the stumble it followed.
func strangerBusReadLogProblems(setup []string) []string {
	text := strings.Join(setup, "\n")
	var problems []string
	if !strings.Contains(text, "earlier records") {
		return problems
	}
	for _, record := range strangerBusReadLogRecords {
		if !strings.Contains(text, record) {
			problems = append(problems, "the Setup read log speaks of earlier records without naming "+record)
		}
	}
	return problems
}

// TestStrangerRunBusTwoNamesIsRecorded holds docs/stranger/bus-two-names.md,
// the cold run of nova-bus between two names on a throwaway Redis, to its
// shape: the four sections, a transcript of real commands, and a proposed card
// for every stumble. The shapes it refuses are pinned on fakes first.
func TestStrangerRunBusTwoNamesIsRecorded(t *testing.T) {
	t.Parallel()

	good := "# run\n\n## Setup\n\nbench\n\n## Transcript\n\n```text\n" +
		"$ podman --cgroup-manager=cgroupfs run -d --rm --name trial image sleep 1\ncontainer\n" +
		"$ cd src && go mod download\n(no output)\n" +
		"$ nova-bus version\nnova-bus devel\n```\n\n" +
		"## Stumbles\n\n### 1. one\n\n- card: a-card\n\n### 2. two\n\ncard: b-card\n\n## Verdict\n\nyes\n"
	require.Empty(t, strangerBusProblems(good), "the fake good record")
	require.Empty(t, strangerBusHostProblems(strings.Join(strangerBusSectionBodies(good)["Transcript"], "\n")), "the fake good record's host commands")
	require.Empty(t, strangerBusReadLogProblems(strangerBusSectionBodies(good)["Setup"]), "the fake good record's Setup read log")

	for name, c := range map[string]struct{ text, want string }{
		"no Verdict":      {strings.Replace(good, "## Verdict", "## Ending", 1), "no ## Verdict section"},
		"no Setup":        {strings.Replace(good, "## Setup", "Setup", 1), "no ## Setup section"},
		"no command":      {strings.ReplaceAll(good, "$ ", ""), "the transcript has no command line"},
		"uncarded":        {strings.Replace(good, "card: b-card", "a card is owed", 1), `stumble "2. two" has no proposed card line`},
		"empty card":      {strings.Replace(good, "- card: a-card", "- card: ", 1), `stumble "1. one" has no proposed card line`},
		"no host command": {strings.Replace(good, "$ podman --cgroup-manager=cgroupfs run -d --rm --name trial image sleep 1\ncontainer\n", "", 1), "the transcript does not show the host-side command `podman --cgroup-manager=cgroupfs run -d`"},
		"unnamed record":  {strings.Replace(good, "bench\n", "bench\nthe two earlier records in docs/stranger/ name the same walls\n", 1), "the Setup read log speaks of earlier records without naming docs/stranger/friend-fake-harness.md"},
	} {
		transcript := strings.Join(strangerBusSectionBodies(c.text)["Transcript"], "\n")
		setup := strangerBusSectionBodies(c.text)["Setup"]
		got := strings.Join(append(append(strangerBusProblems(c.text), strangerBusHostProblems(transcript)...), strangerBusReadLogProblems(setup)...), "\n")
		require.Contains(t, got, c.want, "%s: the fake must be refused", name)
	}

	raw, err := os.ReadFile(strangerBusPath)
	require.NoError(t, err, "reading %s", strangerBusPath)
	text := string(raw)
	require.Empty(t, strangerBusProblems(text), "%s", strangerBusPath)
	require.Contains(t, strangerBusSectionBodies(text), "Stumbles")

	// The correction this attempt carries: the transcript must cover every
	// command, host side included. The bench commands that start the container
	// and fetch the modules stand in the transcript, not only in Setup; and a
	// stumble that names the refused pong line points at the transcript for it,
	// so the refused invocation and its exact line must stand there too.
	transcript := strings.Join(strangerBusSectionBodies(text)["Transcript"], "\n")
	require.Contains(t, transcript, "PONG REFUSED: --to is required: no ping has named a seat yet",
		"the refused pong invocation and its line must stand in the transcript of %s", strangerBusPath)
	require.Empty(t, strangerBusHostProblems(transcript), "%s", strangerBusPath)

	// The correction this attempt carries: the Setup read log names every read
	// past README and help with the stumble it followed. A claim that two
	// earlier records under docs/stranger/ were read must name
	// docs/stranger/friend-fake-harness.md and docs/stranger/three-card-sprint.md
	// and the stumble each followed, instead of the bare "earlier records" the
	// reader found.
	require.Empty(t, strangerBusReadLogProblems(strangerBusSectionBodies(text)["Setup"]), "%s", strangerBusPath)
}
