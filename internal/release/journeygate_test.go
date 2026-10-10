package release

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The sha cutForge's main is at: the revision every piece of evidence below
// has to name.
const journeySHA = "abc123abc123def"

// threeJourneys is a promise of three recovery journeys, the third of which
// runs only where a windows bench answers.
func threeJourneys() []Journey {
	return []Journey{
		{Package: "internal/friend", Test: "TestChaos/harness closed: down within 1 minute"},
		{Package: "internal/friend", Test: "TestChaos/usage limit: down until the reset, woken after"},
		{Package: "internal/friend", Test: "TestChaos/real harness smoke", Optional: []string{"windows"}},
	}
}

// event is one `go test -json` line.
func event(action, test, output string) string {
	raw, err := json.Marshal(map[string]string{
		"Action": action, "Package": "github.com/mas-bandwidth/nova-tools/internal/friend",
		"Test": test, "Output": output,
	})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// header is the evidence's first line, bound to rev.
func header(rev string, installed ...InstalledReceipt) string {
	if installed == nil {
		installed = []InstalledReceipt{{Machine: "bench-a", Build: "v0.16.0", Revision: rev}}
	}
	raw, err := json.Marshal(EvidenceHeader{Evidence: EvidenceKind, Revision: rev, Functions: "nova-7", Schema: "12", Installed: installed})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// evidenceFile writes the header and the events as one evidence file.
func evidenceFile(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journeys.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	return path
}

// allProven is evidence in which every one of threeJourneys passed at
// journeySHA.
func allProven() []string {
	return []string{
		header(journeySHA),
		event("run", "TestChaos", ""),
		event("pass", "TestChaos/harness_closed:_down_within_1_minute", ""),
		event("pass", "TestChaos/usage_limit:_down_until_the_reset,_woken_after", ""),
		event("pass", "TestChaos/real_harness_smoke", ""),
		event("pass", "TestChaos", ""),
	}
}

func journeyDeps(t *testing.T, f Forge) Deps {
	d := cutDeps(t, f)
	d.Journeys = threeJourneys()
	return d
}

// cutRefused asserts the cut refused before anything was written or tagged.
func cutRefused(t *testing.T, code int, f *fakeForge, changelog string, out, errs *bytes.Buffer) {
	t.Helper()
	require.Equal(t, 2, code, "out=%s errs=%s", out, errs)
	raw, err := os.ReadFile(changelog)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "v0.16.0", "a refused cut wrote the changelog")
	assert.Empty(t, f.tagged, "a refused cut tagged")
}

func journeyLine(t *testing.T, text, test, state string) {
	t.Helper()
	want := regexp.MustCompile(`(?m)^RELEASE CUT JOURNEY state=` + state + ` name=\S*` + regexp.QuoteMeta(strings.ReplaceAll(test, " ", "_")))
	assert.Regexp(t, want, text, "no %s line for %s", state, test)
}

// A release that promises recovery journeys is cut only on evidence, bound to
// the revision it tags, that each promised journey ran and passed. Stella's
// review, item 5: the chaos suite turned every unmet check into a named skip,
// and a green run was read as proof the journeys worked.
func TestTheGateRefusesAPromisedJourneyWithoutEvidence(t *testing.T) {
	t.Parallel()

	t.Run("no evidence at all refuses, naming every promised journey not-run", func(t *testing.T) {
		t.Parallel()
		f := cutForge()
		changelog := changelogIn(t, t.TempDir())
		var out, errs bytes.Buffer
		code := Run("nova-update", cutArgs(changelog), &out, &errs, journeyDeps(t, f))
		cutRefused(t, code, f, changelog, &out, &errs)
		assert.Contains(t, errs.String(), "RELEASE CUT REFUSED reason=journey-evidence promised=3")
		for _, j := range threeJourneys() {
			journeyLine(t, out.String(), j.Test, "not-run")
		}
	})

	t.Run("a green run of owed, skipped and absent journeys is not proof", func(t *testing.T) {
		t.Parallel()
		f := cutForge()
		changelog := changelogIn(t, t.TempDir())
		ev := evidenceFile(t,
			header(journeySHA),
			event("output", "TestChaos/harness_closed:_down_within_1_minute", "    owed until the cards land:\n"),
			event("output", "TestChaos/harness_closed:_down_within_1_minute", "OWED fr-harness-alive: bob down within 1m\n"),
			event("skip", "TestChaos/harness_closed:_down_within_1_minute", ""),
			event("output", "TestChaos/usage_limit:_down_until_the_reset,_woken_after", "no redis here\n"),
			event("skip", "TestChaos/usage_limit:_down_until_the_reset,_woken_after", ""),
			// the parent passes: go test calls a run whose subtests skipped green
			event("pass", "TestChaos", ""),
		)
		var out, errs bytes.Buffer
		code := Run("nova-update", cutArgs(changelog, "--journeys", ev), &out, &errs, journeyDeps(t, f))
		cutRefused(t, code, f, changelog, &out, &errs)
		assert.Contains(t, errs.String(), "RELEASE CUT REFUSED reason=journey-gate incomplete=3")
		journeyLine(t, out.String(), "TestChaos/harness closed: down within 1 minute", "owed")
		assert.Contains(t, out.String(), "fr-harness-alive", "the owed line does not name the card that owes it")
		journeyLine(t, out.String(), "TestChaos/usage limit: down until the reset, woken after", "skipped")
		journeyLine(t, out.String(), "TestChaos/real harness smoke", "not-run")
	})

	t.Run("a failed journey refuses", func(t *testing.T) {
		t.Parallel()
		f := cutForge()
		changelog := changelogIn(t, t.TempDir())
		lines := allProven()
		lines[2] = event("fail", "TestChaos/harness_closed:_down_within_1_minute", "")
		var out, errs bytes.Buffer
		code := Run("nova-update", cutArgs(changelog, "--journeys", evidenceFile(t, lines...)), &out, &errs, journeyDeps(t, f))
		cutRefused(t, code, f, changelog, &out, &errs)
		journeyLine(t, out.String(), "TestChaos/harness closed: down within 1 minute", "failed")
	})

	t.Run("an optional platform's skip is named and is not an unmet promise", func(t *testing.T) {
		t.Parallel()
		f := cutForge()
		changelog := changelogIn(t, t.TempDir())
		lines := allProven()
		lines[4] = event("output", "TestChaos/real_harness_smoke", PlatformUnavailable+"windows: no windows bench answered\n")
		lines = append(lines, event("skip", "TestChaos/real_harness_smoke", ""))
		var out, errs bytes.Buffer
		code := Run("nova-update", cutArgs(changelog, "--journeys", evidenceFile(t, lines...)), &out, &errs, journeyDeps(t, f))
		require.Equal(t, 0, code, "out=%s errs=%s", out.String(), errs.String())
		journeyLine(t, out.String(), "TestChaos/real harness smoke", "platform-unavailable")
		assert.Contains(t, out.String(), "journeys=ok")
	})

	t.Run("the same platform skip on a journey with no optional platform is an unmet promise", func(t *testing.T) {
		t.Parallel()
		f := cutForge()
		changelog := changelogIn(t, t.TempDir())
		lines := allProven()
		lines[2] = event("output", "TestChaos/harness_closed:_down_within_1_minute", PlatformUnavailable+"windows: no windows bench answered\n")
		lines = append(lines, event("skip", "TestChaos/harness_closed:_down_within_1_minute", ""))
		var out, errs bytes.Buffer
		code := Run("nova-update", cutArgs(changelog, "--journeys", evidenceFile(t, lines...)), &out, &errs, journeyDeps(t, f))
		cutRefused(t, code, f, changelog, &out, &errs)
		journeyLine(t, out.String(), "TestChaos/harness closed: down within 1 minute", "skipped")
	})

	t.Run("evidence from another revision refuses", func(t *testing.T) {
		t.Parallel()
		f := cutForge()
		changelog := changelogIn(t, t.TempDir())
		lines := allProven()
		lines[0] = header("fff000fff000fff")
		var out, errs bytes.Buffer
		code := Run("nova-update", cutArgs(changelog, "--journeys", evidenceFile(t, lines...)), &out, &errs, journeyDeps(t, f))
		cutRefused(t, code, f, changelog, &out, &errs)
		assert.Contains(t, errs.String(), "fff000fff000fff")
		assert.Contains(t, errs.String(), journeySHA)
	})

	t.Run("an installed build from another revision refuses", func(t *testing.T) {
		t.Parallel()
		f := cutForge()
		changelog := changelogIn(t, t.TempDir())
		lines := allProven()
		lines[0] = header(journeySHA, InstalledReceipt{Machine: "bench-a", Build: "v0.15.10", Revision: "fff000fff000fff"})
		var out, errs bytes.Buffer
		code := Run("nova-update", cutArgs(changelog, "--journeys", evidenceFile(t, lines...)), &out, &errs, journeyDeps(t, f))
		cutRefused(t, code, f, changelog, &out, &errs)
		assert.Contains(t, errs.String(), "bench-a")
	})

	t.Run("evidence with no installed build, function or schema version refuses", func(t *testing.T) {
		t.Parallel()
		for _, h := range []EvidenceHeader{
			{Evidence: EvidenceKind, Revision: journeySHA, Functions: "nova-7", Schema: "12"},
			{Evidence: EvidenceKind, Revision: journeySHA, Schema: "12", Installed: []InstalledReceipt{{Machine: "bench-a", Build: "v0.16.0", Revision: journeySHA}}},
			{Evidence: EvidenceKind, Revision: journeySHA, Functions: "nova-7", Installed: []InstalledReceipt{{Machine: "bench-a", Build: "v0.16.0", Revision: journeySHA}}},
		} {
			raw, err := json.Marshal(h)
			require.NoError(t, err)
			lines := allProven()
			lines[0] = string(raw)
			f := cutForge()
			changelog := changelogIn(t, t.TempDir())
			var out, errs bytes.Buffer
			code := Run("nova-update", cutArgs(changelog, "--journeys", evidenceFile(t, lines...)), &out, &errs, journeyDeps(t, f))
			cutRefused(t, code, f, changelog, &out, &errs)
		}
	})

	t.Run("a broken evidence line refuses rather than reading past it", func(t *testing.T) {
		t.Parallel()
		f := cutForge()
		changelog := changelogIn(t, t.TempDir())
		lines := append(allProven(), "FAIL\tgithub.com/mas-bandwidth/nova-tools/internal/friend [build failed]")
		var out, errs bytes.Buffer
		code := Run("nova-update", cutArgs(changelog, "--journeys", evidenceFile(t, lines...)), &out, &errs, journeyDeps(t, f))
		cutRefused(t, code, f, changelog, &out, &errs)
	})

	t.Run("every journey proven at the revision cuts and binds the evidence into the section", func(t *testing.T) {
		t.Parallel()
		f := cutForge()
		changelog := changelogIn(t, t.TempDir())
		var out, errs bytes.Buffer
		code := Run("nova-update", cutArgs(changelog, "--journeys", evidenceFile(t, allProven()...)), &out, &errs, journeyDeps(t, f))
		require.Equal(t, 0, code, "out=%s errs=%s", out.String(), errs.String())
		for _, j := range threeJourneys() {
			journeyLine(t, out.String(), j.Test, "proven")
		}
		assert.Contains(t, out.String(), "journeys=ok")
		raw, err := os.ReadFile(changelog)
		require.NoError(t, err)
		assert.Contains(t, string(raw), JourneysProvenPrefix+journeySHA)
		assert.Contains(t, string(raw), "functions nova-7, schema 12, installed bench-a v0.16.0")
	})

	t.Run("the waiver enumerates every incomplete journey in the section", func(t *testing.T) {
		t.Parallel()
		f := cutForge()
		changelog := changelogIn(t, t.TempDir())
		var out, errs bytes.Buffer
		code := Run("nova-update", cutArgs(changelog, JourneyWaiveFlag), &out, &errs, journeyDeps(t, f))
		require.Equal(t, 2, code, "a journey waiver with no reason was accepted: out=%s", out.String())
		assert.Contains(t, errs.String(), "--reason")

		out.Reset()
		errs.Reset()
		const why = "the owning fr- cards have not landed"
		code = Run("nova-update", cutArgs(changelog, JourneyWaiveFlag, "--reason", why), &out, &errs, journeyDeps(t, f))
		require.Equal(t, 0, code, "out=%s errs=%s", out.String(), errs.String())
		assert.Contains(t, out.String(), "RELEASE CUT JOURNEYS WAIVED incomplete=3 reason=")
		assert.Contains(t, out.String(), "journeys=waived")
		raw, err := os.ReadFile(changelog)
		require.NoError(t, err)
		assert.Contains(t, string(raw), JourneysIncompletePrefix+why)
		for _, j := range threeJourneys() {
			assert.Contains(t, string(raw), "- "+j.Test+": not-run")
		}
	})

	t.Run("the promise is the checkout's: a checkout that ships the sprint package promises its journeys", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		changelog := changelogIn(t, dir)
		f := cutForge()
		var out, errs bytes.Buffer
		require.Equal(t, 0, Run("nova-update", cutArgs(changelog), &out, &errs, cutDeps(t, f)), "errs=%s", errs.String())
		assert.Contains(t, out.String(), "journeys=none-promised")

		require.NoError(t, os.MkdirAll(filepath.Join(dir, "internal", "sprint"), 0o755))
		f = cutForge()
		changelog = changelogIn(t, dir)
		out.Reset()
		errs.Reset()
		code := Run("nova-update", cutArgs(changelog), &out, &errs, cutDeps(t, f))
		cutRefused(t, code, f, changelog, &out, &errs)
		assert.Contains(t, errs.String(), "reason=journey-evidence promised="+strconv.Itoa(len(PromisedJourneys)))
	})
}
