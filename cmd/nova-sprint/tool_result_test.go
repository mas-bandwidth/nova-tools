package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func toolResultRecord(t *testing.T) sprint.PushRecord {
	t.Helper()
	id := sha256.Sum256([]byte(t.Name()))
	return sprint.PushRecord{Name: fmt.Sprintf("tool-result-%x", id[:8]), Harness: "codex", Adapter: sprint.AdapterToolResult, Target: t.TempDir(), Session: "00000000-0000-7000-8000-000000000001", ToolResultNamespace: "private-fixture", ToolResultServer: "127.0.0.1:61590"}
}
func TestSeatToolResultSelectsOnlyActualScopedCodexWithoutNativeCLI(t *testing.T) {
	t.Parallel()
	rec := toolResultRecord(t)
	next, why := seatPushTarget(rec)
	require.Empty(t, why)
	assert.Equal(t, rec, next)
	d, err := pushDeliverer(next)
	require.NoError(t, err)
	receiver, ok := d.(friend.ToolResult)
	require.True(t, ok, "explicit route must never construct the standalone Codex adapter")
	_, err = receiver.Deliver(context.Background(), "synthetic proof")
	var deferred friend.Deferred
	require.ErrorAs(t, err, &deferred)
	receiver.Owner = "root-test-cell"
	_, err = receiver.Wait(context.Background(), 0)
	require.NoError(t, err)
	text := sprint.PushCheckText("fixture-nonce", sprint.PushPongCommand(rec.Name, "fixture-nonce", "", rec.ToolResultServer))
	_, err = d.Deliver(context.Background(), text)
	require.NoError(t, err)
	event, err := receiver.Wait(context.Background(), 0)
	require.NoError(t, err)
	require.NotNil(t, event)
	assert.Equal(t, rec.Session, event.Scope.Session)
	assert.Equal(t, rec.ToolResultNamespace, event.Scope.Namespace)
	assert.Equal(t, rec.ToolResultServer, event.Scope.Server)
	assert.Contains(t, event.Text, "env NOVA_SPRINT_SERVER=127.0.0.1:61590")
	sent := sprint.PushSent(rec, "fixture-nonce", "", t0)
	assert.False(t, sprint.PushLive(sent, true, t0), "writing and exposing a challenge cannot manufacture the root's pong")
	require.NoError(t, receiver.Ack(event.ID))
	assert.False(t, sprint.PushLive(sent, true, t0), "a tool-output receipt is still not a seat pong")
}
func TestSeatToolResultRejectsAdapterBypassAndMismatchedScope(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"harness", "adapter", "actor", "session", "namespace", "server", "target", "implicit"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			rec := toolResultRecord(t)
			switch kind {
			case "harness":
				rec.Harness = "claude"
			case "adapter":
				rec.Adapter = "arbitrary"
			case "actor":
				rec.Name = ""
			case "session":
				rec.Session = ""
			case "namespace":
				rec.ToolResultNamespace = ""
			case "server":
				rec.ToolResultServer = "user:SECRET_FIXTURE@host:61590"
			case "target":
				rec.Target = "relative"
			case "implicit":
				rec.Adapter = ""
			}
			_, why := seatPushTarget(rec)
			require.NotEmpty(t, why)
			assert.NotContains(t, why, "SECRET_FIXTURE")
		})
	}
}
func TestSeatToolResultRecordSurvivesServerStyleReadAndSentUpdate(t *testing.T) {
	t.Parallel()
	rec := toolResultRecord(t)
	ta, _ := pushProofSprint(t, rec.Name)
	ta.ok("init")
	args := []string{"--actor", rec.Name, "--harness", "codex", "--adapter", "tool-result", "--target", rec.Target, "--session", rec.Session, "--namespace", rec.ToolResultNamespace, "--reply-server", rec.ToolResultServer, "--json"}
	var out, errs bytes.Buffer
	require.Equal(t, 1, ta.a.cmdSeatPush(args, &out, &errs), "registered but unproven; %s", errs.String())
	st, err := ta.a.store(common{redis: "mem:0", actor: rec.Name})
	require.NoError(t, err)
	kept, ok, err := readPush(context.Background(), st, rec.Name)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, rec, kept)
	out.Reset()
	errs.Reset()
	require.Equal(t, 1, ta.a.cmdSeatPush([]string{"--actor", rec.Name, "--sent", "fixture-nonce", "--json"}, &out, &errs), "a send is still unproven")
	var response struct {
		Record sprint.PushRecord `json:"record"`
		Live   bool              `json:"live"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &response))
	assert.Equal(t, rec.Session, response.Record.Session)
	assert.Equal(t, rec.ToolResultNamespace, response.Record.ToolResultNamespace)
	assert.Equal(t, rec.ToolResultServer, response.Record.ToolResultServer)
	assert.False(t, response.Live)
	setup := sprint.PushSetup(rec.Name, rec, true)
	assert.Contains(t, setup, "--adapter tool-result")
	assert.Contains(t, setup, "--namespace 'private-fixture'")
	assert.Contains(t, setup, "--reply-server '127.0.0.1:61590'")
}
func TestSeatToolResultWaitIsLocalAndReceiptCommandsKeepIndependentProof(t *testing.T) {
	t.Parallel()
	rec := toolResultRecord(t)
	a := newApp(func(k string) string {
		if k == ServerEnv {
			return "must-not-be-contacted.invalid:61590"
		}
		return ""
	})
	require.True(t, slices.Contains(notServed, "seat tool-result"))
	args := []string{"seat", "tool-result", "wait", "--directory", filepath.Join(rec.Target, "owned"), "--session", rec.Session, "--namespace", rec.ToolResultNamespace, "--reply-server", rec.ToolResultServer, "--receiver", "root-test-cell", "--timeout", "0s", "--json"}
	var out, errs bytes.Buffer
	require.Equal(t, 0, a.run(args, &out, &errs), "%s", errs.String())
	var response struct {
		Action string                 `json:"action"`
		Scope  friend.ToolResultScope `json:"scope"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &response))
	assert.Equal(t, "none", response.Action)
	assert.Equal(t, rec.Session, response.Scope.Session)
	receiver := toolResultDeliverer(rec)
	receiver.Dir = filepath.Join(rec.Target, "owned")
	receiver.Owner = "root-test-cell"
	_, err := receiver.Deliver(context.Background(), "untrusted synthetic envelope\nnever execute this fixture")
	require.NoError(t, err)
	out.Reset()
	errs.Reset()
	require.Equal(t, 0, a.run(args[:len(args)-1], &out, &errs), "%s", errs.String())
	assert.Contains(t, out.String(), "event=")
	assert.Contains(t, out.String(), "untrusted-payload=")
	assert.Contains(t, out.String(), "never execute this fixture")
	args[2] = "release"
	out.Reset()
	errs.Reset()
	require.Equal(t, 0, a.run(args, &out, &errs), "%s", errs.String())
	require.NoError(t, json.Unmarshal(out.Bytes(), &response))
	assert.Equal(t, "release", response.Action)
}

