package task

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// FunctionLive is the read-only snapshot of live tasks registered by
// internal/nsprint/fn/lua/task.lua (nova-tools #3067).
const FunctionLive = "ns_task_live"

// Overlap names the live task a refused build push collides with: the pushed
// id, the other id (sprint/id when it lives in another sprint), and the two
// PATHS entries that intersect.
type Overlap struct {
	ID        string
	With      string
	Path      string
	OtherPath string
}

// String is the one refusal line; it always prints both ids.
func (o *Overlap) String() string {
	if o == nil {
		return ""
	}
	return fmt.Sprintf("id=%s with=%s path=%s other_path=%s; declare DEPENDS-ON: %s or cut disjoint PATHS",
		o.ID, o.With, o.Path, o.OtherPath, o.With)
}

// isBuild reports whether a task kind edits code and so holds its PATHS. A
// read or review title may quote the build title's PATHS; it never holds them.
func isBuild(kind Kind) bool {
	return kind == KindWork || kind == KindFix
}

var (
	parenRx   = regexp.MustCompile(`\([^)]*\)`)
	splitRx   = regexp.MustCompile(`[\s,;]+`)
	repoTagRx = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*:$`)
)

// titleField returns the text of the `| NAME: ...` segment of a task title,
// the grammar friend-queue and the cut cards share.
func titleField(title, name string) (string, bool) {
	for _, seg := range strings.Split(title, "|") {
		seg = strings.TrimSpace(seg)
		seg = strings.TrimPrefix(seg, "**")
		if rest, ok := strings.CutPrefix(seg, name+":"); ok {
			return strings.TrimSpace(strings.ReplaceAll(rest, "**", "")), true
		}
	}
	return "", false
}

// ParseTitle reads PATHS and DEPENDS-ON from a task title. Each path is
// repo-qualified as repo:path; a `repo:` token switches the repo for the
// tokens after it, and untagged tokens take defaultRepo. Parentheticals and
// the empty markers "-" and "none" are dropped.
func ParseTitle(title, defaultRepo string) (paths, deps []string) {
	if text, ok := titleField(title, "PATHS"); ok {
		repo := defaultRepo
		for _, tok := range splitRx.Split(parenRx.ReplaceAllString(text, " "), -1) {
			tok = strings.Trim(tok, "`'\".")
			if tok == "" || tok == "-" || strings.EqualFold(tok, "none") || strings.HasPrefix(tok, "~") {
				continue
			}
			if repoTagRx.MatchString(tok) {
				repo = strings.TrimSuffix(tok, ":")
				continue
			}
			paths = append(paths, repo+":"+strings.TrimPrefix(tok, "./"))
		}
	}
	if text, ok := titleField(title, "DEPENDS-ON"); ok {
		for _, tok := range splitRx.Split(parenRx.ReplaceAllString(text, " "), -1) {
			tok = strings.Trim(tok, "`'\".")
			if tok == "" || tok == "-" || strings.EqualFold(tok, "none") {
				continue
			}
			deps = append(deps, tok)
		}
	}
	return paths, deps
}

// PathsIntersect is the check-cut.py match rule over repo-qualified paths: an
// empty repo matches any repo; equal paths, a directory (with or without a
// trailing slash, or /** or /*) over a path beneath it, and a glob over a
// path it matches all intersect.
func PathsIntersect(a, b string) bool {
	ra, pa, _ := strings.Cut(a, ":")
	rb, pb, _ := strings.Cut(b, ":")
	if ra != "" && rb != "" && ra != rb {
		return false
	}
	return pathCovers(pa, pb) || pathCovers(pb, pa)
}

