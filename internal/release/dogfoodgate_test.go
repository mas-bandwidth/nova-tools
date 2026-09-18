package release

// The dogfood gate in front of a release, driven by receipts on disk. Every
// test here builds its own command reference and its own receipts directory in
// a temp dir, so nothing reads the fleet's real records and nothing reaches the
// network: the gate's only two inputs are files, which is what makes the
// definition of done checkable at all.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dogfoodCLIFile writes a command reference declaring three verbs, in the shape
// docs/CLI.md declares them and internal/dogfood.ParseCLI reads them.
func dogfoodCLIFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "CLI.md")
	text := "# Command reference\n\n## nova-example\n\n```\nnova-example links --root <dir>\nnova-example corpus --root <dir>\nnova-example attest --root <dir>\n```\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
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

func writeReceipts(t *testing.T, dir string, rs ...receipt) string {
	t.Helper()
	for i, r := range rs {
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Join(dir, r.At+"-"+r.Tool+"-"+r.Verb+"-"+string(rune('a'+i))+".json")
		if err := os.WriteFile(name, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
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
		t.Fatal(err)
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
		t.Fatal(err)
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
	cli, receipts := oneOpenEdge(t)
	v, err := ReadDogfood(cli, receipts)
	if err != nil {
		t.Fatal(err)
	}
	if v.Open != 1 {
		t.Fatalf("open=%d, want 1: %v", v.Open, v.Findings)
	}
	if v.Verbs != 3 {
		t.Fatalf("verbs=%d, want the 3 the reference declares", v.Verbs)
	}
	// The gate says what `nova-check dogfood gate` says, word for word: two
	// spellings of one finding is one of them going stale.
	if !strings.Contains(v.Findings[0], "DOGFOOD GATE FAIL tool=nova-example verb=links") {
		t.Fatalf("the finding is not the gate's own line: %q", v.Findings[0])
	}
	if !strings.Contains(v.Findings[0], "#1411") {
		t.Fatalf("the finding does not name the issue the edge was filed as: %q", v.Findings[0])
	}
}

func TestReadDogfoodIsQuietWhenTheEdgeWasAnswered(t *testing.T) {
	cli, receipts := noOpenEdge(t)
	v, err := ReadDogfood(cli, receipts)
	if err != nil {
		t.Fatal(err)
	}
	if v.Open != 0 {
		t.Fatalf("open=%d after a later run said it worked: %v", v.Open, v.Findings)
	}
}

// A receipt that will not parse is evidence somebody tried to record
// something. Reading past it would report a shorter, greener truth than the one
// on disk -- which is the direction a gate must never fail in.
func TestReadDogfoodRefusesABrokenReceipt(t *testing.T) {
	cli, receipts := noOpenEdge(t)
	if err := os.WriteFile(filepath.Join(receipts, "broken.json"), []byte("{not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDogfood(cli, receipts); err == nil {
		t.Fatal("a receipt that will not parse was read past")
	}
}

func TestReadDogfoodRefusesAReceiptsDirectoryThatIsNotThere(t *testing.T) {
	cli, _ := noOpenEdge(t)
	if _, err := ReadDogfood(cli, filepath.Join(t.TempDir(), "nowhere")); err == nil {
		t.Fatal("an absent receipts directory read as an empty one")
	}
}

// ---------------------------------------------------------------------------
// cut
// ---------------------------------------------------------------------------

func TestCutRefusesOnAnOpenEdgeBeforeItAsksTheForgeAnything(t *testing.T) {
	cli, receipts := oneOpenEdge(t)
	f := cutForge()
	changelog := changelogIn(t, t.TempDir())
	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelog, "--cli", cli, "--receipts", receipts), &out, &errs, cutDeps(t, f))
	if code != 2 {
		t.Fatalf("code=%d, want 2\nstdout:%s\nstderr:%s", code, out.String(), errs.String())
	}
	want := `RELEASE CUT REFUSED reason=dogfood-gate open=1 remedy="fix the open edges or --no-dogfood-gate --reason <why>"`
	if !strings.Contains(errs.String(), want) {
		t.Fatalf("no %q in:\n%s", want, errs.String())
	}
	// The edge itself, not only the count: a person meeting this refusal needs
	// to know WHICH verb before they can do anything about it.
	if !strings.Contains(errs.String(), "verb=links") {
		t.Fatalf("the refusal does not name the open edge:\n%s", errs.String())
	}
	// AND THE FORGE WAS NEVER ASKED. The gate is first, so a release that was
	// never going to be cut costs no network reads.
	if f.headCalls != 0 {
		t.Fatalf("the forge was read %d times before the gate refused", f.headCalls)
	}
	// Nothing was written.
	raw, err := os.ReadFile(changelog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "v0.16.0") {
		t.Fatalf("the changelog was written by a refused cut:\n%s", raw)
	}
	if len(f.tagged) != 0 {
		t.Fatalf("a refused cut tagged: %v", f.tagged)
	}
}

func TestCutPassesTheGateAndSaysSoOnTheLine(t *testing.T) {
	cli, receipts := noOpenEdge(t)
	f := cutForge()
	changelog := changelogIn(t, t.TempDir())
	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelog, "--cli", cli, "--receipts", receipts), &out, &errs, cutDeps(t, f))
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, errs.String())
	}
	if !strings.Contains(out.String(), "dogfood=ok") {
		t.Fatalf("the cut line does not say the gate passed:\n%s", out.String())
	}
}

