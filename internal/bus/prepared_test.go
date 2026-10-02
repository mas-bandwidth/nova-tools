package bus

import (
	"encoding/json"
	"fmt"
	"github.com/stretchr/testify/require"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMakeAndValidatePreparedArtifact(t *testing.T) {
	t.Parallel()
	root := writeBus(t, nil)
	tab := loadBus(t, root)
	now := at("2026-09-12T12:00:00Z")

	draft := "From: Ada\nTo: Bo\nSubject: Prepared test\n\nTesting prepared artifact round-trip.\n"
	p, err := PrepareDraft(tab, draft, now, "prepared-test", "Ada")
	require.NoError(t, err, "PrepareDraft: %v", err)

	art, err := MakePreparedArtifact(p)
	require.NoError(t, err, "MakePreparedArtifact: %v", err)
	if art.Schema != PreparedSchema {
		require.Equal(t, PreparedSchema, art.Schema, "art.Schema = %q, want %q", art.Schema, PreparedSchema)
	}
	require.False(t, !strings.HasSuffix(art.Note, "\n"), "art.Note must end with LF")
	if len(art.SHA256) != 64 {
		require.Equal(t, 64, len(art.SHA256), "art.SHA256 length = %d, want 64", len(art.SHA256))
	}

	raw, err := json.Marshal(art)
	require.NoError(t, err, "json.Marshal: %v", err)

	// Successful validation
	c := tab.Config
	gotArt, gotP, err := ValidatePreparedArtifact(raw, root, c, "Ada")
	require.NoError(t, err, "ValidatePreparedArtifact: %v", err)
	if gotArt.ID != art.ID || gotP.Note.Header.ID != art.ID {
		require.False(t, gotArt.ID != art.ID || gotP.Note.Header.ID != art.ID, "id mismatch: got %q, want %q", gotArt.ID, art.ID)
	}

	// Failure: malformed json
	{
		_, _, err := ValidatePreparedArtifact([]byte("not-json"), root, c, "Ada")
		require.Error(t, err, "ValidatePreparedArtifact accepted malformed JSON")
	}

	// Failure: bad schema
	badSchema := art
	badSchema.Schema = "nova.bus.prepared/99"
	badRaw, _ := json.Marshal(badSchema)
	{
		_, _, err := ValidatePreparedArtifact(badRaw, root, c, "Ada")
		require.Error(t, err, "ValidatePreparedArtifact accepted bad schema")
	}

	// Failure: invalid sha256
	badSHA := art
	badSHA.SHA256 = "invalid-sha"
	badRaw, _ = json.Marshal(badSHA)
	{
		_, _, err := ValidatePreparedArtifact(badRaw, root, c, "Ada")
		require.Error(t, err, "ValidatePreparedArtifact accepted invalid sha256")
	}

	// Failure: digest mismatch
	badDigest := art
	badDigest.SHA256 = strings.Repeat("0", 64)
	badRaw, _ = json.Marshal(badDigest)
	{
		_, _, err := ValidatePreparedArtifact(badRaw, root, c, "Ada")
		require.Error(t, err, "ValidatePreparedArtifact accepted mismatched digest")
	}

	// Failure: missing LF
	noLF := art
	noLF.Note = strings.TrimRight(art.Note, "\n")
	noLF.SHA256 = MakeSHA256(noLF.Note)
	badRaw, _ = json.Marshal(noLF)
	{
		_, _, err := ValidatePreparedArtifact(badRaw, root, c, "Ada")
		require.Error(t, err, "ValidatePreparedArtifact accepted note without final LF")
	}

	// Failure: outside lane path
	outsideLane := art
	outsideLane.Path = "from-bo/" + filepath.Base(art.Path)
	badRaw, _ = json.Marshal(outsideLane)
	{
		_, _, err := ValidatePreparedArtifact(badRaw, root, c, "Ada")
		require.Error(t, err, "ValidatePreparedArtifact accepted path outside sender lane")
	}

	// Failure: path traversal
	traversal := art
	traversal.Path = "../outside.md"
	badRaw, _ = json.Marshal(traversal)
	{
		_, _, err := ValidatePreparedArtifact(badRaw, root, c, "Ada")
		require.Error(t, err, "ValidatePreparedArtifact accepted path leaving root")
	}

	// Failure: speaker mismatch
	{
		_, _, err := ValidatePreparedArtifact(raw, root, c, "Bo")
		require.Error(t, err, "ValidatePreparedArtifact accepted mismatched speaker")
	}

	// Failure: tampered body with same ID
	tampered := art
	tampered.Note = strings.Replace(art.Note, "Testing", "Tampered", 1)
	tampered.SHA256 = MakeSHA256(tampered.Note)
	badRaw, _ = json.Marshal(tampered)
	{
		_, _, err := ValidatePreparedArtifact(badRaw, root, c, "Ada")
		require.Error(t, err, "ValidatePreparedArtifact accepted note with tampered body and mismatched id")
	}
}

// L1: a path that keeps the lane prefix but walks back into another lane. The prefix
// test alone passes "from-ada/../from-bo/<file>", but Join lands it in from-bo.
// Mixed-separator variants ("from-ada/..\from-bo\<file>") and pure backslash paths
// are portably refused so Windows native path resolution cannot cross lanes.
func TestPreparedRefusesLaneTraversalThatKeepsThePrefix(t *testing.T) {
	t.Parallel()
	root := writeBus(t, nil)
	tab := loadBus(t, root)
	now := at("2026-09-12T12:00:00Z")

	draft := "From: Ada\nTo: Bo\nSubject: Lane traversal\n\nA note.\n"
	p, err := PrepareDraft(tab, draft, now, "prepared-traversal", "Ada")
	require.NoError(t, err, "PrepareDraft: %v", err)
	art, err := MakePreparedArtifact(p)
	require.NoError(t, err, "MakePreparedArtifact: %v", err)

	// Normal valid artifact succeeds
	rawValid, _ := json.Marshal(art)
	{
		_, _, err := ValidatePreparedArtifact(rawValid, root, tab.Config, "Ada")
		if err != nil {
			require.NoError(t, err, "ValidatePreparedArtifact rejected valid artifact %q: %v", art.Path, err)
		}
	}

	base := filepath.Base(art.Path)
	for _, tc := range []struct {
		name string
		path string
	}{
		{
			name: "slash traversal across lanes",
			path: "from-ada/../from-bo/" + base,
		},
		{
			name: "mixed separator traversal across lanes",
			path: "from-ada/..\\from-bo\\" + base,
		},
		{
			name: "pure backslash traversal across lanes",
			path: "from-ada\\..\\from-bo\\" + base,
		},
		{
			name: "backslash in filename",
			path: "from-ada/sub\\file.md",
		},
	} {
		traversal := art
		traversal.Path = tc.path
		raw, _ := json.Marshal(traversal)
		{
			_, _, err := ValidatePreparedArtifact(raw, root, tab.Config, "Ada")
			if err == nil {
				require.Error(t, err, "ValidatePreparedArtifact accepted %s %q", tc.name, traversal.Path)
			}
		}
	}
}

func MakeSHA256(s string) string {
	var art PreparedArtifact
	art.Note = s
	p := Prepared{Note: Note{}}
	// Quick helper for test
	p.Note.Header.ID = "dummy"
	h, _ := MakePreparedArtifact(Prepared{Note: Note{Body: s}})
	return h.SHA256
}

func TestSendPreparedArtifactAlreadyPublished(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	tab := loadBus(t, clone)
	now := at("2026-09-12T14:00:00Z")

	draft := "From: Ada\nTo: Bo\nSubject: Fresh publish\n\nA note to test already-published.\n"
	p, err := PrepareDraft(tab, draft, now, "fresh-publish", "Ada")
	require.NoError(t, err, "PrepareDraft: %v", err)
	art, err := MakePreparedArtifact(p)
	require.NoError(t, err, "MakePreparedArtifact: %v", err)

	// First send: should publish
	res1, err := SendPreparedArtifact(clone, "origin", "main", p, art, 3)
	require.NoError(t, err, "SendPreparedArtifact first send: %v", err)
	require.False(t, !res1.Pushed || res1.State != "published", "res1 = %+v, want published", res1)

	headBefore, err := git(bare, "rev-parse", "main")
	require.NoError(t, err)

	// Second send: should detect already-published without second commit or push
	res2, err := SendPreparedArtifact(clone, "origin", "main", p, art, 3)
	require.NoError(t, err, "SendPreparedArtifact retry: %v", err)
	require.False(t, !res2.Pushed || res2.Attempts != 0 || res2.State != "already-published", "res2 = %+v, want already-published with attempts=0", res2)

	headAfter, err := git(bare, "rev-parse", "main")
	require.NoError(t, err)
	require.Equal(t, headAfter, headBefore, "bare HEAD moved during already-published retry: before %s, after %s", headBefore, headAfter)
}

func TestSendPreparedArtifactRefusals(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	tab := loadBus(t, clone)
	now := at("2026-09-12T15:00:00Z")

	draft := "From: Ada\nTo: Bo\nSubject: Refusal checks\n\nTesting refusals.\n"
	p, err := PrepareDraft(tab, draft, now, "refusal-checks", "Ada")
	require.NoError(t, err, "PrepareDraft: %v", err)
	art, err := MakePreparedArtifact(p)
	require.NoError(t, err, "MakePreparedArtifact: %v", err)

	// 1. Unrelated dirty file in checkout
	write(t, clone, "unrelated.txt", "dirty content\n")
	{
		_, err := SendPreparedArtifact(clone, "origin", "main", p, art, 3)
		require.Error(t, err, "SendPreparedArtifact accepted dirty checkout")
	}
	// Verify dirty file is preserved
	{
		data, err := os.ReadFile(filepath.Join(clone, "unrelated.txt"))
		require.False(t, err != nil || string(data) != "dirty content\n", "SendPreparedArtifact failed to preserve unrelated dirty file")
	}
	os.Remove(filepath.Join(clone, "unrelated.txt"))

	// 2. Unrelated ahead commit on branch
	write(t, clone, "manual.txt", "manual work\n")
	git(clone, "add", "manual.txt")
	git(clone, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-m", "manual commit")
	{
		_, err := SendPreparedArtifact(clone, "origin", "main", p, art, 3)
		require.Error(t, err, "SendPreparedArtifact accepted unrelated ahead commit")
	}
	// Reset that manual commit for next test
	git(clone, "reset", "--hard", "origin/main")

	// 3. Same ID with different bytes on remote
	// Publish the note first
	if _, err := SendPreparedArtifact(clone, "origin", "main", p, art, 3); err != nil {
		require.NoError(t, err, "initial send: %v", err)
	}
	// Forge an artifact with the same ID and path, but different note bytes
	tamperedNote := strings.Replace(art.Note, "Testing refusals.", "Conflicting content.", 1)
	tamperedArt := art
	tamperedArt.Note = tamperedNote
	// Try sending tampered artifact
	{
		_, err := SendPreparedArtifact(clone, "origin", "main", p, tamperedArt, 3)
		require.Error(t, err, "SendPreparedArtifact accepted conflicting remote note content")
	}
}

func TestSendPreparedArtifactInterruptedRecoveries(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	tab := loadBus(t, clone)
	now := at("2026-09-12T16:00:00Z")

	draft := "From: Ada\nTo: Bo\nSubject: Interrupted recovery\n\nTesting partial writes.\n"
	p, err := PrepareDraft(tab, draft, now, "interrupted-recovery", "Ada")
	require.NoError(t, err, "PrepareDraft: %v", err)
	art, err := MakePreparedArtifact(p)
	require.NoError(t, err, "MakePreparedArtifact: %v", err)

	// Scenario A: Interrupted after note save, before INDEX append
	require.NoError(t, p.Save(clone))
	// SendPreparedArtifact should recover the partial write, append INDEX, commit and push
	res, err := SendPreparedArtifact(clone, "origin", "main", p, art, 3)
	require.NoError(t, err, "recovery after note save: %v", err)
	require.False(t, !res.Pushed || res.State != "published", "res = %+v, want published", res)

	// Verify exactly one note and one INDEX line on remote
	noteOnRemote, err := git(bare, "show", "main:"+art.Path)
	require.False(t, err != nil || noteOnRemote != art.Note, "note on remote: %v, content = %q", err, noteOnRemote)
	indexOnRemote, err := git(bare, "show", "main:"+IndexPath(p.Sender.Lane))
	require.NoError(t, err)
	if strings.Count(indexOnRemote, art.ID) != 1 {
		require.False(t, strings.Count(indexOnRemote, art.ID) != 1, "expected exactly 1 index entry for %s, got:\n%s", art.ID, indexOnRemote)
	}
}

func TestSendPreparedArtifactConcurrentRemoteLanding(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	bench1 := cloneBus(t, bare)
	bench2 := cloneBus(t, bare)

	tab1 := loadBus(t, bench1)
	tab2 := loadBus(t, bench2)
	now := at("2026-09-12T16:30:00Z")

	// Bench 1 prepares note from Ada
	p1, err := PrepareDraft(tab1, "From: Ada\nTo: Bo\nSubject: Ada's note\n\nNote from Ada.\n", now, "adas-note", "Ada")
	require.NoError(t, err)
	art1, err := MakePreparedArtifact(p1)
	require.NoError(t, err)

	// Bench 2 prepares and pushes note from Bo
	p2, err := PrepareDraft(tab2, "From: Bo\nTo: Ada\nSubject: Bo's note\n\nNote from Bo.\n", now, "bos-note", "Bo")
	require.NoError(t, err)
	art2, err := MakePreparedArtifact(p2)
	require.NoError(t, err)
	res2, err := SendPreparedArtifact(bench2, "origin", "main", p2, art2, 3)
	require.False(t, err != nil || !res2.Pushed, "bench2 send failed: %v", err)

	// Bench 1 sends its prepared note; it will encounter a non-fast-forward push, fetch, rebase, and succeed
	res1, err := SendPreparedArtifact(bench1, "origin", "main", p1, art1, 5)
	require.NoError(t, err, "bench1 send failed: %v", err)
	require.False(t, !res1.Pushed || res1.State != "published", "res1 = %+v, want published", res1)

	// Both notes must be on the remote branch
	if _, err := git(bare, "show", "main:"+art1.Path); err != nil {
		require.NoError(t, err, "Ada's note missing from bare remote: %v", err)
	}
	if _, err := git(bare, "show", "main:"+art2.Path); err != nil {
		require.NoError(t, err, "Bo's note missing from bare remote: %v", err)
	}
}

func stellaPrepared(t *testing.T) (string, string, Prepared, PreparedArtifact) {
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

func TestStellaPreparedRequiresCompleteRemoteIndex(t *testing.T) {
	t.Parallel()

	_, clone, p, a := stellaPrepared(t)
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

func TestStellaPreparedCannotConfirmCommitWithoutIndex(t *testing.T) {
	t.Parallel()

	bare, clone, p, a := stellaPrepared(t)
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
	require.NoError(t, e, "SendPreparedArtifact failed: %v", e)
	index, e := git(bare, "show", "main:"+IndexPath(p.Sender.Lane))
	require.False(t, r.Pushed && (e != nil || !strings.Contains(index, IndexLine(p.Index))), "claimed success after publishing note-only commit without INDEX entry")
}

func TestStellaPreparedPreservesUnrelatedAttributeEdit(t *testing.T) {
	t.Parallel()

	bare, clone, p, a := stellaPrepared(t)
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
	require.False(t, string(now) != string(want), "refusal changed unrelated dirty attribute content")
}

func TestStellaPreparedRefusesUnknownArtifactField(t *testing.T) {
	t.Parallel()

	_, clone, p, a := stellaPrepared(t)
	b, _ := json.Marshal(a)
	b = append(b[:len(b)-1], []byte(`,"unsupported":"synthetic"}`)...)
	{
		_, _, e := ValidatePreparedArtifact(b, clone, loadBus(t, clone).Config, p.Sender.Name)
		require.Error(t, e, "accepted unknown artifact field")
	}
}

func TestPreparedRefusesDuplicateKeys(t *testing.T) {
	t.Parallel()

	_, clone, p, a := stellaPrepared(t)
	dupJSON := fmt.Sprintf(`{"schema":%q,"id":%q,"id":"duplicate-id","path":%q,"note":%q,"sha256":%q}`,
		a.Schema, a.ID, a.Path, a.Note, a.SHA256)
	{
		_, _, err := ValidatePreparedArtifact([]byte(dupJSON), clone, loadBus(t, clone).Config, p.Sender.Name)
		require.Error(t, err, "accepted artifact with duplicate key")
	}
}

func TestStellaPreparedPreservesUnrelatedAheadAttributeEdit(t *testing.T) {
	t.Parallel()

	bare, clone, p, a := stellaPrepared(t)
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

// testWaitBound is how long an event poll waits for an observable before it reports rather
// than waits forever. It is read from NOVA_TEST_WAIT (default 30s), the allowed shape the
// waits class test names: every use returns the MOMENT the observable appears, so a slower
// runner pays only when the event never comes.
func testWaitBound() time.Duration {
	if v := os.Getenv("NOVA_TEST_WAIT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 30 * time.Second
}

// helperStdin keeps a helper process parked until its parent kills it, with no timer in the
// helper: the parent holds the write end of a pipe open for the life of the test, and the
// helper blocks in blockUntilKilled on the read end. When the parent SIGKILLs the helper the
// pipe closes with it; when the test ends, cleanup closes the write end.
func helperStdin(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close(); _ = r.Close() })
	cmd.Stdin = r
}

// blockUntilKilled is the helper's half of helperStdin: a read that returns only on EOF,
// which never arrives before the parent kills the process.
func blockUntilKilled() {
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestSendPreparedProcessDeathHelper(t *testing.T) {
	t.Parallel()

	if os.Getenv("GO_WANT_PREPARED_DEATH_HELPER") != "1" {
		return
	}
	busDir := os.Getenv("PREPARED_HELPER_BUS")
	barrierFile := os.Getenv("PREPARED_HELPER_BARRIER")
	mode := os.Getenv("PREPARED_HELPER_MODE")
	artFile := os.Getenv("PREPARED_HELPER_ART")

	raw, err := os.ReadFile(artFile)
	if err != nil {
		os.Exit(2)
	}
	var art PreparedArtifact
	if err := json.Unmarshal(raw, &art); err != nil {
		os.Exit(2)
	}

	tab := loadBus(t, busDir)
	_, p, err := ValidatePreparedArtifact(raw, busDir, tab.Config, "Ada")
	if err != nil {
		os.Exit(2)
	}

	switch mode {
	case "before-note-write":
		// Pre-note case: helper started, no bus mutations made yet
	case "after-note-write":
		if err := p.Save(busDir); err != nil {
			os.Exit(3)
		}
	case "after-index-write":
		if err := p.Save(busDir); err != nil {
			os.Exit(3)
		}
		if err := p.AppendIndex(busDir); err != nil {
			os.Exit(3)
		}
	case "after-note-commit":
		if err := p.Save(busDir); err != nil {
			os.Exit(3)
		}
		id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
		if _, err := stageAndCommit(busDir, id, []string{p.Path}, WithTrailer(p.Message, TrailerSend+" "+art.ID)); err != nil {
			os.Exit(3)
		}
	case "after-commit":
		if err := p.Save(busDir); err != nil {
			os.Exit(3)
		}
		if err := p.AppendIndex(busDir); err != nil {
			os.Exit(3)
		}
		EnsureMergeAttributes(busDir)
		id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
		if _, err := stageAndCommit(busDir, id, p.Paths(), WithTrailer(p.Message, TrailerSend+" "+art.ID)); err != nil {
			os.Exit(3)
		}
	case "send-call":
		res, err := SendPreparedArtifact(busDir, "origin", "main", p, art, 1)
		if err != nil {
			fmt.Fprintf(os.Stderr, "send-call failed: %v\n", err)
			os.Exit(6)
		}
		if !res.Pushed || res.State != "published" {
			os.Exit(7)
		}
		os.Exit(0)
	default:
		os.Exit(4)
	}

	// Signal observable barrier to parent
	if err := os.WriteFile(barrierFile, []byte("ready\n"), 0644); err != nil {
		os.Exit(5)
	}

	// Block until killed by parent via SIGKILL, with no timer.
	blockUntilKilled()
}

func TestPreparedIndexStagedPartialHelper(t *testing.T) {
	t.Parallel()

	if os.Getenv("GO_WANT_PREPARED_INDEX_DEATH_HELPER") != "1" {
		return
	}
	busDir := os.Getenv("PREPARED_HELPER_BUS")
	barrierFile := os.Getenv("PREPARED_HELPER_BARRIER")
	artFile := os.Getenv("PREPARED_HELPER_ART")
	as := os.Getenv("PREPARED_HELPER_AS")

	raw, err := os.ReadFile(artFile)
	if err != nil {
		os.Exit(2)
	}
	var art PreparedArtifact
	if err := json.Unmarshal(raw, &art); err != nil {
		os.Exit(2)
	}
	tab := loadBus(t, busDir)
	_, p, err := ValidatePreparedArtifact(raw, busDir, tab.Config, as)
	if err != nil {
		os.Exit(2)
	}

	// Stage a partial INDEX from a killed child: save the note, then manually truncate the
	// on-disk INDEX to a strict prefix of the bytes that would result from appending this
	// entry, and signal readiness. The parent SIGKILLs this child before it does anything
	// further. This does not interrupt production recovery mid-write: an actual
	// production-interruption gate (a kill inside SendPreparedArtifact's own INDEX append)
	// remains owed and is named in the PR.
	if err := p.Save(busDir); err != nil {
		os.Exit(3)
	}
	idxPath := filepath.Join(busDir, filepath.FromSlash(IndexPath(p.Sender.Lane)))
	existing, err := os.ReadFile(idxPath)
	if err != nil {
		os.Exit(3)
	}
	want := string(existing) + IndexLine(p.Index) + "\n"
	partial := want[:len(existing)+12]
	if err := os.WriteFile(idxPath, []byte(partial), 0o644); err != nil {
		os.Exit(3)
	}

	if err := os.WriteFile(barrierFile, []byte("ready\n"), 0644); err != nil {
		os.Exit(5)
	}
	blockUntilKilled()
}

func TestSendPreparedRecoveryHelper(t *testing.T) {
	t.Parallel()

	if os.Getenv("GO_WANT_PREPARED_RECOVERY_HELPER") != "1" {
		return
	}
	busDir := os.Getenv("PREPARED_RECOVERY_BUS")
	artFile := os.Getenv("PREPARED_RECOVERY_ART")
	as := os.Getenv("PREPARED_RECOVERY_AS")

	raw, err := os.ReadFile(artFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read art file failed: %v\n", err)
		os.Exit(1)
	}
	tab := loadBus(t, busDir)
	art, p, err := ValidatePreparedArtifact(raw, busDir, tab.Config, as)
	if err != nil {
		fmt.Fprintf(os.Stderr, "validate prepared artifact failed: %v\n", err)
		os.Exit(2)
	}
	res, err := SendPreparedArtifact(busDir, "origin", "main", p, art, 1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "send prepared artifact failed: %v\n", err)
		os.Exit(3)
	}
	if !res.Pushed || (res.State != "published" && res.State != "already-published") {
		fmt.Fprintf(os.Stderr, "unexpected res state: %+v\n", res)
		os.Exit(4)
	}
	os.Exit(0)
}

func TestSendPreparedProcessDeathRecovery(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep; measured over 5 s on the 2026-09-25 PR run). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	modes := []string{"before-note-write", "after-note-write", "after-index-write", "after-note-commit", "after-commit"}

	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			bare, clone, p, a := stellaPrepared(t)
			scratch := t.TempDir()
			barrierFile := filepath.Join(scratch, "barrier.ready")
			artFile := filepath.Join(scratch, "prepared.json")
			artJSON, _ := RenderPreparedArtifact(p)
			os.WriteFile(artFile, []byte(artJSON), 0644)

			cmd := exec.Command(os.Args[0], "-test.run=TestSendPreparedProcessDeathHelper")
			cmd.Env = append(os.Environ(),
				"GO_WANT_PREPARED_DEATH_HELPER=1",
				"PREPARED_HELPER_BUS="+clone,
				"PREPARED_HELPER_BARRIER="+barrierFile,
				"PREPARED_HELPER_MODE="+mode,
				"PREPARED_HELPER_ART="+artFile,
			)
			helperStdin(t, cmd)

			{
				err := cmd.Start()
				require.NoError(t, err, "failed to start helper process: %v", err)
			}

			// Wait for the observable barrier, up to a generous bound the environment
			// can move.
			deadline := time.Now().Add(testWaitBound())
			for {
				if _, err := os.Stat(barrierFile); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					require.FailNow(t, "timed out waiting for helper process barrier")
				}
				time.Sleep(10 * time.Millisecond)
			}

			// Terminate child violently with SIGKILL
			{
				err := cmd.Process.Kill()
				require.NoError(t, err, "failed to kill helper process: %v", err)
			}
			// Wait for process death
			_ = cmd.Wait()

			// Fresh recovery child process reconstructs and validates saved artifact from disk, then delivers
			recCmd := exec.Command(os.Args[0], "-test.run=TestSendPreparedRecoveryHelper")
			recCmd.Env = append(os.Environ(),
				"GO_WANT_PREPARED_RECOVERY_HELPER=1",
				"PREPARED_RECOVERY_BUS="+clone,
				"PREPARED_RECOVERY_ART="+artFile,
				"PREPARED_RECOVERY_AS="+p.Sender.Name,
			)
			out, err := recCmd.CombinedOutput()
			if err != nil {
				require.NoError(t, err, "fresh recovery child failed: %v\noutput:\n%s", err, string(out))
			}

			// Verify exact note on bare remote
			remoteNote, err := git(bare, "show", "main:"+p.Path)
			require.False(t, err != nil || remoteNote != a.Note, "bare remote missing note or note mismatch: %v", err)

			// Verify bare remote contains EXACTLY ONE index line
			remoteIndex, err := git(bare, "show", "main:"+IndexPath(p.Sender.Lane))
			require.NoError(t, err, "bare remote missing index: %v", err)
			matchCount := 0
			for _, l := range strings.Split(strings.TrimSpace(remoteIndex), "\n") {
				if l == IndexLine(p.Index) {
					matchCount++
				}
			}
			require.Equal(t, 1, matchCount, "bare remote has %d occurrences of index line, want exactly 1:\n%s", matchCount, remoteIndex)
		})
	}
}

