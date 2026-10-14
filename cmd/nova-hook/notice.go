package main

import (
	"errors"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// currentChallengeNonce is the one guard for a native notice
// (docs/SPEC-CODEX-DESKTOP-WAKE.md). Codex calls it. The body is the
// canonical challenge, the hook-record sum matches the nonce, and the epoch
// matches. A stale nonce, an altered body, and a wrong epoch return an empty
// nonce. A store error is returned. This does not prove the challenge and
// does not write a proof.
func currentChallengeNonce(s *session, epoch int64, m bus.Message) (string, error) {
	if s == nil || !canonicalHookChallenge(m, s.as) {
		return "", nil
	}
	if s.hooks.C == nil {
		return "", errors.New("hook challenge record could not be read")
	}
	ctx, cancel := call()
	challenge, err := s.hooks.C.HMGet(ctx, hook.ChallengeKey(s.as), "sum", "epoch").Result()
	cancel()
	if err != nil {
		return "", err
	}
	nonce := strings.TrimPrefix(m.Subject, "HOOK CHALLENGE nonce=")
	if len(challenge) < 2 || challenge[0] != hook.Sum(nonce) || challenge[1] != strconv.FormatInt(epoch, 10) {
		return "", nil
	}
	return nonce, nil
}

// holdOneCodexNotice rewrites the one pending native notice in place
// (docs/SPEC-CODEX-DESKTOP-WAKE.md). A new native push waits until the model
// has consumed that notice. The saved file keeps the newest valid challenge
// and a count of the messages. A stale nonce, an altered body, and a wrong
// epoch do not replace the saved nonce. This does not prove the challenge
// and does not write a proof.
func (w world) holdOneCodexNotice(c *tool.Call, s *session, r *codexRoute, role hook.Role, epoch int64, id string, m *bus.Message) *tool.Out {
	if m != nil {
		nonce, err := currentChallengeNonce(s, epoch, *m)
		if err != nil {
			return codexDeliveryFailure(s, role, epoch, id, err)
		}
		r.fold(*m)
		if nonce != "" {
			r.noticeNonce = nonce
			cp := *m
			r.foldedChallenge = &cp
		}
	}
	e := r.notice
	e.EventID = r.pendingID
	e.HookEpoch = epoch
	e.HookRole = role
	e.Summary = r.summary()
	e.NoticeCount = r.foldedCount
	e.NoticeNonce = r.noticeNonce
	if r.foldedChallenge != nil {
		cp := *r.foldedChallenge
		e.Message = &cp
		e.Recovery = []bus.Message{cp}
	}
	saved, err := w.persistCodexEvent(s, r, r.pendingID, e, true)
	if err != nil {
		return codexDeliveryFailure(s, role, epoch, id, err)
	}
	r.notice = saved
	line(c, tool.Done().As("PUSH").Fact("push", "codex").Fact("id", id).Fact("native", "coalesced").Fact("pending", r.foldedCount).Fact("nonce", r.noticeNonce).Fact("summary", tool.Text(r.summary())).Note("one pending notice updated in place; no native push until the model consumes it"))
	return nil
}
