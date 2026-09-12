package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

func TestPrepareDecidingTests(t *testing.T) {
	t.Parallel()
	checkout, bare := busDir(t)
	_ = bare
	scratch := t.TempDir()

	draftFile := filepath.Join(scratch, "draft.md")
	if err := os.WriteFile(draftFile, []byte("# On the merge queue\n\nFrom: Ada\nTo: Bo\n\nThe gate never ran.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Prepare with tolerances outputs valid JSON and notes to stderr
	r := invoke(t, "", "prepare", "--bus", checkout, "--as", "Ada", "--file", draftFile)
	r.mustCode(t, 0).mustContain(t, "stderr", "PREPARE NOTE")

	var art bus.PreparedArtifact
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.stdout)), &art); err != nil {
		t.Fatalf("json.Unmarshal prepare output failed: %v\nstdout: %s", err, r.stdout)
	}
	if art.Schema != bus.PreparedSchema {
		t.Fatalf("schema = %q, want %q", art.Schema, bus.PreparedSchema)
	}
	if !strings.HasSuffix(art.Note, "\n") {
		t.Fatal("prepared note must end with LF")
	}

	// 2. Draft refusal exits 1 with PREPARE FAIL
	badDraft := filepath.Join(scratch, "bad-draft.md")
	if err := os.WriteFile(badDraft, []byte("From: Ada\nSubject: No To line\n\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rBad := invoke(t, "", "prepare", "--bus", checkout, "--as", "Ada", "--file", badDraft)
	rBad.mustCode(t, 1).mustContain(t, "stderr", "PREPARE FAIL")

	// 3. Invocation errors exit 2
	invoke(t, "", "prepare", "--as", "Ada", "--file", draftFile).mustCode(t, 2).mustContain(t, "stderr", "--bus is required")
	invoke(t, "", "prepare", "--bus", checkout, "--file", draftFile).mustCode(t, 2).mustContain(t, "stderr", "--as is required")
	invoke(t, "", "prepare", "--bus", checkout, "--as", "Ada").mustCode(t, 2).mustContain(t, "stderr", "give exactly one of --file and --stdin")
}

func TestSendPreparedDecidingTests(t *testing.T) {
	t.Parallel()
	checkout, bare := busDir(t)
	scratch := t.TempDir()

	draftFile := filepath.Join(scratch, "draft.md")
	if err := os.WriteFile(draftFile, []byte("From: Ada\nTo: Bo\nSubject: Prepared delivery\n\nA test note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Prepare the artifact
	rPrep := invoke(t, "", "prepare", "--bus", checkout, "--as", "Ada", "--file", draftFile)
	rPrep.mustCode(t, 0)
	prepJSON := strings.TrimSpace(rPrep.stdout)

	artFile := filepath.Join(scratch, "prepared.json")
	if err := os.WriteFile(artFile, []byte(prepJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Fresh publication with --prepared
	rSend := invoke(t, "", "send", "--bus", checkout, "--as", "Ada", "--remote", "origin", "--branch", "main", "--prepared", artFile)
	rSend.mustCode(t, 0).mustContain(t, "stdout", "state=published")

	// 2. Retry with --prepared returns already-published with attempts=0
	rRetry := invoke(t, "", "send", "--bus", checkout, "--as", "Ada", "--remote", "origin", "--branch", "main", "--prepared", artFile)
	rRetry.mustCode(t, 0).mustContain(t, "stdout", "state=already-published").mustContain(t, "stdout", "attempts=0")

	// 3. Stdin delivery with --prepared-stdin
	draftFile2 := filepath.Join(scratch, "draft2.md")
	if err := os.WriteFile(draftFile2, []byte("From: Ada\nTo: Bo\nSubject: Prepared stdin\n\nSecond test note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rPrep2 := invoke(t, "", "prepare", "--bus", checkout, "--as", "Ada", "--file", draftFile2)
	rPrep2.mustCode(t, 0)
	prepJSON2 := strings.TrimSpace(rPrep2.stdout)

	rSendStdin := invoke(t, prepJSON2, "send", "--bus", checkout, "--as", "Ada", "--remote", "origin", "--branch", "main", "--prepared-stdin")
	rSendStdin.mustCode(t, 0).mustContain(t, "stdout", "state=published")

	// 4. Dirty checkout refusal preserves dirty file
	draftFileDirty := filepath.Join(scratch, "draft-dirty.md")
	if err := os.WriteFile(draftFileDirty, []byte("From: Ada\nTo: Bo\nSubject: Dirty test note\n\nFresh unpublished note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rPrepDirty := invoke(t, "", "prepare", "--bus", checkout, "--as", "Ada", "--file", draftFileDirty)
	rPrepDirty.mustCode(t, 0)
	artFileDirty := filepath.Join(scratch, "prepared-dirty.json")
	if err := os.WriteFile(artFileDirty, []byte(strings.TrimSpace(rPrepDirty.stdout)), 0o644); err != nil {
		t.Fatal(err)
	}

	dirtyFile := filepath.Join(checkout, "dirty.txt")
	if err := os.WriteFile(dirtyFile, []byte("dirty work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rDirty := invoke(t, "", "send", "--bus", checkout, "--as", "Ada", "--remote", "origin", "--branch", "main", "--prepared", artFileDirty)
	rDirty.mustCode(t, 1).mustContain(t, "stderr", "SEND FAIL")
	if data, err := os.ReadFile(dirtyFile); err != nil || string(data) != "dirty work\n" {
		t.Fatal("failed to preserve dirty file")
	}
	os.Remove(dirtyFile)

	// 5. Mutual exclusivity
	invoke(t, "", "send", "--bus", checkout, "--as", "Ada", "--remote", "origin", "--branch", "main", "--prepared", artFile, "--file", draftFile).
		mustCode(t, 2).mustContain(t, "stderr", "--prepared is mutually exclusive")

	// 6. Child death recovery: kill after commit before push
	draftFile3 := filepath.Join(scratch, "draft3.md")
	if err := os.WriteFile(draftFile3, []byte("From: Ada\nTo: Bo\nSubject: Prepared recovery commit\n\nThird test note.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rPrep3 := invoke(t, "", "prepare", "--bus", checkout, "--as", "Ada", "--file", draftFile3)
	rPrep3.mustCode(t, 0)
	artFile3 := filepath.Join(scratch, "prepared3.json")
	if err := os.WriteFile(artFile3, []byte(strings.TrimSpace(rPrep3.stdout)), 0o644); err != nil {
		t.Fatal(err)
	}
	var art3 bus.PreparedArtifact
	json.Unmarshal([]byte(strings.TrimSpace(rPrep3.stdout)), &art3)

	// Simulate commit already made locally with trailer, but push not completed
	writeFile(t, checkout, art3.Path, art3.Note)
	gitIn(t, checkout, "add", art3.Path)
	gitIn(t, checkout, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-m", "Ada: Prepared recovery commit\n\nNova-Bus: send "+art3.ID)

	// Sending the prepared artifact should reuse the commit, land on remote, and succeed
	rRecover := invoke(t, "", "send", "--bus", checkout, "--as", "Ada", "--remote", "origin", "--branch", "main", "--prepared", artFile3)
	rRecover.mustCode(t, 0).mustContain(t, "stdout", "state=published")

	// Verify on bare remote
	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
	if !strings.Contains(files, art3.Path) {
		t.Fatalf("recovered note not found on remote: %s", files)
	}
}
