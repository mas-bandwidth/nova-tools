package release

// The dogfood gate in front of a release, driven by receipts on disk. Every
// test here builds its own command reference and its own receipts directory in
// a temp dir, so nothing reads the fleet's real records and nothing reaches the
// network: the gate's only two inputs are files, which is what makes the
// definition of done checkable at all.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dogfoodCLIFile writes a command reference declaring three verbs, in the shape
// docs/CLI.md declares them and pkg/dogfood.ParseCLI reads them.
func dogfoodCLIFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "CLI.md")
	text := "# Command reference\n\n## nova-example\n\n```\nnova-example links --root <dir>\nnova-example corpus --root <dir>\nnova-example attest --root <dir>\n```\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		require.NoError(t, err, err)
	}
	return path
}

// receipt is one record on disk, in the file `nova-check dogfood record` writes.
type receipt struct {
	Tool  string `json:"tool"`
	Verb  string `json:"verb"`
	By    string `json:"by"`
	At    string `json:"at"`
	OK    bool   `json:"ok"`
	Notes string `json:"notes"`
	Issue int    `json:"issue,omitempty"`
}

// writeReceipts appends receipts as files, newest-sorting last. The NAME
// carries no timestamp: `dogfood.Record` slugs the colons out of an RFC3339 `at`
// before it names a file, and a test that spelled the name itself put them back
// -- on windows a `:` is not a filename character at all, and every one of these
// tests failed there and nowhere else (test-windows-pr, #1423). The ledger reads
// the `at` INSIDE the record, so the file may be called anything that sorts.
func writeReceipts(t *testing.T, dir string, rs ...receipt) string {
	t.Helper()
	existing, err := os.ReadDir(dir)
	if err != nil {
		require.NoError(t, err, err)
	}
	for i, r := range rs {
		raw, err := json.Marshal(r)
		if err != nil {
			require.NoError(t, err, err)
		}
		name := filepath.Join(dir, fmt.Sprintf("%03d-%s-%s.json", len(existing)+i, r.Tool, r.Verb))
		if err := os.WriteFile(name, append(raw, '\n'), 0o644); err != nil {
			require.NoError(t, err, err)
		}
	}
	return dir
}

// oneOpenEdge is the fixture the whole gate is about: somebody ran a verb, it
// did not do what they needed, they filed the edge, and nobody has run it since
// and said it worked. Feedback filed is not feedback applied.
func oneOpenEdge(t *testing.T) (cli, receipts string) {
	t.Helper()
	root := t.TempDir()
	cli = dogfoodCLIFile(t, root)
	receipts = filepath.Join(root, "receipts")
	if err := os.MkdirAll(receipts, 0o755); err != nil {
		require.NoError(t, err, err)
	}
	writeReceipts(t, receipts,
		receipt{Tool: "nova-example", Verb: "corpus", By: "Stella", At: "2026-09-18T10:00:00Z", OK: true, Notes: "ran it on the schema corpus"},
		receipt{Tool: "nova-example", Verb: "links", By: "Stella", At: "2026-09-18T11:00:00Z", OK: false, Notes: "refused a relative path it should have taken", Issue: 1411},
	)
	return cli, receipts
}

// noOpenEdge is the same fixture with the edge answered: a later run of the
// same verb that did what the person needed.
func noOpenEdge(t *testing.T) (cli, receipts string) {
	t.Helper()
	cli, receipts = oneOpenEdge(t)
	writeReceipts(t, receipts,
		receipt{Tool: "nova-example", Verb: "links", By: "Stella", At: "2026-09-18T15:00:00Z", OK: true, Notes: "the relative path is taken now"},
	)
	return cli, receipts
}

func changelogIn(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(path, []byte("# nova-tools changelog\n\n## v0.15.10 — 2026-09-17\n\n- #1 older\n"), 0o644); err != nil {
		require.NoError(t, err, err)
	}
	return path
}

func cutArgs(changelog string, extra ...string) []string {
	return append([]string{"cut", "--repo", "mas-bandwidth/nova-tools", "--from", "main",
		"--version", "v0.16.0", "--changelog", changelog}, extra...)
}

