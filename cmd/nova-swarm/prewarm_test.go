package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

func TestBenchPrewarmHandsThePinnedTipToTheCacheBuilder(t *testing.T) {
	tip := strings.Repeat("a", 40)
	original := benchPrewarm
	t.Cleanup(func() { benchPrewarm = original })
	var got swarm.PrewarmInput
	benchPrewarm = func(in swarm.PrewarmInput) (swarm.PrewarmResult, error) {
		got = in
		return swarm.PrewarmResult{Checkout: "/pool/ref/acme/tool@" + tip, Tip: tip, Phases: 4}, nil
	}
	var stdout, stderr bytes.Buffer
	exit := run([]string{"bench", "prewarm", "--root", "/pool", "--source", "/mirror/tool", "--repo", "acme/tool", "--tip", tip}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if exit != 0 {
		t.Fatalf("bench prewarm exit=%d stderr=%s", exit, stderr.String())
	}
	if got.Root != "/pool" || got.Source != "/mirror/tool" || got.Repo != "acme/tool" || got.Tip != tip {
		t.Fatalf("Prewarm input = %+v", got)
	}
	for _, want := range []string{"BENCH PREWARM OK", "repo=acme/tool", "tip=" + tip, "phases=4"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout does not contain %q: %s", want, stdout.String())
		}
	}
}

func TestBenchPrewarmRefusesEveryMissingPinnedInput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := run([]string{"bench", "prewarm"}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if exit != 2 {
		t.Fatalf("bench prewarm without inputs exit=%d, want 2", exit)
	}
	for _, want := range []string{"--root is required", "--source is required", "--repo is required", "--tip is required"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr does not contain %q: %s", want, stderr.String())
		}
	}
}
