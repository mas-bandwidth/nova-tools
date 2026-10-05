package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A read asked of a friend (the owner, 2026-10-05: reading should happen
// continually). friend sync delivers each read asked of reader-<friend> the
// way it delivers a card: a directory under her inbox, the read's instructions,
// the brief and the worker's report, and a bus note. Once the read is
// recorded the directory goes. A symlink is refused and left in place.

// friendReadsOf delivers the reads asked of reader-<name>, and any still
// reading whose directory was not written yet, into
// <dir>/inbox/reads/<read-card>/, and removes a directory once that read is
// no longer asked or reading. delivered is how many READ.md files this pass
// wrote. removed is how many directories it took away.
func (a *app) friendReadsOf(ctx context.Context, st *store.Store, name, dir string, say func(string)) (delivered, removed int, err error) {
	reader := sprint.ReaderPrefix + name
	cards, err := st.ReadCells(ctx, sprint.Readers, reader, sprint.Asked, sprint.Reading)
	if err != nil {
		return 0, 0, err
	}
	packets, err := st.Packets(ctx, cards)
	if err != nil {
		return 0, 0, err
	}
	live := map[string]bool{}
	for _, c := range cards {
		if c != nil {
			live[c.ID] = true
		}
	}
	reads := filepath.Join(dir, "inbox", "reads")
	if err := refuseReadSymlink(reads); err != nil {
		return 0, 0, err
	}
	for _, p := range packets {
		if !sprint.ValidCardID(p.Card) {
			continue
		}
		cardDir := filepath.Join(reads, p.Card)
		if err := refuseReadSymlink(cardDir); err != nil {
			return delivered, removed, err
		}
		readPath := filepath.Join(cardDir, "READ.md")
		if _, err := os.Lstat(readPath); err == nil {
			continue
		}
		if err := os.MkdirAll(cardDir, 0o755); err != nil {
			return delivered, removed, err
		}
		if err := writeIfAbsent(filepath.Join(cardDir, "BRIEF.md"), []byte(p.Brief)); err != nil {
			return delivered, removed, err
		}
		if err := writeIfAbsent(filepath.Join(cardDir, "WORKER-REPORT.txt"), []byte(p.Report)); err != nil {
			return delivered, removed, err
		}
		if err := writeIfAbsent(readPath, []byte(readInstructions(name, p))); err != nil {
			return delivered, removed, err
		}
		delivered++
		a.noteReadAsked(ctx, st, name, p, say)
	}
	removed, err = removeRecordedReads(reads, live)
	return delivered, removed, err
}

// readInstructions is READ.md: what a reader runner does with the read.
func readInstructions(friend string, p sprint.Packet) string {
	reader := sprint.ReaderPrefix + friend
	head, branch := p.Head, p.WorkBranch
	if head == "" {
		head = "(none recorded)"
	}
	if branch == "" {
		branch = "(none recorded)"
	}
	return fmt.Sprintf(`Read %s of %s, attempt %d, as %s.
Clone at the head %s (branch %s).
Judge the merge-base diff against the brief.
Run the touched packages' vet and tests on a Linux bench.
Finish with one of:
nova-sprint read --as %s --ok %s
nova-sprint read --as %s --broken %s --finding <file:line>
nova-sprint read --as %s --return %s --reason <reason>
`, p.Card, p.Primary, p.Attempt, reader, head, branch, reader, p.Card, reader, p.Card, reader, p.Card)
}

// noteReadAsked sends the bus note. The inbox files are the record: a send
// that fails is said and does not fail the delivery.
func (a *app) noteReadAsked(ctx context.Context, st *store.Store, name string, p sprint.Packet, say func(string)) {
	if a.bus == nil {
		return
	}
	m := bus.Message{
		From:    st.Actor,
		To:      []string{name},
		Subject: "read asked: " + p.Card,
		Body: fmt.Sprintf("A read %s of %s (attempt %d) is in your inbox at inbox/reads/%s/. Read READ.md, judge the brief, and finish with nova-sprint read --as %s%s.",
			p.Card, p.Primary, p.Attempt, p.Card, sprint.ReaderPrefix+name, ""),
	}
	err := a.bus(ctx, m, say)
	if err == nil || say == nil {
		return
	}
	say(fmt.Sprintf("FRIEND-READ NOTE friend=%s card=%s: the bus message was not sent (%s); the inbox files stand", name, p.Card, oneline.Escape(err.Error())))
}

// refuseReadSymlink refuses a path that is a symlink. A path that is not
// there is fine: the delivery creates it.
func refuseReadSymlink(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; friend sync does not follow it", path)
	}
	return nil
}

// writeIfAbsent writes path when it is absent. A file already there stays.
// A symlink is refused.
func writeIfAbsent(path string, data []byte) error {
	fi, err := os.Lstat(path)
	if err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; friend sync does not follow it", path)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	return atomicfile.WriteFile(path, data, 0o644, atomicfile.NoReplace())
}

// removeRecordedReads removes each directory under reads whose name is a
// card id, that holds READ.md, and that is not still asked or reading.
// A symlink is refused and nothing under it is removed.
func removeRecordedReads(reads string, live map[string]bool) (int, error) {
	entries, err := os.ReadDir(reads)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, e := range entries {
		id := e.Name()
		path := filepath.Join(reads, id)
		fi, err := os.Lstat(path)
		if err != nil {
			return n, err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return n, fmt.Errorf("%s is a symlink; friend sync does not remove it", path)
		}
		if !fi.IsDir() || !sprint.ValidCardID(id) || live[id] {
			continue
		}
		readPath := filepath.Join(path, "READ.md")
		rfi, err := os.Lstat(readPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return n, err
		}
		if rfi.Mode()&os.ModeSymlink != 0 {
			return n, fmt.Errorf("%s is a symlink; friend sync does not follow it", readPath)
		}
		// the directory is computed from the read id; safepath refuses one that
		// is not strictly below inbox/reads
		if err := safepath.RemoveUnder(reads, path); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
