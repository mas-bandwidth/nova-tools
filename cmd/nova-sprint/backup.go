package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	// backup is the schedule of the store's host, like snapshot: it needs no
	// actor and it reads and writes no card (coordinator.go).
	verbClasses["backup"] = classMachine
	// a Redis store's RDB is read from the disk of the host that wrote it
	notServed = append(notServed, "backup")
}

// backupShape is one row of internal/hygiene/keyshapes.txt: a name a finding
// may print and the form a key takes. TestBackupShapesAreTheHygieneShapes
// keeps this list identical with the file's rows.
type backupShape struct {
	name string
	re   *regexp.Regexp
}

var backupShapes = []backupShape{
	{"pem-private-key", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)},
	{"age-secret-key", regexp.MustCompile(`AGE-SECRET-KEY-1[0-9A-Za-z]{10,}`)},
	{"forge-token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`)},
	{"forge-fine-grained-token", regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`)},
	{"anthropic-api-key", regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{20,}`)},
	{"openai-api-key", regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`)},
	{"xai-api-key", regexp.MustCompile(`xai-[A-Za-z0-9-]{20,}`)},
	{"google-api-key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{30,}`)},
	{"slack-token", regexp.MustCompile(`xox[abprs]-[0-9A-Za-z-]{10,}`)},
	{"aws-access-key-id", regexp.MustCompile(`(A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z0-9]{16}`)},
	{"ssh-private-key-blob", regexp.MustCompile(`PRIVATE KEY BLOCK-----`)},
	{"json-web-token", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
}

// backupFindings is every shape the bytes hold, as `shape=<name> line=<n>`;
// the matched text is never returned.
func backupFindings(b []byte) []string {
	var out []string
	for i, line := range bytes.Split(b, []byte("\n")) {
		for _, s := range backupShapes {
			if s.re.Match(line) {
				out = append(out, fmt.Sprintf("shape=%s line=%d", s.name, i+1))
			}
		}
	}
	return out
}

// backupSame says two documents hold the same data: decoded, an empty
// collection and an absent one are the same, as the twin's restore writes the
// one the store wrote as the other.
func backupSame(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(backupNorm(x), backupNorm(y))
}

func backupNorm(v any) any {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			return nil
		}
		for k, e := range t {
			t[k] = backupNorm(e)
		}
	case []any:
		if len(t) == 0 {
			return nil
		}
		for i, e := range t {
			t[i] = backupNorm(e)
		}
	}
	return v
}

// cmdBackup is the sprint backup, formerly a hand procedure: the store is
// written to --file, the file is restored into a twin and compared with the
// store, and scanned for key shapes. A file that fails either check is
// removed, so a backup that is there is one that restored and holds no key
// (SPEC-SPRINT, sprint-backup-verb).
func (a *app) cmdBackup(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("backup")
	file := fs.String("file", "", "the file the backup is written to (required; a file already there is refused)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "backup", argErr("takes no words ", err, pos...))
	}
	if *file == "" {
		return refuse(stderr, "backup", "wants --file <path>; run: nova-sprint backup --file sprint.backup")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "backup", err.Error())
	}
	var src store.SnapshotSource
	var twin store.SnapshotTwin = store.RDBTwin{}
	mem, isMem := st.B.(*store.Mem)
	switch b := st.B.(type) {
	case *store.Redis:
		src = &redisSource{b: b}
	case *store.Mem:
		src, twin = store.MemSource{M: b}, store.MemTwin{}
	default:
		return refuse(stderr, "backup", "this store has no backup; run: nova-sprint backup --redis <a Redis address> --file "+*file)
	}
	data, live, err := src.Save(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "%s backup FAILED: the store gave no backup: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}
	f, err := os.OpenFile(*file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			// a file already there is a failure of the run, not of its words: the schedule's next run, not the caller, changes the name
			fmt.Fprintf(stderr, "%s backup REFUSED: %s already exists and is never overwritten; run: nova-sprint backup --file <a new file>\n", prog, oneline.Escape(*file))
			return 1
		}
		return refuse(stderr, "backup", fmt.Sprintf("%s cannot be written: %v; run: nova-sprint backup --file <a new file in a directory that exists>", *file, err))
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	fail := func(why string) int {
		// ignored: removing the failed file; the failure that condemned it is what is reported
		_ = os.Remove(*file)
		fmt.Fprintf(stderr, "%s backup FAILED: %s; %s was removed\n", prog, oneline.Escape(why), *file)
		return 1
	}
	if werr != nil {
		return fail(fmt.Sprintf("%s cannot be written: %v", *file, werr))
	}
	got, err := os.ReadFile(*file)
	if err != nil {
		return fail(fmt.Sprintf("%s cannot be read back: %v", *file, err))
	}
	sum := sha256.Sum256(got)
	counts, err := twin.Load(got)
	if err != nil {
		return fail("the file does not load into a twin: " + err.Error())
	}
	// a count of -1 is unknown (the loader or the source cannot say) and is not compared
	if (live.Keys >= 0 && counts.Keys >= 0 && live.Keys != counts.Keys) || (live.Cards >= 0 && counts.Cards >= 0 && live.Cards != counts.Cards) {
		return fail(fmt.Sprintf("the twin holds other counts than the store (store %s, twin %s)", countsText(live), countsText(counts)))
	}
	restored := "checked"
	if isMem {
		// the twin, restored from the file, writes the same document the store does
		tw := store.NewMem()
		if err := tw.Restore(got); err != nil {
			return fail("the file does not restore into a twin: " + err.Error())
		}
		again, err := tw.Snapshot()
		if err != nil {
			return fail("the twin cannot be read: " + err.Error())
		}
		now, err := mem.Snapshot()
		if err != nil {
			return fail("the store cannot be read: " + err.Error())
		}
		if !backupSame(again, got) || !backupSame(now, got) {
			return fail("the twin restored from the file is not the store")
		}
		restored = "equal"
	}
	if found := backupFindings(got); len(found) > 0 {
		return fail("the file holds the shape of a secret (" + strings.Join(found, "; ") + "); the text is not printed")
	}
	hexsum := hex.EncodeToString(sum[:])
	line := fmt.Sprintf("BACKUP OK file=%s sha256=%s bytes=%d %s restored=%s secrets=none", *file, hexsum, len(got), countsText(counts), restored)
	sayOK(stdout, c.json, "backup", line, map[string]any{"file": *file, "sha256": hexsum, "bytes": len(got), "counts": counts, "restored": restored, "secrets": "none"})
	return 0
}
