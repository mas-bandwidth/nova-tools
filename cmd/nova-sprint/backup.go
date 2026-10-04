package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/keyshape"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// backup.go is the sprint's backup (docs/SPEC-SPRINT.md, backup): the hand
// procedure a child once ran with no card — write the sprint's state to a
// file, restore it by hand, scan it for secrets — as one verb that proves
// its own file before it says OK. One read of the store (fenced, every
// record the four tables hold a record of, the epoch's log and the
// notifications whole), one file written whole or not at all (writeAtomic,
// twin.go), and two proofs: the secrets scan runs on the bytes the real run
// would write, before anything is written, so a secret never reaches the
// file; the restore test reads the file back and holds it to the bytes the
// verb wrote, this build's format, and a re-encode of the same bytes, so the
// backup can leave the machine and still be the sprint it was taken from.

// backupVersion names the document's shape; a document of another version is
// refused, never guessed at (as a twin snapshot is, store.Mem.Restore).
const backupVersion = 1

// backupPage is how many log or notification events one read of the backup
// takes, as the log's own whole read pages (store's logPage).
const backupPage = 5000

// backupDoc is the sprint as one JSON document: every field the verb read, so
// a restored state answers as the sprint it was taken from did.
type backupDoc struct {
	Version     int                `json:"version"`
	When        time.Time          `json:"when"`
	Epoch       uint64             `json:"epoch"`
	Gen         uint64             `json:"gen"` // the fence generation the tables were read at
	Running     bool               `json:"running"`
	Queued      int                `json:"queued"`
	Coordinator string             `json:"coordinator,omitempty"`
	Owner       string             `json:"owner,omitempty"`
	Rules       string             `json:"rules,omitempty"` // the child rules file's recorded path
	Seat        *sprint.SeatChange `json:"seat,omitempty"`
	Tables      []backupTable      `json:"tables"`
	Log         []sprint.Line      `json:"log,omitempty"`
	Notes       []sprint.Note      `json:"notes,omitempty"`
	Open        []backupOpen       `json:"open,omitempty"` // the open judgments
	Friends     []store.FriendRow  `json:"friends,omitempty"`
}

// backupTable is one of the sprint's four tables as the backup holds it: its
// rows and text cells, its properties and revision, and every record it
// holds, placed and kept, with each one's fields whole (a brief is a child's
// whole brief).
type backupTable struct {
	Name     string                       `json:"name"`
	Revision uint64                       `json:"revision"`
	Rows     []string                     `json:"rows,omitempty"`
	Texts    map[string]map[string]string `json:"texts,omitempty"`
	Props    map[string]string            `json:"props,omitempty"`
	Cards    []backupCard                 `json:"cards,omitempty"`
}

// backupCard is one record of one table: its place (empty when the record is
// kept but unplaced), score, revision and fields.
type backupCard struct {
	Table  string            `json:"table"`
	ID     string            `json:"id"`
	Row    string            `json:"row,omitempty"`
	Col    string            `json:"col,omitempty"`
	Score  float64           `json:"score"`
	Rev    uint64            `json:"rev"`
	Fields map[string]string `json:"fields,omitempty"`
}

// backupOpen is one open judgment as the backup holds it: the open key and
// the note it is of.
type backupOpen struct {
	Key  string      `json:"key"`
	Note sprint.Note `json:"note"`
}

// secretSite is one secret-shaped site of the backup's text: the line it
// stands on and what it is. The value itself is never printed (the
// zero-leak discipline, internal/secrets).
type secretSite struct {
	Line int    `json:"line"`
	What string `json:"what"`
}

// The secrets scan's three shapes: an age private key (the secrets store's
// own key shape, which its check holds out of the store), a PEM private key,
// and a value of at least secretValueLen characters under a secret-shaped
// name that carries an underscore (internal/keyshape: KEY, TOKEN, SECRET,
// PASSWORD or PASSWD; the underscore keeps a word that only contains one of
// them, like monkey, from naming a secret).
const (
	ageKeyPrefix   = "AGE-SECRET-KEY-1"
	pemBegin       = "-----BEGIN"
	pemKey         = "PRIVATE KEY-----"
	secretValueLen = 8
)

// secretPairRe is a document line that sets a string value under a name: the
// scan's third shape, a credential named in the clear.
var secretPairRe = regexp.MustCompile(`"([A-Za-z_][A-Za-z0-9_]*)": "([^"]*)"`)

