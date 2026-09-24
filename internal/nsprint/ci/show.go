package ci

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// RecordFields is the printed order of the verdict record (10.3).
var RecordFields = []string{
	"verdict", "attempt", "card", "repo", "pr", "head", "base", "tree",
	"pkg", "test", "wall_s", "bench", "log", "cut_at", "end_at",
	"flaky", "source", "reruns", "rerun", "why", "disposition", "at",
}

// Record is ci:<repo>:<head>:<gid> as read. Found is false when the key is absent.
type Record struct {
	Key    string
	Found  bool
	Fields map[string]string
	// Attempts are the ci receipts for the record's card, oldest first.
	Attempts []Receipt
}

// Receipt is one ci transition receipt from the sprint log.
type Receipt struct {
	ID       string
	Kind     string
	Attempt  string
	Reason   string
	Evidence string
	At       string
}

// Verdict is the record's verdict, or MISSING when the key or the field is
// absent: a missing verdict is never success and never failure (10.3 item 1).
func (r Record) Verdict() string {
	if !r.Found {
		return Missing
	}
	if v := r.Fields["verdict"]; v != "" {
		return v
	}
	return Missing
}

// Read reads the record and its attempt receipts. The receipts come from the
// sprint log named in the record's card field; the log is never trimmed
// before fold, so every attempt is there.
func Read(ctx context.Context, st *store.Store, repo, head string) (Record, error) {
	fields, err := civerdict.ReadHead(ctx, st.Client(), repo, head)
	if err != nil {
		return Record{}, err
	}
	if len(fields) == 0 {
		return Record{Key: "ci:" + repo + ":" + head, Found: false, Fields: nil}, nil
	}
	gid := fields["gid"]
	key := civerdict.Key(repo, head, gid)
	rec := Record{Key: key, Found: true, Fields: fields}
	sprint, label, ok := strings.Cut(fields["card"], "/")
	if !ok {
		return rec, nil
	}
	rec.Attempts, err = receipts(ctx, st.Client(), sprint, label)
	return rec, err
}

func receipts(ctx context.Context, client *redis.Client, sprint, label string) ([]Receipt, error) {
	key := "s:" + sprint + ":log"
	var out []Receipt
	start := "-"
	for {
		msgs, err := client.XRangeN(ctx, key, start, "+", 1000).Result()
		if err != nil {
			return nil, fmt.Errorf("XRANGE %s: %w", key, err)
		}
		for _, m := range msgs {
			kind := fmt.Sprint(m.Values["kind"])
			if fmt.Sprint(m.Values["id"]) != label || !strings.HasPrefix(kind, "ci ") {
				continue
			}
			out = append(out, Receipt{
				ID: m.ID, Kind: kind, Attempt: fmt.Sprint(m.Values["attempt"]),
				Reason: fmt.Sprint(m.Values["reason"]), Evidence: fmt.Sprint(m.Values["evidence"]),
				At: fmt.Sprint(m.Values["at"]),
			})
		}
		if len(msgs) < 1000 {
			return out, nil
		}
		start = "(" + msgs[len(msgs)-1].ID
	}
}

// WriteShow prints the record and one line per attempt receipt, and returns
// the exit code: 0 with a verdict, 5 MISSING when the key or verdict is
// absent (4.8).
func WriteShow(w io.Writer, rec Record) int {
	if !rec.Found {
		fmt.Fprintf(w, "%s MISSING\n", rec.Key)
		return ExitMissing
	}
	fmt.Fprintf(w, "%s %s\n", rec.Key, rec.Verdict())
	seen := map[string]bool{}
	for _, f := range RecordFields {
		seen[f] = true
		if v, ok := rec.Fields[f]; ok && v != "" {
			fmt.Fprintf(w, "  %s=%s\n", f, v)
		}
	}
	var extra []string
	for f := range rec.Fields {
		if !seen[f] {
			extra = append(extra, f)
		}
	}
	sort.Strings(extra)
	for _, f := range extra {
		fmt.Fprintf(w, "  %s=%s\n", f, rec.Fields[f])
	}
	for _, a := range rec.Attempts {
		fmt.Fprintf(w, "  attempt %s %s %s | %s | %s\n", a.Attempt, a.Kind, a.Reason, a.Evidence, a.ID)
	}
	if rec.Verdict() == Missing {
		return ExitMissing
	}
	return ExitOK
}

// LandReady answers the ci half of land-ready for one exact head (10.6 item
// 2): the record must exist for that head with verdict OK. PENDING, FAIL,
// FLAKY (until a typed disposition) and MISSING are never ready. When base
// is given the record must have been tested on that base; a moved base is
// the lander's retest (10.6 item 1), not a pass here.
func LandReady(ctx context.Context, st *store.Store, repo, head, base string) (bool, string, error) {
	fields, err := civerdict.ReadHead(ctx, st.Client(), repo, head)
	if err != nil {
		return false, "", err
	}
	if len(fields) == 0 {
		return false, "ci: MISSING", nil
	}
	verdict := fields["verdict"]
	recBase := fields["base"]
	switch {
	case verdict == "":
		return false, "ci: MISSING", nil
	case verdict == Flaky:
		return false, "ci: FLAKY, needs a typed disposition", nil
	case verdict != OK:
		return false, "ci: " + verdict, nil
	case base != "" && recBase != base:
		return false, "ci: OK on base " + short(recBase) + ", not " + short(base), nil
	}
	return true, "ci: OK", nil
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
