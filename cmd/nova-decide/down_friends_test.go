package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// When friend:<f>:down is set in Redis, route with --store excludes friend <f>
// from routing and records excluded=<f>:down in the log row (#3397).
func TestRouteStoreExcludesDownFriend(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.Set("friend:emma:down", "out-of-credits")
	log := filepath.Join(t.TempDir(), "decide.jsonl")

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"route",
		"--unit-id", "adopt-3384-fix",
		"--kind", "fix-with-red-test",
		"--files", "17",
		"--packages", "3",
		"--no-jev",
		"--log", log,
		"--store", mr.Addr(),
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "rung=emma") {
		t.Errorf("emma is down and must be excluded, got stdout: %s", out)
	}
	if !strings.Contains(out, "rung=astra") && !strings.Contains(out, "rung=fable") {
		t.Errorf("want route to astra or fable, got stdout: %s", out)
	}

	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	rawStr := string(raw)
	if !strings.Contains(rawStr, "excluded=emma:down") {
		t.Errorf("the log row must say excluded=emma:down, got: %s", rawStr)
	}

	var entry decide.Entry
	if err := json.Unmarshal(bytes.TrimSpace(raw), &entry); err != nil {
		t.Fatalf("unmarshal log row: %v", err)
	}
	if entry.RungTried == "emma" {
		t.Errorf("rung_tried is %s, want astra or fable", entry.RungTried)
	}
	if !strings.Contains(entry.Reason, "excluded=emma:down") {
		t.Errorf("entry reason must contain excluded=emma:down, got: %s", entry.Reason)
	}
}

type recordingDecider struct {
	conf      float64
	questions map[string]decide.Question
}

func (r *recordingDecider) Decide(_ context.Context, _ string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
	r.questions = qs
	var names []string
	for name := range qs[decide.RungQuestion].Choice {
		names = append(names, name)
	}
	sort.Strings(names)
	choice := ""
	if len(names) > 0 {
		choice = names[0]
	}
	return map[string]decide.Answer{
		decide.RungQuestion: {Type: "choice", Choice: choice, Confidence: r.conf},
	}, decide.Usage{InputTokens: 100, HasInput: true, OutputTokens: 10, HasOutput: true}, nil
}

// When friend:<f>:down is set in Redis, Jev is not offered friend <f> in the
// criteria and the decision routes to astra or fable (#3397).
func TestRouteStoreExcludesDownFriendFromJevCriteria(t *testing.T) {
	mr := miniredis.RunT(t)
	mr.Set("friend:emma:down", "out-of-credits")
	log := filepath.Join(t.TempDir(), "decide.jsonl")
	usage := filepath.Join(t.TempDir(), "usage.tsv")

	rec := &recordingDecider{conf: 0.85}
	was := deciderOpener
	deciderOpener = func(string, string) (decide.Decider, error) { return rec, nil }
	t.Cleanup(func() { deciderOpener = was })

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"route",
		"--unit-id", "adopt-3384-fix",
		"--kind", "fix-with-red-test",
		"--files", "17",
		"--packages", "3",
		"--usage", usage,
		"--log", log,
		"--store", mr.Addr(),
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d (stderr=%q)", code, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "rung=emma") {
		t.Errorf("emma is down and must be excluded, got stdout: %s", out)
	}
	if !strings.Contains(out, "rung=astra") && !strings.Contains(out, "rung=fable") {
		t.Errorf("want route to astra or fable, got stdout: %s", out)
	}

	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	rawStr := string(raw)
	if !strings.Contains(rawStr, "excluded=emma:down") {
		t.Errorf("the log row must say excluded=emma:down, got: %s", rawStr)
	}

	var entry decide.Entry
	if err := json.Unmarshal(bytes.TrimSpace(raw), &entry); err != nil {
		t.Fatalf("unmarshal log row: %v", err)
	}
	if entry.RungTried == "emma" {
		t.Errorf("rung_tried is %s, want astra or fable", entry.RungTried)
	}
	if !strings.Contains(entry.Reason, "excluded=emma:down") {
		t.Errorf("entry reason must contain excluded=emma:down, got: %s", entry.Reason)
	}
}
