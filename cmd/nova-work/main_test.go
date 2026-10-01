package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/workgh"
)

func replay(t *testing.T) workgh.Query {
	t.Helper()
	q, err := workgh.Replay("../../internal/workgh/testdata/reliable")
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func do(t *testing.T, q workgh.Query, args ...string) (int, string, string) {
	t.Helper()
	var out, errb strings.Builder
	code := run(args, &out, &errb, q)
	return code, out.String(), errb.String()
}

// TestImportThenVerifyIsZeroDifferences (SPEC-WORK-V1 sections 1.6 and
// 1.10): import writes the tree from the recorded fetch; verify against the
// same fetch finds nothing; one changed field is one DRIFT line and exit 1.
func TestImportThenVerifyIsZeroDifferences(t *testing.T) {
	t.Parallel()
	tree := filepath.Join(t.TempDir(), "tree.lisp")
	repo := []string{"--repo", "mas-bandwidth/reliable", "--page-size", "15"}
	code, out, errs := do(t, replay(t), append([]string{"import", "--org", "mas-bandwidth", "--out", tree}, repo...)...)
	if code != 0 || !strings.Contains(out, "IMPORT OK") || !strings.Contains(out, " issues=20 ") || !strings.Contains(out, " calls=3 ") || !strings.Contains(out, " rest=0 ") {
		t.Fatalf("import exit %d\n%s%s", code, out, errs)
	}
	if !strings.Contains(out, "PLAN OK org=mas-bandwidth repos=1 issues=20 est_calls=3") {
		t.Fatalf("no plan line:\n%s", out)
	}
	data, err := os.ReadFile(tree)
	if err != nil {
		t.Fatal(err)
	}
	got := regexp.MustCompile(`sha256=([0-9a-f]{64})`).FindStringSubmatch(out)
	if got == nil || got[1] != sum(data) {
		t.Fatalf("the import's sha256 does not name the file written:\n%s", out)
	}

	code, out, errs = do(t, replay(t), append([]string{"verify", "--tree", tree}, repo...)...)
	if code != 0 || !strings.Contains(out, "VERIFY OK") || !strings.Contains(out, "differences=0") {
		t.Fatalf("verify exit %d\n%s%s", code, out, errs)
	}

	changed := strings.Replace(string(data), `:title "`, `:title "changed `, 1)
	if err := os.WriteFile(tree, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs = do(t, replay(t), append([]string{"verify", "--tree", tree}, repo...)...)
	if code != 1 || !regexp.MustCompile(`(?m)^DRIFT path=repos/mas-bandwidth/reliable/issues/\d+ field=title want=\S+ got=changed\\x20`).MatchString(out) ||
		!strings.Contains(errs, "VERIFY FAIL") || !strings.Contains(errs, "differences=1 missing=0 extra=0 drift=1") {
		t.Fatalf("verify after a change: exit %d\n%s%s", code, out, errs)
	}

	code, out, errs = do(t, replay(t), append([]string{"import", "--org", "mas-bandwidth", "--dry-run"}, repo...)...)
	if code != 0 || !strings.Contains(out, "dry_run=true") || !strings.Contains(out, "out=-") {
		t.Fatalf("dry run exit %d\n%s%s", code, out, errs)
	}
}

// TestTheBudgetIsCheckedBeforeTheIssuesAreRead: a plan past --max-calls is
// refused at exit 2 after the listing, before any issue is read.
func TestTheBudgetIsCheckedBeforeTheIssuesAreRead(t *testing.T) {
	t.Parallel()
	code, out, errs := do(t, replay(t), "import", "--org", "mas-bandwidth", "--repo", "mas-bandwidth/reliable", "--page-size", "15", "--max-calls", "2", "--dry-run")
	if code != 2 || !strings.Contains(errs, "IMPORT FAIL") || !strings.Contains(errs, "calls=1 ") || !strings.Contains(errs, "needs\\x20about\\x203\\x20calls") {
		t.Fatalf("exit %d\n%s%s", code, out, errs)
	}
}

// TestRefusalsNameTheFlag: a malformed invocation exits 2 naming every
// problem at once and pointing at the verb's help; help exits 0.
func TestRefusalsNameTheFlag(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args []string
		code int
		want []string
	}{
		{[]string{"import"}, 2, []string{"--org is required", "--out is required unless --dry-run", "run: nova-work import -h"}},
		{[]string{"import", "--org", "o", "--dry-run", "--page-size", "0", "--max-calls", "0"}, 2, []string{"--max-calls must be positive", "--page-size must be 1 to 100"}},
		{[]string{"import", "--org", "o", "--out", "/nonexistent-dir/t.lisp"}, 2, []string{"does not exist"}},
		{[]string{"import", "--org", "o", "--dry-run", "--repo", "p/r"}, 2, []string{"--repo p/r is not in --org o"}},
		{[]string{"import", "--org", "o", "--dry-run", "--repo", "bad"}, 2, []string{"is not owner/name"}},
		{[]string{"import", "--org", "o", "--dry-run", "--gh", "/nonexistent/gh-cli"}, 2, []string{"is not found", "--gh"}},
		{[]string{"import", "--bogus"}, 2, []string{"bogus", "run: nova-work import -h"}},
		{[]string{"verify"}, 2, []string{"--tree is required", "run: nova-work verify -h"}},
		{[]string{"verify", "--tree", "/nonexistent/t.lisp"}, 2, []string{"VERIFY FAIL"}},
		{[]string{"frob"}, 2, []string{"unknown verb", "import verify"}},
		{[]string{}, 2, []string{"no verb", "run: nova-work help"}},
	} {
		code, out, errs := do(t, nil, c.args...)
		if code != c.code {
			t.Fatalf("%v: exit %d, want %d\n%s%s", c.args, code, c.code, out, errs)
		}
		for _, w := range c.want {
			if !strings.Contains(errs, w) {
				t.Fatalf("%v: stderr lacks %q:\n%s", c.args, w, errs)
			}
		}
	}
	for _, args := range [][]string{{"help"}, {"help", "import"}, {"help", "verify"}, {"import", "-h"}, {"verify", "--help"}} {
		code, out, _ := do(t, nil, args...)
		if code != 0 || !strings.Contains(out, "nova-work") {
			t.Fatalf("%v: exit %d\n%s", args, code, out)
		}
	}
}

// Both spellings of the build identity print the one line at exit 0, like
// every other tool's version verb and its --version alias.
func TestVersionAndItsAliasPrintTheBuildIdentity(t *testing.T) {
	t.Parallel()
	for _, arg := range []string{"version", "--version"} {
		code, out, errb := do(t, nil, arg)
		if code != 0 || !strings.HasPrefix(out, "nova-work ") || errb != "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want the identity line at exit 0", arg, code, out, errb)
		}
	}
}

// TestABareCommandRefusesInOneLine (ONBOARDING.md point 1): no verb is exit 2
// and one stderr line naming the verbs and the door, never the whole banner.
func TestABareCommandRefusesInOneLine(t *testing.T) {
	t.Parallel()
	code, out, errs := do(t, nil)
	if code != 2 || out != "" || errs != "nova-work: no verb; verbs: import verify help version; run: nova-work help\n" {
		t.Fatalf("bare nova-work: exit %d, stdout %q, stderr %q", code, out, errs)
	}
}
