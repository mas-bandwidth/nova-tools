//go:build functional && linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
	"github.com/mas-bandwidth/nova-tools/internal/sandbox"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
	"github.com/mas-bandwidth/nova-tools/internal/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The helper is a real child, not an LLM: it executes the scoped commands the
// production prompt supplies. The CLI branch invokes the production handlers,
// including RunWall, in this test executable without building another binary.
func init() {
	if len(os.Args) < 3 || os.Args[1] != "--joined-reply-subprocess" {
		return
	}
	args := os.Args[2:]
	switch args[0] {
	case "cli":
		os.Exit(run(args[1:], os.Stdin, os.Stdout, os.Stderr, realWorld()))
	case "provider":
		os.Exit(joinedProvider(args[1:]))
	default:
		fmt.Fprintln(os.Stderr, "unknown fixture subprocess")
		os.Exit(2)
	}
}

func joinedProvider(args []string) int {
	if len(args) < 3 {
		return 2
	}
	addr, home, args := args[0], args[1], args[2:]
	dir, err := os.Getwd()
	if err != nil {
		return 2
	}
	marker := filepath.Join(dir, "joined-session")
	switch {
	case args[0] == "--version":
		fmt.Println("fixture")
		return 0
	case args[0] == "session":
		if _, err := os.Stat(marker); err != nil {
			fmt.Println("[]")
		} else {
			b, err := json.Marshal([]map[string]any{{"id": "ses_joined", "directory": dir, "updated": 1}})
			if err != nil {
				return 2
			}
			fmt.Println(string(b))
		}
		return 0
	case slices.Contains(args, "--help"):
		fmt.Println(strings.Join(friend.OpenCodeRunFlags, " "))
		return 0
	case !slices.Contains(args, "--session"):
		if err := os.WriteFile(marker, []byte("opened\n"), 0o600); err != nil {
			return 2
		}
		return 0
	}
	return joinedCommands(dir, addr, home, args[len(args)-1])
}

func joinedCommands(dir, addr, home, prompt string) int {
	// A live Redis endpoint is independently reachable by the parent. A failure
	// here must be the wall's denial, rather than an absent service.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if c, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr); err == nil {
		if err := c.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		fmt.Fprintln(os.Stderr, "fixture escaped the wall's network denial")
		return 1
	} else if !errors.Is(err, os.ErrPermission) {
		fmt.Fprintln(os.Stderr, "direct Redis access did not return a permission denial")
		return 1
	}
	for _, line := range strings.Split(prompt, "\n") {
		if strings.Contains(line, " local-pong --as ") {
			_, command, found := strings.Cut(line, ": ")
			if !found {
				return 2
			}
			words, err := onboarding.SplitShell(strings.TrimSpace(command))
			if err != nil || joinedExec(ctx, words) != nil {
				return 2
			}
		}
		_, command, found := strings.Cut(line, "answer: ")
		if !found || !strings.Contains(command, " reply --as ") {
			continue
		}
		words, err := onboarding.SplitShell(command)
		if err != nil {
			return 2
		}
		i := slices.Index(words, "--re")
		if i < 0 || i+1 >= len(words) {
			return 2
		}
		re := words[i+1]
		grant := filepath.Join(friend.DefaultStateDir(home, "worker"), "native-broker", "native-reply-grants", re+".json")
		if _, err := os.ReadFile(grant); !errors.Is(err, os.ErrPermission) {
			fmt.Fprintln(os.Stderr, "fixture could read the daemon's private grant, or its grant is absent")
			return 1
		}
		// Save the containment receipt before publication: the daemon may send
		// immediately after the CLI atomically publishes its envelope.
		if err := os.WriteFile(filepath.Join(dir, "joined-contained"), []byte("network=denied broker=denied\n"), 0o600); err != nil {
			return 2
		}
		i = slices.Index(words, "--subject")
		if i < 0 {
			return 2
		}
		words = append(words[:i], "--subject", "Joined answer", "--body", "JOINED_REPLY "+re)
		if err := joinedExec(ctx, words); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	return 0
}

