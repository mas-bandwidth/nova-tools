//go:build functional

package friend

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAntigravityOnTheLiveHarness needs Antigravity open with a conversation
// on NOVA_FRIEND_ANTIGRAVITY_DIR: it finds the language server, the newest
// root conversation of that directory and the port that answers, through
// the real commands. When NOVA_FRIEND_ANTIGRAVITY_DIR is set, it runs the
// ten-run acceptance: nova-friend ping --as rowan --to emma, then
// nova-friend wait-pong --from emma --nonce <n> --timeout 60s, accepting only
// the session pong --nonce (a daemon-pong alone does not count), and names
// the harness transcript file and line showing that turn in the same session id
// during that minute.
func TestAntigravityOnTheLiveHarness(t *testing.T) {
	t.Parallel()
	dir := os.Getenv("NOVA_FRIEND_ANTIGRAVITY_DIR")
	if dir == "" {
		t.Skip("NOVA_FRIEND_ANTIGRAVITY_DIR names no friend's directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	a := &Antigravity{Dir: dir, Run: RealExec}
	ps, _, err := RealExec(ctx, dir, "ps", []string{"-axo", "user=,pid=,args="}, "")
	require.NoError(t, err)
	current, err := user.Current()
	require.NoError(t, err)
	pid, token, err := LanguageServer(ps, current.Username)
	require.NoError(t, err)
	listing, _, err := RealExec(ctx, dir, "lsof", []string{"-nP", "-a", "-p", pid, "-iTCP", "-sTCP:LISTEN", "-Fn"}, "")
	require.NoError(t, err)
	ports := ListenPorts(listing)
	require.NotEmpty(t, ports)
	rows, _, err := RealExec(ctx, dir, "sqlite3", []string{"-json", "file:" + a.home() + "/" + AntigravityData + "/conversation_summaries.db?mode=ro&immutable=1", antigravitySummaries}, "")
	require.NoError(t, err)
	session, err := NewestConversation(rows, dir)
	require.NoError(t, err)
	answered := ""
	for _, p := range ports {
		if _, err := a.agentapi(ctx, p, token, "get-conversation-metadata", session); err == nil {
			answered = p
			break
		}
	}
	require.NotEmpty(t, answered, "no port answered for %s", session)
	t.Logf("language server pid %s, port %s, conversation %s of %s", pid, answered, session, dir)

	transcriptFile := filepath.Join(a.home(), AntigravityData, "brain", session, ".system_generated", "logs", "transcript.jsonl")
	require.FileExists(t, transcriptFile)

	friend := "emma"
	if f := os.Getenv("NOVA_FRIEND_NAME"); f != "" {
		friend = f
	}

	friendBin := strings.Join([]string{"nova", "friend"}, "-")
	novaFriend, err := exec.LookPath(friendBin)
	if err != nil {
		novaFriend = filepath.Join(a.home(), ".local", "bin", friendBin)
	}

	for run := 1; run <= 10; run++ {
		nonceBytes := make([]byte, 3)
		_, _ = rand.Read(nonceBytes)
		nonce := hex.EncodeToString(nonceBytes)
		t0 := time.Now().UTC()

		pingCmd := exec.CommandContext(ctx, novaFriend, "ping", "--as", "rowan", "--to", friend, "--nonce", nonce)
		pingOut, err := pingCmd.CombinedOutput()
		require.NoError(t, err, "ping failed: %s", string(pingOut))

		waitCmd := exec.CommandContext(ctx, novaFriend, "wait-pong", "--from", friend, "--nonce", nonce, "--timeout", "60s")
		waitOut, err := waitCmd.CombinedOutput()
		require.NoError(t, err, "wait-pong failed (session pong required, daemon-pong alone exits 1): %s", string(waitOut))

		tPong := time.Now().UTC()
		duration := tPong.Sub(t0).Round(time.Millisecond)

		lineNum, turnSeenAt := findTranscriptTurn(transcriptFile, nonce)
		require.Positive(t, lineNum, "turn for nonce %s not found in transcript %s", nonce, transcriptFile)

		t.Logf("Run %2d: sent at %s, turn seen at %s (transcript %s line %d), pong at %s, %s",
			run, t0.Format(time.RFC3339), turnSeenAt, transcriptFile, lineNum, tPong.Format(time.RFC3339), duration)
	}
}

func findTranscriptTurn(transcriptPath, nonce string) (int, string) {
	f, err := os.Open(transcriptPath)
	if err != nil {
		return 0, ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if strings.Contains(line, nonce) {
			var entry struct {
				CreatedAt string `json:"created_at"`
			}
			_ = json.Unmarshal([]byte(line), &entry)
			return lineNum, entry.CreatedAt
		}
	}
	return 0, ""
}

// TestAntigravityRefusesWhenNoServerForUser verifies that against real ps execution,
// an absent language server is the harness's refusal (SessionRefused), never a deferral.
func TestAntigravityRefusesWhenNoServerForUser(t *testing.T) {
	t.Parallel()
	dir := os.Getenv("NOVA_FRIEND_ANTIGRAVITY_DIR")
	if dir == "" {
		t.Skip("NOVA_FRIEND_ANTIGRAVITY_DIR names no friend's directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	a := &Antigravity{Dir: dir, User: "nonexistent-user-12345", Run: RealExec}
	exit, err := a.Deliver(ctx, "text")
	require.Equal(t, 1, exit)
	var refused SessionRefused
	require.ErrorAs(t, err, &refused)
	require.Contains(t, refused.Reason, "no antigravity language server is running: is Antigravity open?")
}
