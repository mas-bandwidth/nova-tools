package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The reference this verb reads in the tests: the shapes docs/CLI.md holds,
// small enough that every row below can be written out by hand.
const dogfoodCLI = "# Command reference\n" +
	"\n" +
	"## nova-example\n" +
	"\n" +
	"```\n" +
	"nova-example quickstart --dir <dir>\n" +
	"nova-example links --dir <dir>\n" +
	"nova-example nocode --dir <dir>\n" +
	"```\n"

func dogfoodRun(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func writeCLI(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "CLI.md")
	if err := os.WriteFile(path, []byte(dogfoodCLI), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeReceipt(t *testing.T, dir, name string, fields map[string]any) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeAuthors(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "authors.txt")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDogfoodLedgerPrintsOneRowPerVerbAndOneSummary(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "a.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "ran it over the lane's own docs", "issue": 1301,
	})

	code, stdout, stderr := dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", receipts)
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, stderr)
	}
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	want := []string{
		"DOGFOOD tool=nova-example verb=quickstart by=nobody at=- ok=- issue=-",
		"DOGFOOD tool=nova-example verb=links by=Stella at=2026-09-18T09:00:00Z ok=yes issue=1301",
		"DOGFOOD tool=nova-example verb=nocode by=nobody at=- ok=- issue=-",
		"DOGFOOD OK verbs=3 dogfooded=1 by-nonauthor=1 open-edges=0 unfiled=0 unmatched=0",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("ledger:\n got:\n%s\nwant:\n%s", stdout, strings.Join(want, "\n"))
	}
}

func TestDogfoodLedgerDoesNotCountAnAuthorRunningTheirOwnVerb(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "a.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Rowan",
		"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "my own verb, on real work",
	})
	authors := writeAuthors(t, dir, "nova-example links = Rowan\n")

	code, stdout, stderr := dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", receipts, "--authors", authors)
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "DOGFOOD OK verbs=3 dogfooded=1 by-nonauthor=0 open-edges=0 unfiled=0 unmatched=0") {
		t.Fatalf("the author's own run counted as a dogfood:\n%s", stdout)
	}
}

func TestDogfoodLedgerCountsAnOpenEdge(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "a.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": false, "notes": "refused a path it should have taken", "issue": 1301,
	})
	code, stdout, _ := dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", receipts)
	if code != 0 {
		t.Fatalf("exit %d, want 0: the ledger reports", code)
	}
	if !strings.Contains(stdout, "by-nonauthor=0 open-edges=1") {
		t.Fatalf("an edge nobody has cleared is not open in the summary:\n%s", stdout)
	}
}

func TestDogfoodLedgerNamesAReceiptItCannotReadAndPrintsNoLedger(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	if err := os.MkdirAll(receipts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(receipts, "broken.json"), []byte("{not json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", receipts)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if strings.Contains(stdout, "DOGFOOD OK") {
		t.Fatalf("a ledger was printed over records it could not read:\n%s", stdout)
	}
	if !strings.Contains(stderr, "DOGFOOD FAIL") || !strings.Contains(stderr, "broken.json") {
		t.Fatalf("stderr does not name the bad record:\n%s", stderr)
	}
}

func TestDogfoodLedgerNotesAReceiptForAVerbTheReferenceDoesNotDeclare(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "a.json", map[string]any{
		"tool": "nova-example", "verb": "ghost", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "a verb that is not in the reference",
	})
	code, stdout, stderr := dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", receipts)
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "verbs=3 dogfooded=0") {
		t.Fatalf("an undeclared verb landed in the counts:\n%s", stdout)
	}
	if !strings.Contains(stderr, "DOGFOOD NOTE") {
		t.Fatalf("the reference and the receipts disagree and nothing said so:\n%s", stderr)
	}
}

