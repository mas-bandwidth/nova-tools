package task

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// FunctionBrief is registered by internal/nsprint/fn/lua/task_brief.lua
// (nova-tools#3154): the one writer of a task's brief_tmpl_sha, brief_sha,
// brief_src and brief_at fields.
const FunctionBrief = "ns_task_brief"

// BriefStatus is ns_task_brief's answer.
type BriefStatus string

const (
	// BriefOK recorded all four brief fields in one call.
	BriefOK BriefStatus = "OK"
	// BriefMissing is an absent task hash; nothing was written.
	BriefMissing BriefStatus = "MISSING"
	// BriefStale is a title or kind that changed since the caller read them;
	// nothing was written.
	BriefStale BriefStatus = "STALE"
)

// Key is the task hash s:<sprint>:task:<id>.
func Key(sprint, id string) string { return "s:" + sprint + ":task:" + id }

// Field is one HMGET cell: Set is false for a nil reply.
type Field struct {
	Value string
	Set   bool
}

// BriefFields are the six fields brief lint reads in its one round trip.
type BriefFields struct {
	Title, Kind, TmplSHA, BriefSHA, BriefSrc, BriefAt Field
}

// Exists reports whether the task hash exists as far as lint can tell: a task
// always carries a title or a kind.
func (f BriefFields) Exists() bool { return f.Title.Set || f.Kind.Set }

// ReadTitleKind is render's round trip 1: HMGET title kind.
func ReadTitleKind(ctx context.Context, st *store.Store, sprint, id string) (title, kind Field, err error) {
	cells, err := hmget(ctx, st, Key(sprint, id), "title", "kind")
	if err != nil {
		return Field{}, Field{}, err
	}
	return cells[0], cells[1], nil
}

// ReadBrief is lint's one round trip: HMGET title kind brief_tmpl_sha
// brief_sha brief_src brief_at.
func ReadBrief(ctx context.Context, st *store.Store, sprint, id string) (BriefFields, error) {
	cells, err := hmget(ctx, st, Key(sprint, id), "title", "kind", "brief_tmpl_sha", "brief_sha", "brief_src", "brief_at")
	if err != nil {
		return BriefFields{}, err
	}
	return BriefFields{Title: cells[0], Kind: cells[1], TmplSHA: cells[2], BriefSHA: cells[3], BriefSrc: cells[4], BriefAt: cells[5]}, nil
}

func hmget(ctx context.Context, st *store.Store, key string, fields ...string) ([]Field, error) {
	vals, err := st.Client().HMGet(ctx, key, fields...).Result()
	if err != nil {
		return nil, fmt.Errorf("HMGET %s: %w", key, err)
	}
	if len(vals) != len(fields) {
		return nil, fmt.Errorf("HMGET %s: %d cells, want %d", key, len(vals), len(fields))
	}
	out := make([]Field, len(vals))
	for i, v := range vals {
		if s, ok := v.(string); ok {
			out[i] = Field{Value: s, Set: true}
		}
	}
	return out, nil
}

// RecordBrief is render's round trip 2: FCALL ns_task_brief with the exact
// title and kind read in round trip 1. It returns the status and, on OK, the
// brief_src the Function derived.
func RecordBrief(ctx context.Context, st *store.Store, sprint, id, title, kind, tmplSHA, briefSHA string) (BriefStatus, string, error) {
	reply, err := st.Client().FCall(ctx, FunctionBrief, []string{Key(sprint, id)}, title, kind, tmplSHA, briefSHA).Result()
	if err != nil {
		return "", "", fmt.Errorf("%s %s/%s: %w", FunctionBrief, sprint, id, err)
	}
	row, ok := reply.([]any)
	if !ok || len(row) == 0 {
		return "", "", fmt.Errorf("%s %s/%s: unexpected reply %v", FunctionBrief, sprint, id, reply)
	}
	status, _ := row[0].(string)
	switch BriefStatus(status) {
	case BriefOK:
		src := ""
		if len(row) > 1 {
			src, _ = row[1].(string)
		}
		return BriefOK, src, nil
	case BriefMissing, BriefStale:
		return BriefStatus(status), "", nil
	}
	return "", "", fmt.Errorf("%s %s/%s: unexpected status %q", FunctionBrief, sprint, id, status)
}

// BriefSource is the digest ns_task_brief stores as brief_src:
// hex(sha1(kind + "\x00" + title)). Lint recomputes it from the live task.
func BriefSource(kind, title string) string {
	h := sha1.Sum([]byte(kind + "\x00" + title))
	return hex.EncodeToString(h[:])
}

// fieldBoundaryRx is the start of the next `| NAME:` segment of a title.
var fieldBoundaryRx = regexp.MustCompile(`\|\s*\**[A-Za-z][A-Za-z0-9-]*:`)

// TitleField returns the text of the `| NAME: ...` segment of a title. Unlike
// titleField (which PATHS and DEPENDS-ON keep, so their normalisation is the
// push lint's exactly) a segment ends only where the next `| NAME:` begins, so
// a DONE-WHEN that quotes `-run 'A|B'` is not cut at its own pipes.
func TitleField(title, name string) (string, bool) {
	starts := []int{0}
	for _, m := range fieldBoundaryRx.FindAllStringIndex(title, -1) {
		starts = append(starts, m[0])
	}
	for i, s := range starts {
		end := len(title)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		seg := strings.TrimSpace(strings.TrimPrefix(title[s:end], "|"))
		seg = strings.TrimPrefix(seg, "**")
		if rest, ok := strings.CutPrefix(seg, name+":"); ok {
			return strings.TrimSpace(strings.ReplaceAll(rest, "**", "")), true
		}
	}
	return "", false
}
