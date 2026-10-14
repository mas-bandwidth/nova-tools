package must

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// recording is a testing.TB that records every failure instead of ending the
// test, so a failing row is observed, not assumed. It embeds the interface
// for the methods that stay unused here; every failure path is overridden.
type recording struct {
	testing.TB
	failed  bool
	message []string
}

func (r *recording) Helper()      {}
func (r *recording) Fail()        { r.failed = true }
func (r *recording) FailNow()     { r.failed = true }
func (r *recording) Failed() bool { return r.failed }
func (r *recording) Error(args ...any) {
	r.failed = true
	r.message = append(r.message, fmt.Sprint(args...))
}
func (r *recording) Errorf(format string, args ...any) {
	r.failed = true
	r.message = append(r.message, fmt.Sprintf(format, args...))
}
func (r *recording) Fatal(args ...any) {
	r.failed = true
	r.message = append(r.message, fmt.Sprint(args...))
}
func (r *recording) Fatalf(format string, args ...any) {
	r.failed = true
	r.message = append(r.message, fmt.Sprintf(format, args...))
}

// TestMustRowsPassAndFail pins the module on its rows: a passing row fails
// nothing, a row whose text lacks one want names that want, and one that lacks
// several names each of them, not only the first.
func TestMustRowsPassAndFail(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name    string
		text    string
		want    []string
		failing bool
	}{
		{"has-every-want", "REPORT REFUSED: line 2: unknown kind gadget", []string{"REPORT REFUSED", "line 2", "gadget"}, false},
		{"lacks-one", "REPORT REFUSED", []string{"line 2"}, true},
		{"lacks-many", "a b c", []string{"a", "x", "y"}, true},
		{"no-wants", "anything", nil, false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			rec := &recording{TB: t}
			ContainsAll(rec, row.text, row.want...)
			missing := 0
			for _, w := range row.want {
				if !strings.Contains(row.text, w) {
					missing++
				}
			}
			if row.failing {
				assert.True(t, rec.failed, "a lacking row passed: %q", row.text)
				assert.Equal(t, missing, len(rec.message), "each missing want is named, one message per missing want: %v", rec.message)
			} else {
				assert.False(t, rec.failed, "a passing row failed: %v", rec.message)
			}
		})
	}
}

// TestFileReadsPassAndFail pins FileReads: a file written as the want body
// passes, a file with another body fails, and a path that does not read fails
// as a failure, never a skip.
func TestFileReadsPassAndFail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	body := filepath.Join(dir, "body.tsv")
	assert.NoError(t, os.WriteFile(body, []byte("name\tkind\n"), 0o600))
	rows := []struct {
		name    string
		path    string
		want    string
		failing bool
	}{
		{"reads-the-body", body, "name\tkind\n", false},
		{"wrong-body", body, "name\n", true},
		{"unreadable-path", filepath.Join(dir, "gone"), "any", true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			rec := &recording{TB: t}
			FileReads(rec, row.path, row.want)
			if row.failing {
				assert.True(t, rec.failed, "a bad body or a path that does not read passed: %s", row.path)
			} else {
				assert.False(t, rec.failed, "a passing row failed: %v", rec.message)
			}
		})
	}
}

// ExampleContainsAll is the package doc's table row, run by the go tool: the
// text carries every want, so the recording TB fails nothing.
func ExampleContainsAll() {
	rec := &recording{}
	ContainsAll(rec, "REPORT REFUSED: line 2: unknown kind gadget", "REPORT REFUSED", "line 2")
	fmt.Println(rec.failed)
	// Output: false
}
