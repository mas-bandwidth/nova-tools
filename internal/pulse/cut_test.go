package pulse

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTemplates lays down a fake templates directory with models.tsv and one .md per kind.
func writeTemplates(t *testing.T, dir string, kind map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "models.tsv"), []byte("flash opencode/deepseek-v4-flash\npro opencode/deepseek-v4-pro\n"), 0o644); err != nil {
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
STEP 1. mkdir -p scratch && export TMPDIR=$PWD/scratch && git clone -q https://github.com/<source>.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP 2. Read the named files and write notes.txt in the repo directory.
STEP last. Write RESULT.md with line 1 equal to this card's line 1.`

const fixTemplate = `RESULT <label> sha=<sha12>
You are a worker. The deadline is the machinery's.
STEP 1. mkdir -p scratch && export TMPDIR=$PWD/scratch && git clone -q https://github.com/<source>.git . && git checkout -b <branch>
   check: git rev-parse HEAD prints a head.
STEP 2. Make the fix; report the red line and then the green line, one row per item.
STEP last. Write RESULT.md with line 1 equal to this card's line 1.`

func runCut(t *testing.T, templates map[string]string, pool string) (int, string, string, string) {
	t.Helper()
	td := t.TempDir()
	tmpl := filepath.Join(td, "templates")
	out := filepath.Join(td, "out")
	root := filepath.Join(td, "root")
	writeTemplates(t, tmpl, templates)
	poolPath := filepath.Join(td, "pool.tsv")
	if err := os.WriteFile(poolPath, []byte(pool), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := Cut(CutInput{
		Pool: poolPath, Templates: tmpl, Out: out, Root: root, Max: 20,
		Stdout: &stdout, Stderr: &stderr,
	})
	return code, stdout.String(), stderr.String(), out
}

func cutLine1(t *testing.T, cards string) string {
	t.Helper()
	lines := strings.SplitN(cards, "\n", 2)
	return lines[0]
}

// cut-line1-is-contract: every card written has line 1 RESULT <label> sha=<sha12> with the
// hash equal to SHA-256 of the bytes below line 1, and STEP 1 carrying mkdir -p scratch,
// TMPDIR, an https:// clone and checkout -b; a template whose rendered line 1 is prose, or
// whose STEP 1 clones git@, is CUT REFUSED and writes no card.
func TestCutLine1IsContract(t *testing.T) {
	for kind, tmpl := range map[string]string{"read": readTemplate, "fix": fixTemplate} {
		code, stdout, _, out := runCut(t, map[string]string{kind: tmpl}, "mas-bandwidth/nova-tools\t1\t"+kind+"\tTitle\t"+kind+"\n")
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
		for _, want := range []string{"mkdir -p scratch", "TMPDIR", "https://", "checkout -b"} {
			if !strings.Contains(step1, want) {
				t.Fatalf("STEP 1 %q lacks %q", step1, want)
			}
		}
	}

	// A template whose rendered line 1 is prose is refused, no card written.
	prose := "Read this document carefully.\n" + readTemplate[strings.Index(readTemplate, "\n")+1:]
	code, _, stderr, out := runCut(t, map[string]string{"read": prose}, "mas-bandwidth/nova-tools\t1\tread\tTitle\tread\n")
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
	code, _, stderr, out = runCut(t, map[string]string{"read": gitAt}, "mas-bandwidth/nova-tools\t1\tread\tTitle\tread\n")
	if code != 2 || !strings.Contains(stderr, "does not clone over https") {
		t.Fatalf("git@ clone: code=%d stderr=%q, want CUT REFUSED naming https", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(out, "1.md")); err == nil {
		t.Fatal("git@ clone: a card was written despite the refusal")
	}
}

// cut-text-template-forbids-build: read, text and tone cards each carry the no-build line; a
// text.md lacking it is CUT REFUSED template=text; fix, replay and drift cards carry the
// red-then-green row rule; no template mentions ../scratch.
func TestCutTextTemplateForbidsBuild(t *testing.T) {
	tmpls := map[string]string{"read": readTemplate, "fix": fixTemplate}
	code, stdout, _, out := runCut(t, tmpls,
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
	code, _, stderr, out := runCut(t, map[string]string{"text": noBuild}, "mas-bandwidth/nova-tools\t1\ttext\tTitle\ttext\n")
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
	code, _, stderr, _ = runCut(t, map[string]string{"drift": noRed}, "mas-bandwidth/nova-tools\t1\tdrift\tTitle\tdrift\n")
	if code != 2 || !strings.Contains(stderr, "red-then-green") {
		t.Fatalf("drift no red/green: code=%d stderr=%q, want CUT REFUSED naming the red/green rule", code, stderr)
	}

	// A template that mentions ../scratch is refused.
	scratch := strings.Replace(readTemplate, "notes.txt in the repo directory", "notes.txt in ../scratch", 1)
	code, _, stderr, _ = runCut(t, map[string]string{"read": scratch}, "mas-bandwidth/nova-tools\t1\tread\tTitle\tread\n")
	if code != 2 || !strings.Contains(stderr, "../scratch") {
		t.Fatalf("scratch path: code=%d stderr=%q, want CUT REFUSED naming ../scratch", code, stderr)
	}
}

// cutModelByKind: model is decided by kind only -- flash on read, pro on fix -- and the
// ids come from models.tsv; --local names an ollama/<tag> for read and text.
func TestCutModelByKind(t *testing.T) {
	tmpls := map[string]string{"read": readTemplate, "fix": fixTemplate, "text": readTemplate, "tone": readTemplate, "replay": fixTemplate, "drift": fixTemplate}
	pool := "s\t1\tread\tt\tread\ns\t2\tfix\tt\tfix\ns\t3\ttext\tt\ttext\ns\t4\ttone\tt\ttone\ns\t5\treplay\tt\treplay\ns\t6\tdrift\tt\tdrift\n"
	code, stdout, _, out := runCut(t, tmpls, pool)
	if code != 0 {
		t.Fatalf("cut = %d, want 0; stdout=%s", code, stdout)
	}
	if !strings.Contains(stdout, "flash=3 pro=3") {
		t.Fatalf("stdout=%q, want flash=3 pro=3", stdout)
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
	if rows["1"] != "opencode/deepseek-v4-flash" || rows["2"] != "opencode/deepseek-v4-pro" {
		t.Fatalf("model by kind wrong: %v", rows)
	}

	// A candidate whose template does not exist is skipped, one CUT SKIPPED line, exit 1.
	code, stdout, stderr, out := runCut(t, map[string]string{"read": readTemplate}, "s\t1\tread\tt\tread\ns\t2\tread\tt\tprobe\n")
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
