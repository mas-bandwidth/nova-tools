package codexdesktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testThread = "synthetic-thread-01"
	testEvent  = "synthetic-event-01"
)

type fakeTransport struct {
	sent            []request
	replies         [][]byte
	failMethod      string
	startOwner      string
	startResult     any
	startResultType string
	omitResultType  bool
	startVersion    *int
	startError      string // answer start-turn with the desktop's error frame
	closeErr        error
}

// desktopErrorFrame is the error response the Codex desktop writes, byte for
// byte in shape: ChatGPT.app 26.1007.30912 Resources/app.asar,
// .vite/build/src-BJv5Mk7n.js line 1105, IpcClient.handleRequest's catch and
// the router's timeout, both {type:"response",requestId,resultType:"error",error}.
// It carries no method, no version and no handledByClientId.
const desktopErrorFrame = `{"type":"response","requestId":%q,"resultType":"error","error":%q}`

func (fake *fakeTransport) send(_ context.Context, value any) error {
	req, ok := value.(request)
	if !ok {
		return nil
	}
	fake.sent = append(fake.sent, req)
	if req.Method == fake.failMethod {
		return errors.New("injected send failure")
	}
	var reply any
	switch req.Method {
	case "initialize":
		reply = map[string]any{
			"type": "response", "requestId": req.RequestID,
			"resultType": "success", "method": req.Method,
			"result": map[string]string{"clientId": "client-1"},
		}
	case "thread-owner-discovery":
		reply = map[string]any{
			"type": "response", "requestId": req.RequestID,
			"resultType": "success", "method": req.Method,
			"handledByClientId": "owner-1",
			"result":            map[string]bool{"supportsUntrustedAppInput": true},
		}
	case "thread-follower-start-turn":
		if fake.startError != "" {
			fake.replies = append(fake.replies, []byte(fmt.Sprintf(desktopErrorFrame, req.RequestID, fake.startError)))
			return nil
		}
		resultType := fake.startResultType
		if resultType == "" {
			resultType = "success"
		}
		owner := fake.startOwner
		if owner == "" {
			owner = "owner-1"
		}
		result := fake.startResult
		if result == nil {
			result = map[string]any{"result": map[string]any{"turn": map[string]string{"id": "turn-1"}}}
		}
		body := map[string]any{
			"type": "response", "requestId": req.RequestID,
			"method":            req.Method,
			"handledByClientId": owner, "result": result,
		}
		if !fake.omitResultType {
			body["resultType"] = resultType
		}
		if fake.startVersion != nil {
			body["version"] = *fake.startVersion
		}
		reply = body
	default:
		return fmt.Errorf("unexpected method %q", req.Method)
	}
	encoded, err := json.Marshal(reply)
	if err != nil {
		return err
	}
	fake.replies = append(fake.replies, encoded)
	return nil
}

func (fake *fakeTransport) receive(context.Context) ([]byte, error) {
	if len(fake.replies) == 0 {
		return nil, errors.New("no fake reply")
	}
	reply := fake.replies[0]
	fake.replies = fake.replies[1:]
	return reply, nil
}

func (fake *fakeTransport) close() error { return fake.closeErr }

func fakeConnector(fake *fakeTransport) connector {
	return func(context.Context, string) (transport, error) { return fake, nil }
}