// ---------------------------------------------------------------------------
// the gate itself
// ---------------------------------------------------------------------------

func TestReadDogfoodFindsTheOpenEdge(t *testing.T) {
	t.Parallel()

	cli, receipts := oneOpenEdge(t)
	v, err := ReadDogfood(cli, receipts, "")
	if err != nil {
		require.NoError(t, err, err)
	}
	if v.Open != 1 {
		require.Equal(t, 1, v.Open, "open=%d, want 1: %v", v.Open, v.Findings)
	}
	if v.Verbs != 3 {
		require.Equal(t, 3, v.Verbs, "verbs=%d, want the 3 the reference declares", v.Verbs)
	}
	// The gate says what `nova-check dogfood gate` says, word for word: two
	// spellings of one finding is one of them going stale.
	if !strings.Contains(v.Findings[0], "DOGFOOD GATE FAIL tool=nova-example verb=links") {
		require.Contains(t, v.Findings[0], "DOGFOOD GATE FAIL tool=nova-example verb=links", "the finding is not the gate's own line: %q", v.Findings[0])
	}
	if !strings.Contains(v.Findings[0], "#1411") {
		require.Contains(t, v.Findings[0], "#1411", "the finding does not name the issue the edge was filed as: %q", v.Findings[0])
	}
}

func TestReadDogfoodIsQuietWhenTheEdgeWasAnswered(t *testing.T) {
	t.Parallel()

	cli, receipts := noOpenEdge(t)
	v, err := ReadDogfood(cli, receipts, "")
	if err != nil {
		require.NoError(t, err, err)
	}
	if v.Open != 0 {
		require.Equal(t, 0, v.Open, "open=%d after a later run said it worked: %v", v.Open, v.Findings)
	}
}

// A receipt that will not parse is evidence somebody tried to record
// something. Reading past it would report a shorter, greener truth than the one
// on disk -- which is the direction a gate must never fail in.
func TestReadDogfoodRefusesABrokenReceipt(t *testing.T) {
	t.Parallel()

	cli, receipts := noOpenEdge(t)
	if err := os.WriteFile(filepath.Join(receipts, "broken.json"), []byte("{not json\n"), 0o644); err != nil {
		require.NoError(t, err, err)
	}
	if _, err := ReadDogfood(cli, receipts, ""); err == nil {
		require.Error(t, err, "a receipt that will not parse was read past")
	}
}

func TestReadDogfoodRefusesAReceiptsDirectoryThatIsNotThere(t *testing.T) {
	t.Parallel()

	cli, _ := noOpenEdge(t)
	if _, err := ReadDogfood(cli, filepath.Join(t.TempDir(), "nowhere"), ""); err == nil {
		require.Error(t, err, "an absent receipts directory read as an empty one")
	}
}

// ---------------------------------------------------------------------------
// cut
// ---------------------------------------------------------------------------

func TestCutRefusesOnAnOpenEdgeBeforeItAsksTheForgeAnything(t *testing.T) {
	t.Parallel()

	cli, receipts := oneOpenEdge(t)
	f := cutForge()
	changelog := changelogIn(t, t.TempDir())
	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelog, "--cli", cli, "--receipts", receipts), &out, &errs, cutDeps(t, f))
	if code != 2 {
		require.Equal(t, 2, code, "code=%d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errs.String())
	}
	want := `RELEASE CUT REFUSED reason=dogfood-gate open=1 remedy="fix the open edges or --no-dogfood-gate --reason <why>"`
	if !strings.Contains(errs.String(), want) {
		require.Contains(t, errs.String(), want, "no %q in:\n%s", want, errs.String())
	}
	// The edge itself, not only the count: a person meeting this refusal needs
	// to know WHICH verb before they can do anything about it.
	if !strings.Contains(errs.String(), "verb=links") {
		require.Contains(t, errs.String(), "verb=links", "the refusal does not name the open edge:\n%s", errs.String())
	}
	// AND THE FORGE WAS NEVER ASKED. The gate is first, so a release that was
	// never going to be cut costs no network reads.
	if f.headCalls != 0 {
		require.Equal(t, 0, f.headCalls, "the forge was read %d times before the gate refused", f.headCalls)
	}
	// Nothing was written.
	raw, err := os.ReadFile(changelog)
	if err != nil {
		require.NoError(t, err, err)
	}
	if strings.Contains(string(raw), "v0.16.0") {
		require.NotContains(t, string(raw), "v0.16.0", "the changelog was written by a refused cut:\n%s", raw)
	}
	if len(f.tagged) != 0 {
		require.Len(t, f.tagged, 0, "a refused cut tagged: %v", f.tagged)
	}
}

