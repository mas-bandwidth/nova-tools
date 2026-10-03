package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func stellaIndependentPrepared(t *testing.T) (string, string, Prepared, PreparedArtifact) {
	t.Helper()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	tab := loadBus(t, clone)
	p, e := PrepareDraft(tab, "From: Ada\nTo: Bo\nSubject: Boundary fixture\n\nSynthetic note.\n", at("2026-09-12T17:00:00Z"), "boundary", "Ada")
	require.NoError(t, e, e)
	a, e := MakePreparedArtifact(p)
	require.NoError(t, e, e)
	return bare, clone, p, a
}
func TestStellaPreparedAttributePrefixesPublishExactBytes(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (measured over 5 s on the 2026-09-25 PR run). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	full, _ := ExpectedMergeAttributes("")
	cases := []struct {
		name   string
		prefix int
	}{
		{"empty", 0},
		{"prefix-12", 12},
		{"prefix-209-four-short", len(full) - 4},
		{"prefix-213-complete", len(full)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bare, clone, p, a := stellaIndependentPrepared(t)
			if tc.prefix > len(full) {
				require.False(t, tc.prefix > len(full), "prefix %d exceeds expected %d", tc.prefix, len(full))
			}
			attrsPath := filepath.Join(clone, AttributesName)
			require.NoError(t, os.WriteFile(attrsPath, []byte(full[:tc.prefix]), 0o644))
			res, err := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
			require.NoError(t, err, "SendPreparedArtifact: %v", err)
			require.True(t, res.Pushed, "expected the note to be pushed")
			remote, err := git(bare, "show", "main:"+AttributesName)
			require.NoError(t, err, "remote .gitattributes missing: %v", err)
			require.Equal(t, full, remote, "remote .gitattributes bytes wrong:\n got %q\nwant %q", remote, full)
		})
	}
}

func TestStellaIndependentPreparedRequiresCompleteRemoteIndex(t *testing.T) {
	t.Parallel()

	_, clone, p, a := stellaIndependentPrepared(t)
	{
		_, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
		require.NoError(t, e, e)
	}
	path := filepath.Join(clone, IndexPath(p.Sender.Lane))
	b, e := os.ReadFile(path)
	require.NoError(t, e, e)
	expected := IndexLine(p.Index)
	fields := strings.Split(expected, "\t")
	fields[len(fields)-1] = "SYNTHETIC_WRONG_INDEX_SUBJECT"
	changed := strings.Replace(string(b), expected, strings.Join(fields, "\t"), 1)
	require.False(t, changed == string(b), "did not mutate index")
	os.WriteFile(path, []byte(changed), 0644)
	id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
	{
		_, e := stageAndCommit(clone, id, []string{IndexPath(p.Sender.Lane)}, "mutate synthetic index")
		require.NoError(t, e, e)
	}
	{
		_, e := git(clone, "push", "origin", "HEAD:main")
		require.NoError(t, e, e)
	}
	{
		r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
		require.False(t, e == nil && r.Pushed, "claimed already-published with a different INDEX record")
	}
}
func TestStellaIndependentPreparedCannotConfirmCommitWithoutIndex(t *testing.T) {
	t.Parallel()

	bare, clone, p, a := stellaIndependentPrepared(t)
	{
		e := p.Save(clone)
		require.NoError(t, e, e)
	}
	id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
	{
		_, e := stageAndCommit(clone, id, []string{p.Path}, WithTrailer(p.Message, TrailerSend+" "+a.ID))
		require.NoError(t, e, e)
	}
	r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if e != nil {
		return
	}
	index, e := git(bare, "show", "main:"+IndexPath(p.Sender.Lane))
	require.True(t, (e == nil && strings.Contains(index, IndexLine(p.Index))) || !r.Pushed, "claimed success after publishing note-only commit without INDEX entry")
}
func TestStellaIndependentPreparedPreservesUnrelatedAttributeEdit(t *testing.T) {
	t.Parallel()

	bare, clone, p, a := stellaIndependentPrepared(t)
	path := filepath.Join(clone, AttributesName)
	old, _ := os.ReadFile(path)
	sentinel := "# synthetic_private_unrelated_attribute_edit\n"
	want := append(old, []byte(sentinel)...)
	os.WriteFile(path, want, 0644)
	r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if e == nil && r.Pushed {
		remote, _ := git(bare, "show", "main:"+AttributesName)
		require.NotContains(t, remote, sentinel, "published unrelated dirty attribute content during prepared delivery")
	}
	now, _ := os.ReadFile(path)
	require.Equal(t, string(want), string(now), "refusal changed unrelated dirty attribute content")
}
func TestStellaIndependentPreparedRefusesUnknownArtifactField(t *testing.T) {
	t.Parallel()

	_, clone, p, a := stellaIndependentPrepared(t)
	b, _ := json.Marshal(a)
	b = append(b[:len(b)-1], []byte(`,"unsupported":"synthetic"}`)...)
	{
		_, _, e := ValidatePreparedArtifact(b, clone, loadBus(t, clone).Config, p.Sender.Name)
		require.Error(t, e, "accepted unknown artifact field")
	}
}

