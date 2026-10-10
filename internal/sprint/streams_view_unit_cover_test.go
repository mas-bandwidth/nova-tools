package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSprintStreamsViewCoverStreamTitle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"THE TASK. Do one thing. Then more.", "THE TASK. Do one thing. Then more.", "Do one thing"},
		{"THE TASK: Do it. indented by spaces", "  THE TASK: Do it. More stuff.", "Do it"},
		{"brief with task line after other lines", "Some intro.\nTHE TASK. This is the task.\nMore.", "This is the task"},
		{"no THE TASK line", "Just a regular brief.", ""},
		{"THE TASK with no content after", "THE TASK.", ""},
		{"THE TASK: with no content after", "THE TASK:", ""},
		{"task with only final dot", "THE TASK. Task.", "Task"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StreamTitle(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSprintStreamsViewCoverRepoName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"owner/name unchanged", "owner/name", "owner/name"},
		{"https URL with .git", "https://example.com/owner/name.git", "owner/name"},
		{"scp-style git@host:owner/name.git", "git@example.com:owner/name.git", "owner/name"},
		{"clone path with trailing slash", "https://example.com/owner/name/", "owner/name"},
		{"single word unchanged", "justone", "justone"},
		{"value with surrounding spaces", "  owner/name  ", "owner/name"},
		{"owner/name with git suffix", "owner/name.git", "owner/name"},
		{"git@host:owner/name without .git", "git@example.com:owner/name", "owner/name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RepoName(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSprintStreamsViewCoverStreamsOf(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		build func(w *world) StreamsReq
		check func(t *testing.T, got StreamsView)
	}{
		{"Repos filter keeps matching and drops other", func(w *world) StreamsReq {
			w.must(Add(w.s, AddReq{Stream: "alpha", IDs: []string{"a1"}, Repo: "owner/name", Brief: "THE TASK. A1."}))
			w.must(Add(w.s, AddReq{Stream: "beta", IDs: []string{"b1"}, Repo: "other/repo", Brief: "THE TASK. B1."}))
			return StreamsReq{Repos: []string{"owner/name"}}
		}, func(t *testing.T, got StreamsView) {
				assert.Len(t, got.Streams, 1)
		}},
		{"Release filter keeps matching only", func(w *world) StreamsReq {
			w.must(Add(w.s, AddReq{Stream: "alpha", IDs: []string{"a1"}, Brief: "THE TASK. A1."}))
			w.s.T(Merge).Card(CtlID("alpha")).Fields["release"] = "v1"
			w.must(Add(w.s, AddReq{Stream: "beta", IDs: []string{"b1"}, Brief: "THE TASK. B1."}))
			w.s.T(Merge).Card(CtlID("beta")).Fields["release"] = "v2"
			return StreamsReq{Release: "v1"}
		}, func(t *testing.T, got StreamsView) {
				assert.Len(t, got.Streams, 1)
		}},
		{"Cards true lists each card", func(w *world) StreamsReq {
			w.must(Add(w.s, AddReq{Stream: "alpha", IDs: []string{"a1"}, Brief: "THE TASK. A1."}))
			return StreamsReq{Cards: true}
		}, func(t *testing.T, got StreamsView) {
				assert.Len(t, got.Streams, 1)
			s := got.Streams[0]
			assert.Len(t, s.Cards, 1)
		}},
		{"stream whose cards name two repos is Mixed", func(w *world) StreamsReq {
			w.must(Add(w.s, AddReq{Stream: "alpha", IDs: []string{"a1"}, Repo: "owner/name", Brief: "THE TASK. A1."}))
			w.must(Add(w.s, AddReq{Stream: "alpha", IDs: []string{"a2"}, Repo: "other/repo", Brief: "THE TASK. A2."}))
			return StreamsReq{}
		}, func(t *testing.T, got StreamsView) {
				assert.Len(t, got.Mixed, 1)
		}},
		{"StreamBasesOf answers the bases", func(w *world) StreamsReq {
			// Setup: add a stream and set bases on control card
			w.must(Add(w.s, AddReq{Stream: "beta", IDs: []string{"b1"}, Brief: "THE TASK. B1."}))
			ctl := w.s.T(Merge).Card(CtlID("beta"))
			if ctl != nil {
				ctl.Fields["base"] = "dev,main"
			}
			return StreamsReq{}
		}, func(t *testing.T, got StreamsView) {
				assert.Len(t, got.Streams, 1)
			s := got.Streams[0]
			assert.Len(t, s.Bases, 2)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			req := tt.build(w)
			got := StreamsOf(w.s, req)
			tt.check(t, got)
		})
	}
}