func TestCutPassesTheGateAndSaysSoOnTheLine(t *testing.T) {
	t.Parallel()

	cli, receipts := noOpenEdge(t)
	f := cutForge()
	changelog := changelogIn(t, t.TempDir())
	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelog, "--cli", cli, "--receipts", receipts), &out, &errs, cutDeps(t, f))
	if code != 0 {
		require.Equal(t, 0, code, "code=%d errs=%s", code, errs.String())
	}
	if !strings.Contains(out.String(), "dogfood=ok") {
		require.Contains(t, out.String(), "dogfood=ok", "the cut line does not say the gate passed:\n%s", out.String())
	}
}

// The waiver is the way past the gate, and it is work: the reason is required,
// it is printed, and it is written into the section that travels by git.
func TestCutWaivesTheGateOnlyWithAReasonAndRecordsItEverywhere(t *testing.T) {
	t.Parallel()

	cli, receipts := oneOpenEdge(t)
	f := cutForge()
	changelog := changelogIn(t, t.TempDir())

	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelog, "--cli", cli, "--receipts", receipts, "--no-dogfood-gate"), &out, &errs, cutDeps(t, f))
	if code != 2 {
		require.Equal(t, 2, code, "a waiver with no reason was accepted: code=%d out=%s", code, out.String())
	}
	if !strings.Contains(errs.String(), "--reason") {
		require.Contains(t, errs.String(), "--reason", "the refusal does not name the flag that answers it:\n%s", errs.String())
	}

	out.Reset()
	errs.Reset()
	const why = "the windows bench cannot run the release lane until #1410 lands"
	code = Run("nova-update", cutArgs(changelog, "--cli", cli, "--receipts", receipts, "--no-dogfood-gate", "--reason", why), &out, &errs, cutDeps(t, f))
	if code != 0 {
		require.Equal(t, 0, code, "code=%d errs=%s", code, errs.String())
	}
	if !strings.Contains(out.String(), "RELEASE CUT DOGFOOD WAIVED reason=") {
		require.Contains(t, out.String(), "RELEASE CUT DOGFOOD WAIVED reason=", "the waiver was not printed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "dogfood=waived") {
		require.Contains(t, out.String(), "dogfood=waived", "the cut line does not carry the waiver:\n%s", out.String())
	}
	raw, err := os.ReadFile(changelog)
	if err != nil {
		require.NoError(t, err, err)
	}
	if !strings.Contains(string(raw), DogfoodWaiverPrefix+why) {
		require.Contains(t, string(raw), DogfoodWaiverPrefix+why, "the waiver is not in the changelog section:\n%s", raw)
	}
}

// A run with no reference and no receipts is NOT a run that passed, and the
// line says which input was missing.
func TestCutNamesASkippedGate(t *testing.T) {
	t.Parallel()

	f := cutForge()
	changelog := changelogIn(t, t.TempDir())
	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelog), &out, &errs, cutDeps(t, f))
	if code != 0 {
		require.Equal(t, 0, code, "code=%d errs=%s", code, errs.String())
	}
	if !strings.Contains(errs.String(), "RELEASE CUT NOTE dogfood-gate=skipped") {
		require.Contains(t, errs.String(), "RELEASE CUT NOTE dogfood-gate=skipped", "a gate that could not run said nothing:\n%s", errs.String())
	}
	if !strings.Contains(out.String(), "dogfood=skipped") {
		require.Contains(t, out.String(), "dogfood=skipped", "the cut line does not carry the skip:\n%s", out.String())
	}
}

