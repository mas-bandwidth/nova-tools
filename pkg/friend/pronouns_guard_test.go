package friend

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// personalPronoun is a pronoun for a person. A friend's pronouns are the
// friend row's `pronouns` setting (Pronouns.Say renders them), never a word typed
// into a text: a literal that says "her" or "she" calls every friend a she, and a
// literal that says "his" or "he" calls every friend a he. "its" is not here: a daemon, a
// session or a row is a thing.
var personalPronoun = regexp.MustCompile(`(?i)\b(she|her|hers|herself|he|him|himself|his)\b`)

// pronounGuardRoots are the sources whose string literals reach a person: the
// texts nova-friend and nova-bus print, the daemon's records and the seat's
// judgments, the friend kind's help, and the card lint's friend lines. The paths
// are from this package's directory.
var pronounGuardRoots = []string{".", "../../cmd/nova-friend", "../../cmd/nova-bus", "../config", "../../pkg/swarm"}

// pronounGuardAllowed are the literals that say a pronoun word for a reason that
// is not a friend: file (base name) to the literal's text.
var pronounGuardAllowed = map[string][]string{
	// the stop words the rework matcher drops from a finding before it compares two
	"rework.go": {"he", "her", "his", "him", "she"},
	// the helper that renders the pronouns holds the words themselves
	"pronouns.go": {"she/her", "he/him", "she", "her", "hers", "herself", "he", "him", "his", "himself"},
	// the pronoun sets nova-config lists, by their names
	"kind.go": {"she/her", "he/him"},
}

// TestNoStringLiteralCallsAFriendHerOrHim reads every non-test source of the
// roots with go/parser and fails on a string literal carrying a personal
// pronoun: a text about a friend says {their} and the like through Pronouns.Say,
// or names the friend, or says "the friend's".
func TestNoStringLiteralCallsAFriendHerOrHim(t *testing.T) {
	t.Parallel()
	var found []string
	fset := token.NewFileSet()
	for _, root := range pronounGuardRoots {
		files, err := filepath.Glob(filepath.Join(root, "*.go"))
		require.NoError(t, err)
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, file, nil, 0)
			require.NoError(t, err, "parse %s", file)
			allowed := pronounGuardAllowed[filepath.Base(file)]
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil || !personalPronoun.MatchString(s) {
					return true
				}
				for _, a := range allowed {
					if s == a {
						return true
					}
				}
				excerpt := personalPronoun.FindStringIndex(s)
				from := max(0, excerpt[0]-30)
				to := min(len(s), excerpt[1]+30)
				found = append(found, fset.Position(lit.Pos()).String()+": "+strconv.Quote(s[from:to]))
				return true
			})
		}
	}
	sort.Strings(found)
	assert.Empty(t, found, "%d string literal(s) carry a personal pronoun; say {their}/{them}/{they} through Pronouns.Say (pkg/friend/pronouns.go), name the friend, or say \"the friend's\":\n%s", len(found), strings.Join(found, "\n"))
}
