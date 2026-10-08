package main

import (
	"fmt"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdmitMemberLaunchRequiresFreshRunningClaim(t *testing.T) {
	p := member.Packet{Card: "c1", Gen: 2, Epoch: 7}
	run := func(_ ...string) (int, []byte) {
		return 0, []byte(`{"epoch":7,"machine":"RUNNING","cards":[{"id":"c1","col":"working","gen":2}]}`)
	}
	require.NoError(t, admitMemberLaunch(run, "m", p))
	for _, tc := range []struct {
		name string
		code int
		body string
	}{
		{"stopped", 0, `{"epoch":7,"machine":"STOPPED","cards":[{"id":"c1","col":"working","gen":2}]}`},
		{"disconnected", 2, "server unavailable"},
		{"old-protocol", 0, `{"epoch":7,"cards":[{"id":"c1","col":"working","gen":2}]}`},
		{"moved", 0, `{"epoch":7,"machine":"RUNNING","cards":[{"id":"c1","col":"working","gen":3}]}`},
		{"missing", 0, `{"epoch":7,"machine":"RUNNING","cards":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := admitMemberLaunch(func(_ ...string) (int, []byte) { return tc.code, []byte(tc.body) }, "m", p)
			assert.Error(t, err)
		})
	}
	read := member.Packet{Card: "r1", Kind: "read", Gen: 1, Attempt: 2, Epoch: 7}
	q := fmt.Sprintf(`{"epoch":7,"machine":"RUNNING","cards":[{"id":%q,"col":"reading","gen":0,"attempt":2}]}`, read.Card)
	require.NoError(t, admitMemberLaunch(func(_ ...string) (int, []byte) { return 0, []byte(q) }, "r", read))
}