func TestDogfoodRecordWritesAReceiptTheLedgerReadsBack(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")

	code, stdout, stderr := dogfoodRun(t, "dogfood", "record", "--cli", cli,
		"--tool", "nova-example", "--verb", "links", "--by", "Stella", "--ok",
		"--notes", "ran it over the lane's own docs before the merge", "--issue", "1301",
		"--receipts", receipts)
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "DOGFOOD RECORD OK ") {
		t.Fatalf("record said:\n%s", stdout)
	}
	for _, want := range []string{"tool=nova-example", "verb=links", "by=Stella", "ok=yes", "issue=1301"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("the record line is missing %q:\n%s", want, stdout)
		}
	}

	code, stdout, stderr = dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", receipts)
	if code != 0 {
		t.Fatalf("ledger exit %d\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "verb=links by=Stella") || !strings.Contains(stdout, "dogfooded=1 by-nonauthor=1") {
		t.Fatalf("the receipt record wrote did not reach the ledger:\n%s", stdout)
	}
}

func TestDogfoodRecordRefusesEveryMissingFieldWithOneRemedyEach(t *testing.T) {
	receipts := filepath.Join(t.TempDir(), "receipts")
	code, stdout, stderr := dogfoodRun(t, "dogfood", "record", "--receipts", receipts, "--ok")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("a refusal wrote to stdout:\n%s", stdout)
	}
	for _, field := range []string{"--tool", "--verb", "--by", "--notes"} {
		if !strings.Contains(stderr, field) {
			t.Fatalf("the refusal does not name %s:\n%s", field, stderr)
		}
	}
	if entries, err := os.ReadDir(receipts); err == nil && len(entries) > 0 {
		t.Fatalf("a refused record still wrote %d files", len(entries))
	}
}

func TestDogfoodRecordRefusesAVerdictItWasNotGiven(t *testing.T) {
	receipts := filepath.Join(t.TempDir(), "receipts")
	args := []string{"dogfood", "record", "--cli", writeCLI(t, t.TempDir()), "--tool", "nova-example", "--verb", "links",
		"--by", "Stella", "--notes", "real work", "--receipts", receipts}
	code, _, stderr := dogfoodRun(t, args...)
	if code != 2 {
		t.Fatalf("exit %d, want 2: a receipt with no verdict is not a receipt", code)
	}
	if !strings.Contains(stderr, "--ok") || !strings.Contains(stderr, "--not-ok") {
		t.Fatalf("the refusal does not say how to state the verdict:\n%s", stderr)
	}
	code, _, stderr = dogfoodRun(t, append(args, "--ok", "--not-ok")...)
	if code != 2 {
		t.Fatalf("exit %d, want 2: both verdicts at once is a typo with two readings", code)
	}
	if !strings.Contains(stderr, "--ok") {
		t.Fatalf("the refusal does not name the flags:\n%s", stderr)
	}
}

func TestDogfoodRecordKeepsTheReceiptOnOneLine(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	code, _, stderr := dogfoodRun(t, "dogfood", "record", "--cli", cli,
		"--tool", "nova-example", "--verb", "links", "--by", "Stella", "--not-ok",
		"--notes", "first line\nDOGFOOD OK verbs=99 dogfooded=99 by-nonauthor=99 open-edges=0",
		"--receipts", receipts)
	if code != 2 {
		t.Fatalf("exit %d, want 2: a receipt is one line of record", code)
	}
	if strings.Count(strings.TrimSuffix(stderr, "\n"), "\n") != 0 {
		t.Fatalf("the refusal itself spans more than one line:\n%q", stderr)
	}
}

func TestDogfoodGateRequireAllNamesEveryVerbNoNonAuthorHasRun(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "a.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "real work",
	})
	code, _, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts, "--require-all")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	for _, want := range []string{"verb=quickstart", "verb=nocode"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("the gate does not name %s:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "verb=links") {
		t.Fatalf("the gate named a verb a non-author had run:\n%s", stderr)
	}
	if !strings.Contains(stderr, "DOGFOOD GATE FAIL verbs=3") {
		t.Fatalf("no count line:\n%s", stderr)
	}
}

