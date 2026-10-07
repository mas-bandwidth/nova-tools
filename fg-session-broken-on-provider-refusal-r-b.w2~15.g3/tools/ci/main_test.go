package main

import (
	"bytes"
	"strings"
	"testing"
)

func testEnv() (env, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	return env{stdout: &out, stderr: &errb, getenv: func(string) string { return "" }}, &out, &errb
}

func TestNoVerbPrintsTheBannerAndRefuses(t *testing.T) {
	t.Parallel()
	e, _, errb := testEnv()
	if code := run(nil, e); code != 2 {
		t.Fatalf("no verb: exit %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "usage: go run ./tools/ci <verb>") {
		t.Fatalf("no verb: no banner on stderr:\n%s", errb.String())
	}
}

func TestUnknownVerbIsRefusedByName(t *testing.T) {
	t.Parallel()
	e, _, errb := testEnv()
	if code := run([]string{"no-such-verb"}, e); code != 2 {
		t.Fatalf("unknown verb: exit %d, want 2", code)
	}
	if !strings.Contains(errb.String(), `unknown verb "no-such-verb"`) {
		t.Fatalf("unknown verb: the refusal does not name it:\n%s", errb.String())
	}
}

func TestEveryVerbHasASummaryAndHelp(t *testing.T) {
	t.Parallel()
	for _, n := range names() {
		v := registry[n]
		if strings.TrimSpace(v.summary) == "" || strings.TrimSpace(v.help) == "" || v.do == nil {
			t.Errorf("verb %s: summary, help and do are all required", n)
		}
		e, out, _ := testEnv()
		if code := run([]string{"help", n}, e); code != 0 || out.String() != v.help {
			t.Errorf("help %s: exit %d, printed %q", n, code, out.String())
		}
	}
}
