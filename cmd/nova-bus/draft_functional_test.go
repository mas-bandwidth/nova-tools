//go:build functional

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	require.NotContainsf(t, r.stdout, "DRAFT OK", "stdout without --out should not contain DRAFT OK:\n%s", r.stdout)
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
	require.NoErrorf(t, err, "draft file was not written: %v", err)
	content := string(data)
	require.Falsef(t, !strings.Contains(content, "To: Bo") || !strings.Contains(content, "From: Ada") || !strings.Contains(content, "Subject: gate"), "draft file content missing expected headers:\n%s", content)
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
	require.Equalf(t, 1, len(lines), "draft without --out should print exactly one hint line to stderr, got %d: %q", len(lines), r.stderr)
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

	require.NotContainsf(t, r.stdout, "DRAFT OK", "stdout without --out should not contain DRAFT OK:\n%s", r.stdout)
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

	{
		_, err := os.Lstat(target)
		require.Truef(t, os.IsNotExist(err), "dangling symlink target was created: %s", target)
	}

	// Also verify a dangling symlink pointing inside the bus checkout is not followed.
	insideTarget := filepath.Join(checkout, "from-ada", "planted-via-link.md")
	busLink := filepath.Join(dir, "link-to-bus.md")
	if err := os.Symlink(insideTarget, busLink); err == nil {
		invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", busLink).
			mustCode(t, 1).
			mustContain(t, "stderr", "DRAFT REFUSED: "+busLink+" exists; pass --overwrite to replace it")

		{
			_, err := os.Lstat(insideTarget)
			require.Truef(t, os.IsNotExist(err), "dangling symlink target inside bus was created: %s", insideTarget)
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
			assert.Containsf(t, res.out, "DRAFT OK path="+target, "success run missing DRAFT OK: %s", res.out)
		case 1:
			refusedCount++
			assert.Containsf(t, res.err, "exists; pass --overwrite to replace it", "refused run missing exists message: %s", res.err)
		default:
			assert.Failf(t, "assertion failed", "unexpected exit code %d: stdout=%q, stderr=%q", res.code, res.out, res.err)
		}
	}

	require.Falsef(t, successCount != 1, "expected exactly 1 successful draft creation, got %d (refused=%d)", successCount, refusedCount)
	require.Falsef(t, refusedCount != concurrency-1, "expected %d refused draft creations, got %d", concurrency-1, refusedCount)

	data, err := os.ReadFile(target)
	require.NoErrorf(t, err, "failed to read created draft: %v", err)
	content := string(data)
	require.Falsef(t, !strings.Contains(content, "To: Bo") || !strings.Contains(content, "From: Ada") || !strings.Contains(content, "Subject: gate"), "target file content missing expected headers:\n%s", content)
}

func TestDraftOverwriteSymlinkReplacesLinkWithoutTouchingTarget(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	dir := t.TempDir()
	target := filepath.Join(dir, "sensitive-target.md")
	initialTargetContent := "SENSITIVE TARGET CONTENT - DO NOT OVERWRITE"
	{
		err := os.WriteFile(target, []byte(initialTargetContent), 0o644)
		require.NoErrorf(t, err, "failed to create target file: %v", err)
	}

	link := filepath.Join(dir, "symlink-draft.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", link, "--overwrite").
		mustCode(t, 0).
		mustContain(t, "stdout", "DRAFT OK path="+link)

	// Verify target was NEVER overwritten
	targetData, err := os.ReadFile(target)
	require.NoErrorf(t, err, "failed to read target: %v", err)
	require.Falsef(t, string(targetData) != initialTargetContent, "target content was altered! got %q, want %q", string(targetData), initialTargetContent)

	// Verify link is now a regular file, not a symlink
	fi, err := os.Lstat(link)
	require.NoErrorf(t, err, "failed to lstat link: %v", err)
	require.Falsef(t, fi.Mode()&os.ModeSymlink != 0, "expected link to be replaced with a regular file, but it is still a symlink")

	linkData, err := os.ReadFile(link)
	require.NoErrorf(t, err, "failed to read replaced file: %v", err)
	require.Falsef(t, !strings.Contains(string(linkData), "To: Bo") || !strings.Contains(string(linkData), "From: Ada"), "replaced file missing draft content:\n%s", string(linkData))
}

func TestDraftOverwriteDirectoryRefuses(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)

	dir := filepath.Join(t.TempDir(), "some-dir")
	{
		err := os.Mkdir(dir, 0o755)
		require.NoErrorf(t, err, "failed to create dir: %v", err)
	}

	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", dir, "--overwrite").
		mustCode(t, 1).
		mustContain(t, "stderr", "is a directory")
}
