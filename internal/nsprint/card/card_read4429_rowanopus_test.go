package card

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// devResultLine is origin/dev 415b6df38's resultLine body (bufio scanner).
func devResultLine(results string) string {
	f, err := os.Open(filepath.Join(results, "RESULT.md"))
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if sc.Scan() {
		return sc.Text()
	}
	return ""
}

// TestRead4429ResultLineMatchesDevScanner (rowan-opus cold read of #4429):
// resultLine through typedrec.SplitModel against dev's scanner.
func TestRead4429ResultLineMatchesDevScanner(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"", "\n", "fix: x (#1)   \nDONE\n", "fix: x (#1)\r\nDONE\r\n", "x (#1)\nDONE\n",
		"no kind at all\n", "fix: x (#1)", "fix: x (#1)\r", "fix: x (#1)\t\r\n", "\r\n",
		"a\rb\n", strings.Repeat("y", 70000) + "\nDONE\n",
	} {
		d := t.TempDir()
		if err := os.WriteFile(filepath.Join(d, "RESULT.md"), []byte(in), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, want := string(resultLine(d)), devResultLine(d); got != want {
			g, w := got, want
			if len(g) > 40 {
				g = g[:20] + "..." + string(rune('0'+len(g)%10))
			}
			if len(w) > 40 {
				w = w[:20] + "..."
			}
			t.Errorf("RESULT.md %q: head %q (len %d), dev %q (len %d)", trunc(in), g, len(got), w, len(want))
		}
	}
}

func trunc(s string) string {
	if len(s) > 30 {
		return s[:30] + "..."
	}
	return s
}

// TestRead4429RunCICollectsAnUnterminatedRED (rowan-opus): a last RED line
// with no newline is a red name at head (dev folded it into the "no RED
// line" row); a newline-terminated RED line reads the same on both.
func TestRead4429RunCICollectsAnUnterminatedRED(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ out, want string }{
		{"nova-ci local: base=dev\nRED package=./a test=TestA", "RED package=./a test=TestA"},
		{"RED package=./a test=TestA\nnova-ci local: packages=1 seconds=1 red=1 make-exit=2\n", "RED package=./a test=TestA"},
	} {
		run := func(ctx context.Context, c Cmd) (int, error) {
			_, _ = c.Out.Write([]byte(tc.out))
			return 1, nil
		}
		_, reds, reason, _ := runCI(context.Background(), run, GateInput{Repo: t.TempDir(), Base: "dev", Timeout: time.Minute})
		if len(reds) != 1 || reds[0] != tc.want || reason != GateCIRed {
			t.Errorf("out %q: reds %q reason %q, want [%q]", tc.out, reds, reason, tc.want)
		}
	}
}
