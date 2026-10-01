package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The twin: `--redis mem:<file>` (or NOVA_SPRINT_REDIS=mem:<file>) runs every
// verb against the in-memory store the tests use (store.Mem), loaded from the
// file before the verb and saved to it after, so a whole card flow can be tried
// without a Redis. It is for learning and tests, never a fleet's store: one
// command at a time, one machine, and no machine runs between commands (tick
// is the tick, by hand).

// twinPrefix is what marks an address as a twin file.
const twinPrefix = "mem:"

// twinMachine is what a twin says to a verb that waits for, or is, a machine
// that runs between commands: there is none, and tick is one tick by hand.
const twinMachine = "a mem twin has no machine running between commands: run nova-sprint tick to tick it by hand, then read the sprint"

// isTwin says an address names a twin: mem, or mem:<file>.
func isTwin(addr string) bool { return addr == "mem" || strings.HasPrefix(addr, twinPrefix) }

// twinOpen says the address is a twin this process has opened (an address a
// test's backend answers is not).
func (a *app) twinOpen(addr string) bool { return a.twins[addr] != nil }

// twinSteps is the flow of a card through a twin that nova-sprint help shows,
// one verb a line: the test that runs them holds the help to what a twin does.
var twinSteps = []string{
	"nova-sprint init --readers reader-a,reader-b --members m1",
	"nova-sprint add --stream s1 --count 1",
	"nova-sprint start",
	"nova-sprint tick",
	"nova-sprint tick",
	"nova-sprint take --as m1 --epoch 0",
	"nova-sprint finish --as m1 s1-1.w1@1 --epoch 0 --report done",
	"nova-sprint tick",
	"nova-sprint read --as reader-a --begin --epoch 0",
	"nova-sprint read --as reader-b --begin --epoch 0",
	"nova-sprint read --as reader-a --ok --epoch 0",
	"nova-sprint read --as reader-b --ok --epoch 0",
	"nova-sprint tick",
	"nova-sprint merge --stream s1 --batch 1",
	"nova-sprint tick",
	"nova-sprint where",
}

// twinWords is how a twin is tried, in nova-sprint help and nova-sprint help
// init.
func twinWords() string {
	var b strings.Builder
	b.WriteString(`trying it without a Redis (for learning and tests, not for a fleet): --redis
mem:<file>, or NOVA_SPRINT_REDIS=mem:<file>, runs every verb against an
in-memory twin of the store, loaded from the file before the verb and saved
to it after, the file made by the first verb that writes; run one command at a
time. Every member of the twin's fleet is alive at every verb, and no machine
runs between commands, so the tick is yours: nova-sprint tick; run, inbox
--wait and where --watch are refused. A card's whole flow:

  export NOVA_SPRINT_REDIS=mem:sprint.twin NOVA_SPRINT_ACTOR=boss
`)
	for _, l := range twinSteps {
		b.WriteString("  " + l + "\n")
	}
	return b.String()
}

// twin is one open twin file: the store and the bytes last loaded or saved,
// so a verb that changed nothing writes nothing.
type twin struct {
	path string
	mem  *store.Mem
	last []byte
	mu   sync.Mutex
	// idErr is the first failure to record the id counter: newID has no error
	// to return (store.Store.NewID is a func() string), and a counter that was
	// not kept would hand the same id out again, so saveTwins reports it and the
	// verb fails rather than repeating an id.
	idErr error
}

// keyIDs is the twin's record of the operation-id families it has handed out.
const keyIDs = "twin:ids"

// newID is the next operation-id family of the twin: t1, t2, ... counted in
// the twin itself, so the ids of a card flow read the same on every run and
// are never handed out twice (the store takes an id it has seen again as the
// same operation). A store on Redis makes its families from the clock, the
// process and random bytes (store.NewID); a twin has one writer at a time, and
// a counter it keeps is the simplest thing that is unique.
func (t *twin) newID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	ctx := context.Background()
	n := 0
	if v, ok, _ := t.mem.GetKey(ctx, keyIDs); ok {
		n, _ = strconv.Atoi(v)
	}
	n++
	if err := t.mem.SetKey(ctx, keyIDs, strconv.Itoa(n)); err != nil && t.idErr == nil {
		t.idErr = fmt.Errorf("the twin file %s could not record its id counter: %w; run the verb again", t.path, err)
	}
	return "t" + strconv.Itoa(n)
}

// twinBackend opens the twin file once per process, loading it when it is
// there: a file that is not a twin snapshot is refused, never overwritten.
func (a *app) twinBackend(addr string) (store.Backend, error) {
	if b, ok := a.cached[addr]; ok {
		return b, nil
	}
	path := strings.TrimPrefix(addr, twinPrefix)
	if addr == "mem" || path == "" {
		return nil, errors.New("a twin is a file: --redis mem:<file> (a store in memory alone would be gone when this command ends); the twin is for learning and tests, not for a fleet")
	}
	t := &twin{path: path, mem: store.NewMem()}
	doc, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := t.mem.Restore(doc); err != nil {
			return nil, fmt.Errorf("the twin file %s: %w; it is left as it is", path, err)
		}
		t.last = doc
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, fmt.Errorf("the twin file %s: %w", path, err)
	}
	if a.twins == nil {
		a.twins = map[string]*twin{}
	}
	a.twins[addr] = t
	a.cached[addr] = t.mem
	return t.mem, nil
}

// saveTwins writes every open twin whose state changed, each to its file, in
// one atomic rename (a reader of the file, or a crash, sees the file before or
// after, never half). It is run at the end of every verb.
func (a *app) saveTwins() error {
	var first error
	for _, t := range a.twins {
		t.mu.Lock()
		idErr := t.idErr
		t.idErr = nil
		t.mu.Unlock()
		if idErr != nil && first == nil {
			first = idErr
		}
		doc, err := t.mem.Snapshot()
		if err == nil && string(doc) == string(t.last) {
			continue
		}
		if err == nil {
			err = writeAtomic(t.path, doc)
		}
		if err != nil {
			if first == nil {
				first = fmt.Errorf("the twin file %s was not saved: %w", t.path, err)
			}
			continue
		}
		t.last = doc
	}
	return first
}

// writeAtomic writes the file whole or not at all: a temporary file in the
// same directory, synced, then renamed over the target.
func writeAtomic(path string, doc []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_, err = f.Write(doc)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, 0o600)
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name) // ignored: a best-effort removal of the temporary file; the write's error is the one returned
	}
	return err
}

// beatTwin says every member of the twin's fleet is alive: the machines of a
// twin are this one process's, so each verb begins with a beat of each member
// the fleet table names, at load 0 (fleet down still holds a member down).
// A twin with no fleet table yet (before init) has none to beat.
func (a *app) beatTwin(ctx context.Context, st *store.Store) error {
	shapes, err := st.B.Shapes(ctx, []string{st.Names.Table(sprint.Fleet)})
	if err != nil || len(shapes) != 1 {
		return nil
	}
	zero := 0.0
	for _, r := range shapes[0].Rows {
		if _, err := st.Beat(ctx, r.Key, &zero, a.meter); err != nil {
			return err
		}
	}
	return nil
}
