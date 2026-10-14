package dshdesktop

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDoer is an in-process RoundTripper: it records the one request it saw and
// answers from a canned response, so no socket is opened (rule net).
type fakeDoer struct {
	status int
	body   string
	req    *http.Request
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.req = req
	return &http.Response{
		StatusCode: f.status,
		Body:       io.NopCloser(bytes.NewReader([]byte(f.body))),
		Header:     http.Header{},
	}, nil
}

func acceptedBody() string {
	return `{"type":"server-response","rpcId":"nova-dsh-desktop-wake","result":{"ok":true,"value":{"accepted":true}}}`
}

func promptEnvelope(t *testing.T, do *fakeDoer) map[string]any {
	t.Helper()
	require.NotNil(t, do.req)
	var env map[string]any
	require.NoError(t, json.Unmarshal(mustBody(t, do.req), &env))
	return env
}

func mustBody(t *testing.T, req *http.Request) []byte {
	t.Helper()
	b, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	return b
}

func TestChallengeIsDeliveredAsSteerIntoTheRunningTurn(t *testing.T) {
	t.Parallel()
	do := &fakeDoer{status: http.StatusOK, body: acceptedBody()}
	r := Route{Gateway: "http://127.0.0.1:19387", SessionID: "session-abc", Cookie: "dsh-auth-abc=xyz"}

	turn, err := deliver(context.Background(), r, "event-1", ChallengeNotice, do)
	require.NoError(t, err)
	assert.Equal(t, "session-abc", turn)

	assert.Equal(t, "/api/session/prompt", do.req.URL.Path)
	assert.Equal(t, "dsh-auth-abc=xyz", do.req.Header.Get("Cookie"))
	env := promptEnvelope(t, do)
	args := env["payload"].(map[string]any)["args"].(map[string]any)
	assert.Equal(t, "steer", args["mode"])
	content := args["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	assert.Contains(t, text, ChallengeNotice)
	assert.Contains(t, text, "event-1")
}

func TestBusMessageWakesAnIdleTurnAsFollowup(t *testing.T) {
	t.Parallel()
	do := &fakeDoer{status: http.StatusOK, body: acceptedBody()}
	r := Route{Gateway: "http://127.0.0.1:19387", SessionID: "session-abc", Cookie: "c=v"}

	_, err := deliver(context.Background(), r, "event-2", BusNotice, do)
	require.NoError(t, err)

	args := promptEnvelope(t, do)["payload"].(map[string]any)["args"].(map[string]any)
	_, hasMode := args["mode"]
	assert.False(t, hasMode, "followup has no mode")
	content := args["content"].([]any)
	assert.Contains(t, content[0].(map[string]any)["text"].(string), BusNotice)
}

func TestDeliverRefusesExternalNoticeAndBadInputs(t *testing.T) {
	t.Parallel()
	do := &fakeDoer{status: http.StatusOK, body: acceptedBody()}
	base := Route{Gateway: "http://127.0.0.1:19387", SessionID: "session-abc", Cookie: "c=v"}

	_, err := deliver(context.Background(), base, "e-1", "untrusted text", do)
	assert.ErrorIs(t, err, ErrPreSend)

	bad := base
	bad.Gateway = "http://evil.example"
	_, err = deliver(context.Background(), bad, "e-1", BusNotice, do)
	assert.ErrorIs(t, err, ErrPreSend)
}

func TestDeliverReportsRejection(t *testing.T) {
	t.Parallel()
	do := &fakeDoer{status: http.StatusForbidden, body: "forbidden"}
	r := Route{Gateway: "http://127.0.0.1:19387", SessionID: "session-abc", Cookie: "c=v"}

	_, err := deliver(context.Background(), r, "e-1", BusNotice, do)
	assert.ErrorIs(t, err, ErrRejected)
}
