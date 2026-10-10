package ghevent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGheventEventCoverFieldsPing(t *testing.T) {
	t.Parallel()
	e := Entry{
		Repo:      "test/repo",
		Kind:      "ping",
		Number:    "123",
		Head:      "head",
		Action:    "opened",
		At:        "2024-01-01T00:00:00Z",
		Sender:    "sender",
		CommentID: "456",
	}
	v, err := Fields(e)
	require.NoError(t, err)

	expected := map[string]interface{}{
		"repo":       "test/repo",
		"kind":       "ping",
		"number":     "123",
		"head":       "head",
		"action":     "opened",
		"at":         "2024-01-01T00:00:00Z",
		"sender":     "sender",
		"comment_id": "456",
	}
	assert.Equal(t, expected, v)
}

func TestGheventEventCoverFieldsIssues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		entry    Entry
		wantKeys []string
	}{
		{
			name: "nil labels writes []",
			entry: Entry{
				Repo:        "test/repo",
				Kind:        "issues",
				Action:      "opened",
				Body:        "body",
				State:       "open",
				StateReason: "not_planned",
			},
			wantKeys: []string{"repo", "kind", "number", "head", "action", "at", "sender", "comment_id", "labels", "body", "state", "state_reason"},
		},
		{
			name: "two labels writes JSON array in order",
			entry: Entry{
				Repo:        "test/repo",
				Kind:        "issues",
				Action:      "opened",
				Labels:      []string{"bug", "help wanted"},
				Body:        "body",
				State:       "open",
				StateReason: "not_planned",
			},
			wantKeys: []string{"repo", "kind", "number", "head", "action", "at", "sender", "comment_id", "labels", "body", "state", "state_reason"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v, err := Fields(tt.entry)
			require.NoError(t, err)

			assert.Len(t, v, len(tt.wantKeys))
			for _, k := range tt.wantKeys {
				_, ok := v[k]
				assert.True(t, ok, "missing key: %s", k)
			}

			if tt.name == "two labels writes JSON array in order" {
				b, _ := json.Marshal([]string{"bug", "help wanted"})
				assert.Equal(t, string(b), v["labels"])
			}
		})
	}
}

func TestGheventEventCoverFieldsWorkflowRun(t *testing.T) {
	t.Parallel()
	e := Entry{
		Repo:       "test/repo",
		Kind:       "workflow_run",
		Number:     "123",
		Head:       "head",
		Action:     "completed",
		At:         "2024-01-01T00:00:00Z",
		Sender:     "sender",
		CommentID:  "456",
		RunID:      "789",
		Workflow:   "workflow",
		Status:     "completed",
		Conclusion: "success",
	}
	v, err := Fields(e)
	require.NoError(t, err)

	expectedKeys := []string{"repo", "kind", "number", "head", "action", "at", "sender", "comment_id", "run_id", "workflow", "status", "conclusion"}
	assert.Len(t, v, len(expectedKeys))
	for _, k := range expectedKeys {
		_, ok := v[k]
		assert.True(t, ok, "missing key: %s", k)
	}
	assert.Equal(t, "789", v["run_id"])
	assert.Equal(t, "workflow", v["workflow"])
	assert.Equal(t, "completed", v["status"])
	assert.Equal(t, "success", v["conclusion"])
}

func TestGheventEventCoverFieldsPullRequest(t *testing.T) {
	t.Parallel()
	e := Entry{
		Repo:      "test/repo",
		Kind:      "pull_request",
		Number:    "123",
		Head:      "head",
		Action:    "closed",
		At:        "2024-01-01T00:00:00Z",
		Sender:    "sender",
		CommentID: "456",
		Branch:    "feature",
		Base:      "main",
		Merged:    "true",
	}
	v, err := Fields(e)
	require.NoError(t, err)

	expectedKeys := []string{"repo", "kind", "number", "head", "action", "at", "sender", "comment_id", "branch", "base", "merged"}
	assert.Len(t, v, len(expectedKeys))
	for _, k := range expectedKeys {
		_, ok := v[k]
		assert.True(t, ok, "missing key: %s", k)
	}
	assert.Equal(t, "feature", v["branch"])
	assert.Equal(t, "main", v["base"])
	assert.Equal(t, "true", v["merged"])
}

func TestGheventEventCoverFieldsCheckRun(t *testing.T) {
	t.Parallel()
	e := Entry{
		Repo:       "test/repo",
		Kind:       "check_run",
		Number:     "123",
		Head:       "head",
		Action:     "completed",
		At:         "2024-01-01T00:00:00Z",
		Sender:     "sender",
		CommentID:  "456",
		Check:      "lint",
		CheckRunID: "999",
		Status:     "completed",
		Conclusion: "success",
	}
	v, err := Fields(e)
	require.NoError(t, err)

	expectedKeys := []string{"repo", "kind", "number", "head", "action", "at", "sender", "comment_id", "check", "check_run_id", "status", "conclusion"}
	assert.Len(t, v, len(expectedKeys))
	for _, k := range expectedKeys {
		_, ok := v[k]
		assert.True(t, ok, "missing key: %s", k)
	}
	assert.Equal(t, "lint", v["check"])
	assert.Equal(t, "999", v["check_run_id"])
	assert.Equal(t, "completed", v["status"])
	assert.Equal(t, "success", v["conclusion"])
}

func TestGheventEventCoverPublishRefusalNilClient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, err := Publish(ctx, nil, Entry{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil redis client")
}

func TestGheventEventCoverPublishRefusalEmptyKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, fmt.Errorf("dial error")
	}})
	_, err := Publish(ctx, rdb, Entry{Kind: "", Action: "opened", Repo: "test/repo"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs repo, kind and action")
}

func TestGheventEventCoverPublishRefusalEmptyAction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, fmt.Errorf("dial error")
	}})
	_, err := Publish(ctx, rdb, Entry{Kind: "issues", Action: "", Repo: "test/repo"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs repo, kind and action")
}

func TestGheventEventCoverPublishRefusalEmptyRepo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Dialer: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, fmt.Errorf("dial error")
	}})
	_, err := Publish(ctx, rdb, Entry{Kind: "issues", Action: "opened", Repo: ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs repo, kind and action")
}