// The reference is derived from the checkout the verb was already given, so
// nobody retypes a path the cut already knows.
func TestCutFindsTheReferenceBesideTheChangelog(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	changelog := changelogIn(t, root)
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		require.NoError(t, err, err)
	}
	cli, receipts := oneOpenEdge(t)
	raw, err := os.ReadFile(cli)
	if err != nil {
		require.NoError(t, err, err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "CLI.md"), raw, 0o644); err != nil {
		require.NoError(t, err, err)
	}
	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelog, "--receipts", receipts), &out, &errs, cutDeps(t, cutForge()))
	if code != 2 {
		require.Equal(t, 2, code, "code=%d, want the derived reference to find the open edge\nstdout:%s\nstderr:%s", code, out.String(), errs.String())
	}
	if !strings.Contains(errs.String(), "reason=dogfood-gate open=1") {
		require.Contains(t, errs.String(), "reason=dogfood-gate open=1", "the derived reference was not read:\n%s", errs.String())
	}
}

// ---------------------------------------------------------------------------
// build
// ---------------------------------------------------------------------------

func TestBuildRefusesOnAnOpenEdgeBeforeItCompilesAnything(t *testing.T) {
	t.Parallel()

	cli, receipts := oneOpenEdge(t)
	source, outDir := sourceTree(t), t.TempDir()
	// The edge is on a tool this build ships: the gate judges the source's
	// cmd/, and a tool outside it could not hold the build.
	shipped := filepath.Join(source, "cmd", "nova-example")
	if err := os.MkdirAll(shipped, 0o755); err != nil {
		require.NoError(t, err, err)
	}
	if err := os.WriteFile(filepath.Join(shipped, "main.go"), []byte("package main\n"), 0o644); err != nil {
		require.NoError(t, err, err)
	}
	tc := &fakeToolchain{}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"build", "--version", "v0.16.0", "--out", outDir, "--source", source,
		"--cli", cli, "--receipts", receipts}, &o, &e, Deps{Toolchain: tc})
	if code != 2 {
		require.Equal(t, 2, code, "code=%d, want 2\nstdout:%s\nstderr:%s", code, o.String(), e.String())
	}
	want := `RELEASE BUILD REFUSED reason=dogfood-gate open=1 remedy="fix the open edges or --no-dogfood-gate --reason <why>"`
	if !strings.Contains(e.String(), want) {
		require.Contains(t, e.String(), want, "no %q in:\n%s", want, e.String())
	}
	if len(tc.calls) != 0 {
		require.Len(t, tc.calls, 0, "the compiler ran %d times before the gate refused: %v", len(tc.calls), tc.calls)
	}
	if entries, err := os.ReadDir(outDir); err != nil || len(entries) != 0 {
		require.FailNowf(t, "", "a refused build left %v in the artifact root (err=%v)", entries, err)
	}
}

func TestBuildPassesTheGateAndSaysSoOnTheLine(t *testing.T) {
	t.Parallel()

	cli, receipts := noOpenEdge(t)
	source, outDir := sourceTree(t), t.TempDir()
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"build", "--version", "v0.16.0", "--out", outDir, "--source", source,
		"--cli", cli, "--receipts", receipts}, &o, &e, Deps{Toolchain: &fakeToolchain{}})
	if code != 0 {
		require.Equal(t, 0, code, "code=%d errs=%s", code, e.String())
	}
	if !strings.Contains(o.String(), "dogfood=ok") {
		require.Contains(t, o.String(), "dogfood=ok", "the build line does not say the gate passed:\n%s", o.String())
	}
}

// ---------------------------------------------------------------------------
// the seam
// ---------------------------------------------------------------------------

