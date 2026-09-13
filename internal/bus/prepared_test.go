package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMakeAndValidatePreparedArtifact(t *testing.T) {
	t.Parallel()
	root := writeBus(t, nil)
	tab := loadBus(t, root)
	now := at("2026-09-12T12:00:00Z")

	draft := "From: Ada\nTo: Bo\nSubject: Prepared test\n\nTesting prepared artifact round-trip.\n"
	p, err := PrepareDraft(tab, draft, now, "prepared-test", "Ada")
	if err != nil {
		t.Fatalf("PrepareDraft: %v", err)
	}

	art, err := MakePreparedArtifact(p)
	if err != nil {
		t.Fatalf("MakePreparedArtifact: %v", err)
	}
	if art.Schema != PreparedSchema {
		t.Fatalf("art.Schema = %q, want %q", art.Schema, PreparedSchema)
	}
	if !strings.HasSuffix(art.Note, "\n") {
		t.Fatal("art.Note must end with LF")
	}
	if len(art.SHA256) != 64 {
		t.Fatalf("art.SHA256 length = %d, want 64", len(art.SHA256))
	}

	raw, err := json.Marshal(art)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	// Successful validation
	c := tab.Config
	gotArt, gotP, err := ValidatePreparedArtifact(raw, root, c, "Ada")
	if err != nil {
		t.Fatalf("ValidatePreparedArtifact: %v", err)
	}
	if gotArt.ID != art.ID || gotP.Note.Header.ID != art.ID {
		t.Fatalf("id mismatch: got %q, want %q", gotArt.ID, art.ID)
	}

	// Failure: malformed json
	if _, _, err := ValidatePreparedArtifact([]byte("not-json"), root, c, "Ada"); err == nil {
		t.Fatal("ValidatePreparedArtifact accepted malformed JSON")
	}

	// Failure: bad schema
	badSchema := art
	badSchema.Schema = "nova.bus.prepared/99"
	badRaw, _ := json.Marshal(badSchema)
	if _, _, err := ValidatePreparedArtifact(badRaw, root, c, "Ada"); err == nil {
		t.Fatal("ValidatePreparedArtifact accepted bad schema")
	}

	// Failure: invalid sha256
	badSHA := art
	badSHA.SHA256 = "invalid-sha"
	badRaw, _ = json.Marshal(badSHA)
	if _, _, err := ValidatePreparedArtifact(badRaw, root, c, "Ada"); err == nil {
		t.Fatal("ValidatePreparedArtifact accepted invalid sha256")
	}

	// Failure: digest mismatch
	badDigest := art
	badDigest.SHA256 = strings.Repeat("0", 64)
	badRaw, _ = json.Marshal(badDigest)
	if _, _, err := ValidatePreparedArtifact(badRaw, root, c, "Ada"); err == nil {
		t.Fatal("ValidatePreparedArtifact accepted mismatched digest")
	}

	// Failure: missing LF
	noLF := art
	noLF.Note = strings.TrimRight(art.Note, "\n")
	noLF.SHA256 = MakeSHA256(noLF.Note)
	badRaw, _ = json.Marshal(noLF)
	if _, _, err := ValidatePreparedArtifact(badRaw, root, c, "Ada"); err == nil {
		t.Fatal("ValidatePreparedArtifact accepted note without final LF")
	}

	// Failure: outside lane path
	outsideLane := art
	outsideLane.Path = "from-bo/" + filepath.Base(art.Path)
	badRaw, _ = json.Marshal(outsideLane)
	if _, _, err := ValidatePreparedArtifact(badRaw, root, c, "Ada"); err == nil {
		t.Fatal("ValidatePreparedArtifact accepted path outside sender lane")
	}

	// Failure: path traversal
	traversal := art
	traversal.Path = "../outside.md"
	badRaw, _ = json.Marshal(traversal)
	if _, _, err := ValidatePreparedArtifact(badRaw, root, c, "Ada"); err == nil {
		t.Fatal("ValidatePreparedArtifact accepted path leaving root")
	}

	// Failure: speaker mismatch
	if _, _, err := ValidatePreparedArtifact(raw, root, c, "Bo"); err == nil {
		t.Fatal("ValidatePreparedArtifact accepted mismatched speaker")
	}

	// Failure: tampered body with same ID
	tampered := art
	tampered.Note = strings.Replace(art.Note, "Testing", "Tampered", 1)
	tampered.SHA256 = MakeSHA256(tampered.Note)
	badRaw, _ = json.Marshal(tampered)
	if _, _, err := ValidatePreparedArtifact(badRaw, root, c, "Ada"); err == nil {
		t.Fatal("ValidatePreparedArtifact accepted note with tampered body and mismatched id")
	}
}

