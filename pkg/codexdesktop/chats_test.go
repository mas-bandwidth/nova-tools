package codexdesktop

import (
	"fmt"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// header is a rollout's first line in the shape the desktop writes it
// (ChatGPT.app 26.1007.30912, codex-cli 0.162.0-alpha.17.2): session_meta with
// id, timestamp, cwd, originator, source and thread_source, and a base
// instructions body the reader skips.
func header(id, at, cwd, originator, source, threadSource string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"session_id":%q,"id":%q,"timestamp":%q,"cwd":%q,"originator":%q,"cli_version":"0.162.0-alpha.17.2","source":%s,"thread_source":%q,"model_provider":"openai","base_instructions":{"text":"synthetic"}}}`+"\n"+`{"type":"turn_context","payload":{}}`+"\n",
		at, id, id, at, cwd, originator, source, threadSource)
}

func codexHome(files map[string]string) fstest.MapFS {
	home := fstest.MapFS{}
	for name, body := range files {
		home[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return home
}

const (
	oldChat = "01a13c3f-b525-7a90-bfd1-1612fed9679c"
	newChat = "01a13ce2-ce03-7f52-a04d-abc7077675c8"
	subChat = "01a13c60-977c-76d0-a7a0-b33ef86d7403"
)

func TestDesktopChatsKeepsUserChatsTheDesktopStarted(t *testing.T) {
	t.Parallel()
	home := codexHome(map[string]string{
		"sessions/2026/10/14/rollout-a-" + oldChat + ".jsonl": header(oldChat, "2026-10-14T20:39:18.000Z", "/Users/u/Documents/ChatGPT/stella 2", "Codex Desktop", `"vscode"`, "user"),
		"sessions/2026/10/14/rollout-b-" + subChat + ".jsonl": header(subChat, "2026-10-14T23:01:01.000Z", "/Users/u/Documents/ChatGPT/stella 2", "Codex Desktop", `{"subagent":{"thread_spawn":{"parent_thread_id":"`+oldChat+`","depth":1}}}`, "subagent"),
		"sessions/2026/10/14/rollout-c-exec.jsonl":            header("01a13c00-0000-7000-8000-000000000001", "2026-10-14T23:30:00.000Z", "/tmp/bakeoff", "codex_exec", `"exec"`, "user"),
		"sessions/2026/10/14/notes.txt":                       "not a rollout",
		"sessions/2026/10/14/rollout-empty.jsonl":             "",
	})
	chats, err := DesktopChats(home)
	require.NoError(t, err)
	require.Len(t, chats, 1)
	assert.Equal(t, oldChat, chats[0].ID)
	assert.Equal(t, "/Users/u/Documents/ChatGPT/stella 2", chats[0].Cwd)
}

func TestCurrentChatFollowsTheFriendToANewChat(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"sessions/2026/10/14/rollout-a-" + oldChat + ".jsonl": header(oldChat, "2026-10-14T20:39:18.000Z", "/Users/u/Documents/ChatGPT/stella 2", "Codex Desktop", `"vscode"`, "user"),
		"sessions/2026/10/14/rollout-x-other.jsonl":           header("01a13cf0-0000-7000-8000-000000000002", "2026-10-15T01:00:00.000Z", "/Users/u/Documents/ChatGPT/emma", "Codex Desktop", `"vscode"`, "user"),
	}
	chats, err := DesktopChats(codexHome(files))
	require.NoError(t, err)
	chat, err := CurrentChat(chats, "stella")
	require.NoError(t, err)
	assert.Equal(t, oldChat, chat.ID, "before the move, the one chat")

	// Stella moves to a new chat in the same workspace.
	files["sessions/2026/10/15/rollout-b-"+newChat+".jsonl"] = header(newChat, "2026-10-15T00:07:39.269Z", "/Users/u/Documents/ChatGPT/stella 2", "Codex Desktop", `"vscode"`, "user")
	chats, err = DesktopChats(codexHome(files))
	require.NoError(t, err)
	chat, err = CurrentChat(chats, "Stella")
	require.NoError(t, err)
	assert.Equal(t, newChat, chat.ID, "the newest chat in her workspace, never another friend's newer one")
}

func TestCurrentChatRefusesWithoutAWorkspace(t *testing.T) {
	t.Parallel()
	_, err := CurrentChat([]Chat{{ID: oldChat, Cwd: "/Users/u/Documents/ChatGPT/stellar"}}, "stella")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--codex-thread")
}

func TestFriendWorkspace(t *testing.T) {
	t.Parallel()
	for cwd, want := range map[string]bool{
		"/Users/u/Documents/ChatGPT/stella":    true,
		"/Users/u/Documents/ChatGPT/stella 2":  true,
		"/Users/u/Documents/ChatGPT/Stella 12": true,
		"/Users/u/Documents/ChatGPT/stella/":   true,
		"/Users/u/Documents/ChatGPT/stellar":   false,
		"/Users/u/Documents/ChatGPT/stella x":  false,
		"/Users/u/stella 2/work":               false,
	} {
		assert.Equal(t, want, FriendWorkspace(cwd, "stella"), cwd)
	}
}
