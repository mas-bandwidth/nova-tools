package digest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/digest"
)

func want(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "digest", "want.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// checkGrammar: every line matches exactly one expression of
// digest.Grammar, the sections come in order, facts belong to their section
// and run oldest first, and "none" stands alone.
func checkGrammar(t *testing.T, text string) {
	t.Helper()
	if err := grammarErr(text); err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
}

var atRx = regexp.MustCompile(` at=(\S+)`)

func grammarErr(text string) error {
	if !strings.HasSuffix(text, "\n") {
		return fmt.Errorf("output does not end in a newline")
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	headers := []string{"landed:", "holds:", "reads:"}
	keys := map[string]string{"landed:": "landed", "holds:": "hold", "reads:": "read"}
	sec, next, facts, trims, none, lastAt := "", 0, 0, 0, false, ""
	for i, l := range lines {
		var forms []string
		for name, rx := range digest.Grammar {
			if rx.MatchString(l) {
				forms = append(forms, name)
			}
		}
		if len(forms) != 1 {
			return fmt.Errorf("line %d %q matches %v, want exactly one form", i+1, l, forms)
		}
		form := forms[0]
		if (i == 0) != (form == "header") {
			return fmt.Errorf("line %d %q: the header is line 1 and only line 1", i+1, l)
		}
		if i == 0 {
			continue
		}
		if keys[l] != "" {
			if next >= len(headers) || l != headers[next] {
				return fmt.Errorf("line %d %q: sections are landed:, holds:, reads: in order", i+1, l)
			}
			next++
			sec, facts, trims, none, lastAt = l, 0, 0, false, ""
			continue
		}
		if sec == "" || !strings.HasPrefix(l, keys[sec]+" ") {
			return fmt.Errorf("line %d %q is outside its section %q", i+1, l, sec)
		}
		if none {
			return fmt.Errorf("line %d %q follows %s none", i+1, l, keys[sec])
		}
		switch form {
		case "trimmed":
			if facts > 0 {
				return fmt.Errorf("line %d %q: status lines come before facts", i+1, l)
			}
			trims++
		case "none":
			if facts > 0 || trims > 0 {
				return fmt.Errorf("line %d %q: none only for a section with no facts and no status", i+1, l)
			}
			none = true
		default:
			at := atRx.FindStringSubmatch(l)[1]
			if at < lastAt {
				return fmt.Errorf("line %d %q is older than the fact before it", i+1, l)
			}
			lastAt = at
			facts++
		}
	}
	if next != len(headers) {
		return fmt.Errorf("%d of %d section headers", next, len(headers))
	}
	return nil
}

// TestDigestLineGrammar: want.txt keeps the grammar, and the grammar pins
// field order and the none form; DIGEST_PROBE=<file> checks a saved output.
func TestDigestLineGrammar(t *testing.T) {
	t.Parallel()

	w := want(t)
	checkGrammar(t, w)
	if p := os.Getenv("DIGEST_PROBE"); p != "" {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		checkGrammar(t, string(raw))
	}
	t.Run("pinned", func(t *testing.T) {
		swapped := regexp.MustCompile(`merge_sha=(\S+) members=(\S+)`).ReplaceAllString(w, "members=$2 merge_sha=$1")
		if swapped == w {
			t.Fatal("want.txt has no landing to swap")
		}
		if grammarErr(swapped) == nil {
			t.Fatal("merge_sha and members swapped still passes the grammar")
		}
		i := strings.Index(w, "\nholds:")
		if grammarErr(w[:i+1]+"landed none"+w[i:]) == nil {
			t.Fatal("landed none after a fact still passes the grammar")
		}
		if grammarErr(strings.Replace(w, "reads:\n", "", 1)) == nil {
			t.Fatal("a missing section header still passes the grammar")
		}
	})
}