func TestStellaIndependentPreparedPreservesUnrelatedAheadAttributeEdit(t *testing.T) {
	t.Parallel()

	bare, clone, p, a := stellaIndependentPrepared(t)
	{
		e := p.Save(clone)
		require.NoError(t, e, e)
	}
	{
		e := p.AppendIndex(clone)
		require.NoError(t, e, e)
	}
	path := filepath.Join(clone, AttributesName)
	old, _ := os.ReadFile(path)
	sentinel := "# synthetic_unrelated_ahead_attribute_edit\n"
	{
		e := os.WriteFile(path, append(old, []byte(sentinel)...), 0644)
		require.NoError(t, e, e)
	}
	id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
	{
		_, e := stageAndCommit(clone, id, []string{p.Path, IndexPath(p.Sender.Lane), AttributesName}, WithTrailer(p.Message, TrailerSend+" "+a.ID))
		require.NoError(t, e, e)
	}
	r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if e == nil && r.Pushed {
		remote, _ := git(bare, "show", "main:"+AttributesName)
		require.NotContains(t, remote, sentinel, "published unrelated committed attribute content with an allowed trailer")
	}
}

func TestStellaIndependentPreparedRefusesAheadAttributeDeletion(t *testing.T) {
	t.Parallel()

	bare, clone, p, a := stellaIndependentPrepared(t)
	{
		e := p.Save(clone)
		require.NoError(t, e, e)
	}
	{
		e := p.AppendIndex(clone)
		require.NoError(t, e, e)
	}
	// Seed a tracked attribute file on remote before creating the send contribution.
	id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
	{
		e := os.WriteFile(filepath.Join(clone, AttributesName), []byte("# synthetic original attribute\n"), 0644)
		require.NoError(t, e, e)
	}
	{
		_, e := stageAndCommit(clone, id, []string{AttributesName}, "synthetic base attrs")
		require.NoError(t, e, e)
	}
	{
		_, e := git(clone, "push", "origin", "HEAD:main")
		require.NoError(t, e, e)
	}
	{
		e := os.Remove(filepath.Join(clone, AttributesName))
		require.NoError(t, e, e)
	}
	{
		_, e := stageAndCommit(clone, id, []string{p.Path, IndexPath(p.Sender.Lane), AttributesName}, WithTrailer(p.Message, TrailerSend+" "+a.ID))
		require.NoError(t, e, e)
	}
	if r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1); e == nil && r.Pushed {
		{
			_, e := git(bare, "show", "main:"+AttributesName)
			require.NoError(t, e, "published unrelated attribute deletion")
		}
	}
}
func TestStellaIndependentPreparedRefusesIntermediateIndexLeak(t *testing.T) {
	t.Parallel()

	bare, clone, p, a := stellaIndependentPrepared(t)
	{
		e := p.Save(clone)
		require.NoError(t, e, e)
	}
	{
		e := p.AppendIndex(clone)
		require.NoError(t, e, e)
	}
	indexPath := filepath.Join(clone, IndexPath(p.Sender.Lane))
	good, e := os.ReadFile(indexPath)
	require.NoError(t, e, e)
	sentinel := "SYNTHETIC_PRIVATE_HISTORY_SENTINEL\n"
	{
		e := os.WriteFile(indexPath, append(good, []byte(sentinel)...), 0644)
		require.NoError(t, e, e)
	}
	id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
	msg := WithTrailer(p.Message, TrailerSend+" "+a.ID)
	{
		_, e := stageAndCommit(clone, id, []string{p.Path, IndexPath(p.Sender.Lane)}, msg)
		require.NoError(t, e, e)
	}
	{
		e := os.WriteFile(indexPath, good, 0644)
		require.NoError(t, e, e)
	}
	{
		_, e := stageAndCommit(clone, id, []string{IndexPath(p.Sender.Lane)}, msg)
		require.NoError(t, e, e)
	}
	if r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1); e == nil && r.Pushed {
		history, _ := git(bare, "log", "-p", "main", "--", IndexPath(p.Sender.Lane))
		require.NotContains(t, history, sentinel, "published unrelated content in intermediate commit history")
	}
}
