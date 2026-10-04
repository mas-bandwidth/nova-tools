package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backup_test.go pins the sprint's backup as a verb (docs/SPEC-SPRINT.md,
// backup): the hand procedure a child once ran with no card — write the
// sprint's state to a file, restore it by hand, scan it for secrets — is one
// verb that proves its own file before it says OK: the file reads back as the
// bytes the verb wrote, parses strictly as this build's format and re-encodes
// to the same bytes (the restore test), and its text carries no secret-shaped
// value (the secrets scan), so the backup can leave the machine.

// playedSprint is a sprint one card into review, the clock stepped by hand:
// s1-1 taken, finished at a head with a report, its move drained by the tick.
func playedSprint(t *testing.T) *testApp {
	t.Helper()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	ta.ok("add --stream s1 --count 2")
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 s1-1.w1@1")
	ta.ok("finish --as m1 s1-1.w1@1 --epoch 0 --head 9f3c2e1 --report 'the work is done'")
	ta.ok("tick")
	return ta
}

func TestBackupWritesTheSprintProvesItRestoresAndRefusesASecret(t *testing.T) {
	t.Parallel()

	t.Run("the backup holds the sprint and proves it restores", func(t *testing.T) {
		t.Parallel()
		ta := playedSprint(t)
		file := filepath.Join(t.TempDir(), "sprint-backup.json")
		out := ta.ok("backup " + file)
		assert.Contains(t, out, "BACKUP OK file=", out)
		assert.Contains(t, out, " secrets=0", out)
		assert.Contains(t, out, " restore=ok", out)

		raw, err := os.ReadFile(file)
		require.NoError(t, err, "the verb wrote the file it named")
		require.True(t, json.Valid(raw), "the file is one JSON document")
		text := string(raw)
		assert.Contains(t, text, `"version"`, "the document names its shape")
		assert.Contains(t, text, "s1-1.w1", "the attempt's work card, with its fields")
		assert.Contains(t, text, "the work is done", "the attempt's report field, whole")
		assert.Contains(t, text, "9f3c2e1", "the head the card finished at")
		assert.Contains(t, text, `"log"`, "the epoch's log lines")
	})

	t.Run("a secret-shaped value in the sprint's state refuses the backup", func(t *testing.T) {
		t.Parallel()
		// the third shape, a value under a secret-shaped name, is pinned at
		// the scan (TestTheBackupSecretsScanNamesItsThreeShapes): inside a
		// card's field the pair is JSON-escaped, so the verb-level shapes are
		// the two key materials, which the scan reads as they stand
		for _, tc := range []struct {
			name string
			what string
			text string
		}{
			{"an age private key", "an age private key", "the age key AGE-SECRET-KEY-1QP33N7UJGM2XZVGJQ5MW7FVQ is in the report"},
			{"a PEM private key", "a PEM private key", "the key -----BEGIN RSA PRIVATE KEY----- in the report"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				ta := newTestApp(t)
				ta.ok("init --readers reader-a,reader-b --members m1")
				ta.ok("add --stream s1 --count 2")
				ta.ok("start")
				ta.ok("tick")
				ta.ok("take --as m1 s1-1.w1@1")
				ta.ok("finish --as m1 s1-1.w1@1 --epoch 0 --head 9f3c2e1 --report '" + tc.text + "'")
				ta.ok("tick")
				file := filepath.Join(t.TempDir(), "secret-backup.json")
				code, out, errs := ta.do("backup " + file)
				assert.Equal(t, 1, code, "the backup is refused, not written: %s%s", out, errs)
				assert.Contains(t, errs, "SECRET ", errs)
				assert.Contains(t, errs, tc.what, errs)
				// the value stands at two sites: the work card's report field
				// and the log line the finish wrote of it
				assert.Contains(t, errs, "BACKUP FAILED secrets=2", errs)
				assert.NotContains(t, out, "BACKUP OK", "no OK word on a non-zero exit")
				_, err := os.Stat(file)
				assert.True(t, os.IsNotExist(err), "the secret never reaches the file: the scan runs before the write")
			})
		}
	})

	t.Run("dry run reads the sprint and scans it, writes nothing", func(t *testing.T) {
		t.Parallel()
		ta := playedSprint(t)
		file := filepath.Join(t.TempDir(), "sprint-backup.json")
		out := ta.ok("backup --dry-run " + file)
		assert.Contains(t, out, "BACKUP OK file="+file, out)
		assert.Contains(t, out, " secrets=0", "the scan runs on the bytes the real run would write")
		assert.Contains(t, out, " restore=not-run", "no file, so no restore test")
		assert.Contains(t, out, " dry_run=yes", out)
		_, err := os.Stat(file)
		assert.True(t, os.IsNotExist(err), "a dry run writes no file")
	})

	t.Run("--json is one object of the same facts", func(t *testing.T) {
		t.Parallel()
		ta := playedSprint(t)
		file := filepath.Join(t.TempDir(), "sprint-backup.json")
		var got struct {
			Verb    string `json:"verb"`
			Status  string `json:"status"`
			Exit    int    `json:"exit"`
			File    string `json:"file"`
			Epoch   int    `json:"epoch"`
			Gen     int    `json:"gen"`
			Tables  int    `json:"tables"`
			Cards   int    `json:"cards"`
			Lines   int    `json:"lines"`
			Notes   int    `json:"notes"`
			Secrets int    `json:"secrets"`
			Restore string `json:"restore"`
			DryRun  bool   `json:"dry_run"`
		}
		ta.json("backup "+file, &got)
		assert.Equal(t, "backup", got.Verb)
		assert.Equal(t, "ok", got.Status)
		assert.Equal(t, 0, got.Exit)
		assert.Equal(t, file, got.File)
		assert.Equal(t, 0, got.Epoch)
		assert.Greater(t, got.Gen, 0, "the fence generation the tables were read at")
		assert.Equal(t, 4, got.Tables, "the work, readers, merge and fleet tables")
		assert.Greater(t, got.Cards, 0, "every record the four tables hold")
		assert.Greater(t, got.Lines, 0, "the epoch's log lines")
		assert.Equal(t, 0, got.Secrets)
		assert.Equal(t, "ok", got.Restore)
		assert.False(t, got.DryRun)
	})
}

