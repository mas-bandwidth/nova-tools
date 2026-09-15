package bus

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The bench side of the reply transaction, at the package: the name a generated reply lands
// under and the publish that refuses to overwrite one.
//
// docs/SPEC-BUS-REPLY.md 279-323. Until this file existed, ErrNoExclusivePublish,
// PublishNoReplace and LegacyDraftID appeared in no test in this repository, so the row of
// the refusal table at 408 was asserted nowhere.

// 262-277: "the literal `legacy-` followed by the first 12 lowercase hex digits of the
// SHA-256 of the target's repo-relative path ... It is one segment, it is deterministic,
// and two legacy targets cannot collide onto one name."
//
// expected= `legacy-` + the first 12 hex of the digest, no `/`, the same answer twice, and
// two different paths giving two different names.
func TestLegacyDraftIDIsOneSegmentAndDeterministic(t *testing.T) {
	t.Parallel()
	const path = "from-bo/2026-09-05T0900Z-old.md"
	sum := sha256.Sum256([]byte(path))
	want := "legacy-" + hex.EncodeToString(sum[:])[:12]
	got := LegacyDraftID(path)
	if got != want {
		t.Errorf("LegacyDraftID(%q) = %q, want %q", path, got, want)
	}
	if got != LegacyDraftID(path) {
		t.Error("LegacyDraftID is not deterministic")
	}
	if strings.ContainsAny(got, "/\\") {
		t.Errorf("%q is not one path segment", got)
	}
	if len(got) != len("legacy-")+12 {
		t.Errorf("%q is not `legacy-` and twelve hex digits", got)
	}
	if other := LegacyDraftID("from-bo/2026-09-05T0901Z-older.md"); other == got {
		t.Errorf("two paths composed one name: %q", got)
	}
	// The derived id is a name and not a proof: this asserts the FIELD's job, which is that
	// two ordinary paths do not compose one name. What stands behind the file is the
	// create-exclusive publish below.
	for _, c := range got[len("legacy-"):] {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Errorf("%q is not lowercase hex", got)
			break
		}
	}
}

// 279-281: "An existing file at that path is a refusal, never an overwrite", and 321-323:
// "The temporary is removed on every failing path ... so a refused run leaves --draft-dir
// holding exactly what it held before, and no stray `.tmp` beside it."
//
// expected= ErrDraftExists naming the final path, the existing file byte-unchanged, and one
// file in the directory.
func TestPublishNoReplaceRefusesAnExistingNameAndLeavesNoTemporary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	final := filepath.Join(dir, "2026-09-09T1234Z-re-bo-abcdef012345.md")
	if err := os.WriteFile(final, []byte("somebody is editing this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := PublishNoReplace(dir, filepath.Base(final), []byte("the new one\n"))
	if !errors.Is(err, ErrDraftExists) {
		t.Fatalf("PublishNoReplace over an existing name returned (%q, %v), want ErrDraftExists", path, err)
	}
	if !strings.Contains(err.Error(), final) {
		t.Errorf("the refusal does not name the path: %v", err)
	}
	raw, rerr := os.ReadFile(final)
	if rerr != nil || string(raw) != "somebody is editing this\n" {
		t.Errorf("the existing draft was touched: %v %q", rerr, raw)
	}
	assertOnlyFiles(t, dir, filepath.Base(final))
}

// The ordinary path, asserted so that the two refusals above are refusals and not the only
// thing this function does.
//
// expected= the final path, the content byte for byte, and no temporary beside it.
func TestPublishNoReplaceWritesTheWholeDraftAndRemovesItsTemporary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const content = "From: Ada\nTo: Bo\nRe: bo-abcdef012345\nSubject: Re: the gate\n\nYes.\n"
	path, err := PublishNoReplace(dir, "2026-09-09T1234Z-re-bo-abcdef012345.md", []byte(content))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != content {
		t.Errorf("the published draft is %q (%v), want the content byte for byte", raw, err)
	}
	assertOnlyFiles(t, dir, "2026-09-09T1234Z-re-bo-abcdef012345.md")
}