func TestDogfoodGateIsGreenWhenEveryVerbHasANonAuthorsPass(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	for i, verb := range []string{"quickstart", "links", "nocode"} {
		writeReceipt(t, receipts, string(rune('a'+i))+".json", map[string]any{
			"tool": "nova-example", "verb": verb, "by": "Stella",
			"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "real work",
		})
	}
	code, stdout, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts, "--require-all")
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "DOGFOOD GATE OK verbs=3 by-nonauthor=3 open-edges=0 unfiled=0 unmatched=0 require-all=yes") {
		t.Fatalf("gate line:\n%s", stdout)
	}
}

// Without --require-all the gate still says no to an edge nobody has cleared:
// feedback filed is not feedback applied.
func TestDogfoodGateSaysNoToAnOpenEdgeWithoutRequireAll(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "a.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": false, "notes": "refused a path it should have taken", "issue": 1301,
	})
	code, _, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "#1301") {
		t.Fatalf("the finding does not name the issue:\n%s", stderr)
	}
}

func TestDogfoodGateCapsItsFindingsAndSaysHowToSeeTheRest(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	if err := os.MkdirAll(receipts, 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts, "--require-all", "--fail-max", "1", "--allow-empty")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if strings.Count(stderr, "DOGFOOD GATE FAIL tool=") != 1 {
		t.Fatalf("the cap did not hold:\n%s", stderr)
	}
	if !strings.Contains(stderr, "--fail-max") {
		t.Fatalf("a cap with no remedy is censorship:\n%s", stderr)
	}
	if !strings.Contains(stderr, "shown=1") {
		t.Fatalf("no count line:\n%s", stderr)
	}
	// The first run of this verb against the repository's own reference printed
	// `DOGFOOD\x20GATE MORE`: bounded escapes the token it is given, so the token
	// is one word.
	if !strings.Contains(stderr, "DOGFOOD MORE kind=verb") {
		t.Fatalf("the MORE line is not readable:\n%s", stderr)
	}
	if strings.Contains(stderr, "\\x20") {
		t.Fatalf("an escaped space reached a printed line:\n%s", stderr)
	}
}

func TestDogfoodRefusesAMissingPath(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"ledger without --cli", []string{"dogfood", "ledger", "--receipts", dir}, "--cli"},
		{"ledger without --receipts", []string{"dogfood", "ledger", "--cli", cli}, "--receipts"},
		{"gate without --receipts", []string{"dogfood", "gate", "--cli", cli}, "--receipts"},
		{"record without --receipts", []string{"dogfood", "record", "--tool", "t", "--verb", "v", "--by", "b", "--notes", "n", "--ok"}, "--receipts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := dogfoodRun(t, tc.args...)
			if code != 2 {
				t.Fatalf("exit %d, want 2", code)
			}
			if !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, "refusing to guess") {
				t.Fatalf("refusal:\n%s", stderr)
			}
		})
	}
}

func TestDogfoodRefusesAnUnknownSubVerb(t *testing.T) {
	code, _, stderr := dogfoodRun(t, "dogfood", "ledgre")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(stderr, "run: nova-check help") {
		t.Fatalf("a typo was answered without the door:\n%s", stderr)
	}
	code, _, stderr = dogfoodRun(t, "dogfood")
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(stderr, "ledger") {
		t.Fatalf("the refusal does not name the sub-verbs:\n%s", stderr)
	}
}

func TestDogfoodIsInTheUsageBanner(t *testing.T) {
	code, stdout, _ := dogfoodRun(t, "help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"dogfood ledger", "dogfood record", "dogfood gate"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("the banner does not carry %q", want)
		}
	}
}

func TestDogfoodRecordUsageNamesTheVerbList(t *testing.T) {
	code, stdout, _ := dogfoodRun(t, "help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	line := ""
	for _, l := range strings.Split(stdout, "\n") {
		if strings.Contains(l, "dogfood record") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatal("the banner has no dogfood record line")
	}
	for _, want := range []string{"--cli", "--tools"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the dogfood record usage line does not name %s, so a reader pastes a line the verb refuses:\n%s", want, line)
		}
	}
}