func MakeSHA256(s string) string {
	var art PreparedArtifact
	art.Note = s
	p := Prepared{Note: Note{}}
	// Quick helper for test
	p.Note.Header.ID = "dummy"
	h, _ := MakePreparedArtifact(Prepared{Note: Note{Body: s}})
	return h.SHA256
}

func TestSendPreparedArtifactAlreadyPublished(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	tab := loadBus(t, clone)
	now := at("2026-09-12T14:00:00Z")

	draft := "From: Ada\nTo: Bo\nSubject: Fresh publish\n\nA note to test already-published.\n"
	p, err := PrepareDraft(tab, draft, now, "fresh-publish", "Ada")
	if err != nil {
		t.Fatalf("PrepareDraft: %v", err)
	}
	art, err := MakePreparedArtifact(p)
	if err != nil {
		t.Fatalf("MakePreparedArtifact: %v", err)
	}

	// First send: should publish
	res1, err := SendPreparedArtifact(clone, "origin", "main", p, art, 3)
	if err != nil {
		t.Fatalf("SendPreparedArtifact first send: %v", err)
	}
	if !res1.Pushed || res1.State != "published" {
		t.Fatalf("res1 = %+v, want published", res1)
	}

	headBefore, err := git(bare, "rev-parse", "main")
	if err != nil {
		t.Fatal(err)
	}

	// Second send: should detect already-published without second commit or push
	res2, err := SendPreparedArtifact(clone, "origin", "main", p, art, 3)
	if err != nil {
		t.Fatalf("SendPreparedArtifact retry: %v", err)
	}
	if !res2.Pushed || res2.Attempts != 0 || res2.State != "already-published" {
		t.Fatalf("res2 = %+v, want already-published with attempts=0", res2)
	}

	headAfter, err := git(bare, "rev-parse", "main")
	if err != nil {
		t.Fatal(err)
	}
	if headBefore != headAfter {
		t.Fatalf("bare HEAD moved during already-published retry: before %s, after %s", headBefore, headAfter)
	}
}