// 313-319: "where neither is available ... the tool refuses to publish: exit 2, naming the
// directory, the call it tried and what the call said ... It does not fall back to a
// replacing rename and it does not fall back to check-then-rename."
//
// The seams stand in for the filesystem, which is the only thing that can produce this
// state and is not a thing a test can ask a disk for.
//
// expected= ErrNoExclusivePublish naming the directory, quoting WHAT LINK SAID -- the first
// version threw the link error away and reported the second call's words instead -- and
// naming the second call and its words too. No file, no temporary.
func TestNoCreateExclusivePublishQuotesWhatEachCallSaid(t *testing.T) {
	dir := t.TempDir()
	linkSaid := errors.New("operation not supported by this filesystem")
	renameSaid := errors.New("the second call is not here either")
	restore := stubPublish(func(string, string) error { return linkSaid }, func(string, string) error { return renameSaid })
	defer restore()

	path, err := PublishNoReplace(dir, "2026-09-09T1234Z-re-bo-abcdef012345.md", []byte("body\n"))
	if !errors.Is(err, ErrNoExclusivePublish) {
		t.Fatalf("PublishNoReplace on a filesystem with neither publish returned (%q, %v), want ErrNoExclusivePublish", path, err)
	}
	for _, want := range []string{dir, "link said", linkSaid.Error(), noReplaceRenameCall, renameSaid.Error()} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q: %v", want, err)
		}
	}
	assertOnlyFiles(t, dir)
}

// The second publish is the one that succeeds where the first is not available: a
// filesystem that refuses hard links still publishes, and still refuses an existing name.
func TestTheSecondPublishIsUsedWhenTheFirstIsNotAvailable(t *testing.T) {
	dir := t.TempDir()
	linkSaid := errors.New("operation not supported by this filesystem")
	restore := stubPublish(func(string, string) error { return linkSaid }, os.Rename)
	defer restore()

	path, err := PublishNoReplace(dir, "2026-09-09T1234Z-re-bo-abcdef012345.md", []byte("body\n"))
	if err != nil {
		t.Fatalf("the second publish did not publish: %v", err)
	}
	raw, rerr := os.ReadFile(path)
	if rerr != nil || string(raw) != "body\n" {
		t.Errorf("the second publish wrote %q (%v)", raw, rerr)
	}
	assertOnlyFiles(t, dir, "2026-09-09T1234Z-re-bo-abcdef012345.md")

	// And an existing name is still the refusal, made by the publish and not by a check.
	restore2 := stubPublish(func(string, string) error { return linkSaid }, func(string, string) error { return os.ErrExist })
	defer restore2()
	if _, err := PublishNoReplace(dir, "2026-09-09T1234Z-re-bo-abcdef012345.md", []byte("another\n")); !errors.Is(err, ErrDraftExists) {
		t.Errorf("the second publish's already-exists error is %v, want ErrDraftExists", err)
	}
	assertOnlyFiles(t, dir, "2026-09-09T1234Z-re-bo-abcdef012345.md")
}

// stubPublish stands in for the two create-exclusive publishes and hands back the restore.
func stubPublish(link, rename func(from, to string) error) func() {
	oldLink, oldRename := linkFile, noReplacePublish
	linkFile, noReplacePublish = link, rename
	return func() { linkFile, noReplacePublish = oldLink, oldRename }
}

// assertOnlyFiles is the directory holding exactly these names and nothing else -- a
// temporary that survived a refusal is the thing this asserts against.
func assertOnlyFiles(t *testing.T, dir string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if len(got) != len(want) {
		t.Fatalf("the directory holds %v, want %v (a refused publish leaves no temporary)", got, want)
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
			}
		}
		if !found {
			t.Fatalf("the directory holds %v, want %v", got, want)
		}
	}
}
