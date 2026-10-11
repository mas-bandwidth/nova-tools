package doctor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDoctorFriendLoginNamesAConfigDirThatIsNotSignedIn(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFriendPlist(t, root, "ada", "--as", "ada", "--harness", "claude", "--config-dir", "home/ada-config")
	fake := friendEnv{goos: "darwin", fakeEnv: fakeEnv{env: map[string]string{"HOME": "home"}, root: root,
		exec: func(name string, _ ...string) (string, error) {
			if name == "env" {
				return `{"terminal_reason":"api_error","duration_api_ms":0}`, nil
			}
			return "", errors.New("unexpected " + name)
		}}}

	r := checkFriendLogin(context.Background(), fake)
	assert.Equal(t, Fail, r.Status)
	assert.Contains(t, r.Evidence, "home/ada-config is not signed in")
	assert.Contains(t, r.Fix, "claude login")

	fake.exec = func(string, ...string) (string, error) {
		return `{"terminal_reason":"success"}`, nil
	}
	r = checkFriendLogin(context.Background(), fake)
	assert.Equal(t, OK, r.Status)
}