func TestSendPreparedArtifactRefusals(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	tab := loadBus(t, clone)
	now := at("2026-09-12T15:00:00Z")

	draft := "From: Ada\nTo: Bo\nSubject: Refusal checks\n\nTesting refusals.\n"
	p, err := PrepareDraft(tab, draft, now, "refusal-checks", "Ada")
	if err != nil {
		t.Fatalf("PrepareDraft: %v", err)
	}
	art, err := MakePreparedArtifact(p)
	if err != nil {
		t.Fatalf("MakePreparedArtifact: %v", err)
	}

	// 1. Unrelated dirty file in checkout
	write(t, clone, "unrelated.txt", "dirty content\n")
	if _, err := SendPreparedArtifact(clone, "origin", "main", p, art, 3); err == nil {
		t.Fatal("SendPreparedArtifact accepted dirty checkout")
	}
	// Verify dirty file is preserved
	if data, err := os.ReadFile(filepath.Join(clone, "unrelated.txt")); err != nil || string(data) != "dirty content\n" {
		t.Fatal("SendPreparedArtifact failed to preserve unrelated dirty file")
	}
	os.Remove(filepath.Join(clone, "unrelated.txt"))

	// 2. Unrelated ahead commit on branch
	write(t, clone, "manual.txt", "manual work\n")
	git(clone, "add", "manual.txt")
	git(clone, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-m", "manual commit")
	if _, err := SendPreparedArtifact(clone, "origin", "main", p, art, 3); err == nil {
		t.Fatal("SendPreparedArtifact accepted unrelated ahead commit")
	}
	// Reset that manual commit for next test
	git(clone, "reset", "--hard", "origin/main")

	// 3. Same ID with different bytes on remote
	// Publish the note first
	if _, err := SendPreparedArtifact(clone, "origin", "main", p, art, 3); err != nil {
		t.Fatalf("initial send: %v", err)
	}
	// Forge an artifact with the same ID and path, but different note bytes
	tamperedNote := strings.Replace(art.Note, "Testing refusals.", "Conflicting content.", 1)
	tamperedArt := art
	tamperedArt.Note = tamperedNote
	// Try sending tampered artifact
	if _, err := SendPreparedArtifact(clone, "origin", "main", p, tamperedArt, 3); err == nil {
		t.Fatal("SendPreparedArtifact accepted conflicting remote note content")
	}
}

func TestSendPreparedArtifactInterruptedRecoveries(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	tab := loadBus(t, clone)
	now := at("2026-09-12T16:00:00Z")

	draft := "From: Ada\nTo: Bo\nSubject: Interrupted recovery\n\nTesting partial writes.\n"
	p, err := PrepareDraft(tab, draft, now, "interrupted-recovery", "Ada")
	if err != nil {
		t.Fatalf("PrepareDraft: %v", err)
	}
	art, err := MakePreparedArtifact(p)
	if err != nil {
		t.Fatalf("MakePreparedArtifact: %v", err)
	}

	// Scenario A: Interrupted after note save, before INDEX append
	if err := p.Save(clone); err != nil {
		t.Fatal(err)
	}
	// SendPreparedArtifact should recover the partial write, append INDEX, commit and push
	res, err := SendPreparedArtifact(clone, "origin", "main", p, art, 3)
	if err != nil {
		t.Fatalf("recovery after note save: %v", err)
	}
	if !res.Pushed || res.State != "published" {
		t.Fatalf("res = %+v, want published", res)
	}

	// Verify exactly one note and one INDEX line on remote
	noteOnRemote, err := git(bare, "show", "main:"+art.Path)
	if err != nil || noteOnRemote != art.Note {
		t.Fatalf("note on remote: %v, content = %q", err, noteOnRemote)
	}
	indexOnRemote, err := git(bare, "show", "main:"+IndexPath(p.Sender.Lane))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(indexOnRemote, art.ID) != 1 {
		t.Fatalf("expected exactly 1 index entry for %s, got:\n%s", art.ID, indexOnRemote)
	}
}

func TestSendPreparedArtifactConcurrentRemoteLanding(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	bench1 := cloneBus(t, bare)
	bench2 := cloneBus(t, bare)

	tab1 := loadBus(t, bench1)
	tab2 := loadBus(t, bench2)
	now := at("2026-09-12T16:30:00Z")

	// Bench 1 prepares note from Ada
	p1, err := PrepareDraft(tab1, "From: Ada\nTo: Bo\nSubject: Ada's note\n\nNote from Ada.\n", now, "adas-note", "Ada")
	if err != nil {
		t.Fatal(err)
	}
	art1, err := MakePreparedArtifact(p1)
	if err != nil {
		t.Fatal(err)
	}

	// Bench 2 prepares and pushes note from Bo
	p2, err := PrepareDraft(tab2, "From: Bo\nTo: Ada\nSubject: Bo's note\n\nNote from Bo.\n", now, "bos-note", "Bo")
	if err != nil {
		t.Fatal(err)
	}
	art2, err := MakePreparedArtifact(p2)
	if err != nil {
		t.Fatal(err)
	}
	res2, err := SendPreparedArtifact(bench2, "origin", "main", p2, art2, 3)
	if err != nil || !res2.Pushed {
		t.Fatalf("bench2 send failed: %v", err)
	}

	// Bench 1 sends its prepared note; it will encounter a non-fast-forward push, fetch, rebase, and succeed
	res1, err := SendPreparedArtifact(bench1, "origin", "main", p1, art1, 5)
	if err != nil {
		t.Fatalf("bench1 send failed: %v", err)
	}
	if !res1.Pushed || res1.State != "published" {
		t.Fatalf("res1 = %+v, want published", res1)
	}

	// Both notes must be on the remote branch
	if _, err := git(bare, "show", "main:"+art1.Path); err != nil {
		t.Fatalf("Ada's note missing from bare remote: %v", err)
	}
	if _, err := git(bare, "show", "main:"+art2.Path); err != nil {
		t.Fatalf("Bo's note missing from bare remote: %v", err)
	}
}

func stellaPrepared(t *testing.T) (string, string, Prepared, PreparedArtifact) {
	t.Helper()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)
	tab := loadBus(t, clone)
	p, e := PrepareDraft(tab, "From: Ada\nTo: Bo\nSubject: Boundary fixture\n\nSynthetic note.\n", at("2026-09-12T17:00:00Z"), "boundary", "Ada")
	if e != nil {
		t.Fatal(e)
	}
	a, e := MakePreparedArtifact(p)
	if e != nil {
		t.Fatal(e)
	}
	return bare, clone, p, a
}

