package audit

import (
	"testing"
)

// TestBlankImportIsHeldToTheListNotRefusedAsAliased pins that a blank import is
// held to the allow list rather than refused as an alias: it binds no name, so
// nothing prints under it, and its path must still be on the list. Bypasses
// reports through the caller's t, so this passes when it reports nothing.
func TestBlankImportIsHeldToTheListNotRefusedAsAliased(t *testing.T) {
	t.Parallel()

	code := `package fixture

import _ "github.com/mas-bandwidth/nova-tools/pkg/oneline"

func F() {}
`
	f := fixtureSource(t, "fixture.go", code)
	allowed := map[string]bool{
		`"github.com/mas-bandwidth/nova-tools/pkg/oneline"`: true,
	}
	bypassesIn(t, f, map[string]bool{}, allowed)
}
