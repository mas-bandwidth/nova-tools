package pulse

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// defaultBenches is the cost table writeTemplates lays down unless a test names its own:
// a flat flash route that can hold read, text and replay, and a flat pro route for code.
const defaultBenches = "model\topencode/deepseek-v4-flash\topencode/deepseek-v4-pro\ncost\tflat 1.0\tflat 1.5\ncapability\tread|text|replay\tcode\n"

// writeTemplates lays down a fake templates directory with the cost table and one .md per
// kind.
func writeTemplates(t *testing.T, dir string, kind map[string]string, benches string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "benches.tsv"), []byte(benches), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range kind {
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

const readTemplate = `RESULT <label> sha=<sha12>
You are a worker. The deadline is the machinery's.
Do not run go build, go test or any toolchain; read and write only.
STEP 1. mkdir -p scratch && git clone -q https://github.com/<source>.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP 2. Read the named files and write notes.txt in the repo directory.
STEP last. Write RESULT.md with line 1 equal to this card's line 1.`

const fixTemplate = `RESULT <label> sha=<sha12>
You are a worker. The deadline is the machinery's.
STEP 1. mkdir -p scratch && git clone -q https://github.com/<source>.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP 2. Make the fix; report the red line and then the green line, one row per item.
STEP last. Write RESULT.md with line 1 equal to this card's line 1.`

func runCut(t *testing.T, templates map[string]string, pool string) (int, string, string, string, string) {
	t.Helper()
	return runCutTable(t, defaultBenches, templates, pool)
}

// runCutTable is runCut against a cost table of a test's own choosing.
func runCutTable(t *testing.T, benches string, templates map[string]string, pool string) (int, string, string, string, string) {
	t.Helper()
	return runCutAt(t, t.TempDir(), benches, templates, pool, "")
}

// runCutAt runs cut once inside a caller-owned directory, so a test can write retry.tsv
// into the same root between two runs and watch the route move.
func runCutAt(t *testing.T, td, benches string, templates map[string]string, pool, retry string) (int, string, string, string, string) {
	t.Helper()
	tmpl := filepath.Join(td, "templates")
	out := filepath.Join(td, "out")
	root := filepath.Join(td, "root")
	writeTemplates(t, tmpl, templates, benches)
	poolPath := filepath.Join(td, "pool.tsv")
	if err := os.WriteFile(poolPath, []byte(pool), 0o644); err != nil {
		t.Fatal(err)
	}
	if retry != "" {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "retry.tsv"), []byte(retry), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr strings.Builder
	code := Cut(CutInput{
		Pool: poolPath, Templates: tmpl, Out: out, Root: root, Max: 20,
		Stdout: &stdout, Stderr: &stderr,
	})
	return code, stdout.String(), stderr.String(), out, root
}

func cutLine1(t *testing.T, cards string) string {
	t.Helper()
	lines := strings.SplitN(cards, "\n", 2)
	return lines[0]
}

// cut-line1-is-contract: every card written has line 1 RESULT <label> sha=<sha12> with the
// hash equal to SHA-256 of the bytes below line 1, and STEP 1 carrying mkdir -p scratch,
// an https:// clone and checkout -b; a template whose rendered line 1 is prose, or
// whose STEP 1 clones git@, is CUT REFUSED and writes no card.
func TestCutLine1IsContract(t *testing.T) {
	for kind, tmpl := range map[string]string{"read": readTemplate, "fix": fixTemplate} {
		code, stdout, _, out, _ := runCut(t, map[string]string{kind: tmpl}, "mas-bandwidth/nova-tools\t1\t"+kind+"\tTitle\t"+kind+"\n")
		if code != 0 {
			t.Fatalf("cut(%s) = %d, want 0; stdout=%s", kind, code, stdout)
		}
		card, err := os.ReadFile(filepath.Join(out, "1.md"))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(card), "\n")
		line1 := lines[0]
		if !strings.HasPrefix(line1, "RESULT 1 sha=") {
			t.Fatalf("line 1 = %q, want RESULT 1 sha=<12 hex>", line1)
		}
		sha12 := strings.TrimPrefix(line1, "RESULT 1 sha=")
		if len(sha12) != 12 {
			t.Fatalf("sha12 = %q, want 12 hex", sha12)
		}
		body := strings.Join(lines[1:], "\n")
		sum := sha256.Sum256([]byte(body))
		if sha12 != hex.EncodeToString(sum[:])[:12] {
			t.Fatalf("sha12 %q != sha256(body) %q", sha12, hex.EncodeToString(sum[:])[:12])
		}
		step1 := ""
		for _, ln := range lines {
			if strings.HasPrefix(ln, "STEP 1") {
				step1 = ln
				break
			}
		}
		for _, want := range []string{"mkdir -p scratch", "https://", "checkout -b"} {
			if !strings.Contains(step1, want) {
				t.Fatalf("STEP 1 %q lacks %q", step1, want)
			}
		}
	}

	// A template whose rendered line 1 is prose is refused, no card written.
	prose := "Read this document carefully.\n" + readTemplate[strings.Index(readTemplate, "\n")+1:]
	code, _, stderr, out, _ := runCut(t, map[string]string{"read": prose}, "mas-bandwidth/nova-tools\t1\tread\tTitle\tread\n")
	if code != 2 || !strings.Contains(stderr, "CUT REFUSED") {
		t.Fatalf("prose line 1: code=%d stderr=%q, want CUT REFUSED", code, stderr)
	}
	if !strings.Contains(stderr, "line 1 is not the RESULT contract") {
		t.Fatalf("prose line 1: stderr=%q, want the rule named", stderr)
	}
	if _, err := os.Stat(filepath.Join(out, "1.md")); err == nil {
		t.Fatal("prose line 1: a card was written despite the refusal")
	}

	// A STEP 1 that clones git@ is refused.
	gitAt := strings.Replace(readTemplate, "https://github.com/<source>.git", "git@github.com:<source>.git", 1)
	code, _, stderr, out, _ = runCut(t, map[string]string{"read": gitAt}, "mas-bandwidth/nova-tools\t1\tread\tTitle\tread\n")
	if code != 2 || !strings.Contains(stderr, "does not clone over https") {
		t.Fatalf("git@ clone: code=%d stderr=%q, want CUT REFUSED naming https", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(out, "1.md")); err == nil {
		t.Fatal("git@ clone: a card was written despite the refusal")
	}
}

// cut-step-one-sets-no-tmpdir: the STEP 1 a template renders sets no TMPDIR of its own
// (#460, the template half that follows the runner's): native exports
// TMPDIR=<slot>/tmp/<label> --
// the slot directory is never a repo, while admission git-inits the job directory -- and
// prints tmp=<path> on NATIVE OK, so a card that exports its own puts every t.TempDir()
// inside the job's repo, which is the red cards 247, 266 and 353 reported and did not
// cause. cut accepts a STEP 1 that only mkdirs, clones over https and checks out, and
// refuses one that sets a TMPDIR, naming the rule, no card written.
func TestCutStepOneSetsNoTmpDir(t *testing.T) {
	// The STEP 1 every template now carries: no export, because the runner's own is
	// already in the child's environment.
	bare := strings.Replace(readTemplate, " && export TMPDIR=$PWD/scratch", "", 1)
	code, _, stderr, out, _ := runCut(t, map[string]string{"read": bare}, "mas-bandwidth/nova-tools\t1\tread\tTitle\tread\n")
	if code != 0 {
		t.Fatalf("a STEP 1 with no TMPDIR export is cut, got %d, stderr=%q", code, stderr)
	}
	card, err := os.ReadFile(filepath.Join(out, "1.md"))
	if err != nil {
		t.Fatal(err)
	}
	step1 := ""
	for _, ln := range strings.Split(string(card), "\n") {
		if strings.HasPrefix(ln, "STEP 1") {
			step1 = ln
			break
		}
	}
	if strings.Contains(step1, "TMPDIR") {
		t.Errorf("the card's STEP 1 is %q; the runner exports TMPDIR and a card sets none", step1)
	}

	// The hurt itself: export TMPDIR=$PWD/scratch put the card's temp dir inside the
	// job's git repo. A template that still sets one is refused, no card written.
	exporting := strings.Replace(bare, "mkdir -p scratch &&", "mkdir -p scratch && export TMPDIR=$PWD/scratch &&", 1)
	code, _, stderr, out, _ = runCut(t, map[string]string{"read": exporting}, "mas-bandwidth/nova-tools\t1\tread\tTitle\tread\n")
	if code != 2 || !strings.Contains(stderr, "CUT REFUSED template=read") {
		t.Fatalf("a STEP 1 that sets TMPDIR is CUT REFUSED template=read, got %d, stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "STEP 1 sets TMPDIR") {
		t.Errorf("the refusal names the rule, got %q", stderr)
	}
	if _, err := os.Stat(filepath.Join(out, "1.md")); err == nil {
		t.Error("a card was written despite the refusal")
	}
}

// cut-text-template-forbids-build: read, text and tone cards each carry the no-build line; a
// text.md lacking it is CUT REFUSED template=text; fix, replay and drift cards carry the
// red-then-green row rule; no template mentions ../scratch.
func TestCutTextTemplateForbidsBuild(t *testing.T) {
	tmpls := map[string]string{"read": readTemplate, "fix": fixTemplate}
	code, stdout, _, out, _ := runCut(t, tmpls,
		"mas-bandwidth/nova-tools\t1\tread\tTitle\tread\nmas-bandwidth/nova-tools\t2\tfix\tTitle\tfix\n")
	if code != 0 {
		t.Fatalf("cut = %d, want 0; stdout=%s", code, stdout)
	}
	read, err := os.ReadFile(filepath.Join(out, "1.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(read), "Do not run go build, go test or any toolchain") {
		t.Fatalf("read card %q lacks the no-build line", read)
	}
	fix, err := os.ReadFile(filepath.Join(out, "2.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fix), "red line") || !strings.Contains(string(fix), "green line") {
		t.Fatalf("fix card %q lacks the red-then-green row rule", fix)
	}

	// A text.md fixture lacking the no-build line is refused, naming the template.
	noBuild := strings.Replace(readTemplate, "Do not run go build, go test or any toolchain; read and write only.\n", "", 1)
	code, _, stderr, out, _ := runCut(t, map[string]string{"text": noBuild}, "mas-bandwidth/nova-tools\t1\ttext\tTitle\ttext\n")
	if code != 2 || !strings.Contains(stderr, "CUT REFUSED") {
		t.Fatalf("text no-build: code=%d stderr=%q, want CUT REFUSED template=text", code, stderr)
	}
	if !strings.Contains(stderr, "no-build line") {
		t.Fatalf("text no-build: stderr=%q, want the rule named", stderr)
	}
	if _, err := os.Stat(filepath.Join(out, "1.md")); err == nil {
		t.Fatal("text no-build: a card was written despite the refusal")
	}

	// A writing card lacking the red/green rule is refused.
	noRed := strings.Replace(fixTemplate, "red line", "line", 1)
	code, _, stderr, _, _ = runCut(t, map[string]string{"drift": noRed}, "mas-bandwidth/nova-tools\t1\tdrift\tTitle\tdrift\n")
	if code != 2 || !strings.Contains(stderr, "red-then-green") {
		t.Fatalf("drift no red/green: code=%d stderr=%q, want CUT REFUSED naming the red/green rule", code, stderr)
	}

	// A template that mentions ../scratch is refused.
	scratch := strings.Replace(readTemplate, "notes.txt in the repo directory", "notes.txt in ../scratch", 1)
	code, _, stderr, _, _ = runCut(t, map[string]string{"read": scratch}, "mas-bandwidth/nova-tools\t1\tread\tTitle\tread\n")
	if code != 2 || !strings.Contains(stderr, "../scratch") {
		t.Fatalf("scratch path: code=%d stderr=%q, want CUT REFUSED naming ../scratch", code, stderr)
	}
}

// cutModelByKind: model is decided by the cost table -- flash (read|text|replay) on
// read/text/tone/replay, pro (code) on fix/drift -- never by kind alone.
func TestCutModelByKind(t *testing.T) {
	tmpls := map[string]string{"read": readTemplate, "fix": fixTemplate, "text": readTemplate, "tone": readTemplate, "replay": fixTemplate, "drift": fixTemplate}
	pool := "s\t1\tread\tt\tread\ns\t2\tfix\tt\tfix\ns\t3\ttext\tt\ttext\ns\t4\ttone\tt\ttone\ns\t5\treplay\tt\treplay\ns\t6\tdrift\tt\tdrift\n"
	code, stdout, _, out, _ := runCut(t, tmpls, pool)
	if code != 0 {
		t.Fatalf("cut = %d, want 0; stdout=%s", code, stdout)
	}
	if !strings.Contains(stdout, "zero=0 flat=6 metered=0") {
		t.Fatalf("stdout=%q, want zero=0 flat=6 metered=0", stdout)
	}
	raw, err := os.ReadFile(filepath.Join(out, "cards.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		parts := strings.Split(line, "\t")
		rows[parts[0]] = parts[2]
	}
	if rows["1"] != "opencode/deepseek-v4-flash" || rows["2"] != "opencode/deepseek-v4-pro" || rows["5"] != "opencode/deepseek-v4-flash" {
		t.Fatalf("model by cost table wrong: %v", rows)
	}

	// A candidate whose template does not exist is skipped, one CUT SKIPPED line, exit 1.
	code, stdout, stderr, out, _ := runCut(t, map[string]string{"read": readTemplate}, "s\t1\tread\tt\tread\ns\t2\tread\tt\tprobe\n")
	if code != 1 {
		t.Fatalf("skip = %d, want 1; stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "cards=1 skipped=1") {
		t.Fatalf("stdout=%q, want cards=1 skipped=1", stdout)
	}
	if !strings.Contains(stderr, "CUT SKIPPED source=s id=2 template=probe: no template") {
		t.Fatalf("stderr=%q, want a CUT SKIPPED line", stderr)
	}
}

// cut-steps-are-the-turn-budget: a read card is at most 8 turns and a fix card at most 20;
// a template whose rendered card exceeds its budget is CUT REFUSED naming the rule, no card
// written (#855: every tool call re-sends the whole context, so the step count is the bill).
func TestCutStepsAreTheTurnBudget(t *testing.T) {
	card := func(kind string, steps int) string {
		var b strings.Builder
		b.WriteString("RESULT <label> sha=<sha12>\nYou are a worker. The deadline is the machinery's.\n")
		if kind == "read" {
			b.WriteString("Do not run go build, go test or any toolchain; read and write only.\n")
		}
		b.WriteString("STEP 1. mkdir -p scratch && git clone -q https://github.com/<source>.git . && git checkout -b <branch>\n")
		for i := 2; i <= steps; i++ {
			if kind == "read" {
				b.WriteString(fmt.Sprintf("STEP %d. Read file.go:%d and write the note.\n", i, i))
			} else {
				b.WriteString(fmt.Sprintf("STEP %d. Fix file.go:%d; report the red line and then the green line.\n", i, i))
			}
		}
		return b.String()
	}

	// A read card of exactly eight turns is cut.
	if code, _, stderr, out, _ := runCut(t, map[string]string{"read": card("read", 8)}, "s\t1\tread\tt\tread\n"); code != 0 {
		t.Fatalf("8-step read card: cut = %d, want 0; stderr=%q", code, stderr)
	} else if _, err := os.Stat(filepath.Join(out, "1.md")); err != nil {
		t.Fatal(err)
	}

	// A read card of nine turns is refused, naming the 8-turn budget, no card written.
	code, _, stderr, out, _ := runCut(t, map[string]string{"read": card("read", 9)}, "s\t1\tread\tt\tread\n")
	if code != 2 || !strings.Contains(stderr, "over the 8-turn budget") {
		t.Fatalf("9-step read card: code=%d stderr=%q, want CUT REFUSED naming the 8-turn budget", code, stderr)
	}
	if !strings.Contains(stderr, "CUT REFUSED template=read") {
		t.Fatalf("9-step read card: stderr=%q, want CUT REFUSED template=read", stderr)
	}
	if _, err := os.Stat(filepath.Join(out, "1.md")); err == nil {
		t.Fatal("9-step read card: a card was written despite the refusal")
	}

	// A fix card gets twenty turns; twenty-one is refused.
	if code, _, stderr, _, _ := runCut(t, map[string]string{"fix": card("fix", 20)}, "s\t1\tfix\tt\tfix\n"); code != 0 {
		t.Fatalf("20-step fix card: cut = %d, want 0; stderr=%q", code, stderr)
	}
	code, _, stderr, out, _ = runCut(t, map[string]string{"fix": card("fix", 21)}, "s\t1\tfix\tt\tfix\n")
	if code != 2 || !strings.Contains(stderr, "over the 20-turn budget") {
		t.Fatalf("21-step fix card: code=%d stderr=%q, want CUT REFUSED naming the 20-turn budget", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(out, "1.md")); err == nil {
		t.Fatal("21-step fix card: a card was written despite the refusal")
	}
}

// cut-holds-to-one-model: a models.tsv that names only flash is legal, and every card --
// including a fix card that would take pro when both are named -- routes to that one model,
// so a spend rule ("flash only tonight") is expressible without a --model flag (#635).
func TestCutHoldsToOneModel(t *testing.T) {
	td := t.TempDir()
	tmpl := filepath.Join(td, "templates")
	if err := os.MkdirAll(tmpl, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpl, "models.tsv"), []byte("flash opencode/deepseek-v4-flash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"read": readTemplate, "fix": fixTemplate} {
		if err := os.WriteFile(filepath.Join(tmpl, name+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	poolPath := filepath.Join(td, "pool.tsv")
	if err := os.WriteFile(poolPath, []byte("s\t1\tread\tt\tread\ns\t2\tfix\tt\tfix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(td, "out")
	var stdout, stderr strings.Builder
	code := Cut(CutInput{
		Pool: poolPath, Templates: tmpl, Out: out, Root: filepath.Join(td, "root"), Max: 20,
		Stdout: &stdout, Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("cut = %d, want 0 (one model is a spend rule, not a refusal); stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "flash=2 pro=0") {
		t.Fatalf("stdout=%q, want flash=2 pro=0 (every card holds to flash)", stdout.String())
	}
	raw, err := os.ReadFile(filepath.Join(out, "cards.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 3 || parts[2] != "opencode/deepseek-v4-flash" {
			t.Fatalf("cards.tsv row %q, want model opencode/deepseek-v4-flash", line)
		}
	}
}

// cut-picks-cheapest-capable-route (SPEC-PULSE replay 24): a benches table with a
// zero-cost local, a flat Go and a metered Zen, and one card per capability class, yields
// route=<model> reason=<class> on CUT ROUTE for the cheapest capable model -- the mutation
// that matters: a pick that ignores cost and takes a route by kind.
func TestCutPicksCheapestCapableRoute(t *testing.T) {
	table := "model\tollama/local\topencode/go\topencode/zen\ncost\tzero 0\tflat 1.5\tmetered 8\ncapability\tread|text\tcode\treplay\n"
	tmpls := map[string]string{"read": readTemplate, "fix": fixTemplate, "text": readTemplate, "tone": readTemplate, "replay": fixTemplate, "drift": fixTemplate}
	pool := "s\t1\tread\tt\tread\ns\t2\tfix\tt\tfix\ns\t3\ttext\tt\ttext\ns\t4\ttone\tt\ttone\ns\t5\treplay\tt\treplay\ns\t6\tdrift\tt\tdrift\n"
	code, stdout, _, out, _ := runCutTable(t, table, tmpls, pool)
	if code != 0 {
		t.Fatalf("cut = %d, want 0; stdout=%s", code, stdout)
	}
	for _, want := range []string{
		"CUT ROUTE route=ollama/local reason=zero",
		"CUT ROUTE route=opencode/go reason=flat",
		"CUT ROUTE route=opencode/zen reason=metered",
		"CUT OK cards=6 skipped=0 zero=3 flat=2 metered=1",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout=%q, want %q", stdout, want)
		}
	}
	raw, err := os.ReadFile(filepath.Join(out, "cards.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		parts := strings.Split(line, "\t")
		rows[parts[0]] = parts[2]
	}
	want := map[string]string{
		"1": "ollama/local", "2": "opencode/go", "3": "ollama/local",
		"4": "ollama/local", "5": "opencode/zen", "6": "opencode/go",
	}
	for id, m := range want {
		if rows[id] != m {
			t.Fatalf("cards.tsv model for %s = %q, want %q (rows=%v)", id, rows[id], m, rows)
		}
	}
}

// retry-moves-one-class-up (SPEC-PULSE replay 25): a card rewritten from retry.tsv after
// an abstain routes one capability class above the first attempt's, so CUT ROUTE names a
// stronger class and a reason that reflects it.
func TestRetryMovesOneClassUp(t *testing.T) {
	table := "model\tollama/local\topencode/go\topencode/zen\ncost\tzero 0\tflat 1.5\tmetered 8\ncapability\tread\tcode\tcode|replay\n"
	tmpls := map[string]string{"fix": fixTemplate}
	pool := "s\t1\tfix\tt\tfix\n"
	td := t.TempDir()

	// First attempt: the cheapest code-capable route is Go (flat).
	code, stdout, _, _, _ := runCutAt(t, td, table, tmpls, pool, "")
	if code != 0 {
		t.Fatalf("first cut = %d, want 0; stdout=%s", code, stdout)
	}
	if !strings.Contains(stdout, "CUT ROUTE route=opencode/go reason=flat") {
		t.Fatalf("first attempt stdout=%q, want CUT ROUTE route=opencode/go reason=flat", stdout)
	}

	// The rewritten card (its label is in retry.tsv) routes one capability class above the
	// first attempt's: Zen, the cheapest code-capable route above Go, reason reflecting the
	// metered class.
	code, stdout, _, _, _ = runCutAt(t, td, table, tmpls, pool, "1\t/path/to/card\tpermission denied\n")
	if code != 0 {
		t.Fatalf("retry cut = %d, want 0; stdout=%s", code, stdout)
	}
	if !strings.Contains(stdout, "CUT ROUTE route=opencode/zen reason=metered") {
		t.Fatalf("retry stdout=%q, want CUT ROUTE route=opencode/zen reason=metered", stdout)
	}
}