func TestStellaPreparedRequiresCompleteRemoteIndex(t *testing.T) {
	_, clone, p, a := stellaPrepared(t)
	if _, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(clone, IndexPath(p.Sender.Lane))
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	expected := IndexLine(p.Index)
	fields := strings.Split(expected, "\t")
	fields[len(fields)-1] = "SYNTHETIC_WRONG_INDEX_SUBJECT"
	changed := strings.Replace(string(b), expected, strings.Join(fields, "\t"), 1)
	if changed == string(b) {
		t.Fatal("did not mutate index")
	}
	os.WriteFile(path, []byte(changed), 0644)
	id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
	if _, e := stageAndCommit(clone, id, []string{IndexPath(p.Sender.Lane)}, "mutate synthetic index"); e != nil {
		t.Fatal(e)
	}
	if _, e := git(clone, "push", "origin", "HEAD:main"); e != nil {
		t.Fatal(e)
	}
	if r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1); e == nil && r.Pushed {
		t.Fatal("claimed already-published with a different INDEX record")
	}
}

func TestStellaPreparedCannotConfirmCommitWithoutIndex(t *testing.T) {
	bare, clone, p, a := stellaPrepared(t)
	if e := p.Save(clone); e != nil {
		t.Fatal(e)
	}
	id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
	if _, e := stageAndCommit(clone, id, []string{p.Path}, WithTrailer(p.Message, TrailerSend+" "+a.ID)); e != nil {
		t.Fatal(e)
	}
	r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if e != nil {
		t.Fatalf("SendPreparedArtifact failed: %v", e)
	}
	index, e := git(bare, "show", "main:"+IndexPath(p.Sender.Lane))
	if r.Pushed && (e != nil || !strings.Contains(index, IndexLine(p.Index))) {
		t.Fatal("claimed success after publishing note-only commit without INDEX entry")
	}
}

func TestStellaPreparedPreservesUnrelatedAttributeEdit(t *testing.T) {
	bare, clone, p, a := stellaPrepared(t)
	path := filepath.Join(clone, AttributesName)
	old, _ := os.ReadFile(path)
	sentinel := "# synthetic_private_unrelated_attribute_edit\n"
	want := append(old, []byte(sentinel)...)
	os.WriteFile(path, want, 0644)
	r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if e == nil && r.Pushed {
		remote, _ := git(bare, "show", "main:"+AttributesName)
		if strings.Contains(remote, sentinel) {
			t.Fatal("published unrelated dirty attribute content during prepared delivery")
		}
	}
	now, _ := os.ReadFile(path)
	if string(now) != string(want) {
		t.Fatal("refusal changed unrelated dirty attribute content")
	}
}

func TestStellaPreparedRefusesUnknownArtifactField(t *testing.T) {
	_, clone, p, a := stellaPrepared(t)
	b, _ := json.Marshal(a)
	b = append(b[:len(b)-1], []byte(`,"unsupported":"synthetic"}`)...)
	if _, _, e := ValidatePreparedArtifact(b, clone, loadBus(t, clone).Config, p.Sender.Name); e == nil {
		t.Fatal("accepted unknown artifact field")
	}
}

func TestPreparedRefusesDuplicateKeys(t *testing.T) {
	_, clone, p, a := stellaPrepared(t)
	dupJSON := fmt.Sprintf(`{"schema":%q,"id":%q,"id":"duplicate-id","path":%q,"note":%q,"sha256":%q}`,
		a.Schema, a.ID, a.Path, a.Note, a.SHA256)
	if _, _, err := ValidatePreparedArtifact([]byte(dupJSON), clone, loadBus(t, clone).Config, p.Sender.Name); err == nil {
		t.Fatal("accepted artifact with duplicate key")
	}
}

