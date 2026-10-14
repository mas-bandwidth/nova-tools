package must

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// FileReads fails the test through assert unless the file at path reads back
// as exactly the want body; a path that does not read is a failure, not a
// skip.
func FileReads(t testing.TB, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		assert.Failf(t, "must.FileReads", "reading %s: %v", path, err)
		return
	}
	assert.Equal(t, want, string(got))
}