// A Deps.Dogfood is the seam the release lane's own tests use when the question
// is what the verb DOES about a verdict rather than how the verdict was read.
func TestTheGateIsASeam(t *testing.T) {
	t.Parallel()

	called := 0
	deps := cutDeps(t, cutForge())
	deps.Dogfood = func(cli, receipts, cmd string) (DogfoodVerdict, error) {
		called++
		return DogfoodVerdict{Verbs: 9, Open: 2, Findings: []string{"DOGFOOD GATE FAIL tool=a verb=b: one", "DOGFOOD GATE FAIL tool=c verb=d: two"}}, nil
	}
	cli, receipts := noOpenEdge(t) // a CLEAN fixture: the seam is what decides
	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelogIn(t, t.TempDir()), "--cli", cli, "--receipts", receipts), &out, &errs, deps)
	if code != 2 || called != 1 {
		require.FailNowf(t, "", "code=%d called=%d; the seam was not the one asked\n%s", code, called, errs.String())
	}
	if !strings.Contains(errs.String(), "reason=dogfood-gate open=2") {
		require.Contains(t, errs.String(), "reason=dogfood-gate open=2", "the seam's verdict is not the one refused:\n%s", errs.String())
	}
}

// Tool output costs tokens: the count is the answer and the first few edges are
// the orientation. A hundred open edges must not print a hundred lines.
func TestTheRefusalIsBounded(t *testing.T) {
	t.Parallel()

	var many []string
	for i := 0; i < 40; i++ {
		many = append(many, "DOGFOOD GATE FAIL tool=nova-example verb=v: an edge")
	}
	deps := cutDeps(t, cutForge())
	deps.Dogfood = func(cli, receipts, cmd string) (DogfoodVerdict, error) {
		return DogfoodVerdict{Verbs: 40, Open: len(many), Findings: many}, nil
	}
	cli, receipts := noOpenEdge(t)
	var out, errs bytes.Buffer
	Run("nova-update", cutArgs(changelogIn(t, t.TempDir()), "--cli", cli, "--receipts", receipts), &out, &errs, deps)
	if got := strings.Count(errs.String(), "DOGFOOD GATE FAIL"); got != dogfoodFindingCap {
		require.Equal(t, dogfoodFindingCap, got, "printed %d findings, want the cap of %d:\n%s", got, dogfoodFindingCap, errs.String())
	}
	if !strings.Contains(errs.String(), "DOGFOOD GATE MORE open=40 shown=10") {
		require.Contains(t, errs.String(), "DOGFOOD GATE MORE open=40 shown=10", "the ceiling was not named:\n%s", errs.String())
	}
}

// ---------------------------------------------------------------------------
// the section
// ---------------------------------------------------------------------------

func TestTheSectionCarriesNoWaiverWhenThereWasNone(t *testing.T) {
	t.Parallel()

	s := sectionWith("v0.16.0", "abc", "v0.15.10", "", "", "", time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC), nil)
	if strings.Contains(s, DogfoodWaiverPrefix) {
		require.NotContains(t, s, DogfoodWaiverPrefix, "a gated release wrote a waiver line:\n%s", s)
	}
}

// The help says the gate exists where a person will meet it.
func TestTheHelpSaysWhatTheGateIs(t *testing.T) {
	t.Parallel()

	var out, errs bytes.Buffer
	if code := Run("nova-update", []string{"help"}, &out, &errs, Deps{}); code != 0 {
		require.Equal(t, 0, code, "code=%d", code)
	}
	for _, want := range []string{"--no-dogfood-gate", "--receipts", "open edge"} {
		if !strings.Contains(out.String(), want) {
			assert.Contains(t, out.String(), want, "release help does not name %q", want)
		}
	}
}

