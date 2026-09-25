package task_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/file"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

func TestDoneRefusesEvidenceWithoutSchemaFields(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "ev-sprint"
	client.HSet(ctx, "s:"+sprint, "status", "open")
	client.SAdd(ctx, "sprints", sprint)
	seedFriend(t, client, "ctl-a", 4)

	pushRes, err := task.PushChecked(ctx, st, task.PushRequest{
		Sprint: sprint, ID: "t-ev", Kind: task.KindWork, Title: "task ev", To: "ctl-a", Repo: "nova-tools",
	})
	if err != nil || pushRes.Status != task.PushCreated {
		t.Fatalf("push: %+v, %v", pushRes, err)
	}

	claim, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sprint, ID: "t-ev", As: "ctl-a"})
	if err != nil || !ok {
		t.Fatalf("take: %v, %v", ok, err)
	}

	// 1. Free-text evidence (pre-change shape) without DONE and PR
	status, err := task.Done(ctx, st, task.DoneRequest{
		Sprint: sprint, ID: "t-ev", Token: claim.Token, Evidence: "just free text evidence",
	})
	if err != nil {
		t.Fatalf("Done with free text error: %v", err)
	}
	if status != task.DoneInvalid {
		t.Fatalf("Done status = %s; want INVALID (refused free text)", status)
	}

	// 2. Evidence missing PR
	status, err = task.Done(ctx, st, task.DoneRequest{
		Sprint: sprint, ID: "t-ev", Token: claim.Token, Evidence: "DONE: pass line\nSome explanation",
	})
	if err != nil {
		t.Fatalf("Done missing PR error: %v", err)
	}
	if status != task.DoneInvalid {
		t.Fatalf("Done status = %s; want INVALID (missing PR)", status)
	}

	// 3. Evidence missing DONE
	status, err = task.Done(ctx, st, task.DoneRequest{
		Sprint: sprint, ID: "t-ev", Token: claim.Token, Evidence: "PR: https://github.com/mas-bandwidth/nova-tools/pull/1",
	})
	if err != nil {
		t.Fatalf("Done missing DONE error: %v", err)
	}
	if status != task.DoneInvalid {
		t.Fatalf("Done status = %s; want INVALID (missing DONE)", status)
	}

	// 4. Valid evidence with DONE and PR
	validEv := "DONE: TestPassed green\nPR: https://github.com/mas-bandwidth/nova-tools/pull/1 at abc1234"
	status, err = task.Done(ctx, st, task.DoneRequest{
		Sprint: sprint, ID: "t-ev", Token: claim.Token, Evidence: validEv,
	})
	if err != nil || status != task.DoneClosed {
		t.Fatalf("Done with valid evidence = %s, %v; want DONE CLOSED", status, err)
	}
}

func TestFollowUpFiledThroughFile(t *testing.T) {
	// Setup fake GitHub server for file.Main API calls
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/issues") {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"number": 101, "html_url": "http://example.com/issues/101"}`))
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/issues/101") {
			_, _ = w.Write([]byte(`{"number": 101, "body": "What: followup issue\nDONE-WHEN: x\nPATHS: y\nBASE: dev\nDEPENDS-ON: -\nowner: rowan reader: stella est: 10 min"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	oldFileFollowUp := task.FileFollowUp
	defer func() { task.FileFollowUp = oldFileFollowUp }()
	task.FileFollowUp = func(ctx context.Context, body string) error {
		tmpDir, err := os.MkdirTemp("", "nova-followup-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)
		bodyPath := filepath.Join(tmpDir, "followup.md")
		if err := os.WriteFile(bodyPath, []byte(body), 0644); err != nil {
			return err
		}
		var stdout, stderr io.Writer = io.Discard, io.Discard
		deps := file.Deps{
			API:   srv.URL,
			HTTP:  srv.Client(),
			Token: func() (string, error) { return "token", nil },
		}
		code := file.Main(ctx, []string{
			"--repo", "nova-tools",
			"--title", "follow-up task",
			"--body-file", bodyPath,
		}, stdout, stderr, deps)
		if code != 0 {
			return errors.New("file failed")
		}
		return nil
	}

	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "fu-sprint"
	client.HSet(ctx, "s:"+sprint, "status", "open")
	client.SAdd(ctx, "sprints", sprint)
	seedFriend(t, client, "ctl-a", 4)

	pushRes, err := task.PushChecked(ctx, st, task.PushRequest{
		Sprint: sprint, ID: "t-fu", Kind: task.KindWork, Title: "task fu", To: "ctl-a", Repo: "nova-tools",
	})
	if err != nil || pushRes.Status != task.PushCreated {
		t.Fatalf("push: %+v, %v", pushRes, err)
	}

	claim, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sprint, ID: "t-fu", As: "ctl-a"})
	if err != nil || !ok {
		t.Fatalf("take: %v, %v", ok, err)
	}

	evidence := `DONE: all tests pass
PR: https://github.com/mas-bandwidth/nova-tools/pull/2 at abc1234
FOLLOW-UP
What: followup issue
DONE-WHEN: x
PATHS: y
BASE: dev
DEPENDS-ON: -
owner: rowan reader: stella est: 10 min`

	status, err := task.Done(ctx, st, task.DoneRequest{
		Sprint: sprint, ID: "t-fu", Token: claim.Token, Evidence: evidence,
	})
	if err != nil || status != task.DoneClosed {
		t.Fatalf("Done = %s, %v; want DONE CLOSED", status, err)
	}
}

func TestExitWithoutDoneIsUnreported(t *testing.T) {
	st, client := controlRedis(t)
	ctx := context.Background()
	sprint := "un-sprint"
	client.HSet(ctx, "s:"+sprint, "status", "open")
	client.SAdd(ctx, "sprints", sprint)
	seedFriend(t, client, "ctl-a", 4)

	pushRes, err := task.PushChecked(ctx, st, task.PushRequest{
		Sprint: sprint, ID: "t-un", Kind: task.KindWork, Title: "task unreported", To: "ctl-a", Repo: "nova-tools",
	})
	if err != nil || pushRes.Status != task.PushCreated {
		t.Fatalf("push: %+v, %v", pushRes, err)
	}

	_, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: sprint, ID: "t-un", As: "ctl-a"})
	if err != nil || !ok {
		t.Fatalf("take: %v, %v", ok, err)
	}

	// Without calling Done, read table snapshot
	snap, err := table.ReadNamed(ctx, client, sprint)
	if err != nil {
		t.Fatal(err)
	}
	rendered := snap.Render()
	if !strings.Contains(rendered, "unreported t-un") {
		t.Fatalf("rendered table missing unreported t-un:\n%s", rendered)
	}
}
