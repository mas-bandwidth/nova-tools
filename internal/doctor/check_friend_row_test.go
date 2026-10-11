package doctor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDoctorFriendRowRefusesABatchRowThatCannotBeDealt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFriendPlist(t, root, "ada", "--as", "ada", "--harness", "claude", "--width", "4")
	fake := friendEnv{goos: "darwin", fakeEnv: fakeEnv{env: map[string]string{"HOME": "home"}, root: root,
		exec: func(name string, _ ...string) (string, error) {
			if name == "nova-config" {
				return `{"mode":"one-shot","roles":["builder","reader"],"width":4}`, nil
			}
			return "", errors.New("unexpected " + name)
		}}}

	r := checkFriendRow(context.Background(), fake)
	assert.Equal(t, OK, r.Status)

	fake.exec = func(string, ...string) (string, error) {
		return `{"mode":"batch","roles":["reader"],"width":4}`, nil
	}
	r = checkFriendRow(context.Background(), fake)
	assert.Equal(t, Fail, r.Status)
	assert.Contains(t, r.Evidence, "not one-shot")
	assert.Contains(t, r.Evidence, "lack builder")
	assert.Contains(t, r.Fix, "nova-config friend set")
}
