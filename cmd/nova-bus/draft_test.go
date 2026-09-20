package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Issue #327: nova-bus draft prints the note to stdout and writes no file,
// so send has no path unless the caller redirected stdout or provided --out.
// These tests verify that:
// 1. When no --out is given, draft prints the skeleton to stdout and hints the
//    redirect to send on stderr, so the silence between printing and writing is closed.
// 2. When --out is given, draft writes the skeleton to that file outside the bus,
//    prints DRAFT OK path=<file>, and exits 0.
// 3. When --out points inside the bus checkout, draft refuses with code 2.

func TestDraftWithoutFilePrintsSkeletonToStdoutAndHintsSendOnStderr(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	r := invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate").
		mustCode(t, 0).
		mustContain(t, "stdout", "To: Bo").
		mustContain(t, "stdout", "From: Ada").
		mustContain(t, "stdout", "Subject: gate").
		mustContain(t, "stderr", "DRAFT NOTE redirect this to a file, then send: nova-bus send --file <that file>")

	if strings.Contains(r.stdout, "DRAFT OK") {
		t.Fatalf("stdout without --out should not contain DRAFT OK:\n%s", r.stdout)
	}
}

func TestDraftWritesFileWhenOutFlagGiven(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	draftFile := filepath.Join(t.TempDir(), "draft.md")
	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", draftFile).
		mustCode(t, 0).
		mustContain(t, "stdout", "DRAFT OK path="+draftFile)

	data, err := os.ReadFile(draftFile)
	if err != nil {
		t.Fatalf("draft file was not written: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "To: Bo") || !strings.Contains(content, "From: Ada") || !strings.Contains(content, "Subject: gate") {
		t.Fatalf("draft file content missing expected headers:\n%s", content)
	}
}

// TestDraftUsageShowsRedirectSynopsis holds the synopsis promise: the draft line of
// `nova-bus help` must say the skeleton goes to stdout, so a reader Redirects it to a file
// instead of expecting draft to write one. The released synopsis spells the redirect
// `> <file>`.
func TestDraftUsageShowsRedirectSynopsis(t *testing.T) {
	t.Parallel()
	r := invoke(t, "", "help").mustCode(t, 0)
	if !strings.Contains(r.stdout, "> <file>") {
		t.Fatalf("draft synopsis does not show the redirect `> <file>`:\n%s", r.stdout)
	}
}

// TestDraftPrintsSendHintOnStderr closes the silence the card names: after the skeleton
// is printed to stdout and no --out was given, draft prints one line to stderr naming
// the next step, so a sender who just ran it knows the skeleton is a draft to redirect
// and then send.
func TestDraftPrintsSendHintOnStderr(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	r := invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate").
		mustCode(t, 0).
		mustContain(t, "stderr", "nova-bus send --file")
	lines := strings.Split(strings.TrimRight(r.stderr, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("draft without --out should print exactly one hint line to stderr, got %d: %q", len(lines), r.stderr)
	}
}

func TestDraftRefusesFileInsideBusCheckout(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	insideFile := filepath.Join(checkout, "from-ada", "draft.md")
	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", insideFile).
		mustCode(t, 2).
		mustContain(t, "stderr", "DRAFT REFUSED: --out")
}

func TestDraftOverwriteWithoutOutRefuses(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	r := invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--overwrite").
		mustCode(t, 2).
		mustContain(t, "stderr", "--overwrite requires --out")

	if strings.Contains(r.stdout, "DRAFT OK") {
		t.Fatalf("stdout without --out should not contain DRAFT OK:\n%s", r.stdout)
	}
}

func TestDraftDanglingSymlinkRefusesOverwrite(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	dir := t.TempDir()
	target := filepath.Join(dir, "nonexistent-target.md")
	link := filepath.Join(dir, "dangling-link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", link).
		mustCode(t, 1).
		mustContain(t, "stderr", "DRAFT REFUSED: "+link+" exists; pass --overwrite to replace it")

	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("dangling symlink target was created: %s", target)
	}

	// Also verify a dangling symlink pointing inside the bus checkout is not followed.
	insideTarget := filepath.Join(checkout, "from-ada", "planted-via-link.md")
	busLink := filepath.Join(dir, "link-to-bus.md")
	if err := os.Symlink(insideTarget, busLink); err == nil {
		invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", busLink).
			mustCode(t, 1).
			mustContain(t, "stderr", "DRAFT REFUSED: "+busLink+" exists; pass --overwrite to replace it")

		if _, err := os.Lstat(insideTarget); !os.IsNotExist(err) {
			t.Fatalf("dangling symlink target inside bus was created: %s", insideTarget)
		}
	}
}

func TestDraftAtomicCreationRace(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	target := filepath.Join(t.TempDir(), "race-draft.md")
	const concurrency = 8
	type outcome struct {
		code int
		out  string
		err  string
	}
	ch := make(chan outcome, concurrency)
	var start sync.WaitGroup
	start.Add(1)

	for i := 0; i < concurrency; i++ {
		go func() {
			start.Wait()
			var stdout, stderr bytes.Buffer
			code := run([]string{"draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", target},
				strings.NewReader(""), &stdout, &stderr, now())
			ch <- outcome{code: code, out: stdout.String(), err: stderr.String()}
		}()
	}
	start.Done()

	var successCount, refusedCount int
	for i := 0; i < concurrency; i++ {
		res := <-ch
		switch res.code {
		case 0:
			successCount++
			if !strings.Contains(res.out, "DRAFT OK path="+target) {
				t.Errorf("success run missing DRAFT OK: %s", res.out)
			}
		case 1:
			refusedCount++
			if !strings.Contains(res.err, "exists; pass --overwrite to replace it") {
				t.Errorf("refused run missing exists message: %s", res.err)
			}
		default:
			t.Errorf("unexpected exit code %d: stdout=%q, stderr=%q", res.code, res.out, res.err)
		}
	}

	if successCount != 1 {
		t.Fatalf("expected exactly 1 successful draft creation, got %d (refused=%d)", successCount, refusedCount)
	}
	if refusedCount != concurrency-1 {
		t.Fatalf("expected %d refused draft creations, got %d", concurrency-1, refusedCount)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("failed to read created draft: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "To: Bo") || !strings.Contains(content, "From: Ada") || !strings.Contains(content, "Subject: gate") {
		t.Fatalf("target file content missing expected headers:\n%s", content)
	}
}

