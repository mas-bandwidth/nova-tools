package tool

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestActorIsAnAliasOfAsWhereAVerbTakesAs pins that the acting name is one flag:
// a verb that registers `as` accepts `--actor` with the same meaning, and the
// reverse, and giving both with different values is refused naming both
// (STANDARD §2, one shape across the set; the two spellings are one).
func TestActorIsAnAliasOfAsWhereAVerbTakesAs(t *testing.T) {
	t.Parallel()
	who := func(name string) *Tool {
		return &Tool{
			Name: "nova-demo", What: "a tool that exists to be tested",
			ExitTable: "0 done, 1 said no, 2 could not run.",
			Verbs: []Verb{{Name: "who", Usage: "who [--" + name + " <name>]", Effect: Inspection,
				Flags: func(f *Flags) { f.String(name, "", "the acting name") },
				Run:   func(c *Call) *Out { return Done().Fact(name, c.Str(name)) }}},
		}
	}

	t.Run("a verb that registers as takes --actor", func(t *testing.T) {
		t.Parallel()
		r := NewRig(t, who("as")).Run(0, "who", "--actor", "bob")
		assert.Equal(t, "WHO OK as=bob\n", r.Stdout)
		assert.Empty(t, r.Stderr)
	})

	t.Run("a verb that registers actor takes --as", func(t *testing.T) {
		t.Parallel()
		r := NewRig(t, who("actor")).Run(0, "who", "--as", "bob")
		assert.Equal(t, "WHO OK actor=bob\n", r.Stdout)
		assert.Empty(t, r.Stderr)
	})

	t.Run("both spellings with different values are refused naming both", func(t *testing.T) {
		t.Parallel()
		r := NewRig(t, who("as")).Run(2, "who", "--as", "bob", "--actor", "ada")
		assert.Empty(t, r.Stdout)
		assert.Contains(t, r.Stderr, "--as")
		assert.Contains(t, r.Stderr, "--actor")
		assert.Contains(t, r.Stderr, "one flag")
	})

	t.Run("the help's flag list names the alias", func(t *testing.T) {
		t.Parallel()
		r := NewRig(t, who("as")).Run(0, "who", "-h")
		assert.Contains(t, r.Stdout, "--as")
		assert.Contains(t, r.Stdout, "alias --actor")
	})
}
