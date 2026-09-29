//go:build functional

package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSendPreparedProcessDeathRecovery(t *testing.T) {
	t.Parallel()

	modes := []string{"before-note-write", "after-note-write", "after-index-write", "after-note-commit", "after-commit"}

	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			bare, clone, p, a := stellaPrepared(t)
			scratch := t.TempDir()
			barrierFile := filepath.Join(scratch, "barrier.ready")
			artFile := filepath.Join(scratch, "prepared.json")
			artJSON, _ := RenderPreparedArtifact(p)
			os.WriteFile(artFile, []byte(artJSON), 0644)

			cmd := exec.Command(os.Args[0], "-test.run=TestSendPreparedProcessDeathHelper")
			cmd.Env = append(os.Environ(),
				"GO_WANT_PREPARED_DEATH_HELPER=1",
				"PREPARED_HELPER_BUS="+clone,
				"PREPARED_HELPER_BARRIER="+barrierFile,
				"PREPARED_HELPER_MODE="+mode,
				"PREPARED_HELPER_ART="+artFile,
			)
			helperStdin(t, cmd)

			if err := cmd.Start(); err != nil {
				t.Fatalf("failed to start helper process: %v", err)
			}

			// Wait for the observable barrier, up to a generous bound the environment
			// can move.
			deadline := time.Now().Add(testWaitBound())
			for {
				if _, err := os.Stat(barrierFile); err == nil {
					break
				}
				if time.Now().After(deadline) {
					_ = cmd.Process.Kill()
					t.Fatal("timed out waiting for helper process barrier")
				}
				time.Sleep(10 * time.Millisecond)
			}

			// Terminate child violently with SIGKILL
			if err := cmd.Process.Kill(); err != nil {
				t.Fatalf("failed to kill helper process: %v", err)
			}
			// Wait for process death
			_ = cmd.Wait()

			// Fresh recovery child process reconstructs and validates saved artifact from disk, then delivers
			recCmd := exec.Command(os.Args[0], "-test.run=TestSendPreparedRecoveryHelper")
			recCmd.Env = append(os.Environ(),
				"GO_WANT_PREPARED_RECOVERY_HELPER=1",
				"PREPARED_RECOVERY_BUS="+clone,
				"PREPARED_RECOVERY_ART="+artFile,
				"PREPARED_RECOVERY_AS="+p.Sender.Name,
			)
			out, err := recCmd.CombinedOutput()
			if err != nil {
				t.Fatalf("fresh recovery child failed: %v\noutput:\n%s", err, string(out))
			}

			// Verify exact note on bare remote
			remoteNote, err := git(bare, "show", "main:"+p.Path)
			if err != nil || remoteNote != a.Note {
				t.Fatalf("bare remote missing note or note mismatch: %v", err)
			}

			// Verify bare remote contains EXACTLY ONE index line
			remoteIndex, err := git(bare, "show", "main:"+IndexPath(p.Sender.Lane))
			if err != nil {
				t.Fatalf("bare remote missing index: %v", err)
			}
			matchCount := 0
			for _, l := range strings.Split(strings.TrimSpace(remoteIndex), "\n") {
				if l == IndexLine(p.Index) {
					matchCount++
				}
			}
			if matchCount != 1 {
				t.Fatalf("bare remote has %d occurrences of index line, want exactly 1:\n%s", matchCount, remoteIndex)
			}
		})
	}
}

// TestPreparedIndexRecoveryFromStagedPartialIndexRetainsEarlierEntries stages a partial INDEX
// from a killed child and verifies recovery from that state: earlier entries are retained and
// the recovered entry is appended. It does not interrupt production recovery mid-write; an
// actual production-interruption gate (a kill inside SendPreparedArtifact's own INDEX append)
// remains owed and is named in the PR.
func TestPreparedIndexRecoveryFromStagedPartialIndexRetainsEarlierEntries(t *testing.T) {
	t.Parallel()

	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	tab := loadBus(t, clone)

	send := func(subject, slug, stamp string) (Prepared, PreparedArtifact) {
		t.Helper()
		p, err := PrepareDraft(tab, "From: Ada\nTo: Bo\nSubject: "+subject+"\n\nSynthetic note.\n", at(stamp), slug, "Ada")
		if err != nil {
			t.Fatal(err)
		}
		a, err := MakePreparedArtifact(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := SendPreparedArtifact(clone, "origin", "main", p, a, 1); err != nil {
			t.Fatal(err)
		}
		return p, a
	}

	p1, _ := send("Earlier entry one", "earlier-one", "2026-09-12T17:00:00Z")
	p2, _ := send("Earlier entry two", "earlier-two", "2026-09-12T17:01:00Z")

	p3, err := PrepareDraft(tab, "From: Ada\nTo: Bo\nSubject: Recovered entry\n\nSynthetic note.\n", at("2026-09-12T17:02:00Z"), "recovered", "Ada")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MakePreparedArtifact(p3); err != nil {
		t.Fatal(err)
	}

	scratch := t.TempDir()
	barrierFile := filepath.Join(scratch, "barrier.ready")
	artFile := filepath.Join(scratch, "prepared.json")
	artJSON, _ := RenderPreparedArtifact(p3)
	if err := os.WriteFile(artFile, []byte(artJSON), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestPreparedIndexStagedPartialHelper")
	cmd.Env = append(os.Environ(),
		"GO_WANT_PREPARED_INDEX_DEATH_HELPER=1",
		"PREPARED_HELPER_BUS="+clone,
		"PREPARED_HELPER_AS="+p3.Sender.Name,
		"PREPARED_HELPER_BARRIER="+barrierFile,
		"PREPARED_HELPER_ART="+artFile,
	)
	helperStdin(t, cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start helper process: %v", err)
	}
	deadline := time.Now().Add(testWaitBound())
	for {
		if _, err := os.Stat(barrierFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("timed out waiting for helper process barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("failed to kill helper process: %v", err)
	}
	_ = cmd.Wait()

	recCmd := exec.Command(os.Args[0], "-test.run=TestSendPreparedRecoveryHelper")
	recCmd.Env = append(os.Environ(),
		"GO_WANT_PREPARED_RECOVERY_HELPER=1",
		"PREPARED_RECOVERY_BUS="+clone,
		"PREPARED_RECOVERY_ART="+artFile,
		"PREPARED_RECOVERY_AS="+p3.Sender.Name,
	)
	out, err := recCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fresh recovery child failed: %v\noutput:\n%s", err, string(out))
	}

	remoteIndex, err := git(bare, "show", "main:"+IndexPath(p3.Sender.Lane))
	if err != nil {
		t.Fatalf("bare remote missing index: %v", err)
	}
	for _, earlier := range []Prepared{p1, p2} {
		if !strings.Contains(remoteIndex, IndexLine(earlier.Index)) {
			t.Fatalf("earlier INDEX entry was lost:\n%s", remoteIndex)
		}
	}
	if !strings.Contains(remoteIndex, IndexLine(p3.Index)) {
		t.Fatalf("recovered INDEX entry is missing:\n%s", remoteIndex)
	}
}