// The waiver is the way past the gate, and it is work: the reason is required,
// it is printed, and it is written into the section that travels by git.
func TestCutWaivesTheGateOnlyWithAReasonAndRecordsItEverywhere(t *testing.T) {
	cli, receipts := oneOpenEdge(t)
	f := cutForge()
	changelog := changelogIn(t, t.TempDir())

	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelog, "--cli", cli, "--receipts", receipts, "--no-dogfood-gate"), &out, &errs, cutDeps(t, f))
	if code != 2 {
		t.Fatalf("a waiver with no reason was accepted: code=%d out=%s", code, out.String())
	}
	if !strings.Contains(errs.String(), "--reason") {
		t.Fatalf("the refusal does not name the flag that answers it:\n%s", errs.String())
	}

	out.Reset()
	errs.Reset()
	const why = "the windows bench cannot run the release lane until #1410 lands"
	code = Run("nova-update", cutArgs(changelog, "--cli", cli, "--receipts", receipts, "--no-dogfood-gate", "--reason", why), &out, &errs, cutDeps(t, f))
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, errs.String())
	}
	if !strings.Contains(out.String(), "RELEASE CUT DOGFOOD WAIVED reason=") {
		t.Fatalf("the waiver was not printed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "dogfood=waived") {
		t.Fatalf("the cut line does not carry the waiver:\n%s", out.String())
	}
	raw, err := os.ReadFile(changelog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), DogfoodWaiverPrefix+why) {
		t.Fatalf("the waiver is not in the changelog section:\n%s", raw)
	}
}

// A run with no reference and no receipts is NOT a run that passed, and the
// line says which input was missing.
func TestCutNamesASkippedGate(t *testing.T) {
	f := cutForge()
	changelog := changelogIn(t, t.TempDir())
	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelog), &out, &errs, cutDeps(t, f))
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, errs.String())
	}
	if !strings.Contains(errs.String(), "RELEASE CUT NOTE dogfood-gate=skipped") {
		t.Fatalf("a gate that could not run said nothing:\n%s", errs.String())
	}
	if !strings.Contains(out.String(), "dogfood=skipped") {
		t.Fatalf("the cut line does not carry the skip:\n%s", out.String())
	}
}

// The reference is derived from the checkout the verb was already given, so
// nobody retypes a path the cut already knows.
func TestCutFindsTheReferenceBesideTheChangelog(t *testing.T) {
	root := t.TempDir()
	changelog := changelogIn(t, root)
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	cli, receipts := oneOpenEdge(t)
	raw, err := os.ReadFile(cli)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "CLI.md"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelog, "--receipts", receipts), &out, &errs, cutDeps(t, cutForge()))
	if code != 2 {
		t.Fatalf("code=%d, want the derived reference to find the open edge\nstdout:%s\nstderr:%s", code, out.String(), errs.String())
	}
	if !strings.Contains(errs.String(), "reason=dogfood-gate open=1") {
		t.Fatalf("the derived reference was not read:\n%s", errs.String())
	}
}

// ---------------------------------------------------------------------------
// build
// ---------------------------------------------------------------------------

func TestBuildRefusesOnAnOpenEdgeBeforeItCompilesAnything(t *testing.T) {
	cli, receipts := oneOpenEdge(t)
	source, outDir := sourceTree(t), t.TempDir()
	tc := &fakeToolchain{}
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"build", "--version", "v0.16.0", "--out", outDir, "--source", source,
		"--cli", cli, "--receipts", receipts}, &o, &e, Deps{Toolchain: tc})
	if code != 2 {
		t.Fatalf("code=%d, want 2\nstdout:%s\nstderr:%s", code, o.String(), e.String())
	}
	want := `RELEASE BUILD REFUSED reason=dogfood-gate open=1 remedy="fix the open edges or --no-dogfood-gate --reason <why>"`
	if !strings.Contains(e.String(), want) {
		t.Fatalf("no %q in:\n%s", want, e.String())
	}
	if len(tc.calls) != 0 {
		t.Fatalf("the compiler ran %d times before the gate refused: %v", len(tc.calls), tc.calls)
	}
	if entries, err := os.ReadDir(outDir); err != nil || len(entries) != 0 {
		t.Fatalf("a refused build left %v in the artifact root (err=%v)", entries, err)
	}
}

