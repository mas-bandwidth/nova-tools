//go:build functional

package workgh

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGhQueryRunsTheProgramWithTheBodyOnStdin is the transport's own test,
// apart from the logic the unit tests drive through Replay: GhQuery runs
// `<program> api graphql --input -` with the document and its variables as the
// JSON body on stdin and returns what the program printed; a program that
// fails is an error carrying its stderr. The program is a stand-in in the
// test's own directory, so no network is reached.
func TestGhQueryRunsTheProgramWithTheBodyOnStdin(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in gh runs through /bin/sh")
	}
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	script := "#!/bin/sh\necho \"$@\" > " + dir + "/args\ncat > " + dir + "/body\n" +
		"[ -f " + dir + "/fail ] && { echo 'not logged in' >&2; exit 4; }\necho '{\"data\":{}}'\n"
	require.NoError(t, os.WriteFile(gh, []byte(script), 0o755))

	out, err := GhQuery(gh)(context.Background(), repoDoc, map[string]any{"owner": "acme", "repo": "x"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"data":{}}`, string(out))
	args, err := os.ReadFile(filepath.Join(dir, "args"))
	require.NoError(t, err)
	assert.Equal(t, "api graphql --input -\n", string(args))
	var body struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "body"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &body))
	assert.Equal(t, repoDoc, body.Query)
	assert.Equal(t, map[string]any{"owner": "acme", "repo": "x"}, body.Variables)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "fail"), nil, 0o644))
	_, err = GhQuery(gh)(context.Background(), repoDoc, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not logged in")
}
