package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCairnReceiptJSONIncludesText(t *testing.T) {
	t.Parallel()
	c := newRig(t)
	c.ok("open", "--session", "s", "--publish", "manual")
	words := "line one\nline \"two\"  \n"
	c.ok("append", "--session", "s", "--entry", "e", "--text", words, "--publish", "manual")
	path := c.path("entries", "s", "e.json")
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	plain := c.ok("receipt", "--session", "s", "--entry", "e")
	require.NotContains(t, plain, " text=")
	require.Equal(t, plain, c.ok("receipt", "--session", "s", "--entry", "e", "--text=false"))
	baseline := c.run("receipt", "--session", "s", "--entry", "e", "--json", "--text=false")
	require.Zero(t, baseline.Code, baseline.Stderr)
	var metadata struct {
		Facts map[string]any `json:"facts"`
	}
	require.NoError(t, json.Unmarshal([]byte(baseline.Stdout), &metadata))
	for _, tc := range []struct {
		name  string
		flags []string
		text  bool
	}{
		{"default", nil, true}, {"explicit", []string{"--text"}, true}, {"optout", []string{"--text=false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			args := append([]string{"--session", "s", "--entry", "e", "--json"}, tc.flags...)
			result := c.run("receipt", args...)
			require.Zero(t, result.Code, result.Stderr)
			var out struct {
				Facts map[string]any `json:"facts"`
			}
			require.NoError(t, json.Unmarshal([]byte(result.Stdout), &out))
			if tc.text {
				require.Equal(t, words, out.Facts["text"])
			} else {
				require.NotContains(t, out.Facts, "text")
			}
			require.Equal(t, float64(len(words)), out.Facts["bytes"])
			delete(out.Facts, "text")
			require.Equal(t, metadata.Facts, out.Facts, "adding text must preserve every metadata fact")
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestCairnIndexSinceCursorCLI(t *testing.T) {
	t.Parallel()
	c := newRig(t)
	c.ok("open", "--session", "s", "--publish", "manual")
	for _, id := range []string{"a", "b", "c"} {
		c.ok("append", "--session", "s", "--entry", id, "--text", id, "--publish", "manual", "--now", "2026-10-13T12:00:00Z")
	}
	result := c.run("index", "--since", "a", "--max", "1", "--json")
	require.Zero(t, result.Code, result.Stderr)
	var out struct {
		Facts map[string]any   `json:"facts"`
		Items []map[string]any `json:"items"`
		More  []map[string]any `json:"more"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Stdout), &out))
	require.Equal(t, float64(2), out.Facts["entries"])
	var ids []string
	for _, item := range out.Items {
		if item["kind"] == "entry" {
			ids = append(ids, item["fields"].(map[string]any)["entry"].(string))
		}
	}
	require.Equal(t, []string{"b"}, ids)
	require.Len(t, out.More, 1)
	require.Equal(t, "entry", out.More[0]["kind"])
	require.Equal(t, float64(2), out.More[0]["total"])
	require.Equal(t, float64(1), out.More[0]["shown"])
	require.Equal(t, float64(2), out.Items[0]["fields"].(map[string]any)["entries"])
	next := c.ok("index", "--since", "b", "--max", "1", "--json")
	require.Contains(t, next, `"entry":"c"`)
	require.NotContains(t, next, `"entry":"b"`)
	empty := c.ok("index", "--since", "2026-10-13T12:00:00Z", "--json")
	require.NoError(t, json.Unmarshal([]byte(empty), &out))
	require.Equal(t, float64(0), out.Facts["entries"])
	for _, cursor := range []string{"", "invalid cursor", "missing"} {
		bad := c.run("index", "--since", cursor)
		require.Equal(t, 2, bad.Code, bad.Stdout)
	}
}

func TestCairnReceiptJSONIncludesFlatText(t *testing.T) {
	t.Parallel()
	c := newRig(t)
	raw := "# hand record\n\n## 2026-10-13T12:00:00Z — first\n\n  quoted \"words\"\nsecond line  \n\n## 2026-10-13T12:00:00Z — next\n\nnext words\n"
	path := c.path("flat.md")
	require.NoError(t, os.WriteFile(path, []byte(raw), 0600))
	result := c.run("receipt", "--session", "flat", "--entry", "first", "--json")
	require.Zero(t, result.Code, result.Stderr)
	var out struct {
		Facts map[string]any `json:"facts"`
	}
	require.NoError(t, json.Unmarshal([]byte(result.Stdout), &out))
	words := "quoted \"words\"\nsecond line"
	require.Equal(t, words, out.Facts["text"])
	require.Equal(t, float64(len(words)), out.Facts["bytes"])
	require.Equal(t, "unknown", out.Facts["publish"])
	index := c.ok("index", "--since", "first", "--json")
	require.Contains(t, index, `"entry":"next"`)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, raw, string(after))
}