func TestStellaPreparedPreservesUnrelatedAheadAttributeEdit(t *testing.T) {
	bare, clone, p, a := stellaPrepared(t)
	if e := p.Save(clone); e != nil {
		t.Fatal(e)
	}
	if e := p.AppendIndex(clone); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(clone, AttributesName)
	old, _ := os.ReadFile(path)
	sentinel := "# synthetic_unrelated_ahead_attribute_edit\n"
	if e := os.WriteFile(path, append(old, []byte(sentinel)...), 0644); e != nil {
		t.Fatal(e)
	}
	id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
	if _, e := stageAndCommit(clone, id, []string{p.Path, IndexPath(p.Sender.Lane), AttributesName}, WithTrailer(p.Message, TrailerSend+" "+a.ID)); e != nil {
		t.Fatal(e)
	}
	r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if e == nil && r.Pushed {
		remote, _ := git(bare, "show", "main:"+AttributesName)
		if strings.Contains(remote, sentinel) {
			t.Fatal("published unrelated committed attribute content with an allowed trailer")
		}
	}
}

func TestSendPreparedProcessDeathHelper(t *testing.T) {
	if os.Getenv("GO_WANT_PREPARED_DEATH_HELPER") != "1" {
		return
	}
	busDir := os.Getenv("PREPARED_HELPER_BUS")
	barrierFile := os.Getenv("PREPARED_HELPER_BARRIER")
	mode := os.Getenv("PREPARED_HELPER_MODE")
	artFile := os.Getenv("PREPARED_HELPER_ART")

	raw, err := os.ReadFile(artFile)
	if err != nil {
		os.Exit(2)
	}
	var art PreparedArtifact
	if err := json.Unmarshal(raw, &art); err != nil {
		os.Exit(2)
	}

	tab := loadBus(t, busDir)
	_, p, err := ValidatePreparedArtifact(raw, busDir, tab.Config, "Ada")
	if err != nil {
		os.Exit(2)
	}

	switch mode {
	case "before-note-write":
		// Pre-note case: helper started, no bus mutations made yet
	case "after-note-write":
		if err := p.Save(busDir); err != nil {
			os.Exit(3)
		}
	case "after-index-write":
		if err := p.Save(busDir); err != nil {
			os.Exit(3)
		}
		if err := p.AppendIndex(busDir); err != nil {
			os.Exit(3)
		}
	case "after-note-commit":
		if err := p.Save(busDir); err != nil {
			os.Exit(3)
		}
		id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
		if _, err := stageAndCommit(busDir, id, []string{p.Path}, WithTrailer(p.Message, TrailerSend+" "+art.ID)); err != nil {
			os.Exit(3)
		}
	case "after-commit":
		if err := p.Save(busDir); err != nil {
			os.Exit(3)
		}
		if err := p.AppendIndex(busDir); err != nil {
			os.Exit(3)
		}
		EnsureMergeAttributes(busDir)
		id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
		if _, err := stageAndCommit(busDir, id, p.Paths(), WithTrailer(p.Message, TrailerSend+" "+art.ID)); err != nil {
			os.Exit(3)
		}
	case "send-call":
		res, err := SendPreparedArtifact(busDir, "origin", "main", p, art, 1)
		if err != nil {
			fmt.Fprintf(os.Stderr, "send-call failed: %v\n", err)
			os.Exit(6)
		}
		if !res.Pushed || res.State != "published" {
			os.Exit(7)
		}
		os.Exit(0)
	default:
		os.Exit(4)
	}

	// Signal observable barrier to parent
	if err := os.WriteFile(barrierFile, []byte("ready\n"), 0644); err != nil {
		os.Exit(5)
	}

	// Block indefinitely until killed by parent via SIGKILL
	time.Sleep(10 * time.Minute)
}

func TestSendPreparedRecoveryHelper(t *testing.T) {
	if os.Getenv("GO_WANT_PREPARED_RECOVERY_HELPER") != "1" {
		return
	}
	busDir := os.Getenv("PREPARED_RECOVERY_BUS")
	artFile := os.Getenv("PREPARED_RECOVERY_ART")
	as := os.Getenv("PREPARED_RECOVERY_AS")

	raw, err := os.ReadFile(artFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read art file failed: %v\n", err)
		os.Exit(1)
	}
	tab := loadBus(t, busDir)
	art, p, err := ValidatePreparedArtifact(raw, busDir, tab.Config, as)
	if err != nil {
		fmt.Fprintf(os.Stderr, "validate prepared artifact failed: %v\n", err)
		os.Exit(2)
	}
	res, err := SendPreparedArtifact(busDir, "origin", "main", p, art, 1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "send prepared artifact failed: %v\n", err)
		os.Exit(3)
	}
	if !res.Pushed || (res.State != "published" && res.State != "already-published") {
		fmt.Fprintf(os.Stderr, "unexpected res state: %+v\n", res)
		os.Exit(4)
	}
	os.Exit(0)
}