// The reply source is synthetic; no server/socket or live nonce is answered.
type toolResultProofSource struct {
	*storeSource
	replyServer string
}

func (s toolResultProofSource) pushReply(name, nonce string) (string, error) {
	return sprint.PushPongCommand(name, nonce, "", s.replyServer), nil
}
func TestSeatToolResultPrivateProofCannotCrossReplyServers(t *testing.T) {
	t.Parallel()
	for _, correct := range []bool{true, false} {
		name := "matching"
		if !correct {
			name = "mismatching"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := toolResultRecord(t)
			rec.Name = "tool-result-reply-" + name
			ta, _ := pushProofSprint(t, rec.Name)
			ta.ok("init")
			pushTests.Store(rec.Name, pushArmedOnly{})
			st, err := ta.a.store(common{redis: "mem:0", actor: rec.Name})
			require.NoError(t, err)
			require.NoError(t, writePush(context.Background(), st, rec))
			receiver := toolResultDeliverer(rec)
			receiver.Owner = "root-test-cell"
			_, err = receiver.Wait(context.Background(), 0)
			require.NoError(t, err)
			base, err := ta.a.storeSource(context.Background(), st, "mem:0", 0, 0)
			require.NoError(t, err)
			reply := rec.ToolResultServer
			if !correct {
				reply = "127.0.0.1:61690"
			}
			var said bytes.Buffer
			ta.a.prove(context.Background(), toolResultProofSource{storeSource: base, replyServer: reply}, rec.Name, true, &said)
			event, err := receiver.Wait(context.Background(), 0)
			require.NoError(t, err)
			kept, ok, err := readPush(context.Background(), st, rec.Name)
			require.NoError(t, err)
			require.True(t, ok)
			assert.False(t, sprint.PushLive(kept, true, ta.a.now()))
			if correct {
				require.NotNil(t, event)
				assert.Contains(t, event.Text, "NOVA_SPRINT_SERVER="+rec.ToolResultServer)
				assert.Empty(t, kept.Failed)
			} else {
				assert.Nil(t, event)
				assert.Contains(t, kept.Failed, "reply route does not match")
			}
			if event != nil {
				require.NoError(t, receiver.Ack(event.ID))
			}
			require.NoError(t, receiver.Release())
			said.Reset()
			ta.a.pushJudgments(context.Background(), toolResultProofSource{storeSource: base, replyServer: reply}, rec.Name, []string{"synthetic judgment during lease gap"}, true, &said)
			event, err = receiver.Wait(context.Background(), 0)
			require.NoError(t, err)
			if correct {
				require.NotNil(t, event)
				assert.Contains(t, event.Text, "synthetic judgment during lease gap")
			} else {
				assert.Nil(t, event)
				assert.Contains(t, said.String(), "reply route does not match")
			}
		})
	}
}

