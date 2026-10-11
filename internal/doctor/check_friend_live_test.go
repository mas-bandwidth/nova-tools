package doctor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDoctorFriendLiveNamesARefusedBeat(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFriendPlist(t, root, "ada", "--as", "ada", "--harness", "claude")
	fake := fakeEnv{env: map[string]string{"HOME": "home"}, root: root, clock: time.Now(),
		exec: func(name string, _ ...string) (string, error) {
			switch name {
			case "nova-friend":
				return `{"beat_error":"refused: You must hook in first","inbox_error":""}`, nil
			case "launchctl":
				return "", nil
			}
			return "", errors.New("unexpected " + name)
		}}

	r := checkFriendLive(context.Background(), fake)
	assert.Equal(t, Fail, r.Status)
	assert.Contains(t, r.Evidence, "You must hook in first")
	assert.Contains(t, r.Fix, "nova-sprint hook")

	fake.exec = func(name string, _ ...string) (string, error) {
		switch name {
		case "nova-friend":
			return `{"beat_error":"","inbox_error":"","last_beat":"2020-01-01T00:00:00Z"}`, nil
		case "launchctl":
			return "loaded", nil
		}
		return "", errors.New("unexpected " + name)
	}
	r = checkFriendLive(context.Background(), fake)
	assert.Equal(t, Fail, r.Status)
	assert.Contains(t, r.Evidence, "older than")

	fake.exec = func(string, ...string) (string, error) { return `{"beat_error":"","inbox_error":""}`, nil }
	r = checkFriendLive(context.Background(), fake)
	assert.Equal(t, OK, r.Status)
}
