package tokens

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The unit cover for opencode.go: the cell renderer (opencode.go:231), the copy of the
// database into scratch (opencode.go:257), the one program the read needs (opencode.go:250),
// and the refusals of ReadOpenCode (opencode.go:84) that answer BEFORE the one subprocess.
// The fold past the first `sqlite3` query needs that subprocess, and a unit test runs no
// program: those lines are the cover's named gap, in the card's report. Every test is
// named TestOpencodeCover* so `-run TestOpencodeCover` selects them.

// TestOpencodeCoverCellText renders one JSON cell as text: SQL NULL is the empty string
// (the absence that folds to a dash, never a zero), a string stands as written, a number
// keeps the spelling it arrived with, a bool is 1 or 0, and anything else is fmt.Sprint.
func TestOpencodeCoverCellText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   any
		want string
	}{
		{"NULL is the empty string", nil, ""},
		{"a string stands as written", "opus-4", "opus-4"},
		{"an integer keeps its spelling", json.Number("12345"), "12345"},
		{"a decimal keeps its spelling", json.Number("1.5"), "1.5"},
		{"true is 1", true, "1"},
		{"false is 0", false, "0"},
		{"a bare float is fmt.Sprint", 2.5, "2.5"},
		{"a nested value is fmt.Sprint", []any{1.0, "x"}, "[1 x]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, cellText(tc.in), "the cell renders as the day file reads it")
		})
	}
}

// TestOpencodeCoverCopyFile copies the database and its refusals: a source that is not
// there is refused through the one door openSource, a source that is a directory fails
// in io.Copy, and a destination directory that is not there is refused by os.Create.
func TestOpencodeCoverCopyFile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		content     string
		wantErr     string
		skipSource  bool
		skipDestDir bool
		srcIsDir    bool
	}{
		{"the bytes land beside the target", "hello db", "", false, false, false},
		{"a missing source is refused", "x", "no such file or directory", true, false, false},
		{"a source that is a directory is refused", "x", "is a directory", false, false, true},
		{"a destination with no directory is refused", "x", "no such file or directory", false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			src := filepath.Join(dir, "opencode.db")
			if tc.srcIsDir {
				require.NoError(t, os.Mkdir(src, 0o755))
			} else {
				require.NoError(t, os.WriteFile(src, []byte(tc.content), 0o644))
				if tc.skipSource {
					require.NoError(t, os.Remove(src))
				}
			}
			dstDir := filepath.Join(dir, "copy")
			require.NoError(t, os.MkdirAll(dstDir, 0o755))
			if tc.skipDestDir {
				dstDir = filepath.Join(dir, "gone", "copy")
			}
			dst := filepath.Join(dstDir, filepath.Base(src))

			err := copyFile(src, dst)
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr, "the refusal names what is missing")
				if !tc.skipDestDir && !tc.srcIsDir {
					assert.NoFileExists(t, dst, "a refused copy leaves no half file behind")
				}
				return
			}
			require.NoError(t, err, "the copy runs")
			got, readErr := os.ReadFile(dst)
			require.NoError(t, readErr, "the copy is readable at the target")
			assert.Equal(t, tc.content, string(got), "the copy is the source, byte for byte")
		})
	}
}

// TestOpencodeCoverHaveSQLite answers the one question the program asks: does sqlite3
// resolve on PATH? It agrees with exec.LookPath, and a bench without the program is
// refused with the program's name in the line. On this machine sqlite3 resolves, so the
// refusal branch is the cover's other named gap (it needs PATH taken away, and a
// parallel test may not reach the environment).
func TestOpencodeCoverHaveSQLite(t *testing.T) {
	t.Parallel()

	_, lookErr := exec.LookPath(SQLiteBinary)
	got := HaveSQLite()
	if lookErr == nil {
		assert.NoErrorf(t, got, "%s resolves on PATH, so the read is allowed: %v", SQLiteBinary, got)
		return
	}
	assert.ErrorContains(t, got, SQLiteBinary, "the refusal names the program it needs")
	assert.ErrorContains(t, got, SQLiteBinary+" -readonly", "the refusal says how the program is used")
}

// TestOpencodeCoverReadOpenCodeRefusals refuses the two things the reader can find before
// it runs a program at all: a scratch that cannot hold the copy, and a database that is
// not there. Both are counted unreadable on the Source and answered WITHOUT the one
// subprocess; every field the Source is born with stands.
func TestOpencodeCoverReadOpenCodeRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		scratchFile bool
		noDB        bool
		wantErr     string
	}{
		{"scratch that is not a directory is refused", true, false, "not a directory"},
		{"a database that is not there is refused", false, true, "no such file or directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			db := filepath.Join(dir, "opencode.db")
			require.NoError(t, os.WriteFile(db, []byte("not really sqlite, never opened"), 0o644))

			scratch := filepath.Join(t.TempDir(), "scratch")
			if tc.scratchFile {
				require.NoError(t, os.WriteFile(scratch, []byte("occupied"), 0o644))
			}
			if tc.noDB {
				require.NoError(t, os.Remove(db))
			}

			s := ReadOpenCode("cover", db, scratch, DefaultTimeout, &Rules{})
			assert.Equal(t, Label(KindOpenCode, "cover"), s.Label, "the label is the kind and the name")
			assert.Equal(t, KindOpenCode, s.Kind, "the kind is the reader's own")
			assert.Equal(t, db, s.Path, "the path is the source the caller declared")
			assert.Equal(t, AllTypes, s.Reports, "the reader reports all five types")
			assert.Equal(t, UTC, s.Basis, "the stamp is read as UTC")
			assert.Equal(t, 1, s.Stat.Files, "the database itself is the one file counted")
			assert.Equal(t, 1, s.Stat.Unreadable, "the source is counted unreadable, never silently empty")
			require.Len(t, s.Unreadables, 1, "the refusal is carried in its own note")
			u := s.Unreadables[0]
			assert.Equal(t, s.Label, u.Label, "the note carries the source's label")
			assert.Equal(t, db, u.Path, "the note names the path that could not be read")
			assert.Contains(t, u.Why, tc.wantErr, "the reason names the failure: %q", u.Why)
			assert.Empty(t, s.Stream, "a refused source folds no messages")
		})
	}
}
