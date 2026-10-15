package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/doctor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hookEnv is the fake store the check reads: Exec answers from a table, so no
// store, process, socket or clock is touched.
type hookEnv struct {
	exec func(name string, args ...string) (string, error)
}

func (hookEnv) Getenv(string) string                  { return "" }
func (hookEnv) Now() time.Time                        { return time.Time{} }
func (hookEnv) ReadFile(string) ([]byte, error)       { return nil, fs.ErrNotExist }
func (hookEnv) ReadDir(string) ([]fs.DirEntry, error) { return nil, fs.ErrNotExist }
func (hookEnv) Dial(context.Context, string, string) error {
	return errors.New("no network here")
}
func (e hookEnv) Exec(_ context.Context, name string, args ...string) (string, error) {
	return e.exec(name, args...)
}

// friend is one name's row in the fake hook record.
type friend struct {
	name, role, state, version string
}

// friendsDoc is the `nova-hook friends --json` document the fake store answers
// with, built through the same JSON the tool prints.
func friendsDoc(t *testing.T, serve string, friends ...friend) string {
	t.Helper()
	items := make([]map[string]any, 0, len(friends))
	for _, f := range friends {
		items = append(items, map[string]any{"kind": "name", "fields": map[string]any{
			"name": f.name, "role": f.role, "state": f.state, "version": f.version}})
	}
	raw, err := json.Marshal(map[string]any{
		"facts": map[string]any{"serve_alive": serve != "", "serve_version": serve},
		"items": items,
	})
	require.NoError(t, err)
	return string(raw)
}

// TestFriendsHookedCheck pins the check's four answers: every friend up on the
// installed build is ok; a friend not up, a friend up on another build, and a
// running serve of another build are each a fail whose fix names the friend
// and nova-update (docs/SPEC-DOCTOR.md, "friends-hooked"). Each case is able
// to fail: the ok case fails a check that always refuses, and each failure
// case fails one that always passes.
func TestFriendsHookedCheck(t *testing.T) {
	t.Parallel()
	const installed = "nova-hook v2 darwin/arm64 go1.26.6"
	cases := []struct {
		name         string
		friends      string
		want         doctor.Status
		wantEvidence string
		wantFix      string
	}{
		{
			name:    "all good",
			friends: friendsDoc(t, "v2", friend{"ada", "friend", "up", "v2"}, friend{"bob", "comms", "up", "v2"}),
			want:    doctor.OK,
		},
		{
			name:         "a rostered friend is not up",
			friends:      friendsDoc(t, "v2", friend{"ada", "friend", "up", "v2"}, friend{"carl", "comms", "down", "v2"}),
			want:         doctor.Fail,
			wantEvidence: "friend carl is down",
			wantFix:      "carl",
		},
		{
			name:         "a friend up is hooked by another build",
			friends:      friendsDoc(t, "v2", friend{"ada", "friend", "up", "v1"}),
			want:         doctor.Fail,
			wantEvidence: "friend ada is hooked by v1",
			wantFix:      "ada",
		},
		{
			name:         "the running hook serve passed another build",
			friends:      friendsDoc(t, "v1", friend{"ada", "friend", "up", "v2"}),
			want:         doctor.Fail,
			wantEvidence: "the running hook serve is v1",
			wantFix:      "hook-serve",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := hookEnv{exec: func(name string, args ...string) (string, error) {
				line := strings.Join(append([]string{name}, args...), " ")
				switch line {
				case "nova-hook friends --json":
					return tc.friends, nil
				case "nova-hook version":
					return installed, nil
				}
				return "", errors.New("unexpected " + line)
			}}
			got := checkFriendsHooked(context.Background(), env)
			assert.Equal(t, tc.want, got.Status, got)
			assert.Contains(t, got.Evidence, tc.wantEvidence)
			if tc.want != doctor.OK {
				assert.Contains(t, got.Fix, "nova-update")
				assert.Contains(t, got.Fix, tc.wantFix)
			}
		})
	}
}