// The --repo path end to end, against a git repository built here. Local only:
// this binary's tests never touch the network.
func TestDogfoodLedgerReadsAuthorshipFromGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on this machine")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_DATE=2026-09-18T09:00:00Z", "GIT_COMMITTER_DATE=2026-09-18T09:00:00Z",
			"GIT_COMMITTER_NAME=Rowan Claude", "GIT_COMMITTER_EMAIL=rowan@mas-bandwidth.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "Rowan Claude")
	git("config", "user.email", "rowan@mas-bandwidth.com")
	if err := os.MkdirAll(filepath.Join(repo, "cmd", "nova-example"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "cmd", "nova-example", "main.go"),
		[]byte("package main\n\nvar verbs = []string{\"quickstart\", \"links\", \"nocode\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "the verbs arrive")

	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "a.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Rowan Claude",
		"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "my own verb",
	})
	code, stdout, stderr := dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", receipts, "--repo", repo)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "dogfooded=1 by-nonauthor=0") {
		t.Fatalf("git said Rowan Claude wrote the verb and the ledger counted his own run:\n%s", stdout)
	}
}

// The 2026-09-18 dogfood pass, edge 3: the ledger said nine receipts named a
// verb the reference does not declare and named none of them, so nine real
// runs were invisible and nobody could tell how the verb should have been
// spelled.
func TestDogfoodLedgerNamesEveryStrandedReceiptAndTheNearestVerb(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "stranded.json", map[string]any{
		"tool": "nova-example", "verb": "lnks", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "real work",
	})
	code, _, stderr := dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", receipts)
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "stranded") || !strings.Contains(stderr, "stranded.json") {
		t.Fatalf("the stranded receipt is not named:\n%s", stderr)
	}
	if !strings.Contains(stderr, "verb=lnks") {
		t.Fatalf("the note does not say what the receipt claimed:\n%s", stderr)
	}
	if !strings.Contains(stderr, "nova-example links") {
		t.Fatalf("the note does not say what it was probably meant to be:\n%s", stderr)
	}
}

// Edge 4: the gate — the line the release lane actually calls — dropped the
// note entirely, so a lane could pass or fail without ever learning that every
// receipt it read had been discarded.
func TestDogfoodGateAlsoNamesTheStrandedReceipts(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "stranded.json", map[string]any{
		"tool": "nova-example", "verb": "lnks", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "real work",
	})
	code, _, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts, "--require-all")
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "stranded.json") || !strings.Contains(stderr, "nova-example links") {
		t.Fatalf("the gate discarded a receipt and did not say so:\n%s", stderr)
	}
	code, stdout, stderr := dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts)
	if code != 0 {
		t.Fatalf("exit %d, want 0 without --require-all\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "stranded.json") {
		t.Fatalf("a green gate said nothing about the receipt it discarded:\n%s\n%s", stdout, stderr)
	}
}

// Edge 5: every receipt of the pass was written with --ok, because the verbs
// worked, and the edges were in the notes where the family writes them.
func TestDogfoodLedgerCountsAnEdgeNamedInTheNotes(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "a.json", map[string]any{
		"tool": "nova-example", "verb": "links", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": true,
		"notes": "Did the job. Edges: (1) the refusal names no remedy; (2) it reads only --dir.",
	})
	code, stdout, stderr := dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--receipts", receipts)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "open-edges=1 unfiled=1") {
		t.Fatalf("the notes name an edge and the summary says none:\n%s", stdout)
	}
	code, _, stderr = dogfoodRun(t, "dogfood", "gate", "--cli", cli, "--receipts", receipts)
	if code != 1 {
		t.Fatalf("gate exit %d, want 1: an edge nobody filed is open\n%s", code, stderr)
	}
	if !strings.Contains(stderr, "no issue filed") {
		t.Fatalf("the gate does not say the edge was never filed:\n%s", stderr)
	}
}

