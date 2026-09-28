package docs

import (
	"os"
	"strings"
	"testing"
)

// TestNovaWorkNextCitesNoScratchSource holds nova-tools #1716 as a rule: the
// provenance paragraph of docs/nova-work-next.md must not cite a source under
// scratch/. The repository ignores scratch/ in .gitignore and `make clean`
// removes it, so a path under it can never be a source a reader can follow;
// the page has to say that rather than cite the gone table. The test is scoped
// to this one document because other pages mention scratch/ for other reasons
// -- a sandbox rule, a release note -- and this rule is not about them. It
// still requires the commit the audit was read at (cf9c9986), so the repair
// cannot be to delete the paragraph.
func TestNovaWorkNextCitesNoScratchSource(t *testing.T) {
	t.Parallel()

	const path = "../../docs/nova-work-next.md"
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	content := string(body)

	for _, span := range inlineCodeRe.FindAllString(content, -1) {
		cited := strings.Trim(span, "`")
		if strings.HasPrefix(cited, "scratch/") {
			t.Errorf("%s cites `%s`; scratch/ is ignored by .gitignore and deleted by make clean, so no reader can follow it", path, cited)
		}
	}

	if !strings.Contains(content, "cf9c9986") {
		t.Errorf("%s no longer names the commit the audit was read at (cf9c9986)", path)
	}
}