func pathCovers(x, y string) bool {
	if strings.TrimSuffix(x, "/") == strings.TrimSuffix(y, "/") {
		return true
	}
	if dir, ok := strings.CutSuffix(x, "/**"); ok {
		return strings.HasPrefix(y, dir+"/")
	}
	if dir, ok := strings.CutSuffix(x, "/*"); ok {
		rest, under := strings.CutPrefix(y, dir+"/")
		return under && !strings.Contains(rest, "/")
	}
	if strings.ContainsAny(x, "*?[") {
		ok, err := path.Match(x, y)
		return err == nil && ok
	}
	return strings.HasPrefix(y, strings.TrimSuffix(x, "/")+"/")
}

// liveTask is one row of the ns_task_live snapshot.
type liveTask struct {
	key   string
	id    string
	paths []string
	deps  []string
}

// lintPaths refuses a build push whose PATHS intersect a live build task's
// PATHS unless one reaches the other along DEPENDS-ON (transitively, through
// live task titles). It is one read-only snapshot call before the push call;
// a task without PATHS, a kind that never edits code, or a re-push of an
// existing id is not linted.
func lintPaths(ctx context.Context, st *store.Store, req PushRequest) (*Overlap, error) {
	if !isBuild(req.Kind) {
		return nil, nil
	}
	mine, myDeps := ParseTitle(req.Title, req.Repo)
	if len(mine) == 0 {
		return nil, nil
	}
	reply, err := st.Client().FCallRO(ctx, FunctionLive, nil, req.Sprint, req.ID).Result()
	if err != nil {
		return nil, fmt.Errorf("task push %s: path lint: %w", req.ID, err)
	}
	rows, ok := reply.([]any)
	if !ok || len(rows)%5 != 1 {
		return nil, fmt.Errorf("task push %s: path lint: unexpected reply %T", req.ID, reply)
	}
	if exists, _ := rows[0].(string); exists != "0" {
		// An existing id is answered by the create-only push itself.
		return nil, nil
	}
	rows = rows[1:]
	self := req.Sprint + "/" + req.ID
	byKey := map[string]*liveTask{}
	var live []*liveTask
	for i := 0; i < len(rows); i += 5 {
		cell := func(j int) string { s, _ := rows[i+j].(string); return s }
		sprint, id, kind, repo, title := cell(0), cell(1), cell(2), cell(3), cell(4)
		key := sprint + "/" + id
		if key == self || !isBuild(Kind(kind)) {
			continue
		}
		p, d := ParseTitle(title, repo)
		lt := &liveTask{key: key, id: id, paths: p, deps: qualify(sprint, d)}
		byKey[key] = lt
		live = append(live, lt)
	}
	myClosure := closure(qualify(req.Sprint, myDeps), byKey)
	for _, other := range live {
		if myClosure[other.key] || closure(other.deps, byKey)[self] {
			continue
		}
		for _, p := range mine {
			for _, q := range other.paths {
				if PathsIntersect(p, q) {
					with := other.key
					if strings.HasPrefix(with, req.Sprint+"/") {
						with = other.id
					}
					return &Overlap{ID: req.ID, With: with, Path: bare(p), OtherPath: bare(q)}, nil
				}
			}
		}
	}
	return nil, nil
}

// qualify turns DEPENDS-ON ids into sprint/id keys; an id already naming its
// sprint is kept.
func qualify(sprint string, deps []string) []string {
	out := make([]string, 0, len(deps))
	for _, d := range deps {
		if strings.Contains(d, "/") {
			out = append(out, d)
		} else {
			out = append(out, sprint+"/"+d)
		}
	}
	return out
}

// closure is every key reachable from start along live DEPENDS-ON edges.
func closure(start []string, byKey map[string]*liveTask) map[string]bool {
	seen := map[string]bool{}
	stack := append([]string(nil), start...)
	for len(stack) > 0 {
		k := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[k] {
			continue
		}
		seen[k] = true
		if lt := byKey[k]; lt != nil {
			stack = append(stack, lt.deps...)
		}
	}
	return seen
}

func bare(p string) string {
	_, rest, _ := strings.Cut(p, ":")
	return rest
}
