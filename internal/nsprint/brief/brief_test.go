package brief

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/redis/go-redis/v9"
)

const testSprint = "brief-test"

// testTitle is a build title in the `| NAME: ...` grammar. Its DONE-WHEN
// quotes `-run 'A|B'`, the pipes titleField would cut at.
func testTitle(n, doneWhen, paths, base, model string) string {
	return "mas-bandwidth/nova-tools#" + n + " WHO: any | brief test task " + n +
		" | DONE-WHEN: " + doneWhen +
		" | PATHS: " + paths +
		" | BASE: " + base +
		" | DEPENDS-ON: none | MODEL: " + model + " | est: 30"
}

func defaultTitle(n string) string {
	return testTitle(n, "`go test ./internal/x/ -run 'TestA"+n+"|TestB"+n+"'` exits 0",
		"internal/x/ internal/y/file"+n+".go", "dev base-sha: 0123456789ab"+n, "opus-5.5")
}

// putTask is a test-only bare write of a task hash.
func putTask(t *testing.T, client *redis.Client, id, kind, title string) {
	t.Helper()
	if err := client.HSet(context.Background(), task.Key(testSprint, id), "kind", kind, "title", title, "state", "open").Err(); err != nil {
		t.Fatal(err)
	}
}

func ref(id string) string { return testSprint + "/" + id }

// runRender runs Render to out and returns the exit code and refusal lines.
func runRender(t *testing.T, st *store.Store, id, out string) (int, []string) {
	t.Helper()
	var so, se bytes.Buffer
	code := Render(context.Background(), st, ref(id), out, &so, &se)
	return code, refusedLines(se.String())
}

// renderOK renders the task to dir/<name> and requires exit 0 and a clean lint.
func renderOK(t *testing.T, st *store.Store, id, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if code, lines := runRender(t, st, id, p); code != 0 {
		t.Fatalf("render %s: exit %d, lines %q", id, code, lines)
	}
	if code, lines := runLint(t, st, p, ""); code != 0 {
		t.Fatalf("lint of a fresh render of %s: exit %d, lines %q", id, code, lines)
	}
	return p
}