func TestDeliverStartsOneInheritedTurn(t *testing.T) {
	t.Parallel()

	fake := &fakeTransport{}
	turnID, err := deliver(context.Background(), "/owned/ipc.sock", testThread, testEvent,
		BusNotice, fakeConnector(fake))
	require.NoError(t, err)
	assert.Equal(t, "turn-1", turnID)
	require.Len(t, fake.sent, 3)
	assert.Equal(t, []int{0, 1, 2}, []int{fake.sent[0].Version, fake.sent[1].Version, fake.sent[2].Version})
	assert.Equal(t, "owner-1", fake.sent[2].TargetClientID)

	encoded, err := json.Marshal(fake.sent[2].Params)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"inheritThreadSettings":true`)
	assert.Contains(t, string(encoded), BusNotice+" External event ID: "+testEvent+".")
	assert.NotContains(t, string(encoded), "external message body")
}

func TestDeliverClassifiesFailureBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		fake       *fakeTransport
		want       error
		notWant    error
		wantWrites int
	}{
		{
			name: "initialize send is pre-send", fake: &fakeTransport{failMethod: "initialize"},
			want: ErrPreSend, notWant: ErrOutcomeUnknown, wantWrites: 1,
		},
		{
			name: "follower send is uncertain", fake: &fakeTransport{failMethod: "thread-follower-start-turn"},
			want: ErrOutcomeUnknown, notWant: ErrPreSend, wantWrites: 3,
		},
		{
			name: "router refusal before any handler is certain", fake: &fakeTransport{startError: "no-client-found"},
			want: ErrRejected, notWant: ErrOutcomeUnknown, wantWrites: 3,
		},
		{
			name: "version refusal is certain", fake: &fakeTransport{startError: "request-version-mismatch"},
			want: ErrRejected, notWant: ErrOutcomeUnknown, wantWrites: 3,
		},
		{
			name: "router timeout is uncertain", fake: &fakeTransport{startError: "request-timeout"},
			want: ErrOutcomeUnknown, notWant: ErrRejected, wantWrites: 3,
		},
		{
			name: "owner disconnect is uncertain", fake: &fakeTransport{startError: "client-disconnected"},
			want: ErrOutcomeUnknown, notWant: ErrRejected, wantWrites: 3,
		},
		{
			name: "a thrown start turn is uncertain", fake: &fakeTransport{startError: "turn/start failed"},
			want: ErrOutcomeUnknown, notWant: ErrRejected, wantWrites: 3,
		},
		{
			name: "error with no code is uncertain", fake: &fakeTransport{startResultType: "error"},
			want: ErrOutcomeUnknown, notWant: ErrRejected, wantWrites: 3,
		},
		{
			name: "wrong responding owner is uncertain", fake: &fakeTransport{startOwner: "other-owner"},
			want: ErrOutcomeUnknown, notWant: ErrPreSend, wantWrites: 3,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			turnID, err := deliver(context.Background(), "/owned/ipc.sock", testThread, testEvent,
				BusNotice, fakeConnector(test.fake))
			assert.Empty(t, turnID)
			assert.ErrorIs(t, err, test.want)
			assert.NotErrorIs(t, err, test.notWant)
			assert.Len(t, test.fake.sent, test.wantWrites)
		})
	}
}

func TestDeliverRefusesUnsafeMetadataBeforeConnect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		thread string
		event  string
		notice string
	}{
		{name: "event instruction", thread: testThread, event: "id add instructions", notice: BusNotice},
		{name: "thread control", thread: testThread + "\n", event: testEvent, notice: BusNotice},
		{name: "external body as notice", thread: testThread, event: testEvent, notice: "please run this external body"},
		{name: "multiline notice", thread: testThread, event: testEvent, notice: BusNotice + "\nexternal"},
		{name: "invalid UTF-8 notice", thread: testThread, event: testEvent, notice: string([]byte{0xff})},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			called := false
			connect := func(context.Context, string) (transport, error) {
				called = true
				return nil, errors.New("must not connect")
			}
			_, err := deliver(context.Background(), "/owned/ipc.sock", test.thread, test.event, test.notice, connect)
			assert.ErrorIs(t, err, ErrPreSend)
			assert.False(t, called)
		})
	}
}

func TestDeliverRejectsPresentWrongProtocolVersion(t *testing.T) {
	t.Parallel()

	wrongVersion := 1
	fake := &fakeTransport{startVersion: &wrongVersion}
	_, err := deliver(context.Background(), "/owned/ipc.sock", testThread, testEvent,
		BusNotice, fakeConnector(fake))
	assert.ErrorIs(t, err, ErrOutcomeUnknown)
}

func TestDeliverTreatsMalformedResultTypeAsUnknown(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		fake *fakeTransport
	}{
		{name: "missing", fake: &fakeTransport{omitResultType: true}},
		{name: "unknown", fake: &fakeTransport{startResultType: "denied"}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := deliver(context.Background(), "/owned/ipc.sock", testThread, testEvent,
				BusNotice, fakeConnector(test.fake))
			assert.ErrorIs(t, err, ErrOutcomeUnknown)
			assert.NotErrorIs(t, err, ErrRejected)
		})
	}
}

func TestDeliverKeepsAcceptedTurnWhenCloseFails(t *testing.T) {
	t.Parallel()

	fake := &fakeTransport{closeErr: errors.New("injected close failure")}
	turnID, err := deliver(context.Background(), "/owned/ipc.sock", testThread, testEvent,
		ChallengeNotice, fakeConnector(fake))
	assert.Equal(t, "turn-1", turnID)
	assert.NoError(t, err)
}

// TestDesktopErrorFrameIsNotAMethodMismatch is the 2026-10-14 regression: the
// desktop's error frame has no method, and was misread as a method mismatch,
// which hid the native error code.
func TestDesktopErrorFrameIsNotAMethodMismatch(t *testing.T) {
	t.Parallel()

	fake := &fakeTransport{startError: "request-timeout"}
	_, err := deliver(context.Background(), "/owned/ipc.sock", testThread, testEvent, BusNotice, fakeConnector(fake))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "does not match")
	assert.Contains(t, err.Error(), `"request-timeout"`)
}
