package main

import (
	"context"
	"github.com/redis/go-redis/v9"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/file"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// nova-sprint file files an issue or a comment by REST from a body FILE, with
// the card-line lint before the post and a byte-for-byte read-back after it
// (nova-tools #3089, #2756 spec 4.11, control 50). The verb lives in
// internal/nsprint/file; this file wires the token and the task store.
//
// Exit 0 filed and read back equal, 1 the stored body differs, 2 refused or
// could not run.
func init() {
	register(Verb{
		Name:    "file",
		Summary: "file an issue (--title) or comment (--comment <n>) from --body-file by REST; lints before posting, reads back after (exit 1 differs); --push-to queues the build task",
		Run:     cmdFile,
	})
}

func cmdFile(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return file.Main(ctx, args, stdout, stderr, file.Deps{
		Token: githubToken,
		Push:  pushFiledTask,
		Open:  openFileStore,
	})
}

// githubToken is the seat's GitHub token when its seats.tsv row names one
// (#4330), else GH_TOKEN, then GITHUB_TOKEN, then `gh auth token` through
// internal/gh's one token read (#4343).
func githubToken() (string, error) {
	if v, err := envGitHubToken(); v != "" || err != nil {
		return v, err
	}
	return gh.Token()
}

// openFileStore is the store the file verb counts its calls in (#4343):
// the one resolver's address (seat.go: the seat's, else NOVA_SPRINT_REDIS,
// NOVA_REDIS_ADDR, NOVA_REDIS).
func openFileStore(ctx context.Context) (redis.Cmdable, func(), error) {
	st, err := store.Open(ctx, redisDefault())
	if err != nil {
		return nil, nil, err
	}
	return st.Client(), func() { _ = st.Close() }, nil
}

func pushFiledTask(ctx context.Context, addr string, req task.PushRequest) (task.PushResult, error) {
	st, err := store.Open(ctx, addr)
	if err != nil {
		return task.PushResult{}, err
	}
	defer st.Close()
	return task.PushChecked(ctx, st, req)
}
