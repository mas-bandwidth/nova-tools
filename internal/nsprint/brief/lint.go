package brief

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// Lint checks a brief file against the task record named by its TASK: header
// or by taskFlag (both given and different, or neither, refuses). It writes
// nothing and reads the task in one round trip (HMGET title kind
// brief_tmpl_sha brief_sha brief_src brief_at). One BRIEF REFUSED line is
// printed per mismatch, in the order: (1) rules_sha, (2) coauthor, (3) paths,
// (5) brief_sha, (6) brief_src; a missing task (4) is the one line
// field=task got=MISSING.
//
// Exit 0 clean; 1 refused; 2 usage error or Redis unreachable (never a pass).
func Lint(ctx context.Context, st *store.Store, path, taskFlag string, stdout, stderr io.Writer) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		printf(stderr, "nova-sprint brief lint: %v\n", err)
		return 2
	}
	headTask, headRules := headers(raw)
	ref := headTask
	switch {
	case headTask == "" && taskFlag == "":
		printf(stderr, "BRIEF REFUSED task=- field=task_ref want=TASK-header-or---task got=ABSENT\n")
		return 1
	case headTask != "" && taskFlag != "" && headTask != taskFlag:
		printf(stderr, "BRIEF REFUSED task=- field=task_ref want=%s got=%s\n", taskFlag, headTask)
		return 1
	case headTask == "":
		ref = taskFlag
	}
	sprint, id, err := parseRef(ref)
	if err != nil {
		printf(stderr, "BRIEF REFUSED task=- field=task_ref want=<sprint>/<id> got=%s\n", strings.ReplaceAll(ref, " ", "_"))
		return 1
	}
	rec, err := task.ReadBrief(ctx, st, sprint, id)
	if err != nil {
		printf(stderr, "nova-sprint brief lint: %v\n", err)
		return 2
	}
	if !rec.Exists() {
		printf(stderr, "BRIEF REFUSED task=%s field=task got=MISSING\n", id)
		return 1
	}

	var refused []string
	add := func(field, want, got string) {
		refused = append(refused, fmt.Sprintf("BRIEF REFUSED task=%s field=%s want=%s got=%s", id, field, want, got))
	}
	text := string(raw)

	// (1) RULES-SHA against the template digest recorded on the task.
	if wantRules := orAbsent(rec.TmplSHA); headRules == "" || headRules != rec.TmplSHA.Value {
		add("rules_sha", wantRules, nonEmpty(headRules))
	}
	// (2) co-author model against MODEL.
	wantModel := nonEmpty(modelOf(rec.Title.Value))
	got := coauthorModels(text)
	switch {
	case len(got) == 0:
		add("coauthor", wantModel, absent)
	default:
		for _, g := range got {
			if g != wantModel {
				add("coauthor", wantModel, g)
				break
			}
		}
	}
	// (3) PATHS as a set, after the paths.go normalisation on both sides.
	wantPaths := pathSet(rec.Title.Value)
	gotPaths := briefPaths(raw)
	if strings.Join(wantPaths, ",") != strings.Join(gotPaths, ",") {
		add("paths", joinOrAbsent(wantPaths), joinOrAbsent(gotPaths))
	}
	// (5) the whole file against brief_sha; absent or empty refuses.
	fileSHA := sha256hex(raw)
	if !rec.BriefSHA.Set || rec.BriefSHA.Value == "" {
		add("brief_sha", absent, fileSHA)
	} else if rec.BriefSHA.Value != fileSHA {
		add("brief_sha", rec.BriefSHA.Value, fileSHA)
	}
	// (6) the task as it is now against the source digest render recorded.
	src := task.BriefSource(rec.Kind.Value, rec.Title.Value)
	if !rec.BriefSrc.Set || rec.BriefSrc.Value == "" {
		add("brief_src", src, absent)
	} else if rec.BriefSrc.Value != src {
		add("brief_src", src, rec.BriefSrc.Value)
	}

	if len(refused) > 0 {
		for _, l := range refused {
			printf(stderr, "%s\n", l)
		}
		return 1
	}
	printf(stdout, "BRIEF OK task=%s sha=%s\n", id, fileSHA)
	return 0
}

// headers reads the TASK: and RULES-SHA: headers from the first two lines.
func headers(raw []byte) (taskRef, rules string) {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for n := 0; n < 2 && sc.Scan(); n++ {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, headerTask); ok && n == 0 {
			taskRef = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(line, headerRules); ok && n == 1 {
			rules = strings.TrimSpace(v)
		}
	}
	return taskRef, rules
}

// pathSet is the task's PATHS through task.ParseTitle, sorted and unique.
func pathSet(title string) []string {
	paths, _ := task.ParseTitle(title, "")
	return uniqSorted(paths)
}

// briefPaths reads the brief's PATHS: line through the same parser.
func briefPaths(raw []byte) []string {
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "PATHS:"); ok {
			return pathSet("PATHS: " + v)
		}
	}
	return nil
}

func uniqSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func joinOrAbsent(s []string) string {
	if len(s) == 0 {
		return absent
	}
	return strings.Join(s, ",")
}

func nonEmpty(s string) string {
	if s == "" {
		return absent
	}
	return s
}
