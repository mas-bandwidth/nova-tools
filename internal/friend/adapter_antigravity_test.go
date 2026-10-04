package friend

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	agPS = `  root 1 /sbin/launchd
emma 87100 /Applications/Antigravity.app/Contents/MacOS/Antigravity
emma 87357 /Applications/Antigravity.app/Contents/Resources/bin/language_server --standalone --override_ide_name antigravity --https_server_port 0 --csrf_token tok-1 --app_data_dir antigravity
`
	agLsof = "p87357\nf7\nn127.0.0.1:52569\nf8\nn127.0.0.1:52570\n"
	agRows = `[{"conversation_id":"root-new","workspace_uris":"[\"file:///w/emma\"]"},
{"conversation_id":"root-old","workspace_uris":"[\"file:///w/emma\"]"},
{"conversation_id":"other","workspace_uris":"[\"file:///w/ada\"]"}]`
	agMailbox = ".gemini/antigravity/brain/root-new/.system_generated/messages"
)

// agExec plays the friend's machine: ps, lsof, sqlite3, and agentapi through env, whose
// plaintext port is 52570; send-message drops the message into the mailbox
// and the session reads it one poll later.
type agExec struct {
	fsys     fstest.MapFS
	calls    [][]string
	sendOut  string // what send-message prints, when not the real reply
	rows     string
	waits    int
	maxWaits int
}

func (e *agExec) run(_ context.Context, dir, name string, args []string, _ string) (string, int, error) {
	e.calls = append(e.calls, append([]string{dir, name}, args...))
	switch name {
	case "ps":
		return agPS, 0, nil
	case "lsof":
		return agLsof, 0, nil
	case "sqlite3":
		return e.rows, 0, nil
	}
	if args[0] != "ANTIGRAVITY_LS_ADDRESS=localhost:52570" {
		return `{"response": {}, "error": "rpc error: code = Unavailable desc = connection error: desc = \"error reading server preface: EOF\""}`, 0, nil
	}
	switch args[3] {
	case "get-conversation-metadata":
		return `{"response": {"conversationMetadata": {}}}`, 0, nil
	case "send-message":
		if e.sendOut != "" {
			return e.sendOut, 0, nil
		}
		e.fsys[agMailbox+"/m-2.json"] = &fstest.MapFile{Data: []byte(`{"id":"m-2","content":"` + args[5] + `"}`)}
		return `{"response": {"sendMessage": {"recipientId": "root-new"}}}`, 0, nil
	}
	return "", 1, nil
}

func (e *agExec) wait(context.Context) bool {
	e.waits++
	if e.waits == 1 {
		e.fsys[agMailbox+"/read.json"] = &fstest.MapFile{Data: []byte(`{"m-1":true,"m-2":true}`)}
	}
	return e.waits <= e.maxWaits
}

func newAgExec() *agExec {
	return &agExec{rows: agRows, maxWaits: 3, fsys: fstest.MapFS{
		agMailbox + "/m-1.json":    &fstest.MapFile{Data: []byte(`{"id":"m-1"}`)},
		agMailbox + "/read.json":   &fstest.MapFile{Data: []byte(`{"m-1":true}`)},
		agMailbox + "/undelivered": &fstest.MapFile{Mode: 0o755 | fs.ModeDir},
	}}
}

func TestAntigravityDeliversIntoTheNewestRootConversationAndAcksOnceRead(t *testing.T) {
	t.Parallel()
	e := newAgExec()
	var record strings.Builder
	d, err := NewDeliverer("antigravity", "/w/emma", "", e.run, &record)
	require.NoError(t, err)
	a := d.(*Antigravity)
	a.User = "emma"
	a.Home, a.FS, a.Wait = "/home", e.fsys, e.wait
	exit, err := a.Deliver(context.Background(), "hello there")
	require.NoError(t, err)
	assert.Equal(t, 0, exit)
	require.Len(t, e.calls, 6)
	assert.Equal(t, []string{"/w/emma", "ps", "-axo", "user=,pid=,args="}, e.calls[0])
	assert.Equal(t, []string{"/w/emma", "lsof", "-nP", "-a", "-p", "87357", "-iTCP", "-sTCP:LISTEN", "-Fn"}, e.calls[1])
	assert.Equal(t, []string{"/w/emma", "sqlite3", "-json", "file:/home/.gemini/antigravity/conversation_summaries.db?mode=ro&immutable=1", antigravitySummaries}, e.calls[2])
	env := []string{"/w/emma", "/usr/bin/env", "ANTIGRAVITY_LS_ADDRESS=localhost:52569", "ANTIGRAVITY_CSRF_TOKEN=tok-1", "/home/.gemini/antigravity/bin/agentapi", "get-conversation-metadata", "root-new"}
	assert.Equal(t, env, e.calls[3], "the first port is tried")
	env[2] = "ANTIGRAVITY_LS_ADDRESS=localhost:52570"
	assert.Equal(t, env, e.calls[4], "then the next, which answers")
	assert.Equal(t, []string{"/w/emma", "/usr/bin/env", "ANTIGRAVITY_LS_ADDRESS=localhost:52570", "ANTIGRAVITY_CSRF_TOKEN=tok-1", "/home/.gemini/antigravity/bin/agentapi", "send-message", "--title=nova-friend", "root-new", "hello there"}, e.calls[5], "the text is an argument, never a shell line")
	assert.Equal(t, 1, e.waits, "read one poll after it landed")
	assert.Equal(t, "antigravity: message m-2 read by conversation root-new\n", record.String())

	e = newAgExec()
	a = &Antigravity{User: "emma", Dir: "/w/emma", Session: "named", Run: e.run, Home: "/home", FS: e.fsys, Wait: e.wait}
	e.fsys[".gemini/antigravity/brain/named/.system_generated/messages/read.json"] = &fstest.MapFile{Data: []byte(`{}`)}
	_, err = a.Deliver(context.Background(), "x")
	require.Error(t, err)
	for _, c := range e.calls {
		assert.NotEqual(t, "sqlite3", c[1], "a named session is not looked up")
	}
	assert.Equal(t, "named", e.calls[3][6])
}

