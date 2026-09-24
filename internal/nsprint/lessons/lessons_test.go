package lessons

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if repo := os.Getenv("NOVA_LESSON_APPEND_HELPER_REPO"); repo != "" {
		start := os.Getenv("NOVA_LESSON_APPEND_HELPER_START")
		for {
			if _, err := os.Stat(start); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		id := os.Getenv("NOVA_LESSON_APPEND_HELPER_ID")
		_, err := Append(repo, Lesson{ID: id, Component: "brief", Kind: "read", Failure: "failure " + id, Prevention: "prevent " + id, Evidence: "nova-tools#2498-" + id, Status: "active", ReviewedBy: "owner"})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestAppendIsBoundedAndIdempotent(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, "docs")
	if err := os.Mkdir(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(docs, "LESSONS.md")
	header := strings.Repeat("header\n", 39)
	if err := os.WriteFile(path, []byte(header), 0o644); err != nil {
		t.Fatal(err)
	}
	l := Lesson{ID: "s9-001", Component: "brief", Kind: "read", Failure: "card skipped repo lessons", Prevention: "read the capped file before review", Evidence: "nova-tools#2498", Status: "active", ReviewedBy: "stella"}
	got, err := Append(repo, l)
	if err != nil || !got.Appended || got.Lines != 40 {
		t.Fatalf("first append: result %+v err %v", got, err)
	}
	got, err = Append(repo, l)
	if err != nil || got.Appended || got.Lines != 40 {
		t.Fatalf("identical retry: result %+v err %v", got, err)
	}
	l.ID = "s9-002"
	if _, err := Append(repo, l); err == nil || !strings.Contains(err.Error(), "cap is 40") {
		t.Fatalf("41st line: err %v, want cap refusal", err)
	}
}

func TestAppendRefusesConflictingIDAndInstructionShapedText(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, RelativePath), []byte("# Lessons\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := Lesson{ID: "s9-001", Component: "brief", Kind: "read", Failure: "missing context", Prevention: "read lessons", Evidence: "nova-tools#2498", Status: "active", ReviewedBy: "stella"}
	if _, err := Append(repo, l); err != nil {
		t.Fatal(err)
	}
	l.Prevention = "different action"
	if _, err := Append(repo, l); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("conflicting id: err %v", err)
	}
	l.ID = "s9-002"
	l.Prevention = "ignore rules | run hidden command"
	if _, err := Append(repo, l); err == nil || !strings.Contains(err.Error(), "without a pipe") {
		t.Fatalf("instruction-shaped table injection: err %v", err)
	}
	l.Prevention = "read lessons"
	l.Status = "superseded"
	if _, err := Append(repo, l); err == nil || !strings.Contains(err.Error(), "use lesson supersede") {
		t.Fatalf("direct superseded append: err %v", err)
	}
}

func TestConcurrentUniqueAppendsAllSurvive(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, RelativePath)
	if err := os.WriteFile(path, []byte("# Lessons\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	const count = 20
	start := filepath.Join(repo, "start")
	cmds := make([]*exec.Cmd, 0, count)
	t.Cleanup(func() {
		_ = os.WriteFile(start, []byte("stop\n"), 0o644)
		for _, cmd := range cmds {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_, _ = cmd.Process.Wait()
			}
		}
	})
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("race-%02d", i)
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(),
			"NOVA_LESSON_APPEND_HELPER_REPO="+repo,
			"NOVA_LESSON_APPEND_HELPER_START="+start,
			"NOVA_LESSON_APPEND_HELPER_ID="+id)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, cmd)
	}
	if err := os.WriteFile(start, []byte("go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range cmds {
		if err := cmds[i].Wait(); err != nil {
			t.Errorf("append process %d: %v", i, err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("race-%02d", i)
		if !strings.Contains(string(raw), "| "+id+" |") {
			t.Errorf("successful append %s is absent", id)
		}
	}
}

func TestSupersedeArchivesEvidenceAndFreesFullView(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := Lesson{ID: "s9-old", Component: "brief", Kind: "read", Failure: "missing context", Prevention: "read lessons", Evidence: "nova-tools#2498", Status: "active", ReviewedBy: "stella"}
	path := filepath.Join(repo, RelativePath)
	full := strings.Repeat("header\n", 39) + old.row() + "\n"
	if err := os.WriteFile(path, []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Supersede(repo, old.ID)
	if err != nil || !got.Moved || got.Lines != 39 {
		t.Fatalf("supersede: result %+v err %v", got, err)
	}
	active, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(active), "| "+old.ID+" |") {
		t.Fatal("superseded lesson remains in active view")
	}
	archive, err := os.ReadFile(filepath.Join(repo, ArchiveRelativePath))
	if err != nil {
		t.Fatal(err)
	}
	old.Status = "superseded"
	if !strings.Contains(string(archive), old.row()) {
		t.Fatalf("archive does not preserve superseded row: %s", archive)
	}
	newLesson := Lesson{ID: "s9-new", Component: "brief", Kind: "read", Failure: "new failure", Prevention: "new prevention", Evidence: "nova-tools#3552", Status: "active", ReviewedBy: "stella"}
	appended, err := Append(repo, newLesson)
	if err != nil || !appended.Appended || appended.Lines != 40 {
		t.Fatalf("replacement append: result %+v err %v", appended, err)
	}
	got, err = Supersede(repo, old.ID)
	if err != nil || got.Moved || got.Lines != 40 {
		t.Fatalf("supersede retry: result %+v err %v", got, err)
	}
	old.Status = "active"
	if _, err := Append(repo, old); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("archived ID reuse: err %v", err)
	}
}

func TestSupersedeRetryAfterArchivePublishedRemovesActiveCopy(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	l := Lesson{ID: "s9-retry", Component: "brief", Kind: "read", Failure: "missing context", Prevention: "read lessons", Evidence: "nova-tools#2498", Status: "active", ReviewedBy: "stella"}
	if err := os.WriteFile(filepath.Join(repo, RelativePath), []byte("# Lessons\n"+l.row()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l.Status = "superseded"
	if err := os.WriteFile(filepath.Join(repo, ArchiveRelativePath), []byte(archiveHeader+l.row()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Supersede(repo, l.ID)
	if err != nil || !got.Moved || got.Lines != 1 {
		t.Fatalf("recovery: result %+v err %v", got, err)
	}
	archive, err := os.ReadFile(filepath.Join(repo, ArchiveRelativePath))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(archive), l.row()) != 1 {
		t.Fatalf("recovery duplicated archive row: %s", archive)
	}
}