// THE GATE JUDGES WHAT SHIPS. The checkout's cmd/ is the shipped set: a parked
// tool's open edge, and its not-ok receipt on a verb nobody declares, do not
// hold the tag; the same edge on a shipped tool does.
func TestCutJudgesOnlyTheToolsUnderCmd(t *testing.T) {
	t.Parallel()

	checkout := func(t *testing.T) (changelog, receipts string) {
		t.Helper()
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
			require.NoError(t, err, err)
		}
		dogfoodCLIFile(t, filepath.Join(root, "docs"))
		for _, tool := range []string{"nova-example", "nova-shipped"} {
			dir := filepath.Join(root, "cmd", tool)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				require.NoError(t, err, err)
			}
			if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
				require.NoError(t, err, err)
			}
		}
		receipts = filepath.Join(root, "receipts")
		if err := os.MkdirAll(receipts, 0o755); err != nil {
			require.NoError(t, err, err)
		}
		writeReceipts(t, receipts,
			receipt{Tool: "nova-parked", Verb: "fill", By: "Stella", At: "2026-09-18T10:00:00Z", OK: false, Notes: "parked under deprecated/"},
			receipt{Tool: "nova-parked", Verb: "land", By: "Stella", At: "2026-09-18T10:01:00Z", OK: true, Notes: "Edges: (1) parked"},
		)
		return changelogIn(t, root), receipts
	}

	// Only parked tools have open items: the gate passes and says what it set aside.
	changelog, receipts := checkout(t)
	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelog, "--receipts", receipts), &out, &errs, cutDeps(t, cutForge()))
	if code != 0 {
		require.Equal(t, 0, code, "code=%d, want 0: a parked tool's open items held the tag\nstderr:%s", code, errs.String())
	}
	if !strings.Contains(out.String(), "dogfood=ok") {
		require.Contains(t, out.String(), "dogfood=ok", "the cut line does not say the gate passed:\n%s", out.String())
	}
	if want := "RELEASE CUT NOTE dogfood-gate shipped=2 outside=2 cmd="; !strings.Contains(errs.String(), want) {
		require.Contains(t, errs.String(), want, "no %q in:\n%s", want, errs.String())
	}

	// A shipped tool's open edge still refuses.
	changelog, receipts = checkout(t)
	writeReceipts(t, receipts,
		receipt{Tool: "nova-example", Verb: "links", By: "Stella", At: "2026-09-18T11:00:00Z", OK: false, Notes: "refused a relative path"},
	)
	out.Reset()
	errs.Reset()
	code = Run("nova-update", cutArgs(changelog, "--receipts", receipts), &out, &errs, cutDeps(t, cutForge()))
	if code != 2 || !strings.Contains(errs.String(), "reason=dogfood-gate open=1") {
		require.FailNowf(t, "", "code=%d, want 2 with open=1 for the shipped tool's edge\nstderr:%s", code, errs.String())
	}
}

// Stella's witness on #4531: a shipped tool whose directory cannot be read was
// left out of the set, its open edge set aside, and the gate passed with
// {Shipped:1 Outside:2 Findings:[]}. An I/O error is not a parked tool: the
// read refuses, and the cut with it.
func TestTheGateRefusesAToolDirectoryItCannotRead(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 directory; the refusal cannot be provoked")
	}

	cli, receipts := oneOpenEdge(t)
	cmd := filepath.Join(t.TempDir(), "cmd")
	for _, tool := range []string{"nova-shipped", "nova-example"} {
		dir := filepath.Join(cmd, tool)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			require.NoError(t, err, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
			require.NoError(t, err, err)
		}
	}
	locked := filepath.Join(cmd, "nova-example")
	if err := os.Chmod(locked, 0o000); err != nil {
		require.NoError(t, err, err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("this filesystem reads a mode-000 directory; the refusal cannot be provoked")
	}

	v, err := ReadDogfood(cli, receipts, cmd)
	if err == nil {
		require.Error(t, err, "the gate read an unreadable tool directory as parked: %+v", v)
	}
	if !strings.Contains(err.Error(), locked) {
		require.Contains(t, err.Error(), locked, "the refusal does not name the tool's path %s: %v", locked, err)
	}
	// And release build's own list refuses the same tree: one definition.
	if _, err := Tools(filepath.Dir(cmd)); err == nil {
		require.Error(t, err, "release.Tools listed a tool directory it could not read")
	}
}