func TestBuildPassesTheGateAndSaysSoOnTheLine(t *testing.T) {
	cli, receipts := noOpenEdge(t)
	source, outDir := sourceTree(t), t.TempDir()
	var o, e bytes.Buffer
	code := Run("nova-update", []string{"build", "--version", "v0.16.0", "--out", outDir, "--source", source,
		"--cli", cli, "--receipts", receipts}, &o, &e, Deps{Toolchain: &fakeToolchain{}})
	if code != 0 {
		t.Fatalf("code=%d errs=%s", code, e.String())
	}
	if !strings.Contains(o.String(), "dogfood=ok") {
		t.Fatalf("the build line does not say the gate passed:\n%s", o.String())
	}
}

// ---------------------------------------------------------------------------
// the seam
// ---------------------------------------------------------------------------

// A Deps.Dogfood is the seam the release lane's own tests use when the question
// is what the verb DOES about a verdict rather than how the verdict was read.
func TestTheGateIsASeam(t *testing.T) {
	called := 0
	deps := cutDeps(t, cutForge())
	deps.Dogfood = func(cli, receipts string) (DogfoodVerdict, error) {
		called++
		return DogfoodVerdict{Verbs: 9, Open: 2, Findings: []string{"DOGFOOD GATE FAIL tool=a verb=b: one", "DOGFOOD GATE FAIL tool=c verb=d: two"}}, nil
	}
	cli, receipts := noOpenEdge(t) // a CLEAN fixture: the seam is what decides
	var out, errs bytes.Buffer
	code := Run("nova-update", cutArgs(changelogIn(t, t.TempDir()), "--cli", cli, "--receipts", receipts), &out, &errs, deps)
	if code != 2 || called != 1 {
		t.Fatalf("code=%d called=%d; the seam was not the one asked\n%s", code, called, errs.String())
	}
	if !strings.Contains(errs.String(), "reason=dogfood-gate open=2") {
		t.Fatalf("the seam's verdict is not the one refused:\n%s", errs.String())
	}
}

// Tool output costs tokens: the count is the answer and the first few edges are
// the orientation. A hundred open edges must not print a hundred lines.
func TestTheRefusalIsBounded(t *testing.T) {
	var many []string
	for i := 0; i < 40; i++ {
		many = append(many, "DOGFOOD GATE FAIL tool=nova-example verb=v: an edge")
	}
	deps := cutDeps(t, cutForge())
	deps.Dogfood = func(cli, receipts string) (DogfoodVerdict, error) {
		return DogfoodVerdict{Verbs: 40, Open: len(many), Findings: many}, nil
	}
	cli, receipts := noOpenEdge(t)
	var out, errs bytes.Buffer
	Run("nova-update", cutArgs(changelogIn(t, t.TempDir()), "--cli", cli, "--receipts", receipts), &out, &errs, deps)
	if got := strings.Count(errs.String(), "DOGFOOD GATE FAIL"); got != dogfoodFindingCap {
		t.Fatalf("printed %d findings, want the cap of %d:\n%s", got, dogfoodFindingCap, errs.String())
	}
	if !strings.Contains(errs.String(), "DOGFOOD GATE MORE open=40 shown=10") {
		t.Fatalf("the ceiling was not named:\n%s", errs.String())
	}
}

// ---------------------------------------------------------------------------
// the section
// ---------------------------------------------------------------------------

func TestTheSectionCarriesNoWaiverWhenThereWasNone(t *testing.T) {
	s := Section("v0.16.0", "abc", "v0.15.10", "", "", time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC), nil)
	if strings.Contains(s, DogfoodWaiverPrefix) {
		t.Fatalf("a gated release wrote a waiver line:\n%s", s)
	}
}

// The help says the gate exists where a person will meet it.
func TestTheHelpSaysWhatTheGateIs(t *testing.T) {
	var out, errs bytes.Buffer
	if code := Run("nova-update", []string{"help"}, &out, &errs, Deps{}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	for _, want := range []string{"--no-dogfood-gate", "--receipts", "open edge"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("release help does not name %q", want)
		}
	}
}
