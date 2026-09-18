package ci

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ci_cardtemplates_test.go is the red-test contract of the card-template
// checker in docs/SPEC-CI.md, "`cardtemplates` -- no card template carries a
// command only one of the estate's platforms has". Every fixture below is
// written into a throwaway tree and handed to CheckCardTemplates, so the
// checker is exercised on directories given to it and never by reaching into
// the repository. The fixtures are inline strings rather than files under
// testdata, because a card template is inert text: there is nothing to compile
// and nothing for another test to trip over.
//
// The class test at the bottom is the one that reads this repository.

// cardTemplateAllowlistPath is the shrink-only list of the OS-specific
// spellings this repository still ships in a card template. Every row is
// checked in BOTH directions -- a spelling not listed is a red run, and a
// listed spelling that has left is a stale row and also a red run -- so the
// list can only ever get shorter. It lives in testdata so a reader sees the
// whole exception set without reading the test.
const cardTemplateAllowlistPath = "testdata/cardtemplate_allowlist.txt"

// cardTree writes one template into <tmp>/<dir>/<name> and returns the root,
// the way a caller hands the checker a tree and a list of directories.
func cardTree(t *testing.T, dir, name, body string) string {
	t.Helper()
	root := t.TempDir()
	full := filepath.Join(root, filepath.FromSlash(dir))
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// checkTree runs the checker over one fixture directory with no allowlist.
func checkTree(t *testing.T, root, dir string) CardTemplatesResult {
	t.Helper()
	res, err := CheckCardTemplates(root, []string{dir}, "")
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// spells is the set of rule names a result refused, sorted, so a test asserts
// on what was found rather than on the order the rules happen to sit in.
func spells(res CardTemplatesResult) []string {
	var out []string
	for _, f := range res.Findings {
		out = append(out, f.Spell)
	}
	sort.Strings(out)
	return out
}

// 1. The four spellings the schema dogfood measured on 2026-09-18 are each one
// refusal, and each refusal carries the portable spelling.
func TestCardTemplateRefusesTheFourSpellingsTheDogfoodMeasured(t *testing.T) {
	body := "RESULT <label> sha=<sha12>\n" +
		"STEP 1. /usr/bin/time -f '%e' go build ./...\n" +
		"STEP 2. cores=$(nproc)\n" +
		"STEP 3. go --version\n" +
		"STEP 4. java --version\n"
	root := cardTree(t, "templates", "measure.md", body)
	res := checkTree(t, root, "templates")

	want := []string{"go_version", "gnu_time", "java_version", "nproc"}
	got := spells(res)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("spells = %v, want %v", got, want)
	}
	if res.Templates != 1 {
		t.Errorf("templates = %d, want 1", res.Templates)
	}
	if res.ExitCode() != 2 {
		t.Errorf("exit = %d, want 2", res.ExitCode())
	}
	for _, f := range res.Findings {
		if f.Remedy == "" {
			t.Errorf("%s carries no remedy", f.Spell)
		}
		if f.Line < 1 {
			t.Errorf("%s has line %d", f.Spell, f.Line)
		}
	}
}

// 2. The portable spelling of a fact is NOT refused. The uname-chosen pair
// names both halves on one line and is the shape the templates ship; a
// `readlink -f` with a fallback is the same idea with a different spelling.
func TestCardTemplateAcceptsThePortableSpellings(t *testing.T) {
	body := "STEP 1. cores=$(if [ \"$(uname -s)\" = Darwin ]; then sysctl -n hw.ncpu; else nproc; fi)\n" +
		"STEP 2. command -v go && go version\n" +
		"STEP 3. dotnet --version; java -version 2>&1 | head -1\n" +
		"STEP 4. p=$(readlink -f \"$f\" || printf '%s' \"$f\")\n" +
		"STEP 5. shasum -a 256 RESULT.md\n"
	root := cardTree(t, "templates", "portable.md", body)
	res := checkTree(t, root, "templates")
	if res.Refused() != 0 {
		t.Fatalf("refused = %d, want 0: %v", res.Refused(), spells(res))
	}
	if res.OKLine() != "CI-CARDTEMPLATES OK templates=1 allowlisted=0 refused=0" {
		t.Errorf("OK line = %q", res.OKLine())
	}
}

// 3. A darwin-only command is refused the same way a linux-only one is: the
// rule is "one of the estate's platforms", not "not linux". A card dealt to
// hulk has no diskutil.
func TestCardTemplateRefusesADarwinOnlyCommand(t *testing.T) {
	root := cardTree(t, "templates", "mac.md", "STEP 1. sw_vers -productVersion && diskutil list\n")
	res := checkTree(t, root, "templates")
	if len(res.Findings) != 1 || res.Findings[0].Spell != "mac_only" {
		t.Fatalf("findings = %v, want one mac_only", spells(res))
	}
	if res.Findings[0].Only != "darwin" {
		t.Errorf("only = %q, want darwin", res.Findings[0].Only)
	}
}

// 4. A .card is read and so is a .md; a .tsv beside them is a table and is not.
func TestCardTemplateReadsMdAndCardAndNothingElse(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "templates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"a.md":        "STEP 1. nproc\n",
		"b.card":      "STEP 1. nproc\n",
		"benches.tsv": "model\tnproc\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res := checkTree(t, root, "templates")
	if res.Templates != 2 {
		t.Errorf("templates = %d, want 2 (the .tsv is a table)", res.Templates)
	}
	if len(res.Findings) != 2 {
		t.Errorf("findings = %d, want 2", len(res.Findings))
	}
}

// 5. An allowlist row honours exactly its own file and spelling -- never a line
// number, which a merge moves -- and a row that names no offender is itself
// refused: the list only shrinks.
func TestCardTemplateAllowlistIsCheckedInBothDirections(t *testing.T) {
	root := cardTree(t, "templates", "one.md", "STEP 1. cat /proc/loadavg\n")
	allow := filepath.Join(t.TempDir(), "allow.txt")
	if err := os.WriteFile(allow, []byte("# reason column is for a reader\ntemplates/one.md proc 2026-09-18 the linux bench's own load, read nowhere else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := CheckCardTemplates(root, []string{"templates"}, allow)
	if err != nil {
		t.Fatal(err)
	}
	if res.Refused() != 0 || res.Allowlisted != 1 {
		t.Fatalf("refused=%d allowlisted=%d, want 0 and 1", res.Refused(), res.Allowlisted)
	}

	// The same row against a tree with nothing in it is stale, and stale is red.
	empty := t.TempDir()
	if err := os.MkdirAll(filepath.Join(empty, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err = CheckCardTemplates(empty, []string{"templates"}, allow)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stale) != 1 {
		t.Fatalf("stale = %d, want 1", len(res.Stale))
	}
	if res.ExitCode() != 2 {
		t.Errorf("exit = %d, want 2 for a stale row", res.ExitCode())
	}
	if !strings.Contains(res.Stale[0].Remedy, "only shrinks") {
		t.Errorf("stale remedy = %q", res.Stale[0].Remedy)
	}
}

// 6. A directory the repository does not have yet is skipped, not an error: the
// list names where a template MAY live.
func TestCardTemplateSkipsADirectoryThatIsNotThere(t *testing.T) {
	res, err := CheckCardTemplates(t.TempDir(), CardTemplateDirs, "")
	if err != nil {
		t.Fatalf("a tree with none of the directories is not an error: %v", err)
	}
	if res.Templates != 0 || res.Refused() != 0 {
		t.Errorf("templates=%d refused=%d, want 0 and 0", res.Templates, res.Refused())
	}
}

// 7. The refusal line names the file, the line, the spelling and the remedy, so
// a reader fixes the card without opening the checker.
func TestCardTemplateRefusalLineNamesEverythingAFixNeeds(t *testing.T) {
	root := cardTree(t, "templates", "t.md", "STEP 1. df -BG $HOME\n")
	res := checkTree(t, root, "templates")
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %v", spells(res))
	}
	line := res.Findings[0].Render()
	for _, want := range []string{"templates/t.md", "line=1", "spell=df_blocksize", "only=linux", "df -k"} {
		if !strings.Contains(line, want) {
			t.Errorf("refusal %q does not name %q", line, want)
		}
	}
	if !strings.Contains(res.FailLine(), "refused=1") {
		t.Errorf("fail line = %q", res.FailLine())
	}
}

// TestNoCardTemplateCarriesAnOSSpecificCommand is the class rule. It reads
// every card template this repository ships and refuses a command that only
// one of the estate's platforms has, or a --version spelling the tool does not
// take. The hurt is measured: the schema dogfood's round-1 templates spelt
// `/usr/bin/time -f`, `nproc`, `go --version` and `java --version`, and the
// first card cut for the Air died inside the worker on all four.
func TestNoCardTemplateCarriesAnOSSpecificCommand(t *testing.T) {
	root := repoRoot(t)
	res, err := CheckCardTemplates(root, CardTemplateDirs, cardTemplateAllowlistPath)
	if err != nil {
		t.Fatal(err)
	}
	if res.Templates == 0 {
		t.Fatalf("no card template was read under %v; the directory list has gone stale", CardTemplateDirs)
	}
	for _, f := range res.Findings {
		t.Errorf("%s", f.Render())
	}
	for _, s := range res.Stale {
		t.Errorf("%s", s.Render())
	}
	if res.Refused() > 0 {
		t.Fatalf("%s", res.FailLine())
	}
	t.Logf("%s", res.OKLine())
}