// cmdBackup is the sprint's backup: one read of the store, one file, two
// proofs. The reads are the sprint's own (a fenced read of the four tables
// with every record the store holds a record of, the epoch's log and the
// notifications paged whole), the file is written whole or not at all, and
// the verb says OK only when the file has proven itself.
func (a *app) cmdBackup(args []string, stdout, stderr io.Writer) int {
	fs, c := a.verbSetup("backup")
	dry := fs.Bool("dry-run", false, "say the file the real run would write, with its counts: the secrets scan still runs on the bytes the real run would write, and nothing is written (no file, so no restore test)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return refuse(stderr, "backup", argErr("wants one file to write the sprint's backup to ", err, pos...))
	}
	file := pos[0]
	st, err := a.store(*c)
	if err != nil {
		return refuse(stderr, "backup", err.Error())
	}
	doc, err := a.readBackupDoc(context.Background(), st)
	if err != nil {
		return a.readFailed("backup", err, stderr)
	}
	encoded, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		return a.backupFailed(*c, file, fmt.Errorf("the document did not encode: %w", err), stdout, stderr)
	}
	if sites := secretScan(encoded); len(sites) > 0 {
		return a.backupSecrets(*c, file, sites, stdout, stderr)
	}
	restore := "not-run"
	if !*dry {
		if err := writeAtomic(file, encoded); err != nil {
			return a.backupFailed(*c, file, fmt.Errorf("the file was not written: %w", err), stdout, stderr)
		}
		if err := backupRestores(file, encoded); err != nil {
			return a.backupFailed(*c, file, fmt.Errorf("the restore test failed: %w", err), stdout, stderr)
		}
		restore = "ok"
	}
	cards := 0
	for _, t := range doc.Tables {
		cards += len(t.Cards)
	}
	facts := map[string]any{
		"file": file, "bytes": len(encoded), "epoch": doc.Epoch, "gen": doc.Gen,
		"tables": len(doc.Tables), "cards": cards, "lines": len(doc.Log), "notes": len(doc.Notes), "judgments": len(doc.Open),
		"secrets": 0, "restore": restore, "dry_run": *dry,
	}
	line := fmt.Sprintf("BACKUP OK file=%s bytes=%d epoch=%d gen=%d tables=%d cards=%d lines=%d notes=%d judgments=%d secrets=0 restore=%s",
		oneline.Field(file), len(encoded), doc.Epoch, doc.Gen, len(doc.Tables), cards, len(doc.Log), len(doc.Notes), len(doc.Open), restore)
	if *dry {
		line += " dry_run=yes"
	}
	sayOK(stdout, c.json, "backup", line, facts)
	return 0
}

// readBackupDoc reads the sprint's whole state as its verbs read it: the four
// tables with every record the store holds a record of (placed and kept,
// their fields whole), the epoch's log and the notifications whole, the open
// judgments, the machine's state, the coordinator, the owner, the rules
// file, the seat and the friends' roster, read once, fenced, at one
// generation, so the document is the sprint at one instant.
func (a *app) readBackupDoc(ctx context.Context, st *store.Store) (backupDoc, error) {
	doc := backupDoc{Version: backupVersion, When: a.now()}
	// every record the store holds a record of, named for the fenced read: a
	// record the change log names and no cell holds (a dropped card's) is
	// read with them
	kept := map[string][]string{}
	for _, name := range store.All {
		ids, err := st.B.RecordIDs(ctx, st.Names.Table(name))
		if err != nil {
			return doc, fmt.Errorf("the records of %s: %w", st.Names.Table(name), err)
		}
		kept[name] = ids
	}
	snap, gen, err := st.Fenced(ctx, store.All, func(*sprint.Snapshot) map[string][]string { return kept }, nil)
	if err != nil {
		return doc, err
	}
	doc.Epoch, doc.Gen, doc.Running, doc.Queued, doc.Coordinator = snap.Epoch, gen, snap.Running, snap.QueueLen, snap.Coordinator
	for _, o := range snap.Open {
		doc.Open = append(doc.Open, backupOpen{Key: o.Key, Note: o.Note})
	}
	for _, name := range store.All {
		t := snap.T(name)
		if t == nil {
			return doc, fmt.Errorf("the sprint has no %s table", name)
		}
		tb := backupTable{Name: name, Revision: t.Revision, Rows: t.Rows(), Texts: t.Texts, Props: t.Props()}
		for _, card := range t.Cards() {
			tb.Cards = append(tb.Cards, backupCard{Table: name, ID: card.ID, Row: card.Row, Col: card.Col, Score: card.Score, Rev: card.Rev, Fields: card.Fields})
		}
		slices.SortFunc(tb.Cards, func(a, b backupCard) int { return strings.Compare(a.ID, b.ID) })
		doc.Tables = append(doc.Tables, tb)
	}
	if doc.Owner, err = st.Owner(ctx); err != nil {
		return doc, err
	}
	if doc.Rules, err = st.RulesPath(ctx); err != nil {
		return doc, err
	}
	seat, ok, err := st.Seat(ctx)
	if err != nil {
		return doc, err
	}
	if ok {
		doc.Seat = &seat
	}
	if doc.Friends, err = st.FriendRows(ctx, a.now()); err != nil {
		return doc, err
	}
	if doc.Log, err = st.Log(ctx); err != nil {
		return doc, err
	}
	doc.Notes, err = a.notesWhole(ctx, st)
	return doc, err
}

