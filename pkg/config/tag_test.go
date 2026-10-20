package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestATagIsAConfigRowAndAMachineNamesOnlyKnownTags: the acceptance of the tag
// registry (docs/SPEC-CONFIG.md, "The tag registry"; tla/Tags.tla). A new tag
// is a config row, a machine names only tag rows that exist, and removing a
// tag a machine names is refused naming the machine.
func TestATagIsAConfigRowAndAMachineNamesOnlyKnownTags(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	tag, _ := Lookup(KindTag)
	machine, _ := Lookup(KindMachine)

	row, err := tag.NewRow("jq", map[string]string{
		"description": "jq is on PATH",
		"source":      "probed",
		"probe":       "command",
		"probe_argv":  "jq --version",
		"probe_match": `jq-([0-9.]+)`,
		"probe_min":   "1.6",
	})
	require.NoError(t, err, "a probed command tag is accepted")
	_, err = st.Insert(ctx, KindTag, row, "test")
	require.NoError(t, err)

	mrow, err := machine.NewRow("m", map[string]string{"user": "u", "seat": "s", "slots": "8"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindMachine, mrow, "test")
	require.NoError(t, err)
	_, _, err = st.Update(ctx, KindMachine, "m", map[string]string{"tags": "jq"}, "test")
	require.NoError(t, err, "machine set m --tags jq is accepted")

	_, _, err = st.Update(ctx, KindMachine, "m", map[string]string{"tags": "nosuch"}, "test")
	require.Error(t, err, "machine set m --tags nosuch is refused")
	assert.Contains(t, err.Error(), "nosuch", "the refusal names the unknown tag")

	_, err = st.Delete(ctx, KindTag, "jq", "test")
	require.Error(t, err, "tag remove jq is refused while a machine names it")
	assert.Contains(t, err.Error(), "m", "the refusal names the machine")

	_, err = tag.NewRow("noprobe", map[string]string{"source": "probed", "probe": "command"})
	require.Error(t, err, "a probed row with no probe argv is refused")

	_, err = tag.NewRow("badre", map[string]string{"source": "probed", "probe": "command", "probe_argv": "jq --version", "probe_match": "("})
	require.Error(t, err, "a probe_match that does not compile is refused")

	_, err = tag.NewRow("decl", map[string]string{"source": "declared", "probe": "path", "probe_path": "/x"})
	require.Error(t, err, "a declared row with probe fields is refused")
}

// TestTheSeedRowsCoverEveryCompiledFact: migration 0048 seeds every word the
// member's machineFacts can emit (cmd/nova-swarm/facts.go): the OS, the ISA,
// landlock, kvm, tla and the three toolchains. Adoption changes no card.
func TestTheSeedRowsCoverEveryCompiledFact(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	rows, err := st.List(ctx, KindTag)
	require.NoError(t, err)
	have := map[string]bool{}
	for _, r := range rows {
		have[r.Name] = true
	}
	for _, fact := range []string{"linux", "darwin", "windows", "amd64", "arm64", "landlock", "kvm", "tla", "go", "dotnet", "java"} {
		assert.True(t, have[fact], "the registry seeds %q, a word machineFacts emits", fact)
	}
}

// TestApplyWritesNoTagCopyToRedis: the tag kind is Postgres-only, and a
// machine's tags and gates_only never reach Redis (docs/SPEC-CONFIG.md, "The
// tag registry", the one-source rule).
func TestApplyWritesNoTagCopyToRedis(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := NewMem()
	ap := newFake()
	tag, _ := Lookup(KindTag)
	machine, _ := Lookup(KindMachine)

	row, err := tag.NewRow("jq", map[string]string{"description": "jq is on PATH", "source": "declared"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindTag, row, "test")
	require.NoError(t, err)
	_, err = Apply(ctx, st, ap, KindTag, "test", false, func(Op) {})
	require.NoError(t, err)
	assert.Empty(t, ap.log, "apply writes no tag copy to Redis")

	mrow, err := machine.NewRow("m", map[string]string{"user": "u", "seat": "s", "slots": "8", "tags": "jq", "gates_only": "jq"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindMachine, mrow, "test")
	require.NoError(t, err)
	_, err = Apply(ctx, st, ap, KindMachine, "test", false, func(Op) {})
	require.NoError(t, err)
	view := ap.views[KindMachine]["m"]
	assert.NotContains(t, view, "tags", "a machine's tags are never written to Redis")
	assert.NotContains(t, view, "gates_only", "a machine's gates_only is never written to Redis")
}
