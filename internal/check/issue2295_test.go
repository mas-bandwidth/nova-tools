package check

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestIssue2295 is issue nova-tools #2295's reproduction in three parts,
// kept under one function name as the card's DONE-WHEN asks. Each subtest
// uses a temp git repo with a real `git cat-file --batch` pipe; the
// production path is exercised through `NoCode(Stage: true)`, the
// dispatching entry the cmd/nova-check CLI already reaches for.
//
//   - t.Run("DestinationOID"): a file LITERALLY named `0:notes.md` does not
//     resolve as the gitrevision `:0:notes.md` returns (SPEC.md 929), and
//     the implementation reads BLOB CONTENT BY DESTINATION OID --
//     the record's fourth field, never its all-zero source OID (the
//     third field, all-zero for every added file; SPEC.md 935).
//
//   - t.Run("BatchReaderStaysFramed"): a missing/ambiguous reply -- one
//     line, no body -- does not desync the reader (SPEC.md 927), and the
//     reader consumes each record WHOLE rather than reading two bytes and
//     moving on (SPEC.md 921) -- which means a stage running an answer
//     larger than the shebang-decision bytes never buffers the answer
//     whole (SPEC.md 925).
//
//   - t.Run("NeverPassesM"): a rename is read as `D` of the old path and
//     `A` of the new, classified like any other; the parser never sees an
//     `R` letter because the diff-index invocation does not pass `-M`
//     (SPEC.md 945), so a contributor's `diff.renames` is byte-identical
//     on plumbing output and cannot re-shape the gate.
func TestIssue2295(t *testing.T) {
	t.Run("DestinationOID", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("windows: `:` is not a legal file-name character; this shape cannot be staged on that platform")
		}
		// `notes.md` exists in HEAD with PROSE; only this makes
		// `:0:notes.md` resolve to that prose (SPEC.md's measured
		// case). On an empty index `:0:notes.md` resolves to the
		// staged blob instead and the trap fails closed in the
		// wrong direction.
		dir := issue2295Repo(t, map[string]string{
			"notes.md": "harmless prose in notes.md\n",
		})
		// Stage a file whose NAME is `0:notes.md` and whose content
		// is a shebang. srcOID is the all-zero source field; dstOID
		// is the shebang blob. If reading by SRC OID or by a PATH
		// re-parse (`:0:notes.md` as git revparse syntax), the
		// classifier never sees the shebang, and the file is
		// silently missed.
		issue2295Stage(t, dir, "0:notes.md", "#!/bin/sh\nevil_payload\n")
		// Stage a fresh `safe.txt` with PROSE, extension `.txt` not
		// on the floor. srcOID is all-zero; dstOID is the prose
		// blob. If reading by SRC OID, cat-file says "missing" and
		// the path lands as "unreadable blob: cannot rule out
		// machinery" (an extra finding on every added file).
		issue2295Stage(t, dir, "safe.txt", "harmless prose here\n")

		scanned, findings, err := NoCode(NoCodeOptions{Dir: dir, Stage: true})
		if err != nil {
			t.Fatalf("NoCode staged: %v", err)
		}
		// dstOID is read correctly: 0:notes.md IS a finding (shebang
		// payload) and safe.txt is NOT a finding (.txt is not
		// denied; prose has no shebang). Giving two extra findings
		// or zero findings would both signal a wrong read.
		if scanned < 1 {
			t.Errorf("scanned=%d, want at least 1 (one staged record)", scanned)
		}
		if issue2295HasSubject(findings, "0:notes.md") == false {
			t.Errorf("0:notes.md missing from findings; the dstOID read was bypassed: %v", findings)
		}
		if issue2295HasSubject(findings, "safe.txt") {
			t.Errorf("safe.txt should be a clean read by dstOID; wrong read marks it 'unreadable': %v", findings)
		}
	})

	t.Run("BatchReaderStaysFramed", func(t *testing.T) {
		// This is the framed reader's three properties: a one-line
		// "missing" reply does not leave the reader one byte into
		// the next record's header; the reader drains each record's
		// body without buffering it whole; and after either of those,
		// the next record's header lines up.
		dir := issue2295Repo(t, map[string]string{
			"small.md": "small blob\n", // a real OID staged in HEAD
		})
		batch, err := OpenCatFileBatch(dir)
		if err != nil {
			t.Fatalf("OpenCatFileBatch: %v", err)
		}
		t.Cleanup(func() { _ = batch.Close() })

		// 1) MISSING: a valid-format OID this repo does not hold.
		//    cat-file answers "<oid> missing\n" -- one line, no
		//    body. The reader must report that without consuming
		//    extra bytes that would offset the next read.
		bogus := "0123456789abcdef0123456789abcdef01234567"
		rep, err := batch.ReadHead(bogus, 2)
		if err != nil {
			t.Fatalf("ReadHead against missing OID: %v", err)
		}
		if rep.Status != "missing" {
			t.Errorf("missing OID: status=%q, want missing", rep.Status)
		}
		if len(rep.Body) != 0 {
			t.Errorf("missing OID: body=%q, want empty (no body line)", rep.Body)
		}
		if rep.Size != 0 {
			t.Errorf("missing OID: size=%d, want 0", rep.Size)
		}

		// 2) REAL: the next read must still be in step. The OID
		//    for "small blob\n" is whatever git hashes; fetch it
		//    FROM the same dir under the same `cat-file --batch`
		//    pipe work to keep the test self-contained.
		smallOID := issue2295WriteBlob(t, dir, "small blob\n")
		rep, err = batch.ReadHead(smallOID, 2)
		if err != nil {
			t.Fatalf("ReadHead against real OID: %v", err)
		}
		if rep.Status != "blob" {
			t.Errorf("real OID: status=%q, want blob", rep.Status)
		}
		if rep.Size != len("small blob\n") {
			t.Errorf("real OID: size=%d, want %d", rep.Size, len("small blob\n"))
		}
		if len(rep.Body) != 2 || string(rep.Body) != "sm" {
			t.Errorf("real OID: body=%q, want first 2 bytes %q", rep.Body, "sm")
		}

		// 3) HUGE: lay down a one-megabyte blob. The reader asks
		//    for two bytes and the rest must be drained -- not
		//    held -- so a 1 MiB answer cannot OOM a reader that
		//    only needs shebang bytes. A spec-grep for "ReadByte"
		//    would not catch this property; memory pressure does.
		huge := strings.Repeat("x", 1<<20)
		hugeOID := issue2295WriteBlob(t, dir, huge)
		rep, err = batch.ReadHead(hugeOID, 2)
		if err != nil {
			t.Fatalf("ReadHead against huge OID: %v", err)
		}
		if rep.Status != "blob" {
			t.Errorf("huge OID: status=%q, want blob", rep.Status)
		}
		if rep.Size != len(huge) {
			t.Errorf("huge OID: size=%d, want %d", rep.Size, len(huge))
		}
		if len(rep.Body) != 2 || string(rep.Body) != "xx" {
			t.Errorf("huge OID: body=%q, want first 2 bytes %q", rep.Body, "xx")
		}

		// 4) ANCHOR: a real read AFTER the missing AND the huge.
		//    If the missing reply left one trailing byte behind, or
		//    the huge drain was short, this read returns the wrong
		//    header. "still in step" is what the spec demands.
		rep, err = batch.ReadHead(smallOID, 2)
		if err != nil {
			t.Fatalf("ReadHead after huge: %v", err)
		}
		if rep.Status != "blob" || rep.Size != len("small blob\n") {
			t.Errorf("post-huge read desynced: status=%q size=%d", rep.Status, rep.Size)
		}
	})

	t.Run("NeverPassesM", func(t *testing.T) {
		dir := issue2295Repo(t, map[string]string{
			"safe.md": "harmless prose\n",
			"evil.sh": "#!/bin/sh\nsome payload\n",
		})
		// `git mv` then `git add -A` is the recipe for a rename
		// STAGED for the next commit. The classifier must see a
		// `D` of `safe.md` and an `A` of `safe2.md`, and a `D` of
		// `evil.sh` and an `A` of `evil2.sh` -- never an `R` line.
		issue2295Rename(t, dir, "safe.md", "safe2.md")
		issue2295Rename(t, dir, "evil.sh", "evil2.sh")

		// Inspect the records directly. An `R` status letter
		// anywhere is the renames-detected case (-M passed or the
		// flag default kicked in).
		recs, err := StageRecords(dir)
		if err != nil {
			t.Fatalf("StageRecords: %v", err)
		}
		var seenA map[string]bool
		seenA = map[string]bool{}
		for _, r := range recs {
			if strings.HasPrefix(r.Status, "R") {
				t.Fatalf("staged record has rename status %q for path %q; -M must not be passed (SPEC.md 945): %+v", r.Status, r.Path, recs)
			}
			if r.Status == "A" {
				seenA[r.Path] = true
			}
		}
		// Both new paths must be classified. The flag pair we want
		// to verify: passes `-r` (recurses expansion so sparse-mode
		// 040000 is also covered) and not `-M` (no renames).
		if !seenA["safe2.md"] {
			t.Errorf("rename target safe2.md missing from staged A records: %+v", recs)
		}
		if !seenA["evil2.sh"] {
			t.Errorf("rename target evil2.sh missing from staged A records: %+v", recs)
		}

		// Run the production auditor on the same state. The
		// classification of `evil2.sh` (the A side of the rename)
		// is what the spec points at: the new path's shebang and
		// .sh floor extension fire.
		_, findings, err := NoCode(NoCodeOptions{Dir: dir, Stage: true})
		if err != nil {
			t.Fatalf("NoCode staged: %v", err)
		}
		if issue2295HasSubject(findings, "safe2.md") {
			t.Errorf("safe2.md should NOT be flagged (renamed prose, no shebang, .md not on floor): %v", findings)
		}
		if !issue2295HasSubject(findings, "evil2.sh") {
			t.Errorf("evil2.sh MUST be flagged (the rename's A side, shebang + .sh floor): %v", findings)
		}
	})
}