func TestSendPreparedProcessDeathRecovery(t *testing.T) {
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

			if err := cmd.Start(); err != nil {
				t.Fatalf("failed to start helper process: %v", err)
			}

			// Wait for observable barrier
			deadline := time.Now().Add(5 * time.Second)
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

func TestSendPreparedChildExecutionAndRecovery(t *testing.T) {
	bare, clone, p, a := stellaPrepared(t)
	scratch := t.TempDir()
	artFile := filepath.Join(scratch, "prepared.json")
	artJSON, _ := RenderPreparedArtifact(p)
	os.WriteFile(artFile, []byte(artJSON), 0644)

	// 1. Initial child executes the actual sending call
	cmdSend := exec.Command(os.Args[0], "-test.run=TestSendPreparedProcessDeathHelper")
	cmdSend.Env = append(os.Environ(),
		"GO_WANT_PREPARED_DEATH_HELPER=1",
		"PREPARED_HELPER_BUS="+clone,
		"PREPARED_HELPER_MODE=send-call",
		"PREPARED_HELPER_ART="+artFile,
	)
	out, err := cmdSend.CombinedOutput()
	if err != nil {
		t.Fatalf("sending child failed: %v\noutput:\n%s", err, string(out))
	}

	// Verify on bare remote
	remoteNote, err := git(bare, "show", "main:"+p.Path)
	if err != nil || remoteNote != a.Note {
		t.Fatalf("bare remote missing note after child send: %v", err)
	}

	// 2. Fresh recovery child confirms delivery via already-published
	recCmd := exec.Command(os.Args[0], "-test.run=TestSendPreparedRecoveryHelper")
	recCmd.Env = append(os.Environ(),
		"GO_WANT_PREPARED_RECOVERY_HELPER=1",
		"PREPARED_RECOVERY_BUS="+clone,
		"PREPARED_RECOVERY_ART="+artFile,
		"PREPARED_RECOVERY_AS="+p.Sender.Name,
	)
	outRec, err := recCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subsequent recovery child failed: %v\noutput:\n%s", err, string(outRec))
	}
}