// editLine rewrites the first line starting with prefix (or deletes it when
// repl is "\x00") into a copy of src and returns the copy's path.
func editLine(t *testing.T, src, prefix, repl, name string) string {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	found := false
	for i, l := range lines {
		if strings.HasPrefix(l, prefix) {
			if repl == "\x00" {
				lines = append(lines[:i], lines[i+1:]...)
			} else {
				lines[i] = repl
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no line starting %q in %s", prefix, src)
	}
	out := filepath.Join(filepath.Dir(src), name)
	if err := os.WriteFile(out, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	return out
}

func lineWith(t *testing.T, path, prefix string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("no line starting %q in %s", prefix, path)
	return ""
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256hex(raw)
}

func hget(t *testing.T, client *redis.Client, id, field string) string {
	t.Helper()
	v, err := client.HGet(context.Background(), task.Key(testSprint, id), field).Result()
	if err != nil && err != redis.Nil {
		t.Fatal(err)
	}
	return v
}

// only requires exactly one refusal line, equal to want.
func only(t *testing.T, what string, code int, lines []string, want string) {
	t.Helper()
	if code != 1 || len(lines) != 1 || lines[0] != want {
		t.Fatalf("%s: exit %d lines %q, want exit 1 and exactly %q", what, code, lines, want)
	}
}

func TestBriefLintRefusesCoauthorModelMismatch(t *testing.T) {
	st, client := briefRedis(t)
	dir := t.TempDir()
	putTask(t, client, "co-1", "build", defaultTitle("1"))
	p := renderOK(t, st, "co-1", dir, "b.md")
	if got := lineWith(t, p, "Co-Authored-By:"); got != "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" {
		t.Fatalf("rendered co-author line %q, want the task's MODEL opus-5.5", got)
	}
	// (a) the brief's co-author line edited to another model.
	edited := editLine(t, p, "Co-Authored-By:", "Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>", "edited.md")
	code, lines := runLint(t, st, edited, "")
	co := linesWithField(lines, "coauthor")
	if code != 1 || len(co) != 1 || co[0] != "BRIEF REFUSED task=co-1 field=coauthor want=opus-5.5 got=fable-5.1" {
		t.Fatalf("edited co-author: exit %d lines %q, want one field=coauthor want=opus-5.5 got=fable-5.1", code, lines)
	}
	// (b) the co-author line deleted.
	gone := editLine(t, p, "Co-Authored-By:", "\x00", "gone.md")
	code, lines = runLint(t, st, gone, "")
	co = linesWithField(lines, "coauthor")
	if code != 1 || len(co) != 1 || co[0] != "BRIEF REFUSED task=co-1 field=coauthor want=opus-5.5 got=ABSENT" {
		t.Fatalf("deleted co-author: exit %d lines %q, want one field=coauthor got=ABSENT", code, lines)
	}
	// (c) the task's MODEL changed after render: the unchanged file now names
	// the wrong model.
	putTask(t, client, "co-1", "build", strings.Replace(defaultTitle("1"), "MODEL: opus-5.5", "MODEL: sonnet-5", 1))
	code, lines = runLint(t, st, p, "")
	co = linesWithField(lines, "coauthor")
	if code != 1 || len(co) != 1 || co[0] != "BRIEF REFUSED task=co-1 field=coauthor want=sonnet-5 got=opus-5.5" {
		t.Fatalf("MODEL changed: exit %d lines %q, want one field=coauthor want=sonnet-5 got=opus-5.5", code, lines)
	}
}

func TestBriefLintRefusesRulesShaNotOnTask(t *testing.T) {
	st, client := briefRedis(t)
	dir := t.TempDir()
	putTask(t, client, "rules-1", "build", defaultTitle("1"))
	p := renderOK(t, st, "rules-1", dir, "b.md")
	tmplBytes, _ := templateFor("build")
	stored := hget(t, client, "rules-1", "brief_tmpl_sha")
	if stored != sha256hex(tmplBytes) || lineWith(t, p, headerRules) != headerRules+stored {
		t.Fatalf("brief_tmpl_sha %q, header %q, want both the template's sha256", stored, lineWith(t, p, headerRules))
	}
	other := strings.Repeat("0", 64)
	// (a) a brief carrying another template's RULES-SHA.
	code, lines := runLint(t, st, editLine(t, p, headerRules, headerRules+other, "other.md"), "")
	rs := linesWithField(lines, "rules_sha")
	if code != 1 || len(rs) != 1 || rs[0] != "BRIEF REFUSED task=rules-1 field=rules_sha want="+stored+" got="+other {
		t.Fatalf("other RULES-SHA: exit %d lines %q", code, lines)
	}
	// (b) the header removed (the TASK: header stays, so line 2 is not RULES-SHA).
	code, lines = runLint(t, st, editLine(t, p, headerRules, "\x00", "none.md"), "")
	rs = linesWithField(lines, "rules_sha")
	if code != 1 || len(rs) != 1 || rs[0] != "BRIEF REFUSED task=rules-1 field=rules_sha want="+stored+" got=ABSENT" {
		t.Fatalf("missing RULES-SHA: exit %d lines %q", code, lines)
	}
	// (c) the unchanged file against a task that records another template
	// (test-only bare write): a present sha is not enough, it must be the
	// task's.
	if err := client.HSet(context.Background(), task.Key(testSprint, "rules-1"), "brief_tmpl_sha", other).Err(); err != nil {
		t.Fatal(err)
	}
	code, lines = runLint(t, st, p, "")
	only(t, "task records another template", code, lines, "BRIEF REFUSED task=rules-1 field=rules_sha want="+other+" got="+stored)
}

func TestBriefLintRefusesPathsMismatch(t *testing.T) {
	st, client := briefRedis(t)
	dir := t.TempDir()
	putTask(t, client, "paths-1", "build", defaultTitle("1"))
	p := renderOK(t, st, "paths-1", dir, "b.md")
	// (a) one path added in the brief.
	code, lines := runLint(t, st, editLine(t, p, "PATHS:", "PATHS: internal/x/ internal/y/file1.go cmd/extra.go", "more.md"), "")
	ps := linesWithField(lines, "paths")
	want := "BRIEF REFUSED task=paths-1 field=paths want=:internal/x/,:internal/y/file1.go got=:cmd/extra.go,:internal/x/,:internal/y/file1.go"
	if code != 1 || len(ps) != 1 || ps[0] != want {
		t.Fatalf("extra path: exit %d lines %q, want %q", code, lines, want)
	}
	// (b) one path dropped.
	code, lines = runLint(t, st, editLine(t, p, "PATHS:", "PATHS: internal/x/", "fewer.md"), "")
	if ps = linesWithField(lines, "paths"); code != 1 || len(ps) != 1 {
		t.Fatalf("dropped path: exit %d lines %q, want one field=paths line", code, lines)
	}
	// (c) the same set reordered and re-spelled: no paths line (the file
	// still differs, so brief_sha alone refuses it).
	code, lines = runLint(t, st, editLine(t, p, "PATHS:", "PATHS: `internal/y/file1.go`, internal/x/", "same.md"), "")
	if ps = linesWithField(lines, "paths"); code != 1 || len(ps) != 0 || len(linesWithField(lines, "brief_sha")) != 1 {
		t.Fatalf("same set: exit %d lines %q, want only field=brief_sha", code, lines)
	}
	// (d) render refuses a title without PATHS.
	putTask(t, client, "paths-2", "build", strings.Replace(defaultTitle("2"), "| PATHS: internal/x/ internal/y/file2.go ", "", 1))
	code, lines = runRender(t, st, "paths-2", filepath.Join(dir, "p2.md"))
	only(t, "render without PATHS", code, lines, "BRIEF REFUSED task=paths-2 field=paths got=ABSENT")
}

func TestBriefLintRefusesMissingTaskRef(t *testing.T) {
	st, client := briefRedis(t)
	dir := t.TempDir()
	putTask(t, client, "ref-1", "build", defaultTitle("1"))
	putTask(t, client, "ref-2", "build", defaultTitle("2"))
	p := renderOK(t, st, "ref-1", dir, "b.md")
	// (a) no TASK: header and no --task.
	noHeader := editLine(t, p, headerTask, "\x00", "noheader.md")
	code, lines := runLint(t, st, noHeader, "")
	only(t, "no task reference", code, lines, "BRIEF REFUSED task=- field=task_ref want=TASK-header-or---task got=ABSENT")
	// (b) header and --task disagree.
	code, lines = runLint(t, st, p, ref("ref-2"))
	only(t, "header and --task differ", code, lines, "BRIEF REFUSED task=- field=task_ref want="+ref("ref-2")+" got="+ref("ref-1"))
	// (c) header and --task agree: clean.
	if code, lines = runLint(t, st, p, ref("ref-1")); code != 0 {
		t.Fatalf("header and --task agree: exit %d lines %q", code, lines)
	}
	// (d) the named task does not exist.
	code, lines = runLint(t, st, editLine(t, p, headerTask, headerTask+ref("nope"), "nope.md"), "")
	only(t, "missing task", code, lines, "BRIEF REFUSED task=nope field=task got=MISSING")
	// (e) render of a missing task refuses and writes nothing.
	out := filepath.Join(dir, "missing.md")
	code, lines = runRender(t, st, "nope", out)
	only(t, "render of a missing task", code, lines, "BRIEF REFUSED task=nope field=task got=MISSING")
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("render of a missing task left %s (stat err %v)", out, err)
	}
	if n, _ := client.Exists(context.Background(), task.Key(testSprint, "nope")).Result(); n != 0 {
		t.Fatalf("render created the missing task hash")
	}
}

// taskFieldPrefixes are the lines of a rendered brief that carry task values.
var taskFieldPrefixes = []string{headerTask, "PATHS:", "BASE:", "base-sha:", "DEPENDS-ON:", "DONE-WHEN:", "CHECK:", "EXPECT:", "REPORT:", "Co-Authored-By:"}

func isTaskFieldLine(l string) bool {
	for _, p := range taskFieldPrefixes {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

func TestBriefRenderDiffersOnlyInTaskFields(t *testing.T) {
	st, client := briefRedis(t)
	dir := t.TempDir()
	models := []string{"opus-5.5", "sonnet-5", "fable-5.1", "opus-5.5", "haiku-5"}
	var files [][]string
	for i, m := range models {
		n := string(rune('1' + i))
		id := "same-" + n
		title := testTitle(n, "`go test ./internal/p"+n+"/ -run 'TestOne|TestTwo'` exits 0",
			"internal/p"+n+"/ cmd/c"+n+".go", "dev base-sha: abcdef"+n, m)
		if i == 2 {
			title += " | CHECK: go test ./internal/p3/ | EXPECT: ok"
		}
		putTask(t, client, id, "build", title)
		p := renderOK(t, st, id, dir, id+".md")
		raw, _ := os.ReadFile(p)
		files = append(files, strings.Split(string(raw), "\n"))
		if got := lineWith(t, p, "DONE-WHEN:"); !strings.Contains(got, "'TestOne|TestTwo'") {
			t.Fatalf("DONE-WHEN cut at its own pipe: %q", got)
		}
	}
	for i := range files {
		for j := i + 1; j < len(files); j++ {
			a, b := files[i], files[j]
			if len(a) != len(b) {
				t.Fatalf("briefs %d and %d have %d and %d lines", i+1, j+1, len(a), len(b))
			}
			for k := range a {
				if a[k] != b[k] && (!isTaskFieldLine(a[k]) || !isTaskFieldLine(b[k])) {
					t.Fatalf("briefs %d and %d differ outside task fields at line %d:\n%q\n%q", i+1, j+1, k+1, a[k], b[k])
				}
			}
		}
	}
	// Every kind with a template renders and lints clean; a kind without one
	// refuses.
	for _, kind := range []string{"fix", "read", "work"} {
		putTask(t, client, "kind-"+kind, kind, defaultTitle("9"))
		renderOK(t, st, "kind-"+kind, dir, kind+".md")
	}
	putTask(t, client, "kind-harvest", "harvest", defaultTitle("9"))
	code, lines := runRender(t, st, "kind-harvest", filepath.Join(dir, "harvest.md"))
	only(t, "kind without a template", code, lines, "BRIEF REFUSED task=kind-harvest field=kind want=build|fix|read|rebase got=harvest")
}

func TestBriefLintRefusesEditedBrief(t *testing.T) {
	st, client := briefRedis(t)
	dir := t.TempDir()
	putTask(t, client, "edit-1", "build", defaultTitle("1"))
	p := renderOK(t, st, "edit-1", dir, "b.md")
	sha := hget(t, client, "edit-1", "brief_sha")
	trailing := lineWith(t, p, "REPORT:") + " "
	edits := []struct{ name, prefix, repl string }{
		{"base-sha", "base-sha:", "base-sha: ffffffffffff"},
		{"done-when", "DONE-WHEN:", "DONE-WHEN: `true` exits 0"},
		{"check-expect", "CHECK:", "CHECK: nothing"},
		{"scratch-clone deleted", "- SCRATCH-CLONE:", "\x00"},
		{"trailing space", "REPORT:", trailing},
	}
	for _, e := range edits {
		f := editLine(t, p, e.prefix, e.repl, strings.ReplaceAll(e.name, " ", "-")+".md")
		code, lines := runLint(t, st, f, "")
		only(t, e.name, code, lines, "BRIEF REFUSED task=edit-1 field=brief_sha want="+sha+" got="+fileSHA(t, f))
	}
	if code, lines := runLint(t, st, p, ""); code != 0 {
		t.Fatalf("unedited brief: exit %d lines %q", code, lines)
	}
}

func TestBriefLintRefusesMissingBriefSha(t *testing.T) {
	st, client := briefRedis(t)
	ctx := context.Background()
	dir := t.TempDir()
	putTask(t, client, "sha-1", "build", defaultTitle("1"))
	p := renderOK(t, st, "sha-1", dir, "b.md")
	want := "BRIEF REFUSED task=sha-1 field=brief_sha want=ABSENT got=" + fileSHA(t, p)
	// (a) HDEL (test-only bare write).
	if err := client.HDel(ctx, task.Key(testSprint, "sha-1"), "brief_sha").Err(); err != nil {
		t.Fatal(err)
	}
	code, lines := runLint(t, st, p, "")
	only(t, "brief_sha absent", code, lines, want)
	// (b) the empty string (test-only bare write).
	if err := client.HSet(ctx, task.Key(testSprint, "sha-1"), "brief_sha", "").Err(); err != nil {
		t.Fatal(err)
	}
	code, lines = runLint(t, st, p, "")
	only(t, "brief_sha empty", code, lines, want)
}

func TestBriefRenderRefusesTaskChangedBetweenRoundTrips(t *testing.T) {
	st, client := briefRedis(t)
	ctx := context.Background()
	dir := t.TempDir()
	title := defaultTitle("1")
	putTask(t, client, "race-1", "build", title)
	edited := strings.Replace(title, "exits 0", "exits 0 twice", 1)
	briefFields := []string{"brief_tmpl_sha", "brief_sha", "brief_src", "brief_at"}

	t.Run("title changed between round trips", func(t *testing.T) {
		afterRead = func() {
			// Test-only bare write: the DONE-WHEN text swapped in the window.
			if err := client.HSet(ctx, task.Key(testSprint, "race-1"), "title", edited).Err(); err != nil {
				t.Error(err)
			}
		}
		defer func() { afterRead = nil }()
		out := filepath.Join(dir, "b.md")
		code, lines := runRender(t, st, "race-1", out)
		only(t, "render across an edit", code, lines, "BRIEF REFUSED task=race-1 field=task got=STALE")
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatalf("a refused render left %s (stat err %v)", out, err)
		}
		vals, err := client.HMGet(ctx, task.Key(testSprint, "race-1"), briefFields...).Result()
		if err != nil {
			t.Fatal(err)
		}
		for i, v := range vals {
			if v != nil {
				t.Fatalf("a refused render wrote %s=%v", briefFields[i], v)
			}
		}
	})

	t.Run("seam cleared renders", func(t *testing.T) {
		p := renderOK(t, st, "race-1", dir, "b2.md")
		if got := hget(t, client, "race-1", "brief_sha"); got != fileSHA(t, p) {
			t.Fatalf("brief_sha %q, want the file's sha256 %q", got, fileSHA(t, p))
		}
		if got, want := hget(t, client, "race-1", "brief_src"), task.BriefSource("build", edited); got != want {
			t.Fatalf("brief_src %q, want hex(sha1(kind NUL title)) %q", got, want)
		}
		if hget(t, client, "race-1", "brief_at") == "" {
			t.Fatal("brief_at not recorded")
		}
	})

	t.Run("FCALL with another kind is STALE", func(t *testing.T) {
		putTask(t, client, "race-2", "build", title)
		status, _, err := task.RecordBrief(ctx, st, testSprint, "race-2", title, "fix", "t", "b")
		if err != nil {
			t.Fatal(err)
		}
		if status != task.BriefStale {
			t.Fatalf("ns_task_brief with a different kind: %s, want STALE", status)
		}
		vals, err := client.HMGet(ctx, task.Key(testSprint, "race-2"), briefFields...).Result()
		if err != nil {
			t.Fatal(err)
		}
		for i, v := range vals {
			if v != nil {
				t.Fatalf("a STALE call wrote %s=%v", briefFields[i], v)
			}
		}
	})
}

func TestBriefLintRefusesTaskEditedAfterRender(t *testing.T) {
	st, client := briefRedis(t)
	ctx := context.Background()
	title := defaultTitle("1")
	fresh := func(t *testing.T, id string) (string, string) {
		t.Helper()
		putTask(t, client, id, "build", title)
		p := renderOK(t, st, id, t.TempDir(), "b.md")
		return p, hget(t, client, id, "brief_src")
	}
	setField := func(t *testing.T, id, field, v string) {
		t.Helper()
		// Test-only bare write: an edit by any writer after render.
		if err := client.HSet(ctx, task.Key(testSprint, id), field, v).Err(); err != nil {
			t.Fatal(err)
		}
	}
	doneEdited := strings.Replace(title, "exits 0", "exits 0 on linux", 1)

	t.Run("a DONE-WHEN", func(t *testing.T) {
		p, stored := fresh(t, "edit-a")
		setField(t, "edit-a", "title", doneEdited)
		code, lines := runLint(t, st, p, "")
		only(t, "DONE-WHEN edited", code, lines, "BRIEF REFUSED task=edit-a field=brief_src want="+task.BriefSource("build", doneEdited)+" got="+stored)
	})
	t.Run("b BASE", func(t *testing.T) {
		p, stored := fresh(t, "edit-b")
		edited := strings.Replace(title, "BASE: dev", "BASE: main", 1)
		setField(t, "edit-b", "title", edited)
		code, lines := runLint(t, st, p, "")
		only(t, "BASE edited", code, lines, "BRIEF REFUSED task=edit-b field=brief_src want="+task.BriefSource("build", edited)+" got="+stored)
	})
	t.Run("c kind", func(t *testing.T) {
		p, stored := fresh(t, "edit-c")
		setField(t, "edit-c", "kind", "fix")
		code, lines := runLint(t, st, p, "")
		only(t, "kind edited", code, lines, "BRIEF REFUSED task=edit-c field=brief_src want="+task.BriefSource("fix", title)+" got="+stored)
	})
	t.Run("d absent", func(t *testing.T) {
		p, _ := fresh(t, "edit-d")
		if err := client.HDel(ctx, task.Key(testSprint, "edit-d"), "brief_src").Err(); err != nil {
			t.Fatal(err)
		}
		code, lines := runLint(t, st, p, "")
		only(t, "brief_src absent", code, lines, "BRIEF REFUSED task=edit-d field=brief_src want="+task.BriefSource("build", title)+" got=ABSENT")
	})
	t.Run("e re-render", func(t *testing.T) {
		old, _ := fresh(t, "edit-e")
		oldSHA := fileSHA(t, old)
		setField(t, "edit-e", "title", doneEdited)
		p := renderOK(t, st, "edit-e", t.TempDir(), "b.md")
		code, lines := runLint(t, st, old, "")
		only(t, "old file after re-render", code, lines, "BRIEF REFUSED task=edit-e field=brief_sha want="+fileSHA(t, p)+" got="+oldSHA)
	})
}