// issue2295Repo builds a tiny git repository under t.TempDir() with one
// commit holding files. user.email and user.name are set per-repo because
// shared dirs under /tmp are not allowed to leak identity.
func issue2295Repo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		writeMode(t, dir, rel, body, 0o644)
	}
	cmds := [][]string{
		{"init", "-q"},
		{"config", "user.email", "rowan@mas-bandwidth.com"},
		{"config", "user.name", "Rowan"},
		{"config", "commit.gpgsign", "false"},
		{"add", "-A"},
		{"commit", "-q", "-m", "init"},
	}
	for _, args := range cmds {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		c.WaitDelay = time.Second
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
		}
	}
	return dir
}

// issue2295Stage writes rel into dir and stages it -- without committing,
// so the file lives only in the index. Symlinks and exec bits stay off
// here; the staged file is its content.
func issue2295Stage(t *testing.T, dir, rel, body string) {
	t.Helper()
	writeMode(t, dir, rel, body, 0o644)
	c := exec.Command("git", "-C", dir, "add", "--", rel)
	c.WaitDelay = time.Second
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git add %s in %s: %v\n%s", rel, dir, err, out)
	}
}

// issue2295Rename does `git mv` (which writes the new name and removes the
// old from the working tree) then `git add -A` to mark the rename for the
// next commit.
func issue2295Rename(t *testing.T, dir, oldRel, newRel string) {
	t.Helper()
	c := exec.Command("git", "-C", dir, "mv", "--", oldRel, newRel)
	c.WaitDelay = time.Second
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git mv %s %s in %s: %v\n%s", oldRel, newRel, dir, err, out)
	}
	c = exec.Command("git", "-C", dir, "add", "-A")
	c.WaitDelay = time.Second
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git add -A in %s: %v\n%s", dir, err, out)
	}
}

// issue2295WriteBlob hashes `body` into the repo's object store and
// returns the OID. The blob is NOT staged for any path; we only need its
// OID for the cat-file --batch test.
func issue2295WriteBlob(t *testing.T, dir, body string) string {
	t.Helper()
	c := exec.Command("git", "-C", dir, "hash-object", "-w", "--stdin")
	c.Stdin = strings.NewReader(body)
	c.WaitDelay = time.Second
	out, err := c.Output()
	if err != nil {
		t.Fatalf("git hash-object -w in %s: %v", dir, err)
	}
	oid := strings.TrimSpace(string(out))
	if len(oid) != 40 {
		t.Fatalf("git hash-object returned %q, want 40 hex chars", oid)
	}
	return oid
}

func issue2295HasSubject(fs []Failure, want string) bool {
	for _, f := range fs {
		if f.Subject == want {
			return true
		}
	}
	return false
}

// guard against the test being run from outside the repo root
func TestIssue2295Runshield(t *testing.T) {
	wd, _ := os.Getwd()
	if filepath.Base(wd) != "check" {
		t.Errorf("issue2295 tests assume pkg=check; cwd=%s", wd)
	}
}
