package main

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"

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
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		_, _ = io.WriteString(stdout, file.Usage)
		return 2
	}
	return file.Main(ctx, args, stdout, stderr, file.Deps{
		Token: githubToken,
		Push:  pushFiledTask,
	})
}

// githubToken is the seat's GitHub token when its seats.tsv row names one
// (#4330), else GH_TOKEN, then GITHUB_TOKEN, then `gh auth token` (which
// honours GH_CONFIG_DIR, the fleet's per-seat gh config).
func githubToken() (string, error) {
	if v, err := envGitHubToken(); v != "" || err != nil {
		return v, err
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return "", errors.New("no GH_TOKEN or GITHUB_TOKEN and no seat token, and gh auth token failed (" + strings.TrimSpace(err.Error()) + "); name the seat's GitHub token env in its seats.tsv row (the seventh column) and pass --seat")
	}
	return strings.TrimSpace(string(out)), nil
}

func pushFiledTask(ctx context.Context, addr string, req task.PushRequest) (task.PushResult, error) {
	st, err := store.Open(ctx, addr)
	if err != nil {
		return task.PushResult{}, err
	}
	defer st.Close()
	return task.PushChecked(ctx, st, req)
}