// notesWhole is the notifications, every one, read a page at a time as the
// log's own whole read pages them.
func (a *app) notesWhole(ctx context.Context, st *store.Store) ([]sprint.Note, error) {
	var all []sprint.Note
	after := ""
	for {
		notes, ids, err := st.B.NotesSince(ctx, after, backupPage)
		if err != nil {
			return nil, err
		}
		all = append(all, notes...)
		if len(ids) < backupPage {
			return all, nil
		}
		after = ids[len(ids)-1]
	}
}

// secretScan is the backup's secrets scan: every line of the document's text
// that carries a secret-shaped value — an age private key, a PEM private key,
// or a value of at least secretValueLen characters under a secret-shaped name
// that carries an underscore — each named by its line and what it is, never
// by its value.
func secretScan(doc []byte) []secretSite {
	var sites []secretSite
	for i, line := range strings.Split(string(doc), "\n") {
		switch {
		case strings.Contains(line, ageKeyPrefix):
			sites = append(sites, secretSite{Line: i + 1, What: "an age private key"})
		case strings.Contains(line, pemBegin) && strings.Contains(line, pemKey):
			sites = append(sites, secretSite{Line: i + 1, What: "a PEM private key"})
		default:
			if m := secretPairRe.FindStringSubmatch(line); m != nil && strings.Contains(m[1], "_") &&
				keyshape.SecretName(m[1]) && len(strings.TrimSpace(m[2])) >= secretValueLen {
				sites = append(sites, secretSite{Line: i + 1, What: "a value under the secret-shaped name " + m[1]})
			}
		}
	}
	return sites
}

// backupRestores is the restore test: the file on disk, read back, is the
// backup — its bytes are the bytes the verb wrote, they parse strictly as
// this build's backup format, and the state they parse to re-encodes to the
// same bytes, so the format loses nothing a restore needs.
func backupRestores(path string, encoded []byte) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, encoded) {
		return errors.New("the file on disk is not the bytes the verb wrote")
	}
	doc, err := decodeBackup(raw)
	if err != nil {
		return err
	}
	again, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		return err
	}
	if !bytes.Equal(again, raw) {
		return errors.New("the file's state, read back and written again, is not the file")
	}
	return nil
}

// decodeBackup is a backup document read strictly: unknown fields, other
// versions and trailing bytes are refused, never guessed at.
func decodeBackup(doc []byte) (backupDoc, error) {
	var d backupDoc
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return d, fmt.Errorf("not a backup this build reads: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return d, errors.New("not a backup this build reads: the document holds more than the backup")
	}
	if d.Version != backupVersion {
		return d, fmt.Errorf("a backup of version %d; this build reads version %d", d.Version, backupVersion)
	}
	return d, nil
}

// backupSecrets is the secrets scan's answer: each site on its own SECRET
// line, bounded by --max, the summary on stderr, exit 1, and no file
// written: the scan runs before the write.
func (a *app) backupSecrets(c common, file string, sites []secretSite, stdout, stderr io.Writer) int {
	var lines []string
	for _, s := range sites {
		lines = append(lines, fmt.Sprintf("%s at line %d of the backup's text", s.What, s.Line))
	}
	listed(stderr, "SECRET", lines, c.max, "backup")
	why := fmt.Sprintf("the backup's text would hold secret-shaped values (secrets=%d); the file was not written; run: nova-sprint backup %s again once the value is out of the sprint's state", len(sites), oneline.Field(file))
	if c.json {
		sayFailed(stdout, "backup", why, map[string]any{"file": file, "secrets": len(sites), "secret_sites": boundSites(sites, c.max), "restore": "not-run"})
		return 1
	}
	fmt.Fprintf(stderr, "BACKUP FAILED secrets=%d; %s\n", len(sites), oneline.Escape(why))
	return 1
}

// backupFailed is the verb's own failure: the write or the restore test did
// not hold, said on stderr with the remedy, exit 1.
func (a *app) backupFailed(c common, file string, err error, stdout, stderr io.Writer) int {
	why := err.Error() + "; run: nova-sprint backup " + oneline.Field(file) + " again"
	if c.json {
		sayFailed(stdout, "backup", why, map[string]any{"file": file})
		return 1
	}
	fmt.Fprintf(stderr, "BACKUP FAILED %s\n", oneline.Escape(why))
	return 1
}

// boundSites is at most max of the sites, all of them when max is 0.
func boundSites(sites []secretSite, max int) []secretSite {
	if max > 0 && len(sites) > max {
		return sites[:max]
	}
	return sites
}

// sayFailed prints a verb's failure as one JSON object where a reader of
// --json looks for it: the same facts sayOK holds, with the failure's own
// words.
func sayFailed(w io.Writer, verbName, why string, facts map[string]any) {
	o := map[string]any{"verb": verbName, "status": "failed", "exit": 1, "error": why}
	maps.Copy(o, facts)
	// ignored: a map of strings, numbers and lists of strings always encodes
	b, _ := json.Marshal(o)
	fmt.Fprintln(w, string(b))
}
