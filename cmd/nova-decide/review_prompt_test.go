package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/jevcalib"
	"github.com/mas-bandwidth/nova-tools/internal/prereview"
)

// TestMain keeps every review test off the machine's own jev.conf: the verb
// reads prompt= from $NOVA_JEV_CONF, and a test must not score with whatever
// prompt the bench it runs on is tuned to.
func TestMain(m *testing.M) {
	os.Setenv("NOVA_JEV_CONF", "none")
	os.Exit(m.Run())
}

const cachedHead = "0123456789abcdef0123456789abcdef01234567"

// cachedPR writes one pull request in the --pr-dir layout and a recorded
// answer for it, and returns the pr dir and the replay dir.
func cachedPR(t *testing.T, baseRef, ciConclusion string) (prDir, replay string) {
	t.Helper()
	root := t.TempDir()
	prDir, replay = filepath.Join(root, "prs"), filepath.Join(root, "answers")
	d := filepath.Join(prDir, "7")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	view, _ := json.Marshal(map[string]any{
		"number": 7, "headRefOid": cachedHead, "title": "a fix", "baseRefName": baseRef, "mergeable": "MERGEABLE",
		"body":  "Fixes the thing.\n\nPATHS: internal/x/x.go, internal/x/x_test.go\nDONE-WHEN: go test ./internal/x/ -run TestX passes\n",
		"files": []map[string]string{{"path": "internal/x/x.go"}, {"path": "internal/x/x_test.go"}},
	})
	diff := "--- a/internal/x/x_test.go\n+++ b/internal/x/x_test.go\n@@ -0,0 +1,2 @@\n+func TestX(t *testing.T) {\n+}\n"
	for name, body := range map[string]string{"view.json": string(view), "diff.txt": diff} {
		if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if ciConclusion != "" {
		runs := `{"total_count":1,"check_runs":[{"name":"ci-ok","status":"completed","conclusion":"` + ciConclusion + `","head_sha":"` + cachedHead + `"}]}`
		if err := os.WriteFile(filepath.Join(d, "check-runs.json"), []byte(runs), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := prereview.RecordFixture(replay, "mas-bandwidth/nova-tools", 7, 9, 0.8); err != nil {
		t.Fatal(err)
	}
	return prDir, replay
}

func ledgerRow(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &row); err != nil {
		t.Fatalf("ledger %q: %v", raw, err)
	}
	return row
}

// TestReviewPrDirDryRunReadsTheCacheAndNeverCallsGH: --pr-dir scores a cached
// pull request through the real review path with no gh at all (the --gh path
// does not exist), reads the rollup and the base from the cache, and refuses
// --post.
func TestReviewPrDirDryRunReadsTheCacheAndNeverCallsGH(t *testing.T) {
	noGH := filepath.Join(t.TempDir(), "no-such-gh")
	checks := "donewhen,selfcheck,paths,claims,ci,base,score"
	for _, c := range []struct {
		name, baseRef, ci, verdict, want string
	}{
		{"green", "dev", "success", "PASS", "checks=donewhen:ok,selfcheck:missing,paths:ok,claims:missing,ci:ok,base:ok,score:9"},
		{"ci-red", "dev", "failure", "BOUNCE", "score=7 "},
		{"stacked", "rowan/other", "success", "BOUNCE", "base:fail"},
		{"rollup-unread", "dev", "", "PASS", "ci:missing"},
	} {
		t.Run(c.name, func(t *testing.T) {
			prDir, replay := cachedPR(t, c.baseRef, c.ci)
			ledger := filepath.Join(t.TempDir(), "ledger.jsonl")
			var out, errb bytes.Buffer
			run([]string{"review", "--repo", "mas-bandwidth/nova-tools", "--pr", "7", "--pr-dir", prDir, "--gh", noGH,
				"--replay", replay, "--checks", checks, "--ledger-path", ledger}, &out, &errb)
			line := out.String()
			if !strings.Contains(line, "JEV head="+cachedHead+" verdict="+c.verdict) || !strings.Contains(line, c.want) {
				t.Fatalf("stdout=%q stderr=%q, want verdict %s and %q", line, errb.String(), c.verdict, c.want)
			}
		})
	}
	prDir, replay := cachedPR(t, "dev", "success")
	var out, errb bytes.Buffer
	if code := run([]string{"review", "--repo", "mas-bandwidth/nova-tools", "--pr", "7", "--pr-dir", prDir, "--post", "--replay", replay}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "--pr-dir") {
		t.Fatalf("--post with --pr-dir: exit=%d stderr=%q, want a refusal (2)", code, errb.String())
	}
}

// TestReviewPromptFromConf: the score question's prompt is --prompt, else the
// conf's prompt=, else the embedded default; a named prompt that does not
// resolve is a refusal, never a silent fall back.
func TestReviewPromptFromConf(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "p.txt")
	if err := os.WriteFile(file, []byte("Score it strictly.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileSha := jevcalib.Sha8([]byte("Score it strictly.\n"))
	conf := filepath.Join(dir, "jev.conf")
	if err := os.WriteFile(conf, []byte("pass_above=7\nprompt=p.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.conf")
	if err := os.WriteFile(bad, []byte("prompt=deadbeef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"conf", []string{"--conf", conf}, fileSha},
		{"env", nil, fileSha},
		{"flag-beats-conf", []string{"--conf", conf, "--prompt", jevcalib.SeedSha8}, jevcalib.SeedSha8},
		{"default", []string{"--conf", "none"}, jevcalib.DefaultSha8},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.name == "env" {
				t.Setenv("NOVA_JEV_CONF", conf)
			}
			prDir, replay := cachedPR(t, "dev", "success")
			ledger := filepath.Join(t.TempDir(), "ledger.jsonl")
			var out, errb bytes.Buffer
			args := append([]string{"review", "--repo", "mas-bandwidth/nova-tools", "--pr", "7", "--pr-dir", prDir, "--replay", replay, "--ledger-path", ledger}, c.args...)
			run(args, &out, &errb)
			if got := ledgerRow(t, ledger)["prompt8"]; got != c.want {
				t.Fatalf("prompt8=%v, want %s (stdout=%q stderr=%q)", got, c.want, out.String(), errb.String())
			}
		})
	}
	prDir, replay := cachedPR(t, "dev", "success")
	var out, errb bytes.Buffer
	code := run([]string{"review", "--repo", "mas-bandwidth/nova-tools", "--pr", "7", "--pr-dir", prDir, "--replay", replay, "--conf", bad}, &out, &errb)
	if code != 2 || !strings.Contains(errb.String(), "bad-prompt") {
		t.Fatalf("an unresolvable prompt=: exit=%d stderr=%q, want REVIEW REFUSED reason=bad-prompt", code, errb.String())
	}
}

// TestReviewDefaultPromptCarriesItsThreshold: the default prompt passes only
// above 8 (the threshold it was tuned with); another prompt keeps 7; an
// explicit --pass-above wins either way.
func TestReviewDefaultPromptCarriesItsThreshold(t *testing.T) {
	for _, c := range []struct {
		name    string
		args    []string
		verdict string
	}{
		{"default-prompt", nil, "UNSURE"},
		{"default-prompt-explicit-7", []string{"--pass-above", "7"}, "PASS"},
		{"seed-prompt", []string{"--prompt", jevcalib.SeedSha8}, "PASS"},
	} {
		t.Run(c.name, func(t *testing.T) {
			prDir, replay := cachedPR(t, "dev", "success")
			if err := prereview.RecordFixture(replay, "mas-bandwidth/nova-tools", 7, 8, 0.8); err != nil {
				t.Fatal(err)
			}
			var out, errb bytes.Buffer
			args := append([]string{"review", "--repo", "mas-bandwidth/nova-tools", "--pr", "7", "--pr-dir", prDir, "--replay", replay,
				"--ledger-path", filepath.Join(t.TempDir(), "l.jsonl")}, c.args...)
			run(args, &out, &errb)
			if !strings.Contains(out.String(), "verdict="+c.verdict+" score=8 ") {
				t.Fatalf("stdout=%q stderr=%q, want verdict=%s at score 8", out.String(), errb.String(), c.verdict)
			}
		})
	}
}