func TestAntigravityRefusesWhatItCannotProve(t *testing.T) {
	t.Parallel()
	deliver := func(e *agExec) error {
		a := &Antigravity{User: "emma", Dir: "/w/emma", Run: e.run, Home: "/home", FS: e.fsys, Wait: e.wait}
		exit, err := a.Deliver(context.Background(), "x")
		assert.Equal(t, 1, exit)
		return err
	}
	t.Run("no conversation has the directory open", func(t *testing.T) {
		t.Parallel()
		e := newAgExec()
		e.rows = `[{"conversation_id":"other","workspace_uris":"[\"file:///w/ada\"]"}]`
		assert.EqualError(t, deliver(e), "no antigravity conversation has /w/emma open; open one there, or name one with --session")
		e.rows = ""
		assert.ErrorContains(t, deliver(e), "no antigravity conversation has /w/emma open")
	})
	t.Run("agentapi says no at exit 0", func(t *testing.T) {
		t.Parallel()
		e := newAgExec()
		e.sendOut = `{"response": {}, "error": "rpc error: code = Unknown desc = trajectory not found: root-new"}`
		assert.EqualError(t, deliver(e), "agentapi: rpc error: code = Unknown desc = trajectory not found: root-new")
		e.sendOut = "panic: boom"
		assert.EqualError(t, deliver(e), "agentapi printed no JSON: panic: boom")
	})
	t.Run("the message is not read before the wait ends", func(t *testing.T) {
		t.Parallel()
		e := newAgExec()
		e.maxWaits = 0
		assert.EqualError(t, deliver(e), "message m-2 is in the mailbox of conversation root-new but was not read within 2m0s; the session takes it at its next turn")
	})
	t.Run("no message appears", func(t *testing.T) {
		t.Parallel()
		e := newAgExec()
		e.sendOut = `{"response": {"sendMessage": {}}}`
		e.maxWaits = 0
		assert.EqualError(t, deliver(e), "agentapi accepted the message for conversation root-new but none appeared in its mailbox within 2m0s")
	})
	t.Run("no mailbox", func(t *testing.T) {
		t.Parallel()
		e := newAgExec()
		delete(e.fsys, agMailbox+"/m-1.json")
		delete(e.fsys, agMailbox+"/read.json")
		delete(e.fsys, agMailbox+"/undelivered")
		assert.ErrorContains(t, deliver(e), "conversation root-new has no mailbox")
	})
}

func TestAntigravityReadsTheServerTheListingAndTheReplies(t *testing.T) {
	t.Parallel()
	pid, token, err := LanguageServer(agPS, "emma")
	require.NoError(t, err)
	assert.Equal(t, []string{"87357", "tok-1"}, []string{pid, token})
	_, _, err = LanguageServer("root 1 /sbin/launchd\nemma 2 /x/language_server --override_ide_name windsurf --csrf_token t\n", "emma")
	assert.EqualError(t, err, "no antigravity language server is running: is Antigravity open?")
	_, _, err = LanguageServer("emma 9 /x/language_server --override_ide_name antigravity\n", "emma")
	assert.EqualError(t, err, "the antigravity language server (pid 9) runs without a --csrf_token")

	assert.Equal(t, []string{"52569", "52570"}, ListenPorts(agLsof))
	assert.Empty(t, ListenPorts("p1\n"))

	reply, err := AgentAPI(`{"response": {"sendMessage": {"recipientId": "r"}}}`)
	require.NoError(t, err)
	assert.JSONEq(t, `{"sendMessage": {"recipientId": "r"}}`, string(reply))
	_, err = AgentAPI(`{"response": {}, "error": "rpc error: code = Unauthenticated desc = missing CSRF token"}`)
	assert.EqualError(t, err, "agentapi: rpc error: code = Unauthenticated desc = missing CSRF token")

	id, err := NewestConversation(agRows, "/w/emma")
	require.NoError(t, err)
	assert.Equal(t, "root-new", id, "the first row is the newest")
	id, err = NewestConversation(agRows, "/Volumes/emma", "/w/ada")
	require.NoError(t, err)
	assert.Equal(t, "other", id, "a directory's real path counts too")
	_, err = NewestConversation("nope", "/w/emma")
	assert.ErrorContains(t, err, "not a JSON list")

	assert.Equal(t, "m-2", NewMessage([]string{"m-1"}, []string{"m-1", "m-2"}))
	assert.Equal(t, "", NewMessage([]string{"m-1"}, []string{"m-1"}))
	assert.True(t, Read([]byte(`{"m-2":true}`), "m-2"))
	assert.False(t, Read([]byte(`{"m-2":false}`), "m-2"))
	assert.False(t, Read(nil, "m-2"))
}

func TestAntigravitySelectsOnlyTheDaemonsUser(t *testing.T) {
	t.Parallel()
	ps := "other 123 /x/language_server --override_ide_name antigravity --csrf_token foreign\n" + agPS
	pid, token, err := LanguageServer(ps, "emma")
	require.NoError(t, err)
	assert.Equal(t, "87357", pid)
	assert.Equal(t, "tok-1", token)
	_, _, err = LanguageServer(ps, "absent")
	assert.ErrorContains(t, err, "no antigravity language server")
}
