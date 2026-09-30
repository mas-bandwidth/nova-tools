package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func TestTheTwinRefusesWhatIsNotATwinFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	for _, addr := range []string{"mem", "mem:"} {
		code, out, errs := runTable("list", "--redis", addr)
		if code != 2 || !strings.Contains(errs, "--redis mem:<file>") {
			t.Errorf("--redis %s: exit %d, stdout %q, stderr %q; want exit 2 naming mem:<file>", addr, code, out, errs)
		}
	}

	notes := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notes, []byte("plain text file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runTable("list", "--redis", "mem:"+notes)
	if code != 2 || !strings.Contains(errs, "not a twin snapshot") {
		t.Errorf("list over non-twin file: exit %d, stdout %q, stderr %q", code, out, errs)
	}
	if got, _ := os.ReadFile(notes); string(got) != "plain text file\n" {
		t.Errorf("the refused file changed: %q", got)
	}
}

func TestTheTwinRunsTableLifecycle(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "table.twin")
	twinAddr := "mem:" + file

	// 1. Create table
	code, out, errs := runTable("create", "demo", "--columns", "ready,working,done", "--redis", twinAddr)
	if code != 0 {
		t.Fatalf("create: exit %d\n%s%s", code, out, errs)
	}
	if !strings.Contains(out, "TABLE CREATE table=demo columns=3") {
		t.Errorf("unexpected create output: %s", out)
	}

	// Verify twin file exists on disk
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("expected twin file %s to exist: %v", file, err)
	}

	// 2. Add row
	code, out, errs = runTable("row", "add", "demo", "build", "--redis", twinAddr)
	if code != 0 {
		t.Fatalf("row add: exit %d\n%s%s", code, out, errs)
	}

	// 3. Add members to cells
	code, out, errs = runTable("cell", "add", "demo", "build", "ready", "b1", "--redis", twinAddr)
	if code != 0 {
		t.Fatalf("cell add b1: exit %d\n%s%s", code, out, errs)
	}
	code, out, errs = runTable("cell", "add", "demo", "build", "ready", "b2", "--redis", twinAddr)
	if code != 0 {
		t.Fatalf("cell add b2: exit %d\n%s%s", code, out, errs)
	}

	// 4. Move member
	code, out, errs = runTable("cell", "move", "demo", "build", "ready", "working", "b1", "--redis", twinAddr)
	if code != 0 {
		t.Fatalf("cell move b1: exit %d\n%s%s", code, out, errs)
	}

	// 5. Inspect cell members
	code, out, errs = runTable("cell", "members", "demo", "build", "working", "--redis", twinAddr)
	if code != 0 || !strings.Contains(out, "b1") {
		t.Errorf("cell members working: exit %d, out: %s, errs: %s", code, out, errs)
	}
	code, out, errs = runTable("cell", "members", "demo", "build", "ready", "--redis", twinAddr)
	if code != 0 || !strings.Contains(out, "b2") || strings.Contains(out, "b1") {
		t.Errorf("cell members ready: exit %d, out: %s, errs: %s", code, out, errs)
	}

	// 6. Show table
	code, out, errs = runTable("show", "demo", "--redis", twinAddr)
	if code != 0 || !strings.Contains(out, "TABLE table=demo columns=3 rows=1") {
		t.Errorf("show demo: exit %d, out: %s, errs: %s", code, out, errs)
	}

	// 7. Render table
	code, out, errs = runTable("render", "demo", "--redis", twinAddr)
	if code != 0 {
		t.Fatalf("render demo: exit %d\n%s%s", code, out, errs)
	}
	if !strings.Contains(out, "ready") || !strings.Contains(out, "working") || !strings.Contains(out, "done") || !strings.Contains(out, "build") {
		t.Errorf("render output missing expected headers or row:\n%s", out)
	}

	// 8. List tables
	code, out, errs = runTable("list", "--redis", twinAddr)
	if code != 0 || !strings.Contains(out, "demo") {
		t.Errorf("list: exit %d, out: %s, errs: %s", code, out, errs)
	}

	// 9. Check table
	code, out, errs = runTable("check", "demo", "--redis", twinAddr)
	if code != 0 {
		t.Errorf("check: exit %d, out: %s, errs: %s", code, out, errs)
	}

	// 10. Watch once
	code, out, errs = runTable("watch", "demo", "--once", "--redis", twinAddr)
	if code != 0 {
		t.Errorf("watch --once: exit %d, out: %s, errs: %s", code, out, errs)
	}

	// 11. Remove cell member
	code, out, errs = runTable("cell", "remove", "demo", "build", "working", "b1", "--redis", twinAddr)
	if code != 0 {
		t.Errorf("cell remove: exit %d, out: %s, errs: %s", code, out, errs)
	}

	// 12. Delete row
	code, out, errs = runTable("row", "del", "demo", "build", "--redis", twinAddr)
	if code != 0 {
		t.Errorf("row del: exit %d, out: %s, errs: %s", code, out, errs)
	}

	// 13. Drop table
	code, out, errs = runTable("drop", "demo", "--redis", twinAddr)
	if code != 0 {
		t.Errorf("drop: exit %d, out: %s, errs: %s", code, out, errs)
	}

	// Verify only 1 twin file exists and no temporary files
	dir, err := os.ReadDir(filepath.Dir(file))
	if err != nil {
		t.Fatal(err)
	}
	if len(dir) != 1 {
		t.Errorf("expected 1 file in directory, found %d", len(dir))
	}
}