// TestPreparedIndexRecoveryFromStagedPartialIndexRetainsEarlierEntries stages a partial INDEX
// from a killed child and verifies recovery from that state: earlier entries are retained and
// the recovered entry is appended. It does not interrupt production recovery mid-write; an
// actual production-interruption gate (a kill inside SendPreparedArtifact's own INDEX append)
// remains owed and is named in the PR.
func TestPreparedIndexRecoveryFromStagedPartialIndexRetainsEarlierEntries(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	tab := loadBus(t, clone)

	send := func(subject, slug, stamp string) (Prepared, PreparedArtifact) {
		t.Helper()
		p, err := PrepareDraft(tab, "From: Ada\nTo: Bo\nSubject: "+subject+"\n\nSynthetic note.\n", at(stamp), slug, "Ada")
		require.NoError(t, err)
		a, err := MakePreparedArtifact(p)
		require.NoError(t, err)
		if _, err := SendPreparedArtifact(clone, "origin", "main", p, a, 1); err != nil {
			require.NoError(t, err)
		}
		return p, a
	}

	p1, _ := send("Earlier entry one", "earlier-one", "2026-09-12T17:00:00Z")
	p2, _ := send("Earlier entry two", "earlier-two", "2026-09-12T17:01:00Z")

	p3, err := PrepareDraft(tab, "From: Ada\nTo: Bo\nSubject: Recovered entry\n\nSynthetic note.\n", at("2026-09-12T17:02:00Z"), "recovered", "Ada")
	require.NoError(t, err)
	if _, err := MakePreparedArtifact(p3); err != nil {
		require.NoError(t, err)
	}

	scratch := t.TempDir()
	barrierFile := filepath.Join(scratch, "barrier.ready")
	artFile := filepath.Join(scratch, "prepared.json")
	artJSON, _ := RenderPreparedArtifact(p3)
	require.NoError(t, os.WriteFile(artFile, []byte(artJSON), 0644))

	cmd := exec.Command(os.Args[0], "-test.run=TestPreparedIndexStagedPartialHelper")
	cmd.Env = append(os.Environ(),
		"GO_WANT_PREPARED_INDEX_DEATH_HELPER=1",
		"PREPARED_HELPER_BUS="+clone,
		"PREPARED_HELPER_AS="+p3.Sender.Name,
		"PREPARED_HELPER_BARRIER="+barrierFile,
		"PREPARED_HELPER_ART="+artFile,
	)
	helperStdin(t, cmd)
	{
		err := cmd.Start()
		require.NoError(t, err, "failed to start helper process: %v", err)
	}
	deadline := time.Now().Add(testWaitBound())
	for {
		if _, err := os.Stat(barrierFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			require.FailNow(t, "timed out waiting for helper process barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
	{
		err := cmd.Process.Kill()
		require.NoError(t, err, "failed to kill helper process: %v", err)
	}
	_ = cmd.Wait()

	recCmd := exec.Command(os.Args[0], "-test.run=TestSendPreparedRecoveryHelper")
	recCmd.Env = append(os.Environ(),
		"GO_WANT_PREPARED_RECOVERY_HELPER=1",
		"PREPARED_RECOVERY_BUS="+clone,
		"PREPARED_RECOVERY_ART="+artFile,
		"PREPARED_RECOVERY_AS="+p3.Sender.Name,
	)
	out, err := recCmd.CombinedOutput()
	if err != nil {
		require.NoError(t, err, "fresh recovery child failed: %v\noutput:\n%s", err, string(out))
	}

	remoteIndex, err := git(bare, "show", "main:"+IndexPath(p3.Sender.Lane))
	require.NoError(t, err, "bare remote missing index: %v", err)
	for _, earlier := range []Prepared{p1, p2} {
		require.Contains(t, remoteIndex, IndexLine(earlier.Index), "earlier INDEX entry was lost:\n%s", remoteIndex)
	}
	require.Contains(t, remoteIndex, IndexLine(p3.Index), "recovered INDEX entry is missing:\n%s", remoteIndex)
}

func TestSendPreparedChildExecutionAndRecovery(t *testing.T) {
	t.Parallel()

	bare, clone, p, a := stellaPrepared(t)
	scratch := t.TempDir()
	artFile := filepath.Join(scratch, "prepared.json")
	artJSON, _ := RenderPreparedArtifact(p)
	os.WriteFile(artFile, []byte(artJSON), 0644)

	// 1. Initial child executes the actual sending call
	cmdSend := exec.Command(os.Args[0], "-test.run=TestSendPreparedProcessDeathHelper")
	cmdSend.Env = append(os.Environ(),
		"GO_WANT_PREPARED_DEATH_HELPER=1",
		"PREPARED_HELPER_BUS="+clone,
		"PREPARED_HELPER_MODE=send-call",
		"PREPARED_HELPER_ART="+artFile,
	)
	out, err := cmdSend.CombinedOutput()
	if err != nil {
		require.NoError(t, err, "sending child failed: %v\noutput:\n%s", err, string(out))
	}

	// Verify on bare remote
	remoteNote, err := git(bare, "show", "main:"+p.Path)
	require.False(t, err != nil || remoteNote != a.Note, "bare remote missing note after child send: %v", err)

	// 2. Fresh recovery child confirms delivery via already-published
	recCmd := exec.Command(os.Args[0], "-test.run=TestSendPreparedRecoveryHelper")
	recCmd.Env = append(os.Environ(),
		"GO_WANT_PREPARED_RECOVERY_HELPER=1",
		"PREPARED_RECOVERY_BUS="+clone,
		"PREPARED_RECOVERY_ART="+artFile,
		"PREPARED_RECOVERY_AS="+p.Sender.Name,
	)
	outRec, err := recCmd.CombinedOutput()
	if err != nil {
		require.NoError(t, err, "subsequent recovery child failed: %v\noutput:\n%s", err, string(outRec))
	}
}

func TestRowanProbeAheadMergeCommitPublishesUnrelatedTree(t *testing.T) {
	t.Parallel()

	bare, clone, p, a := stellaIndependentPrepared(t)
	id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
	msg := WithTrailer(p.Message, TrailerSend+" "+a.ID)
	require.NoError(t, p.Save(clone))
	require.NoError(t, p.AppendIndex(clone))
	noteSha, err := stageAndCommit(clone, id, []string{p.Path, IndexPath(p.Sender.Lane)}, msg)
	require.NoError(t, err)
	base, err := git(clone, "rev-parse", "origin/main")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(clone, "SYNTHETIC_UNRELATED_LEAK.txt"), []byte("synthetic unrelated payload\n"), 0644))
	if _, err := git(clone, "add", "SYNTHETIC_UNRELATED_LEAK.txt"); err != nil {
		require.NoError(t, err)
	}
	tree, err := git(clone, "write-tree")
	require.NoError(t, err)
	merge, err := git(clone, append(identityArgs(id), "commit-tree", strings.TrimSpace(tree),
		"-p", strings.TrimSpace(noteSha), "-p", strings.TrimSpace(base), "-m", msg)...)
	require.NoError(t, err)
	if _, err := git(clone, "reset", "--hard", strings.TrimSpace(merge)); err != nil {
		require.NoError(t, err)
	}
	r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if e == nil && r.Pushed {
		{
			_, err := git(bare, "show", "main:SYNTHETIC_UNRELATED_LEAK.txt")
			require.Error(t, err, "published unrelated content through an ahead merge commit")
		}
	}
}

func TestRowanProbeStaleIndexLock(t *testing.T) {
	t.Parallel()

	_, clone, p, a := stellaIndependentPrepared(t)
	lockFile := filepath.Join(clone, ".git", "index.lock")
	require.NoError(t, os.WriteFile(lockFile, []byte("stale lock\n"), 0644))
	defer os.Remove(lockFile)
	_, err := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	require.Error(t, err, "expected error with index.lock present")
	if !strings.Contains(err.Error(), "index is locked") || !strings.Contains(err.Error(), a.ID) {
		require.False(t, !strings.Contains(err.Error(), "index is locked") || !strings.Contains(err.Error(), a.ID), "expected bounded index lock refusal with prepared ID %q, got: %v", a.ID, err)
	}
}

func TestPreparedDeliveryRecoversEmptyOrPartialGitattributes(t *testing.T) {
	t.Parallel()

	bare, clone, p, a := stellaIndependentPrepared(t)
	// Write empty .gitattributes (simulating crash right after open/create before EnsureMergeAttributes wrote content)
	attrsPath := filepath.Join(clone, AttributesName)
	require.NoError(t, os.WriteFile(attrsPath, []byte(""), 0644))

	res, err := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	require.NoError(t, err, "SendPreparedArtifact failed on empty .gitattributes: %v", err)
	require.True(t, res.Pushed, "expected note to be pushed")

	// Verify remote received note, index, and complete .gitattributes with union rules
	noteRemote, err := git(bare, "show", "main:"+p.Path)
	require.False(t, err != nil || noteRemote != a.Note, "remote note mismatch: %v", err)
	attrsRemote, err := git(bare, "show", "main:"+AttributesName)
	require.False(t, err != nil || !strings.Contains(attrsRemote, "from-*/INDEX merge=union"), "remote .gitattributes missing union rule: %v\n%s", err, attrsRemote)
}

func TestPreparedDeliveryRecoversPartialNoteOnDisk(t *testing.T) {
	t.Parallel()

	bare, clone, p, a := stellaIndependentPrepared(t)
	fullNote := filepath.Join(clone, filepath.FromSlash(p.Path))
	require.NoError(t, os.MkdirAll(filepath.Dir(fullNote), 0755))
	// Write partial prefix of note (first 20 bytes)
	prefix := a.Note[:20]
	require.NoError(t, os.WriteFile(fullNote, []byte(prefix), 0644))

	res, err := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	require.NoError(t, err, "SendPreparedArtifact failed on partial note write: %v", err)
	require.True(t, res.Pushed, "expected note to be pushed")

	noteRemote, err := git(bare, "show", "main:"+p.Path)
	require.False(t, err != nil || noteRemote != a.Note, "remote note mismatch: %v", err)
}

func TestPreparedDeliveryRecoversPartialIndexOnDisk(t *testing.T) {
	t.Parallel()

	bare, clone, p, a := stellaIndependentPrepared(t)
	fullIndex := filepath.Join(clone, filepath.FromSlash(IndexPath(p.Sender.Lane)))
	require.NoError(t, os.MkdirAll(filepath.Dir(fullIndex), 0755))
	// Write partial prefix of index line
	line := IndexLine(p.Index)
	prefix := line[:15]
	require.NoError(t, os.WriteFile(fullIndex, []byte(prefix), 0644))

	res, err := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	require.NoError(t, err, "SendPreparedArtifact failed on partial index write: %v", err)
	require.True(t, res.Pushed, "expected note to be pushed")

	idxRemote, err := git(bare, "show", "main:"+IndexPath(p.Sender.Lane))
	require.False(t, err != nil || !strings.Contains(idxRemote, line), "remote index missing completed line: %v\n%s", err, idxRemote)
}

func TestPreparedDeliveryRefusesUnrelatedForeignGitattributes(t *testing.T) {
	t.Parallel()

	_, clone, p, a := stellaIndependentPrepared(t)
	attrsPath := filepath.Join(clone, AttributesName)
	require.NoError(t, os.WriteFile(attrsPath, []byte("*.iso filter=lfs\n"), 0644))

	_, err := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	require.Error(t, err, "expected error on foreign .gitattributes content")
	require.Contains(t, err.Error(), "unrelated dirty changes in .gitattributes", "unexpected error message: %v", err)
}

func TestPreparedDeliveryRefusesConflictingNoteOnDisk(t *testing.T) {
	t.Parallel()

	_, clone, p, a := stellaIndependentPrepared(t)
	fullNote := filepath.Join(clone, filepath.FromSlash(p.Path))
	require.NoError(t, os.MkdirAll(filepath.Dir(fullNote), 0755))
	require.NoError(t, os.WriteFile(fullNote, []byte("completely conflicting note\n"), 0644))

	_, err := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	require.Error(t, err, "expected error on conflicting note content")
	require.False(t, !strings.Contains(err.Error(), "conflicting") && !strings.Contains(err.Error(), "unrelated dirty changes"), "unexpected error message: %v", err)
}
