package cairn

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The store's shape is decided from its contents, once, here, and every verb
// asks this file and nothing else. The model of the lifecycle that depends on
// it is tla/CairnStore.tla (checked by TLC; the records are in tla/RUNS.tsv); its
// "one shape per store" invariant is what MixedShapeError enforces and its
// Open action is what openBench implements.
//
//	bench  at least one top-level <id>.md session file and no sessions/,
//	       entries/ or log.jsonl; README.md, in any case, is documentation and
//	       never a session file, so it counts for no shape
//	own    anything with sessions/, entries/ or log.jsonl and no top-level
//	       <id>.md; an empty or absent store directory is own too, so a new
//	       store gets the tool's own shape
//	mixed  both; every verb refuses it
type shape int

const (
	shapeOwn shape = iota
	shapeBench
	shapeMixed
)

// MixedShapeError is the refusal for a store that holds the bench shape and
// the tool's own shape at once. It names the operation, the cause, the paths
// found of each shape and the next action, so the caller need not inspect the
// store to learn what to do.
type MixedShapeError struct {
	Op    string
	Store string
	Bench []string // top-level <id>.md files, sorted
	Own   []string // sessions/, entries/, log.jsonl as found
}

func (e *MixedShapeError) Error() string {
	shown := e.Bench
	more := ""
	if len(shown) > 3 {
		more = fmt.Sprintf(" and %d more", len(shown)-3)
		shown = shown[:3]
	}
	return fmt.Sprintf("cannot %s: store %q holds two shapes at once (bench files: %s%s; own shape: %s); "+
		"keep one shape: move the top-level <id>.md files out of the store, or move sessions/, entries/ and log.jsonl out of it",
		e.Op, e.Store, strings.Join(shown, ", "), more, strings.Join(e.Own, ", "))
}

// storeShape reads the store's contents and names its shape. A directory that
// does not exist has no contents and is the tool's own shape. Any other read
// failure is returned, never read as "empty".
func storeShape(op, store string) (shape, error) {
	files, err := os.ReadDir(store)
	if os.IsNotExist(err) {
		return shapeOwn, nil
	}
	if err != nil {
		return shapeOwn, fmt.Errorf("cannot %s: cannot read store %q: %v", op, store, err)
	}
	var bench, own []string
	for _, f := range files {
		name := f.Name()
		switch {
		case name == "sessions" && f.IsDir(), name == "entries" && f.IsDir():
			own = append(own, name+"/")
		case name == "log.jsonl" && !f.IsDir():
			own = append(own, name)
		case !f.IsDir() && strings.HasSuffix(name, ".md") && sessionFileID(strings.TrimSuffix(name, ".md")):
			bench = append(bench, name)
		}
	}
	switch {
	case len(bench) > 0 && len(own) > 0:
		return shapeMixed, &MixedShapeError{Op: op, Store: store, Bench: bench, Own: own}
	case len(bench) > 0:
		return shapeBench, nil
	}
	return shapeOwn, nil
}

// benchHeader is the short header a bench open writes: a title, one line
// naming the full session id and the open stamp, and the source pointer when
// one was given. The source is escaped to one line so it can never form a
// section heading.
func benchHeader(session, source, stamp string) string {
	short := session
	if r := []rune(session); len(r) > 8 {
		short = string(r[:8])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Cairn %s\n\nSession %s opened %s\n", short, session, stamp)
	if source != "" {
		fmt.Fprintf(&b, "Source: %s\n", oneline.Escape(source))
	}
	return b.String()
}

func openBench(store, session, source, stamp string) error {
	name := benchFile(store, session)
	if fileExists(name) {
		return nil
	}
	// NoReplace: a record that appears between the check and the write is kept.
	err := atomicfile.WriteFile(name, []byte(benchHeader(session, source, stamp)), 0o644, atomicfile.NoReplace())
	if err != nil && errors.Is(err, os.ErrExist) {
		return nil
	}
	return err
}
