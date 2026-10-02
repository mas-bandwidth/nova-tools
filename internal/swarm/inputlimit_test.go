package swarm

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)


// A PHRASE HAS A FLOOR, AND THE REFUSAL QUOTES IT (Fable's read of #150, finding 4). The
// only floor was non-emptiness, so `input_limit_phrases: ["limit"]` would class every failed
// job whose log holds the word `limit` -- and a job classed `input-limit` is a job that is
// never retried. A provider's sentence is a SENTENCE: long enough to be one, and carrying a
// space or a digit so that one word can never be it.
func TestAProviderPhraseHasAFloor(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		phrase string
		ok     bool
	}{
		{"input token limit exceeded", true},
		{"prompt is too long", true},
		{"exceeds 200000 tokens", true},
		{"limit", false},
		{"too long", false},
		{"contextlengthexceeded", false},
		{"  ", false},
	} {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "home"), 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "worker.json")
		body := `{"name":"w","provider":"p","model":"m","env_var":"P_KEY","key_file":"` +
			filepath.ToSlash(filepath.Join(dir, "key")) + `","usage":"none","harness":"h",` +
			`"harness_args":["run","--model","{model}","--","{prompt}"],"worker_dir":"` +
			filepath.ToSlash(filepath.Join(dir, "home")) + `","deadline":"20m","input_limit_phrases":["` + c.phrase + `"]}`
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		_, problems := LoadWorker(path)
		switch {
		case c.ok && len(problems) > 0:
			t.Errorf("%q is a provider's sentence and is accepted: %v", c.phrase, problems)
		case !c.ok && len(problems) == 0:
			t.Errorf("%q is not a sentence; a phrase this short ends healthy-looking jobs as input-limit and they are never retried", c.phrase)
		case !c.ok:
			// AND THE REFUSAL QUOTES THE PHRASE, so the caller reads back what they typed.
			if !strings.Contains(problems[0].Error(), strconv.Quote(c.phrase)) {
				t.Errorf("%q: the refusal names the phrase it refused, got %v", c.phrase, problems[0])
			}
		}
	}
}