func joinedExec(ctx context.Context, words []string) error {
	if len(words) == 0 {
		return errors.New("missing local command")
	}
	cmd, cancel := subproc.Command(ctx, subproc.Tool, words[0], words[1:]...)
	defer cancel()
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// Only the response is lost: Redis applies the real atomic send. This models
// the reply broker's uncertain outcome without replacing its store behavior.
type joinedLostAnswer struct {
	bus.Store
	re   string
	once sync.Once
	lost chan struct{}
	stop context.CancelFunc
}

func (s *joinedLostAnswer) AddOnce(ctx context.Context, key, record string, keep time.Duration, streams []string, fields map[string]string, marks ...bus.Mark) (string, bool, error) {
	prior, found, err := s.Store.AddOnce(ctx, key, record, keep, streams, fields, marks...)
	if err != nil || fields["re"] != s.re {
		return prior, found, err
	}
	dropped := false
	s.once.Do(func() {
		dropped = true
		close(s.lost)
		s.stop()
	})
	if dropped {
		return "", false, errors.New("fixture loses the applied send response")
	}
	return prior, found, nil
}

type joinedReplyRig struct {
	root, dir, home, state, tools, cli, provider, addr string
	client                                             *redis.Client
	request                                            bus.Message
	mu                                                 sync.Mutex
	output                                             strings.Builder
	rawChecks                                          int
}

func newJoinedReplyRig(t *testing.T, ctx context.Context, existingOutbox bool) *joinedReplyRig {
	t.Helper()
	r := &joinedReplyRig{root: t.TempDir(), addr: testredis.Start(t)}
	r.dir, r.home, r.tools = filepath.Join(r.root, "work"), filepath.Join(r.root, "private"), filepath.Join(r.root, "tools")
	r.state = filepath.Join(r.home, "state")
	for _, d := range []string{r.dir, r.home, r.tools} {
		require.NoError(t, os.MkdirAll(d, 0o700))
	}
	if existingOutbox {
		require.NoError(t, os.Mkdir(filepath.Join(r.dir, "outbox"), 0o700))
	}
	bin, err := os.Executable()
	require.NoError(t, err)
	fixture := filepath.Join(r.tools, "fixture")
	require.NoError(t, testbin.Place(bin, fixture))
	r.cli, r.provider = filepath.Join(r.tools, "nova-friend"), filepath.Join(r.tools, "opencode")
	for _, row := range []struct{ path, args string }{
		{r.cli, "cli"}, {r.provider, "provider '" + r.addr + "' '" + r.home + "'"},
	} {
		require.NoError(t, testbin.WriteExecutable(row.path, []byte("#!/bin/sh\nexec '"+fixture+"' --joined-reply-subprocess "+row.args+" \"$@\"\n"), 0o555))
	}
	r.client = redis.NewClient(&redis.Options{Addr: r.addr})
	t.Cleanup(func() { assert.NoError(t, r.client.Close()) })
	require.NoError(t, r.client.Ping(ctx).Err())
	require.NoError(t, r.client.SAdd(ctx, "friends", "worker", "coordinator").Err())
	b := &bus.Bus{Store: bus.Redis{C: r.client}}
	r.request, err = b.Send(ctx, bus.Message{From: "coordinator", To: []string{"worker"}, Kind: bus.KindRequest, Subject: "joined request", Body: "Use the local reply command."})
	require.NoError(t, err)
	return r
}

func (r *joinedReplyRig) world(ctx context.Context, store bus.Store) world {
	w := realWorld()
	w.home, w.sqlite, w.stage, w.tip, w.cards, w.view, w.friends, w.holders = r.home, nil, nil, nil, nil, nil, nil, nil
	w.progress, w.finish, w.down = nil, nil, nil
	w.friends = func(context.Context, string) ([]friend.WakeRow, string, error) { return nil, "coordinator", nil }
	w.getenv = func(k string) string {
		if k == "PATH" {
			return r.tools + ":/usr/bin:/bin"
		}
		return ""
	}
	w.binary = func() (string, error) { return r.cli, nil }
	w.signals = func(context.Context) (context.Context, context.CancelFunc) { return context.WithCancel(ctx) }
	w.open = func(_ context.Context, addr string) (bus.Store, func(), error) {
		if addr != r.addr {
			return nil, nil, errors.New("fixture refuses an unowned Redis endpoint")
		}
		return store, func() {}, nil
	}
	w.exec = func(ctx context.Context, dir, program string, args []string, stdin string) (string, int, error) {
		if program == "opencode" {
			program = r.provider
		}
		if program == r.cli {
			args = slices.Clone(args)
			if i := slices.Index(args, "--"); i >= 0 && i+1 < len(args) && args[i+1] == "opencode" {
				args[i+1] = r.provider
			}
		}
		if strings.Contains(strings.Join(args, " "), friend.SessionCheckPrefix) && !friend.InLane(ctx) {
			r.mu.Lock()
			r.rawChecks++
			r.mu.Unlock()
		}
		return friend.RealExec(ctx, dir, program, args, stdin)
	}
	beat := func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		return "FRIEND-BEAT OK worker row_mode=one-shot row_width=1 machine=STOPPED", nil
	}
	w.beat = beat
	w.beatDownAnswer = func(ctx context.Context, server, name string, active, until time.Time, reason string, words friend.BeatWords) (string, error) {
		return beat(ctx, server, name, active, words)
	}
	return w
}

