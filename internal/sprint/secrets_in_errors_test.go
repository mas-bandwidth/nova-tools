package sprint_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
	sprintstore "github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/secretcheck"
)

// THE CLASS RULE: NO SECRET REACHES AN ERROR (docs/SPEC-CI.md, `secrets-never-in-errors`),
// over nova-sprint's own paths. internal/ci's TestNoSecretReachesAnError holds it over the
// rest of the tree and leaves these out; this is the same rule with the same harness
// (internal/secretcheck), its table and allowlist the rows that were internal/ci's for
// these paths.

// sprintSecretRoots are nova-sprint's paths the walk reads, from the repository's root.
var sprintSecretRoots = []string{
	"cmd/nova-sprint", "cmd/nova-card", "cmd/nova-work",
	"internal/sprint", "internal/sprintdash", "internal/card", "internal/cardgen",
	"internal/workfile", "internal/workgh", "internal/worklang",
}

// sprintSecretOpeners is the reviewed table of every function the rule drives here, keyed
// `<repo-relative package directory>.<Name>`.
var sprintSecretOpeners = map[string]any{
	"internal/cardgen.ParseFindings":        cardgen.ParseFindings,
	"internal/cardgen.ParseLedger":          cardgen.ParseLedger,
	"internal/sprint.NewTable":              sprint.NewTable,
	"internal/sprint.OpenKey":               sprint.OpenKey,
	"internal/sprint.ParseAttempts":         sprint.ParseAttempts,
	"internal/sprint.ParseDeadline":         sprint.ParseDeadline,
	"internal/sprint.ParseFriendReadReport": sprint.ParseFriendReadReport,
	"internal/sprint.ParseLaneCap":          sprint.ParseLaneCap,
	"internal/sprint.ParseMembers":          sprint.ParseMembers,
	"internal/sprint.ParseReadCard":         sprint.ParseReadCard,
	"internal/sprint.ParseReaderTiers":      sprint.ParseReaderTiers,
	"internal/sprint.ParseRoute":            sprint.ParseRoute,
	"internal/sprint.ParseWidth":            sprint.ParseWidth,
	"internal/sprint.ParseWorkCard":         sprint.ParseWorkCard,
	"internal/sprint/refmodel.New":          refmodel.New,
	"internal/sprint/store.NewDeliverer":    sprintstore.NewDeliverer,
}

// sprintSecretLeakAllowlist are the functions known to carry a secret-shaped string into
// an error, one `<key> <reason>` per line. It only shrinks.
const sprintSecretLeakAllowlist = `
internal/sprint.ParseAttempts echoes the rejected value with %q (internal/sprint/brief_bound.go:83)
internal/sprint.ParseDeadline echoes the rejected value with %q (internal/sprint/deadline.go:165)
internal/sprint.ParseMembers echoes the rejected width with %q (internal/sprint/width.go:42)
internal/sprint.ParseReaderTiers echoes the rejected tier names (internal/sprint/reader_tiers.go:51)
internal/sprint.ParseRoute echoes the rejected route with %q (internal/sprint/remind.go:108)
internal/sprint.ParseWidth echoes the rejected width with %q (internal/sprint/width.go:42)
internal/sprint/store.NewDeliverer echoes the rejected route with %q (internal/sprint/store/remind.go:147 returns the error of internal/sprint/remind.go:108; the bus refusal at remind.go:155 echoes it too)
`

// sprintSecretFiles is every non-test Go file under the roots, parsed, keyed from the
// repository's root (this package's directory is internal/sprint).
func sprintSecretFiles(t *testing.T) []secretcheck.File {
	t.Helper()
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var out []secretcheck.File
	for _, r := range sprintSecretRoots {
		err := filepath.WalkDir(filepath.Join(root, r), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			f, _ := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution) // ignored: a file that does not parse has a nil AST and is skipped
			out = append(out, secretcheck.File{Rel: rel, AST: f, Testdata: strings.Contains("/"+rel, "/testdata/")})
			return nil
		})
		require.NoError(t, err)
	}
	return out
}

// TestNoSecretReachesAnErrorInTheSprint is the class rule over nova-sprint's paths.
func TestNoSecretReachesAnErrorInTheSprint(t *testing.T) {
	t.Parallel()
	found := secretcheck.OpenersIn(sprintSecretFiles(t))
	require.NotEmpty(t, found, "the walk found no Open*/Parse*/Dial*/New* function; a rule that checks nothing passes")
	for _, f := range secretcheck.TableFindings(found, sprintSecretOpeners, nil) {
		t.Error(f)
	}
	allowed, bad := secretcheck.ParseAllowlist(sprintSecretLeakAllowlist)
	for _, b := range bad {
		t.Error(b)
	}
	for _, f := range secretcheck.Verdict(secretcheck.LeakFindings(sprintSecretOpeners, secretcheck.Shapes), allowed) {
		t.Error(f)
	}
	assert.True(t, found["internal/sprint.ParseRoute"], "the walk reads the sprint's own package")
}