func TestTheTwinViews(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "views.twin")
	twinAddr := "mem:" + file

	// Create table
	code, _, errs := runTable("create", "tasks", "--columns", "ready,done", "--redis", twinAddr)
	if code != 0 {
		t.Fatalf("create: exit %d, errs: %s", code, errs)
	}

	// View set
	code, out, errs := runTable("view", "set", "v1", "--tables", "tasks", "--title", "Active Tasks", "--redis", twinAddr)
	if code != 0 {
		t.Fatalf("view set: exit %d, out: %s, errs: %s", code, out, errs)
	}

	// View show
	code, out, errs = runTable("view", "show", "v1", "--redis", twinAddr)
	if code != 0 || !strings.Contains(out, "Active Tasks") {
		t.Errorf("view show: exit %d, out: %s, errs: %s", code, out, errs)
	}

	// View state
	code, _, errs = runTable("view", "state", "v1", "PAUSED", "--redis", twinAddr)
	if code != 0 {
		t.Fatalf("view state: exit %d, errs: %s", code, errs)
	}

	// Render view
	code, out, errs = runTable("render", "--view", "v1", "--redis", twinAddr)
	if code != 0 || !strings.Contains(out, "PAUSED") || !strings.Contains(out, "Active Tasks") {
		t.Errorf("render view: exit %d, out: %s, errs: %s", code, out, errs)
	}

	// View list
	code, out, errs = runTable("view", "list", "--redis", twinAddr)
	if code != 0 || !strings.Contains(out, "v1") {
		t.Errorf("view list: exit %d, out: %s, errs: %s", code, out, errs)
	}

	// View del
	code, _, errs = runTable("view", "del", "v1", "--redis", twinAddr)
	if code != 0 {
		t.Errorf("view del: exit %d, errs: %s", code, errs)
	}
}

func TestTheTwinShell(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "shell.twin")
	twinAddr := "mem:" + file

	commands := strings.Join([]string{
		"create stream --columns count,pct",
		"row add stream r1",
		"row add stream r2",
		"show stream",
		"quit",
	}, "\n") + "\n"

	var stdout, stderr bytes.Buffer
	app := &application{in: strings.NewReader(commands)}
	code := app.run([]string{"shell", "--redis", twinAddr, "--receipt=false"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("shell: exit %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}

	if !strings.Contains(stdout.String(), "TABLE CREATE table=stream") {
		t.Errorf("shell did not create table; stdout:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "TABLE table=stream columns=2 rows=2") {
		t.Errorf("shell did not show table with 2 rows; stdout:\n%s", stdout.String())
	}

	// Reopen twin in a separate invocation to verify persistence from shell
	code, out, errs := runTable("show", "stream", "--redis", twinAddr)
	if code != 0 || !strings.Contains(out, "rows=2") {
		t.Errorf("reopen twin after shell: exit %d, out: %s, errs: %s", code, out, errs)
	}
}

func TestTheTwinStoreMemInterop(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "interop.twin")
	twinAddr := "mem:" + file

	// Create a table via nova-table
	code, _, errs := runTable("create", "fleet", "--columns", "id,status", "--redis", twinAddr)
	if code != 0 {
		t.Fatalf("create: exit %d, errs: %s", code, errs)
	}

	// Load file directly using store.Mem (from sprint)
	doc, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	mem := store.NewMem()
	if err := mem.Restore(doc); err != nil {
		t.Fatalf("mem.Restore failed: %v", err)
	}

	tb, ok := mem.Table("fleet")
	if !ok {
		t.Fatalf("expected table 'fleet' to exist in restored store.Mem")
	}
	if len(tb.Columns) != 2 || tb.Columns[0].Name != "id" || tb.Columns[1].Name != "status" {
		t.Errorf("unexpected columns in restored table: %+v", tb.Columns)
	}
}