func (r *joinedReplyRig) run(ctx context.Context, store bus.Store) int {
	out := &lockedBuilder{mu: &r.mu}
	var errs strings.Builder
	code := run([]string{"run", "--as", "worker", "--coordinator", "coordinator", "--harness", "opencode", "--dir", r.dir, "--state-dir", r.state, "--redis", r.addr, "--mode", "one-shot", "--width", "1", "--deny-self", r.home, "--wall-reads", r.tools}, strings.NewReader(""), out, &errs, r.world(ctx, store))
	r.mu.Lock()
	r.output.WriteString(out.String())
	r.output.WriteString(errs.String())
	r.mu.Unlock()
	return code
}

// SPEC-FRIEND's scoped native reply, owned wall and durable send receipt meet
// tla/BusSendOnce.tla here: a real contained child publishes via the real CLI, a real
// Redis send applies but loses its answer, and a new whole daemon reconciles it.
func TestJoinedWallReplySurvivesLostRedisAnswerAndDaemonRestart(t *testing.T) {
	t.Parallel()
	joinedReplyProof(t, true)
}

// A fresh zero-card workspace has no JOB or outbox fixture. Go must prepare
// the real local spool before issuing the first native pong/reply command.
func TestJoinedFreshWorkspaceRepliesBeforeAnyCard(t *testing.T) {
	t.Parallel()
	joinedReplyProof(t, false)
}