func TestRowanProbeAheadMergeCommitPublishesUnrelatedTree(t *testing.T) {
	bare, clone, p, a := stellaIndependentPrepared(t)
	id := Identity{Name: p.Sender.GitName, Email: p.Sender.GitEmail}
	msg := WithTrailer(p.Message, TrailerSend+" "+a.ID)
	if err := p.Save(clone); err != nil {
		t.Fatal(err)
	}
	if err := p.AppendIndex(clone); err != nil {
		t.Fatal(err)
	}
	noteSha, err := stageAndCommit(clone, id, []string{p.Path, IndexPath(p.Sender.Lane)}, msg)
	if err != nil {
		t.Fatal(err)
	}
	base, err := git(clone, "rev-parse", "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "SYNTHETIC_UNRELATED_LEAK.txt"), []byte("synthetic unrelated payload\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := git(clone, "add", "SYNTHETIC_UNRELATED_LEAK.txt"); err != nil {
		t.Fatal(err)
	}
	tree, err := git(clone, "write-tree")
	if err != nil {
		t.Fatal(err)
	}
	merge, err := git(clone, append(identityArgs(id), "commit-tree", strings.TrimSpace(tree),
		"-p", strings.TrimSpace(noteSha), "-p", strings.TrimSpace(base), "-m", msg)...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := git(clone, "reset", "--hard", strings.TrimSpace(merge)); err != nil {
		t.Fatal(err)
	}
	r, e := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if e == nil && r.Pushed {
		if _, err := git(bare, "show", "main:SYNTHETIC_UNRELATED_LEAK.txt"); err == nil {
			t.Fatal("published unrelated content through an ahead merge commit")
		}
	}
}

func TestRowanProbeStaleIndexLock(t *testing.T) {
	_, clone, p, a := stellaIndependentPrepared(t)
	lockFile := filepath.Join(clone, ".git", "index.lock")
	if err := os.WriteFile(lockFile, []byte("stale lock\n"), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(lockFile)
	_, err := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if err == nil {
		t.Fatal("expected error with index.lock present")
	}
	if !strings.Contains(err.Error(), "index is locked") || !strings.Contains(err.Error(), a.ID) {
		t.Fatalf("expected bounded index lock refusal with prepared ID %q, got: %v", a.ID, err)
	}
}

func TestPreparedDeliveryRecoversEmptyOrPartialGitattributes(t *testing.T) {
	bare, clone, p, a := stellaIndependentPrepared(t)
	// Write empty .gitattributes (simulating crash right after open/create before EnsureMergeAttributes wrote content)
	attrsPath := filepath.Join(clone, AttributesName)
	if err := os.WriteFile(attrsPath, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if err != nil {
		t.Fatalf("SendPreparedArtifact failed on empty .gitattributes: %v", err)
	}
	if !res.Pushed {
		t.Fatal("expected note to be pushed")
	}

	// Verify remote received note, index, and complete .gitattributes with union rules
	noteRemote, err := git(bare, "show", "main:"+p.Path)
	if err != nil || noteRemote != a.Note {
		t.Fatalf("remote note mismatch: %v", err)
	}
	attrsRemote, err := git(bare, "show", "main:"+AttributesName)
	if err != nil || !strings.Contains(attrsRemote, "from-*/INDEX merge=union") {
		t.Fatalf("remote .gitattributes missing union rule: %v\n%s", err, attrsRemote)
	}
}

func TestPreparedDeliveryRecoversPartialNoteOnDisk(t *testing.T) {
	bare, clone, p, a := stellaIndependentPrepared(t)
	fullNote := filepath.Join(clone, filepath.FromSlash(p.Path))
	if err := os.MkdirAll(filepath.Dir(fullNote), 0755); err != nil {
		t.Fatal(err)
	}
	// Write partial prefix of note (first 20 bytes)
	prefix := a.Note[:20]
	if err := os.WriteFile(fullNote, []byte(prefix), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if err != nil {
		t.Fatalf("SendPreparedArtifact failed on partial note write: %v", err)
	}
	if !res.Pushed {
		t.Fatal("expected note to be pushed")
	}

	noteRemote, err := git(bare, "show", "main:"+p.Path)
	if err != nil || noteRemote != a.Note {
		t.Fatalf("remote note mismatch: %v", err)
	}
}

func TestPreparedDeliveryRecoversPartialIndexOnDisk(t *testing.T) {
	bare, clone, p, a := stellaIndependentPrepared(t)
	fullIndex := filepath.Join(clone, filepath.FromSlash(IndexPath(p.Sender.Lane)))
	if err := os.MkdirAll(filepath.Dir(fullIndex), 0755); err != nil {
		t.Fatal(err)
	}
	// Write partial prefix of index line
	line := IndexLine(p.Index)
	prefix := line[:15]
	if err := os.WriteFile(fullIndex, []byte(prefix), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if err != nil {
		t.Fatalf("SendPreparedArtifact failed on partial index write: %v", err)
	}
	if !res.Pushed {
		t.Fatal("expected note to be pushed")
	}

	idxRemote, err := git(bare, "show", "main:"+IndexPath(p.Sender.Lane))
	if err != nil || !strings.Contains(idxRemote, line) {
		t.Fatalf("remote index missing completed line: %v\n%s", err, idxRemote)
	}
}

func TestPreparedDeliveryRefusesUnrelatedForeignGitattributes(t *testing.T) {
	_, clone, p, a := stellaIndependentPrepared(t)
	attrsPath := filepath.Join(clone, AttributesName)
	if err := os.WriteFile(attrsPath, []byte("*.iso filter=lfs\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if err == nil {
		t.Fatal("expected error on foreign .gitattributes content")
	}
	if !strings.Contains(err.Error(), "unrelated dirty changes in .gitattributes") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestPreparedDeliveryRefusesConflictingNoteOnDisk(t *testing.T) {
	_, clone, p, a := stellaIndependentPrepared(t)
	fullNote := filepath.Join(clone, filepath.FromSlash(p.Path))
	if err := os.MkdirAll(filepath.Dir(fullNote), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullNote, []byte("completely conflicting note\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := SendPreparedArtifact(clone, "origin", "main", p, a, 1)
	if err == nil {
		t.Fatal("expected error on conflicting note content")
	}
	if !strings.Contains(err.Error(), "conflicting") && !strings.Contains(err.Error(), "unrelated dirty changes") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
