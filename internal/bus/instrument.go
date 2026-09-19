package bus

import (
	"path/filepath"
	"sync"
	"sync/atomic"
)

// THE SAME TWO COUNTS, PER BUS.
//
// NoteParses and CommitsWalked are one number each for the whole process, and that made
// every test asserting a delta of either one SERIAL: a sibling test parsing a note or
// counting commits on its own bus while one of these counted made the count somebody
// else's work, and every one of those assertions is an exact number. In cmd/nova-bus the
// three tests that assert them were the package's critical path -- the ten-thousand-note
// fixture and the thousand-commit walk, one after another while every other test waited.
//
// The count a test actually wants is the count OVER ITS OWN BUS, and every place the work
// happens already knows which bus it is working on: the full read holds Bus.Root, the
// since-walk's note cache holds its root, and the rev-list count is handed its directory.
// So the same events are counted a second time here, under the bus they happened on, and
// NoteParsesIn and CommitsWalkedIn read that. Two tests on two buses no longer see each
// other, and the process totals are untouched for whoever still reads them.
//
// The key is the resolved path, so that the directory a caller names and the directory the
// tool resolved it to -- /var against /private/var on macOS, a trailing slash, a symlink --
// land on one counter. Resolving costs a few stats and is done once per read, per walk
// and per count, never per note.
type busCounters struct {
	noteParses    atomic.Int64
	commitsWalked atomic.Int64
}

var countersByRoot sync.Map // resolved root -> *busCounters

func rootKey(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return filepath.Clean(abs)
}

// countersFor is the one counter pair for a bus root, made on first use.
func countersFor(root string) *busCounters {
	key := rootKey(root)
	if c, ok := countersByRoot.Load(key); ok {
		return c.(*busCounters)
	}
	c, _ := countersByRoot.LoadOrStore(key, &busCounters{})
	return c.(*busCounters)
}

// NoteParsesIn is how many notes this process has parsed OUT OF THE BUS AT root: the full
// read's lane walk and the since-walk's note cache, which between them are every note an
// inbox run opens. Tests take it before and after a run against their own bus and assert on
// the difference, and two such tests may run at once. NoteParses is the process total.
func NoteParsesIn(root string) int64 { return countersFor(root).noteParses.Load() }

// CommitsWalkedIn is how many commits this process's rev-list counts have enumerated IN
// THE BUS AT root. CommitsWalked is the process total.
func CommitsWalkedIn(root string) int64 { return countersFor(root).commitsWalked.Load() }
