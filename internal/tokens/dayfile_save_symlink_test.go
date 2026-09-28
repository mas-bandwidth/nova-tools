package tokens

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Save must refuse a directory symlink for both spellings. A trailing
// separator is not a different directory: on macOS Lstat of link/ reports the
// directory, and filepath.Join would strip it before atomicfile follows the
// link. Neither spelling may change the referent day file.
func TestSaveRefusesDirectorySymlink(t *testing.T) {
	t.Parallel()

	const day = "2026-09-11"
	referentBody := []byte("referent-bytes-stay\n")
	spellings := []struct {
		name string
		out  func(link string) string
	}{
		{name: "symlink", out: func(link string) string { return link }},
		{name: "trailing separator", out: func(link string) string {
			return link + string(filepath.Separator)
		}},
	}
	for _, spelling := range spellings {
		t.Run(spelling.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			real := filepath.Join(root, "real")
			if err := os.Mkdir(real, 0o755); err != nil {
				t.Fatal(err)
			}
			referent := filepath.Join(real, day+FileSuffix)
			if err := os.WriteFile(referent, referentBody, 0o644); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "link")
			if err := os.Symlink(real, link); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadDir(real)
			if err != nil {
				t.Fatal(err)
			}

			out := spelling.out(link)
			df := &DayFile{
				Day: day, At: "2026-09-11T00:00:00Z", Build: "test", Turns: Dash,
				Rows: []DayRow{{
					Date: day, Model: "m", Repo: "r", Basis: UTC, Sources: []string{"test"},
				}},
			}
			err = df.Save(out)
			if err == nil {
				t.Fatal("Save wrote through a directory symlink")
			}
			msg := err.Error()
			if !strings.Contains(msg, "save") {
				t.Errorf("error %q does not name the save", msg)
			}
			if !strings.Contains(msg, "symlink") {
				t.Errorf("error %q does not name the symlink", msg)
			}
			if !strings.Contains(msg, out) {
				t.Errorf("error %q does not name the path %q", msg, out)
			}
			if !strings.Contains(msg, "next: pass the directory") {
				t.Errorf("error %q does not name the next action", msg)
			}

			got, err := os.ReadFile(referent)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, referentBody) {
				t.Fatalf("referent bytes = %q, want %q", got, referentBody)
			}
			after, err := os.ReadDir(real)
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != len(before) {
				t.Fatalf("referent directory changed: before %d entries, after %d", len(before), len(after))
			}
			linkInfo, err := os.Lstat(link)
			if err != nil {
				t.Fatal(err)
			}
			if linkInfo.Mode()&os.ModeSymlink == 0 {
				t.Fatal("the symlink was replaced")
			}
		})
	}
}