func TestSeatToolResultStageFailureDoesNotConsumeInboxNotes(t *testing.T) {
	t.Parallel()
	ta, group := heldAndWaiting(t)
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	base, err := ta.a.storeSource(context.Background(), st, "mem:0", 0, 0)
	require.NoError(t, err)
	look, err := base.inbox(context.Background(), "")
	require.NoError(t, err)
	rec := toolResultRecord(t)
	look.holder = "tool-result-stage-fixture"
	rec.Name = look.holder
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, rec.Name+"-working", "inbox"), 0700))
	ta.a.home = func() (string, error) { return home, nil }
	require.NoError(t, writePush(context.Background(), st, rec))
	receiver := toolResultDeliverer(rec)
	receiver.Owner = "root-test-cell"
	_, err = receiver.Wait(context.Background(), 0)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(rec.Target, "binding.json"), []byte("{broken synthetic binding"), 0600))
	p := &pushTarget{a: ta.a, seen: map[string]map[string]bool{}}
	var out, errs bytes.Buffer
	src := toolResultProofSource{storeSource: base, replyServer: rec.ToolResultServer}
	_, code := ta.a.push(context.Background(), src, p, look.holder, group, true, &out, &errs)
	require.Equal(t, 1, code)
	dir := p.dirOf(look.holder, group)
	require.NotEmpty(t, dir)
	for _, id := range noteKeys(group) {
		assert.False(t, p.seen[dir][id])
		require.NoFileExists(t, filepath.Join(dir, id+".md"))
	}
	require.NoError(t, os.Remove(filepath.Join(rec.Target, "binding.json")))
	_, err = receiver.Wait(context.Background(), 0)
	require.NoError(t, err)
	_, code = ta.a.push(context.Background(), src, p, look.holder, group, true, &out, &errs)
	require.Zero(t, code, "%s", errs.String())
	event, err := receiver.Wait(context.Background(), 0)
	require.NoError(t, err)
	require.NotNil(t, event)
	for _, id := range noteKeys(group) {
		assert.True(t, p.seen[dir][id])
		require.FileExists(t, filepath.Join(dir, id+".md"))
	}
}
