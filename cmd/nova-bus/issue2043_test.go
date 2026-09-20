package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
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
	if err != nil {
		t.Fatalf("target was not written: %v", err)
	}
	if !strings.Contains(string(data), "Subject: gate") {
		t.Fatalf("target missing header: %s", string(data))
	}
}

func TestIssue2043_DraftRefusesExistingFileWithoutOverwrite(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	target := filepath.Join(t.TempDir(), "note.md")
	const original = "my existing 5 KB note"
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", target).
		mustCode(t, 1).
		mustContain(t, "stderr", "DRAFT REFUSED: "+target+" exists; pass --overwrite to replace it")

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatalf("existing file was clobbered: got %q, want %q", string(data), original)
	}
}

func TestIssue2043_DraftOverwritesWhenFlagGiven(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	target := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(target, []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}

	invoke(t, "", "draft", "--bus", checkout, "--as", "Ada", "--to", "Bo", "--subject", "gate", "--out", target, "--overwrite").
		mustCode(t, 0).
		mustContain(t, "stdout", "DRAFT OK path="+target)

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Subject: gate") {
		t.Fatalf("expected overwritten content, got: %s", string(data))
	}
}

func TestIssue2043_SendRefusesUntouchedTemplatePlaceholder(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, remote := busDir(t)
	draft := filepath.Join(t.TempDir(), "draft.md")
	content := "From: Ada\nTo: Bo\nSubject: testing\n\n" + bus.PlaceholderBody + "\n"
	if err := os.WriteFile(draft, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	invoke(t, "", "send", "--bus", checkout, "--file", draft, "--remote", remote, "--branch", "main").
		mustCode(t, 1).
		mustContain(t, "stderr", "SEND FAIL").
		mustContain(t, "stderr", "the body is the unedited template placeholder (<the note goes here>)")
}

func TestIssue2043_PrepareRefusesUntouchedTemplatePlaceholder(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	draft := filepath.Join(t.TempDir(), "draft.md")
	content := "From: Ada\nTo: Bo\nSubject: testing\n\n" + bus.PlaceholderBody + "\n"
	if err := os.WriteFile(draft, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	invoke(t, "", "prepare", "--bus", checkout, "--as", "Ada", "--file", draft).
		mustCode(t, 1).
		mustContain(t, "stderr", "PREPARE FAIL").
		mustContain(t, "stderr", "the body is the unedited template placeholder (<the note goes here>)")
}

func TestIssue2043_ReplyRefusesUntouchedTemplatePlaceholder(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, remote := busDir(t)
	bodyFile := filepath.Join(t.TempDir(), "body.md")
	content := bus.PlaceholderBody + "\n"
	if err := os.WriteFile(bodyFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	invoke(t, "", "reply", "--bus", checkout, "--as", "Ada", "--re", "bo-abcdef012345", "--file", bodyFile, "--remote", remote, "--branch", "main").
		mustCode(t, 1).
		mustContain(t, "stderr", "REPLY FAIL").
		mustContain(t, "stderr", "the body is the unedited template placeholder (<the note goes here>)")
}

func TestIssue2043_SendOKPrintsBodyBytes(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout, remote := busDir(t)
	draft := filepath.Join(t.TempDir(), "draft.md")
	const bodyText = "This is a real note body with twenty-six characters."
	content := "From: Ada\nTo: Bo\nSubject: testing\n\n" + bodyText + "\n"
	if err := os.WriteFile(draft, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := invoke(t, "", "send", "--bus", checkout, "--file", draft, "--remote", remote, "--branch", "main").
		mustCode(t, 0).
		mustContain(t, "stdout", "SEND OK id=ada-").
		mustContain(t, "stdout", "body_bytes=")

	if !strings.Contains(r.stdout, "body_bytes=") {
		t.Fatalf("SEND OK missing body_bytes: %s", r.stdout)
	}
}
