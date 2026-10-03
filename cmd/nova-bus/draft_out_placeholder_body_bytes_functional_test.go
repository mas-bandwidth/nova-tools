//go:build functional

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/require"
)

// Issue #2043:
// 1. draft --file silently overwrote existing files.
// 2. send/prepare/reply accepted untouched template placeholder `<the note goes here>`.
// 3. SEND OK did not report body_bytes.

func TestIssue2043_DraftRefusesRetiredFileFlag(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	target := filepath.Join(t.TempDir(), "note.md")

	r := invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--file", target)
	r.mustCode(t, 2).
		mustContain(t, "stderr", "nova-bus draft: --file is retired because --file means input on send; use --out <path> (or --out <path> --overwrite)")
}

func TestIssue2043_DraftWritesOutFlag(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	target := filepath.Join(t.TempDir(), "note.md")

	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", target).
		mustCode(t, 0).
		mustContain(t, "stdout", "DRAFT OK path="+target)

	data, err := os.ReadFile(target)
	require.NoErrorf(t, err, "target was not written: %v", err)
	require.Containsf(t, string(data), "Subject: gate", "target missing header: %s", string(data))
}

func TestIssue2043_DraftRefusesExistingFileWithoutOverwrite(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	target := filepath.Join(t.TempDir(), "note.md")
	const original = "my existing 5 KB note"
	require.NoError(t, os.WriteFile(target, []byte(original), 0o644))

	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", target).
		mustCode(t, 1).
		mustContain(t, "stderr", "DRAFT REFUSED: "+target+" exists; pass --overwrite to replace it")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Falsef(t, string(data) != original, "existing file was clobbered: got %q, want %q", string(data), original)
}

func TestIssue2043_DraftOverwritesWhenFlagGiven(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	target := filepath.Join(t.TempDir(), "note.md")
	require.NoError(t, os.WriteFile(target, []byte("old content"), 0o644))

	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", target, "--overwrite").
		mustCode(t, 0).
		mustContain(t, "stdout", "DRAFT OK path="+target)

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Containsf(t, string(data), "Subject: gate", "expected overwritten content, got: %s", string(data))
}

func TestIssue2043_SendRefusesUntouchedTemplatePlaceholder(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, remote := busDir(t)
	draft := filepath.Join(t.TempDir(), "draft.md")
	content := "From: Ada\nTo: Bo\nSubject: testing\n\n" + bus.PlaceholderBody + "\n"
	require.NoError(t, os.WriteFile(draft, []byte(content), 0o644))

	invoke(t, "", "send", "--bus", checkout, "--file", draft, "--remote", remote, "--branch", "main").
		mustCode(t, 1).
		mustContain(t, "stderr", "SEND FAILED").
		mustContain(t, "stderr", "the body is the unedited template placeholder (<the note goes here>)")
}

func TestIssue2043_PrepareRefusesUntouchedTemplatePlaceholder(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := filepath.Join(t.TempDir(), "draft.md")
	content := "From: Ada\nTo: Bo\nSubject: testing\n\n" + bus.PlaceholderBody + "\n"
	require.NoError(t, os.WriteFile(draft, []byte(content), 0o644))

	invoke(t, "", "prepare", "--bus", checkout, "--as", "Ada", "--file", draft).
		mustCode(t, 1).
		mustContain(t, "stderr", "PREPARE FAILED").
		mustContain(t, "stderr", "the body is the unedited template placeholder (<the note goes here>)")
}

func TestIssue2043_ReplyRefusesUntouchedTemplatePlaceholder(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, remote := busDir(t)
	bodyFile := filepath.Join(t.TempDir(), "body.md")
	content := bus.PlaceholderBody + "\n"
	require.NoError(t, os.WriteFile(bodyFile, []byte(content), 0o644))

	invoke(t, "", "reply", "--bus", checkout, "--as", "Ada", "--re", "bo-abcdef012345", "--file", bodyFile, "--remote", remote, "--branch", "main").
		mustCode(t, 1).
		mustContain(t, "stderr", "REPLY FAILED").
		mustContain(t, "stderr", "the body is the unedited template placeholder (<the note goes here>)")
}

func TestIssue2043_SendOKPrintsBodyBytes(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, remote := busDir(t)
	draft := filepath.Join(t.TempDir(), "draft.md")
	const bodyText = "This is a real note body with twenty-six characters."
	content := "From: Ada\nTo: Bo\nSubject: testing\n\n" + bodyText + "\n"
	require.NoError(t, os.WriteFile(draft, []byte(content), 0o644))

	r := invoke(t, "", "send", "--bus", checkout, "--file", draft, "--remote", remote, "--branch", "main").
		mustCode(t, 0).
		mustContain(t, "stdout", "SEND OK id=ada-").
		mustContain(t, "stdout", "body_bytes=")

	require.Containsf(t, r.stdout, "body_bytes=", "SEND OK missing body_bytes: %s", r.stdout)
}