func joinedReplyProof(t *testing.T, existingOutbox bool) {
	t.Helper()
	_, available := sandbox.Available()
	require.True(t, available, "this functional gate requires the Linux wall")
	require.NotZero(t, os.Geteuid(), "the wall requires the container's unprivileged user")
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	r := newJoinedReplyRig(t, ctx, existingOutbox)
	real := bus.Redis{C: r.client}
	first, stop := context.WithCancel(ctx)
	defer stop()
	loss := &joinedLostAnswer{Store: real, re: r.request.ID, lost: make(chan struct{}), stop: stop}
	ended := make(chan int, 1)
	go func() { ended <- r.run(first, loss) }()
	select {
	case <-loss.lost:
	case <-ctx.Done():
		stop()
		select {
		case <-ended:
		case <-time.After(30 * time.Second):
			t.Fatal("daemon failed to stop after fixture deadline")
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		t.Fatal("contained native reply never reached the real Redis send: ", r.output.String())
	}
	require.Equal(t, 0, <-ended)
	contained, err := os.ReadFile(filepath.Join(r.dir, "joined-contained"))
	require.NoError(t, err)
	assert.Equal(t, "network=denied broker=denied\n", string(contained))
	r.assertReceipt(t, false)
	b := &bus.Bus{Store: real}
	var reply bus.Message
	for ctx.Err() == nil {
		entry, ok, err := b.Recv(ctx, "coordinator", time.Second)
		require.NoError(t, err)
		if ok && entry.Message().Re == r.request.ID {
			reply = entry.Message()
			break
		}
	}
	require.NotEmpty(t, reply.ID, "a pong does not substitute for the ordinary reply")
	assert.Equal(t, "worker", reply.From)
	assert.Equal(t, []string{"coordinator"}, reply.To)
	assert.Equal(t, r.request.ID, reply.Re)
	assert.Equal(t, "Joined answer", reply.Subject)
	assert.Equal(t, "JOINED_REPLY "+r.request.ID, reply.Body)
	second, stopSecond := context.WithCancel(ctx)
	go func() { ended <- r.run(second, real) }()
	joined := false
	t.Cleanup(func() {
		stopSecond()
		if !joined {
			select {
			case <-ended:
			case <-time.After(30 * time.Second):
				assert.Fail(t, "restarted daemon failed to clean up")
			}
		}
	})
	require.Eventually(t, func() bool { return r.receiptSent() }, 30*time.Second, 20*time.Millisecond)
	stopSecond()
	require.Equal(t, 0, <-ended)
	joined = true
	r.assertReceipt(t, true)
	entries, err := b.Store.Range(ctx, bus.StreamOf("coordinator"), "-", "+", 0)
	require.NoError(t, err)
	replies := slices.DeleteFunc(entries, func(e bus.Entry) bool { return e.Message().Re != r.request.ID })
	require.Len(t, replies, 1, "restart reconciles the same token; no second reply")
	assert.Equal(t, reply.ID, replies[0].Message().ID)
	r.assertQuietControls(t)
	r.mu.Lock()
	defer r.mu.Unlock()
	assert.Zero(t, r.rawChecks, "production one-shot checks stay in owned lanes")
	assert.NotContains(t, r.output.String(), "fixture escaped")
}

func (r *joinedReplyRig) assertQuietControls(t *testing.T) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(r.dir, "jobs", "control-*", "RUN"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "actual provider turns have durable launch receipts")
	for _, path := range paths {
		b, err := os.ReadFile(path)
		require.NoError(t, err)
		var receipt struct {
			PID      int    `json:"pid"`
			Identity string `json:"identity"`
		}
		require.NoError(t, json.Unmarshal(b, &receipt))
		require.Positive(t, receipt.PID)
		require.NotEmpty(t, receipt.Identity)
		assert.FileExists(t, path+".anchor")
		assert.False(t, friend.ProcessGroupAlive(receipt.PID), "owned native group is gone after daemon shutdown")
	}
}

func (r *joinedReplyRig) receiptPath() string {
	return filepath.Join(friend.DefaultStateDir(r.home, "worker"), "native-broker", "native-reply-receipts", r.request.ID+".json")
}

func (r *joinedReplyRig) receiptSent() bool {
	b, err := os.ReadFile(r.receiptPath())
	var receipt struct {
		Sent bool `json:"sent"`
	}
	return err == nil && json.Unmarshal(b, &receipt) == nil && receipt.Sent
}

func (r *joinedReplyRig) assertReceipt(t *testing.T, sent bool) {
	t.Helper()
	b, err := os.ReadFile(r.receiptPath())
	require.NoError(t, err)
	var receipt struct {
		AttemptedAt time.Time `json:"attempted_at"`
		Sent        bool      `json:"sent"`
		ID          string    `json:"id"`
	}
	require.NoError(t, json.Unmarshal(b, &receipt))
	assert.False(t, receipt.AttemptedAt.IsZero(), "uncertain send retains its durable attempt marker")
	assert.Equal(t, sent, receipt.Sent)
	if sent {
		assert.NotEmpty(t, receipt.ID)
	}
}
