package main

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Card ci-functional-supports-json: functional --json emits one structured
// object whose facts carry the selected packages and the -run pattern, the
// same selection the two text lines print.
func TestCiFunctionalEmitsStructuredJson(t *testing.T) {
	t.Parallel()

	dir := filepath.Join("..", "..", "internal", "ci", "slowtests")
	code, stdout, stderr := runCI(t, []string{"functional", "--json", dir}, "")
	assert.Equal(t, 0, code, "functional --json %s: exit %d stdout %q stderr %q, want 0", dir, code, stdout, stderr)
	var got struct {
		Facts struct {
			Packages []string `json:"packages"`
			Pattern  string   `json:"pattern"`
		} `json:"facts"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "functional --json: stdout %q is not JSON", stdout)
	assert.Equal(t, 0, len(got.Facts.Packages), "functional --json: packages = %v, want none for a dir without the tag", got.Facts.Packages)

	code, stdout, _ = runCI(t, []string{"functional", "--json", "--bogus"}, "")
	assert.Equal(t, 2, code, "functional --json --bogus: exit %d stdout %q, want refusal 2 as JSON on stdout", code, stdout)
	var refused struct {
		Result struct {
			Status string `json:"status"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &refused), "functional --json --bogus: stdout %q is not JSON", stdout)
	assert.Equal(t, "refused", refused.Result.Status, "functional --json --bogus: status = %q, want refused", refused.Result.Status)
}