// The backup names the file it could not write beside the sprint it read.
func TestBackupRefusesAFileItCannotWrite(t *testing.T) {
	t.Parallel()
	ta := playedSprint(t)
	code, out, errs := ta.do("backup " + filepath.Join(t.TempDir(), "no-such-dir", "sprint-backup.json"))
	assert.Equal(t, 1, code, "the verb failed, saying why: %s", errs)
	assert.Contains(t, errs, "BACKUP FAILED", errs)
	assert.True(t, strings.Contains(errs, "no-such-dir") || strings.Contains(errs, "not exist"), "the line names the file: %s", errs)
	assert.NotContains(t, out, "BACKUP OK", "no OK word on a non-zero exit")
}

// The secrets scan names its three shapes, and only them: a word that
// merely contains a secret word (monkey, which holds key), or a short value
// under a secret-shaped name, is not a secret.
func TestTheBackupSecretsScanNamesItsThreeShapes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		line string
		want []string
	}{
		{"an age private key", `"report": "the age key AGE-SECRET-KEY-1QP33N7UJGM2XZVGJQ5MW7FVQ is here"`, []string{"an age private key"}},
		{"a PEM private key", `"report": "the key -----BEGIN RSA PRIVATE KEY----- is here"`, []string{"a PEM private key"}},
		{"a value under a secret-shaped name", `"api_key": "AKIA1234567890ABCDEF"`, []string{"a value under the secret-shaped name api_key"}},
		{"a word that contains a secret word is not a name", `"monkey": "AKIA1234567890ABCDEF"`, nil},
		{"a short value under a secret-shaped name", `"api_key": "short"`, nil},
		{"a name with no secret word in it", `"the_report": "AKIA1234567890ABCDEF"`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sites := secretScan([]byte(tc.line))
			var got []string
			for _, s := range sites {
				got = append(got, s.What)
			}
			assert.Equal(t, tc.want, got, "the scan's answer for %s", tc.line)
			if len(tc.want) > 0 {
				assert.Equal(t, 1, sites[0].Line, "the site names its line, one-based")
			}
		})
	}
}

// The restore proof refuses a file that is not the backup the verb wrote, and
// a document this build does not read: the proof, not the reader's eye, is
// what says the file restores.
func TestTheBackupRestoreProofRefusesADoctoredFile(t *testing.T) {
	t.Parallel()

	t.Run("the file the verb wrote restores", func(t *testing.T) {
		t.Parallel()
		ta := playedSprint(t)
		file := filepath.Join(t.TempDir(), "sprint-backup.json")
		ta.ok("backup " + file)
		raw, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.NoError(t, backupRestores(file, raw), "the verb's own file, read back, restores")
	})

	t.Run("a file that is not the bytes the verb wrote", func(t *testing.T) {
		t.Parallel()
		file := filepath.Join(t.TempDir(), "sprint-backup.json")
		require.NoError(t, os.WriteFile(file, []byte("{\"version\":1}\n"), 0o600))
		assert.ErrorContains(t, backupRestores(file, []byte("{\"version\":1}\n ")), "not the bytes the verb wrote")
	})

	t.Run("a document that does not re-encode to itself", func(t *testing.T) {
		t.Parallel()
		file := filepath.Join(t.TempDir(), "sprint-backup.json")
		compact := []byte(`{"version":1,"when":"2030-01-02T03:04:05Z"}`)
		require.NoError(t, os.WriteFile(file, compact, 0o600))
		assert.ErrorContains(t, backupRestores(file, compact), "read back and written again, is not the file")
	})

	t.Run("a document this build does not read", func(t *testing.T) {
		t.Parallel()
		for _, doc := range []string{`{"version":1,"surprise":true}`, `{"version":1} more`} {
			_, err := decodeBackup([]byte(doc))
			assert.ErrorContains(t, err, "not a backup this build reads", "decodeBackup(%q)", doc)
		}
		_, err := decodeBackup([]byte(`{"version":99}`))
		assert.ErrorContains(t, err, "a backup of version 99; this build reads version 1", "a document of another version is refused, never guessed at")
	})
}
