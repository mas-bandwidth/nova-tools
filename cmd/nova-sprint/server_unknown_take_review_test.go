package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exhausting every HTTP retry can still leave a committed take. The next
// member pass (including a restarted member) must recover those claims before
// requesting more lanes with a new operation identity.
func TestServerReviewMemberRecoversATakeWhenEveryReplyWasLost(t *testing.T) {
	t.Parallel()
	for _, restart := range []bool{false, true} {
		name := "next pass"
		if restart {
			name = "restarted member"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newServerRig(t, twoLanes()...)
			runner := &twinRunner{children: map[string]*twinChild{}}
			var log bytes.Buffer
			lost := 0
			send := func(ctx context.Context, verbs ...[]string) ([]sprintwire.Result, error) {
				res, err := r.send(ctx, verbs...)
				if len(verbs) == 1 && verbs[0][0] == "take" && lost < sprintwire.Tries {
					lost++
					return nil, errors.New("reply lost after server execution")
				}
				return res, err
			}
			newMember := func() *member.Member {
				return member.New(member.Config{As: "m1"}, &sprintwire.Worker{Send: send}, runner, nil, &log)
			}
			m := newMember()
			_, err := m.Tick(time.Unix(0, 0))
			require.Error(t, err, "all take replies were lost")
			assert.Equal(t, sprintwire.Tries, lost)
			assert.Empty(t, runner.packets, "an unanswered take starts no child")
			require.Len(t, r.queue("m1")["working"], 2, "the first request still committed")
			if restart {
				m = newMember()
			}
			_, err = m.Tick(time.Unix(1, 0))
			require.NoError(t, err, log.String())
			require.Len(t, runner.packets, 2, "recover the two existing claims")
			assert.NotEqual(t, runner.packets[0].Card, runner.packets[1].Card)
			assert.Equal(t, 2, m.Live())
			q := r.queue("m1")
			assert.Len(t, q["working"], 2)
			assert.Len(t, q["ready"], 2, "no new take on the recovery pass")
			takes := 0
			for _, v := range r.served {
				if v[0] == "take" {
					takes++
				}
			}
			assert.Equal(t, sprintwire.Tries, takes, "recovery must precede requesting new lanes")
		})
	}
}