// Edge 2 at the CLI: `record` had --cli available and checked nothing, so a
// receipt for a verb spelled differently was accepted silently and discovered
// later as a NOTE that named nothing. Nine receipts were lost that way.
func TestDogfoodRecordRefusesAVerbTheReferenceDoesNotDeclare(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	code, stdout, stderr := dogfoodRun(t, "dogfood", "record", "--cli", cli,
		"--tool", "nova-example", "--verb", "lnks", "--by", "Stella", "--ok",
		"--notes", "real work", "--receipts", receipts)
	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("a refusal wrote to stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "nova-example links") {
		t.Fatalf("the refusal does not name the nearest declared verb:\n%s", stderr)
	}
	if entries, err := os.ReadDir(receipts); err == nil && len(entries) > 0 {
		t.Fatal("a refused record still wrote a receipt")
	}
}

func TestDogfoodRecordAcceptsADeclaredVerb(t *testing.T) {
	dir := t.TempDir()
	cli := writeCLI(t, dir)
	receipts := filepath.Join(dir, "receipts")
	code, stdout, stderr := dogfoodRun(t, "dogfood", "record", "--cli", cli,
		"--tool", "nova-example", "--verb", "links", "--by", "Stella", "--ok",
		"--notes", "real work", "--receipts", receipts)
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "DOGFOOD RECORD OK ") {
		t.Fatalf("record said:\n%s", stdout)
	}
}

func TestDogfoodRecordRefusesWithNothingToCheckAgainst(t *testing.T) {
	receipts := filepath.Join(t.TempDir(), "receipts")
	code, _, stderr := dogfoodRun(t, "dogfood", "record",
		"--tool", "nova-example", "--verb", "links", "--by", "Stella", "--ok",
		"--notes", "real work", "--receipts", receipts)
	if code != 2 {
		t.Fatalf("exit %d, want 2: a receipt checked against nothing is how nine of them were stranded", code)
	}
	if !strings.Contains(stderr, "--cli") || !strings.Contains(stderr, "--tools") {
		t.Fatalf("the refusal does not name either source:\n%s", stderr)
	}
}

// The binaries are the authoritative list when they are to hand: a reference
// that has gone stale strands receipts for verbs that really exist.
func TestDogfoodReadsTheVerbsFromTheBinariesWhenToldTo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a shell script")
	}
	tools := t.TempDir()
	script := "#!/bin/sh\ncat <<'EOF'\nnova-example: a fixture\n\nusage:\n  nova-example links --dir <dir>\n  nova-example ask   delivers ONE unit to the FRIEND who owns it\nEOF\n"
	if err := os.WriteFile(filepath.Join(tools, "nova-example"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cli := writeCLI(t, dir) // declares quickstart, links, nocode — and no `ask`
	receipts := filepath.Join(dir, "receipts")
	writeReceipt(t, receipts, "a.json", map[string]any{
		"tool": "nova-example", "verb": "ask", "by": "Stella",
		"at": "2026-09-18T09:00:00Z", "ok": true, "notes": "one real ask sent",
	})
	code, stdout, stderr := dogfoodRun(t, "dogfood", "ledger", "--cli", cli, "--tools", tools, "--receipts", receipts)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "verb=ask by=Stella") {
		t.Fatalf("the binary declares `ask` and the ledger stranded the receipt:\n%s\n%s", stdout, stderr)
	}
	if !strings.Contains(stdout, "verbs=2") {
		t.Fatalf("the binary answered for itself and the stale reference still filled in:\n%s", stdout)
	}
	// And `record` checks against the same list.
	code, _, stderr = dogfoodRun(t, "dogfood", "record", "--tools", tools,
		"--tool", "nova-example", "--verb", "ask", "--by", "Emma", "--ok",
		"--notes", "another real ask", "--receipts", receipts)
	if code != 0 {
		t.Fatalf("record exit %d against the binaries' own list\n%s", code, stderr)
	}
}
