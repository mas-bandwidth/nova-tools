package ghevent_test

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
)

func keys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

var common = []string{"repo", "kind", "number", "head", "action", "at", "sender", "comment_id"}

// TestFieldsKeySetPerKind: every entry carries the eight common fields; the
// four typed kinds add all of their own fields, empty included, so a reader
// can rely on the key set; labels are a JSON array, [] when nil; any other
// kind adds nothing.
func TestFieldsKeySetPerKind(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		entry ghevent.Entry
		extra []string
		pins  map[string]interface{}
	}{
		{"ping adds nothing", ghevent.Entry{Kind: "ping", Action: "ping"}, nil, map[string]interface{}{"repo": "", "number": ""}},
		{"unknown kind adds nothing", ghevent.Entry{Kind: "star", Action: "created", Repo: "o/r"}, nil, nil},
		{"issues with nil labels", ghevent.Entry{Kind: "issues", Action: "opened", Repo: "o/r", Number: "7", Body: "b", State: "open"},
			[]string{"labels", "body", "state", "state_reason"}, map[string]interface{}{"labels": "[]", "body": "b", "state": "open", "state_reason": ""}},
		{"issues with labels", ghevent.Entry{Kind: "issues", Action: "labeled", Repo: "o/r", Labels: []string{"bug", "p1"}},
			[]string{"labels", "body", "state", "state_reason"}, map[string]interface{}{"labels": `["bug","p1"]`}},
		{"workflow_run", ghevent.Entry{Kind: "workflow_run", Action: "completed", Repo: "o/r", RunID: "9", Workflow: "ci", Status: "completed", Conclusion: "success"},
			[]string{"run_id", "workflow", "status", "conclusion"}, map[string]interface{}{"run_id": "9", "workflow": "ci", "conclusion": "success"}},
		{"pull_request closed unmerged", ghevent.Entry{Kind: "pull_request", Action: "closed", Repo: "o/r", Number: "3", Branch: "b", Base: "dev", Merged: "false"},
			[]string{"branch", "base", "merged"}, map[string]interface{}{"branch": "b", "base": "dev", "merged": "false", "number": "3"}},
		{"pull_request opened has an empty merged", ghevent.Entry{Kind: "pull_request", Action: "opened", Repo: "o/r"},
			[]string{"branch", "base", "merged"}, map[string]interface{}{"merged": ""}},
		{"check_run", ghevent.Entry{Kind: "check_run", Action: "completed", Repo: "o/r", Check: "unit", CheckRunID: "44", Status: "completed", Conclusion: "failure"},
			[]string{"check", "check_run_id", "status", "conclusion"}, map[string]interface{}{"check": "unit", "check_run_id": "44", "conclusion": "failure"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, err := ghevent.Fields(tc.entry)
			require.NoError(t, err)
			assert.ElementsMatch(t, append(append([]string{}, common...), tc.extra...), keys(v))
			assert.Equal(t, tc.entry.Kind, v["kind"])
			assert.Equal(t, tc.entry.Action, v["action"])
			for k, want := range tc.pins {
				assert.Equal(t, want, v[k], k)
			}
		})
	}
}

// TestPublishRefusesBeforeTouchingTheStore: a nil client is refused first;
// then an entry without kind or action, or without a repo for any kind but
// ping, is refused before anything is sent, so the client here is never
// dialed.
func TestPublishRefusesBeforeTouchingTheStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	id, err := ghevent.Publish(ctx, nil, ghevent.Entry{Repo: "o/r", Kind: "issues", Action: "opened"})
	assert.ErrorContains(t, err, "nil redis client")
	assert.Equal(t, "", id)

	// Lazy client: no connection is made until a command runs, and every
	// entry below is refused before the XADD.
	rdb := redis.NewClient(&redis.Options{Addr: "unreached.invalid:6379"})
	t.Cleanup(func() { _ = rdb.Close() })
	cases := []struct {
		name  string
		entry ghevent.Entry
	}{
		{"no kind", ghevent.Entry{Repo: "o/r", Action: "opened"}},
		{"no action", ghevent.Entry{Repo: "o/r", Kind: "issues"}},
		{"no repo on a typed kind", ghevent.Entry{Kind: "issues", Action: "opened"}},
		{"no repo on an unknown kind", ghevent.Entry{Kind: "star", Action: "created"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id, err := ghevent.Publish(ctx, rdb, tc.entry)
			assert.ErrorContains(t, err, "entry needs repo, kind and action")
			assert.Equal(t, "", id)
		})
	}
}

// TestStreamName: the stream is the wire package's one name.
func TestStreamName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "ev:github", ghevent.Stream)
}
