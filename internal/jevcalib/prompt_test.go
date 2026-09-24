package jevcalib

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/prereview"
)

// TestSeedPromptIsScoreQuestion: the seed file is today's score question
// verbatim, at sha8 fd94795e, with no levels of its own.
func TestSeedPromptIsScoreQuestion(t *testing.T) {
	p, err := Resolve(SeedSha8)
	if err != nil {
		t.Fatal(err)
	}
	if p.Sha8 != "fd94795e" || p.Levels != nil || len(p.Exemplars) != 0 {
		t.Fatalf("seed sha8=%s levels=%d exemplars=%d", p.Sha8, len(p.Levels), len(p.Exemplars))
	}
	if want := prereview.ScoreQuestion()["score"].Instructions; p.Instructions != want {
		t.Fatalf("seed instructions differ from ScoreQuestion:\n%q\n%q", p.Instructions, want)
	}
	q := p.Question()["score"]
	if strings.Join(q.Score, "\x00") != strings.Join(prereview.ScoreLevels, "\x00") {
		t.Fatal("a prompt without LEVEL lines must send prereview.ScoreLevels")
	}
}

// TestPromptFileExemplarsAndSha8: EXEMPLAR and LEVEL lines are parsed out and
// never sent as instructions; the sha8 is over the file bytes as stored.
func TestPromptFileExemplarsAndSha8(t *testing.T) {
	var b strings.Builder
	b.WriteString("Score it.\nEXEMPLAR mas-bandwidth/nova-tools#2436\nSecond line.\n")
	for i := 1; i <= 10; i++ {
		b.WriteString("LEVEL level " + string(rune('0'+i%10)) + "\n")
	}
	raw := []byte(b.String())
	p, err := ParsePrompt(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.Instructions != "Score it.\nSecond line." {
		t.Fatalf("instructions = %q", p.Instructions)
	}
	if len(p.Exemplars) != 1 || p.Exemplars[0] != "mas-bandwidth/nova-tools#2436" || len(p.Levels) != 10 {
		t.Fatalf("exemplars=%v levels=%d", p.Exemplars, len(p.Levels))
	}
	if p.Sha8 != Sha8(raw) || len(p.Sha8) != 8 {
		t.Fatalf("sha8 = %s, want %s", p.Sha8, Sha8(raw))
	}
	if _, err := ParsePrompt([]byte("x\nLEVEL one\nLEVEL two\n")); err == nil {
		t.Fatal("two LEVEL lines were accepted; want none or ten")
	}
	if _, err := ParsePrompt([]byte("x\nEXEMPLAR nova-tools\n")); err == nil {
		t.Fatal("a malformed EXEMPLAR line was accepted")
	}
}

// TestEmbeddedPromptsAreNamedBySha8: every shipped prompt is stored under its
// own sha8 and resolves, and the default is the tuning run's winner.
func TestEmbeddedPromptsAreNamedBySha8(t *testing.T) {
	n := 0
	err := fs.WalkDir(promptFS, "prompts", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		name := strings.TrimSuffix(filepath.Base(path), ".txt")
		p, err := Resolve(name)
		if err != nil {
			t.Errorf("%s: %v", path, err)
		} else if p.Sha8 != name {
			t.Errorf("%s hashes to %s", path, p.Sha8)
		}
		return nil
	})
	if err != nil || n < 2 {
		t.Fatalf("walked %d prompts: %v", n, err)
	}
	if d := Default(); DefaultSha8 != "6b7343c3" || d.Sha8 != DefaultSha8 || len(d.Levels) != 10 {
		t.Fatalf("default prompt sha8=%s levels=%d", d.Sha8, len(d.Levels))
	}
}

// TestConfPrompt reads prompt= from a jev.conf: comments and other keys read
// past, a relative path resolved beside the conf, a sha8 kept as is, an absent
// file or key is "".
func TestConfPrompt(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "jev.conf")
	for _, c := range []struct{ body, want string }{
		{"# c\npass_above=7\nprompt=6b7343c3 # the tuned one\n", "6b7343c3"},
		{"prompt=prompts/x.txt\n", dir + "/prompts/x.txt"},
		{"prompt=/abs/x.txt\n", "/abs/x.txt"},
		{"pass_above=7\n", ""},
	} {
		if err := os.WriteFile(conf, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := ConfPrompt(conf)
		if err != nil || got != c.want {
			t.Fatalf("conf %q: got %q err=%v, want %q", c.body, got, err, c.want)
		}
	}
	if got, err := ConfPrompt(filepath.Join(dir, "absent.conf")); err != nil || got != "" {
		t.Fatalf("absent conf: %q %v", got, err)
	}
}
