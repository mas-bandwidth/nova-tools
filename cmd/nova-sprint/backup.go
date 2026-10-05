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
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/log"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

func init() {
	// backup is the schedule of the store's host, like snapshot: it needs no
	// actor and reads and writes no card (the class test, coordinator.go).
	verbClasses["backup"] = classMachine
	// a Redis's RDB is read from the disk the store's own host writes it to
	notServed = append(notServed, "backup")
}

// cmdBackup is the sprint backup as one verb (docs/SPEC-SPRINT.md,
// sprint-backup-verb): the store is written to --file, the file is read back
// and restored into a twin that is compared with the store, and the file is
// scanned for secret-shaped text. It changes nothing in the store.
func (a *app) cmdBackup(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("backup")
	file := fs.String("file", "", "the file the store is written to (required; it must not be there)")
	dry := fs.Bool("dry-run", false, "prove the backup and scan it, and write no file")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "backup", argErr("takes no words ", err, pos...))
	}
	var why []string
	if *file == "" {
		why = append(why, "--file <path> is required")
	} else if _, err := os.Lstat(*file); err == nil {
		why = append(why, *file+" is there and a backup never overwrites a file")
	}
	if len(why) > 0 {
		return refuse(stderr, "backup", strings.Join(why, "; ")+"; run: nova-sprint backup --file backup-<date>.json")
	}
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "backup", err.Error())
	}
	var src store.SnapshotSource
	var twin store.SnapshotTwin = store.RDBTwin{}
	switch b := st.B.(type) {
	case *store.Redis:
		src = &redisSource{b: b}
	case *store.Mem:
		src, twin = store.MemSource{M: b}, store.MemTwin{}
	default:
		return refuse(stderr, "backup", "this store has no backup; run: nova-sprint backup --redis <a Redis address or mem:<file>> --file "+*file)
	}
	res, err := takeBackup(context.Background(), *file, *dry, src, twin)
	if err != nil {
		fmt.Fprintf(stderr, "%s backup FAILED: %s\n", prog, err)
		return 1
	}
	if c.json {
		b, _ := json.Marshal(res)
		fmt.Fprintln(stdout, string(b))
	} else if len(res.Secrets) == 0 {
		kind := "BACKUP OK"
		if *dry {
			kind = "BACKUP OK (dry run, no file written)"
		}
		fmt.Fprintf(stdout, kind+" file=%s sha256=%s bytes=%d %s restored=twin compared=%s secrets=none\n", res.File, res.SHA256, res.Bytes, countsText(res.Counts), res.Compared)
	}
	if len(res.Secrets) > 0 {
		fmt.Fprintf(stderr, "%s backup FAILED: the file holds secret-shaped text at line %s; the file is kept private and is not a clean backup (%s sha256=%s): remove the text from the store, then run: nova-sprint backup --file <a new path>\n", prog, joinInts(res.Secrets), res.File, res.SHA256)
		return 1
	}
	return 0
}

// backupResult is one backup: where it is, what it holds, how it was proved,
// and the lines (1-based) of the file the scan found secret-shaped text on.
type backupResult struct {
	File     string               `json:"file"`
	SHA256   string               `json:"sha256"`
	Bytes    int                  `json:"bytes"`
	Counts   store.SnapshotCounts `json:"counts"`
	Compared string               `json:"compared"`
	Secrets  []int                `json:"secrets,omitempty"`
}

// takeBackup writes the store's snapshot to file (private, whole or not at
// all), reads it back, loads it into a twin and compares it with the store,
// then scans it; dry writes no file and proves the snapshot itself. A file that does not read back or does not restore is removed
// (it is no backup); one the scan flags is kept, private, and reported.
func takeBackup(ctx context.Context, file string, dry bool, src store.SnapshotSource, twin store.SnapshotTwin) (backupResult, error) {
	var res backupResult
	doc, live, err := src.Save(ctx)
	if err != nil {
		return res, fmt.Errorf("the store gave no snapshot: %w", err)
	}
	copied := doc
	fail := func(err error) (backupResult, error) { return res, err }
	if !dry {
		if err := writeAtomic(file, doc); err != nil {
			return res, fmt.Errorf("%s cannot be written: %v", file, err)
		}
		fail = func(err error) (backupResult, error) {
			_ = os.Remove(file) // ignored: removing the failed copy; the error that failed it is what is returned
			return res, err
		}
		if copied, err = os.ReadFile(file); err != nil {
			return fail(fmt.Errorf("%s cannot be read back: %v", file, err))
		}
	}
	sum := sha256.Sum256(doc)
	if sha256.Sum256(copied) != sum {
		return fail(fmt.Errorf("%s does not match the store's snapshot on reading it back; the file was removed", file))
	}
	got, err := twin.Load(copied)
	if err != nil {
		return fail(fmt.Errorf("%s does not restore into a twin: %v; the file was removed", file, err))
	}
	if d := countsAgree(live, got); d != "" {
		return fail(fmt.Errorf("%s restored with other counts than the store held (%s); the file was removed", file, d))
	}
	res = backupResult{File: file, SHA256: hex.EncodeToString(sum[:]), Bytes: len(copied), Counts: got, Compared: "counts+crc"}
	if _, ok := twin.(store.MemTwin); ok {
		// the twin's own snapshot is the file again, byte for byte
		back := store.NewMem()
		if err := back.Restore(copied); err != nil {
			return fail(fmt.Errorf("%s does not restore into a twin: %v; the file was removed", file, err))
		}
		again, err := back.Snapshot()
		if err != nil || !bytes.Equal(again, copied) {
			return fail(errors.New(file + " restored to a twin that differs from the store's document; the file was removed"))
		}
		res.Compared = "document"
	}
	res.Secrets = secretLines(copied)
	return res, nil
}

// countsAgree is why two counts differ: "" is they agree (a count of -1 is
// unknown and is not compared).
func countsAgree(want, got store.SnapshotCounts) string {
	var why []string
	if want.Keys >= 0 && got.Keys >= 0 && want.Keys != got.Keys {
		why = append(why, fmt.Sprintf("keys %d, twin %d", want.Keys, got.Keys))
	}
	if want.Cards >= 0 && got.Cards >= 0 && want.Cards != got.Cards {
		why = append(why, fmt.Sprintf("cards %d, twin %d", want.Cards, got.Cards))
	}
	return strings.Join(why, ", ")
}

// secretLines is the 1-based lines of doc that the log redactor would change:
// the same provider keys, PEM blocks, bearer credentials, keyed values and
// long mixed runs it hides from every log (internal/log/redact.go). It names
// lines, never values.
func secretLines(doc []byte) []int {
	var out []int
	for i, l := range strings.Split(string(doc), "\n") {
		if log.Redact(l) != l {
			out = append(out, i+1)
		}
	}
	return out
}

func joinInts(n []int) string {
	s := make([]string, len(n))
	for i, v := range n {
		s[i] = strconv.Itoa(v)
	}
	return strings.Join(s, ", ")
}
