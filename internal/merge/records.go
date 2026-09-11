package merge

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Work list 6a, and the whole of rule 22: A READ AND A GATE ARE EACH ONE IMMUTABLE FILE
// IN THE LANE'S BRANCH, PUSHED BY THE TOOL.
//
// The failure this closes is a person: a reader on another machine had nowhere to put a
// verdict but a bus note the coordinator transcribed, and on 2026-09-11 the coordinator
// read 26 receipt lines and 21 review notes to record 21 reads by hand and mis-attributed
// all 21 once. "The tool pushes the state" named no place, and a cloned state.json plus a
// local lock defines neither an authority nor an immutable verdict.
//
// So: one file per submission, never edited and never replaced. Two writers never touch
// one path, so the compare-and-swap loop -- fetch, reset to the fetched tip, RESTORE the
// outbox bytes, add, commit, push -- can never lose the loser's record. The restore is
// the half a fixture taught us: after a rejected push the new record was tracked in the
// local commit, the reset to the competing tip removed it, and the next add failed with
// `pathspec ... did not match any files`.

// Rounds is how many times the CAS loop repeats before it gives the record back to its
// writer. Five rounds within --timeout: past that, the answer a person needs is that the
// record is in the outbox and the same verb pushes it.
const Rounds = 5

// ErrNotDelivered is a record that is written and not yet at the remote tip. It is exit 1
// with pushed=false, and the file stays in the outbox.
var ErrNotDelivered = errors.New("the record is in the outbox and is not yet at the remote tip")

// Records is the lane's checkout: the one place a Git operation on it may happen, and the
// checkout lock is taken around every one of them.
type Records struct {
	Lane   string
	Branch string
	Remote string
	Git    *Git
	Wait   time.Duration
}

// NewRecords returns the record layer for a lane.
func NewRecords(lane, branch, remote string, g *Git, wait time.Duration) *Records {
	return &Records{Lane: lane, Branch: branch, Remote: remote, Git: g.In(lane), Wait: wait}
}

// LockCheckout takes the checkout lock: one Git operation on this checkout at a time,
// held for the whole of a loop, a pull or a fetch. It is a different lock from the state
// lock of rule 1, which protects state.json and nothing else.
func (r *Records) LockCheckout() (func(), error) {
	return Lock(filepath.Join(r.Lane, CheckoutLock), r.Wait)
}

// Submission is the id drawn once per verb and never reused: the instant, and six random
// characters. It is in the record's file name AND in the gate record's run= field, so a
// record quoted from a log names its file.
type Submission struct {
	At   string
	Rand string
}

// NewSubmission draws one.
func NewSubmission(now time.Time) (Submission, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return Submission{}, err
	}
	return Submission{At: now.UTC().Format(Stamp), Rand: hex.EncodeToString(b[:])}, nil
}

// ID is `<at>-<rand6>` with the instant in the compact form a file name can hold.
func (s Submission) ID() string { return compactStamp(s.At) + "-" + s.Rand }

func compactStamp(at string) string {
	r := strings.NewReplacer("-", "", ":", "")
	return r.Replace(at)
}

// ReadFile is where one read record lives: reads/<entry>/<who>-<head12>-<at>-<rand6>.json
func ReadFile(entry, who, head string, s Submission) string {
	return path.Join(ReadsDir, entry, safeName(who)+"-"+Short(head)+"-"+s.ID()+".json")
}

// GateFile is where one gate record lives: gates/<entry>/<head12>-<base12>-<at>-<rand6>.json
func GateFile(entry, head, base string, s Submission) string {
	return path.Join(GatesDir, entry, Short(head)+"-"+Short(base)+"-"+s.ID()+".json")
}

// SummaryFile is the gate summary, copied beside its record under the same name.
func SummaryFile(gateFile string) string {
	return strings.TrimSuffix(gateFile, ".json") + ".summary"
}

// safeName keeps a reader's name to what a path may hold, so that a --who of "../.."
// cannot name a path outside the lane. It is not a prettifier: a name that reduces to
// nothing is a refusal at the verb, before this is reached.
func safeName(who string) string {
	var b strings.Builder
	for _, r := range who {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// EntryDirName is the directory one entry's records live in: the pull request's number or
// the branch's name with its slashes flattened, so that reads/<entry>/ is one level and
// the fold can read the entry out of the path.
func EntryDirName(id string) string { return strings.ReplaceAll(id, "/", "%2F") }

// EntryFromDir is the inverse, for the fold.
func EntryFromDir(dir string) string { return strings.ReplaceAll(dir, "%2F", "/") }

// Item is one thing on its way to the branch: the bytes, and where they go.
type Item struct {
	Path string // relative to the lane, the path in the branch
	Body []byte
}

// Deliver writes the records to the durable outbox, then commits and pushes them to the
// lane branch in the compare-and-swap loop, and marks them delivered ONLY after a fetch
// shows the record's path at the remote tip with the outbox's bytes.
//
// A kill at any boundary -- before add, after commit, after a rejected push, after a
// landed push and before the confirming fetch -- is repaired by re-running the same verb,
// which finds the outbox and restarts the loop.
// THE OUTBOX IS WRITTEN BEFORE THE LOCK IS TAKEN. The outbox is outside the branch and
// untracked, so writing it needs no checkout; and a Deliver that took the lock first and
// lost the wait printed `READ FAIL ... file=<path> pushed=false: ...; re-run the same verb
// to push it` naming a file that was never written -- a remedy that cannot work, about a
// record that does not exist. Written first, the refusal is true: the record is in the
// outbox and the same verb pushes it.
func (r *Records) Deliver(sub Submission, items []Item) error {
	if err := r.writeOutbox(sub, items); err != nil {
		return err
	}
	release, err := r.LockCheckout()
	if err != nil {
		return err
	}
	defer release()
	return r.flush()
}

// writeOutbox puts the record's exact bytes somewhere the branch cannot take them away:
// outside the branch, untracked, through .tmp and rename.
func (r *Records) writeOutbox(sub Submission, items []Item) error {
	dir := filepath.Join(r.Lane, OutboxDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, it := range items {
		name := sub.ID() + filepath.Ext(it.Path)
		tmp := filepath.Join(dir, name+".tmp")
		if err := os.WriteFile(tmp, it.Body, 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

// outbox reads what is waiting, oldest first, grouped by the submission id in the name.
// The destination path is in the record itself -- every record carries `file` -- so the
// outbox needs no second file to say where its bytes belong.
func (r *Records) outbox() ([]Item, []string, error) {
	entries, err := os.ReadDir(filepath.Join(r.Lane, OutboxDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && !strings.HasSuffix(e.Name(), ".tmp") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var items []Item
	// THE OUTBOX FILE NAMES THIS READ CONSUMED, and only these: what is delivered is
	// removed by name afterwards, so an item another verb wrote into the outbox while
	// this one was pushing is still there to be delivered by its own flush.
	var taken []string
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(r.Lane, OutboxDir, name))
		if err != nil {
			return nil, nil, err
		}
		if strings.HasSuffix(name, ".summary") {
			// A summary is carried beside its record below, under the record's own
			// name; on its own it names no path in the branch.
			continue
		}
		dest, err := destinationOf(body)
		if err != nil {
			return nil, nil, fmt.Errorf("the outbox item %s does not name the path it belongs at: %w", name, err)
		}
		items = append(items, Item{Path: dest, Body: body})
		taken = append(taken, name)
		// Its summary, if this submission wrote one, goes beside it under the same name.
		id := strings.TrimSuffix(name, ".json")
		summary := id + ".summary"
		if s, err := os.ReadFile(filepath.Join(r.Lane, OutboxDir, summary)); err == nil {
			items = append(items, Item{Path: SummaryFile(dest), Body: s})
			taken = append(taken, summary)
		}
	}
	return items, taken, nil
}

// destinationOf reads the `file` field every record carries.
func destinationOf(body []byte) (string, error) {
	var probe struct {
		File string `json:"file"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return "", err
	}
	if probe.File == "" || strings.Contains(probe.File, "..") || path.IsAbs(probe.File) {
		return "", fmt.Errorf("a record's file is a path under the lane, got %q", probe.File)
	}
	return probe.File, nil
}

// flush is the compare-and-swap loop. The caller holds the checkout lock.
func (r *Records) flush() error {
	items, taken, err := r.outbox()
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	var lastErr error
	for round := 0; round < Rounds; round++ {
		if err := r.fetchAndReset(); err != nil {
			lastErr = err
			r.backoff(round)
			continue
		}
		// RESTORE: the reset removed whatever this loop's previous round had staged, and
		// the outbox's bytes are what goes back. Without this the next add fails with
		// `pathspec ... did not match any files`, which is the failure a fixture found.
		var paths []string
		for _, it := range items {
			full := filepath.Join(r.Lane, filepath.FromSlash(it.Path))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(full, it.Body, 0o644); err != nil {
				return err
			}
			paths = append(paths, it.Path)
		}
		if _, err := r.Git.Run(append([]string{"add", "--"}, paths...)...); err != nil {
			return err
		}
		// WHETHER THERE IS ANYTHING TO COMMIT IS A QUESTION GIT ANSWERS, not a phrase to
		// match in its error text (lesson 24: `strings.Contains(err.Error(), "nothing to
		// commit")` is a branch on wording that a git release, a locale or a translation
		// can change under us). The porcelain is empty or it is not.
		clean, err := r.nothingStaged()
		if err != nil {
			lastErr = err
			r.backoff(round)
			continue
		}
		if clean {
			// Everything this loop carries is already at the tip: a push that landed
			// before a kill, delivered by this re-run's confirming fetch below.
			if r.confirm(items) {
				return r.deliveredOK(taken)
			}
		} else if _, err := r.Git.Run(Identity("commit", "-m", "nova-merge: "+strings.Join(paths, " "))...); err != nil {
			lastErr = err
			r.backoff(round)
			continue
		}
		if _, err := r.Git.Run("push", r.Remote, "HEAD:refs/heads/"+r.Branch); err != nil {
			lastErr = err
			r.backoff(round)
			continue
		}
		if !r.confirm(items) {
			lastErr = fmt.Errorf("the push landed and the confirming fetch did not find the record at the remote tip")
			r.backoff(round)
			continue
		}
		return r.deliveredOK(taken)
	}
	if lastErr == nil {
		lastErr = ErrNotDelivered
	}
	return fmt.Errorf("%w: %v", ErrNotDelivered, lastErr)
}

// nothingStaged asks git whether this checkout has anything to commit. The answer is the
// porcelain, which is empty or is not; it is never a phrase in an error's text.
func (r *Records) nothingStaged() (bool, error) {
	out, err := r.Git.Out("status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// backoff is lesson 66, on the loop two lanes actually contend on. The lock had jitter and
// this did not: five rounds with no wait and no spread means two writers that collide
// collide again immediately, five times, and then both give up -- which is the lockstep
// the jitter on the lock exists to break. The wait grows with the round and is spread, and
// the last round never waits, because the budget belongs to the work and not to the sleep.
func (r *Records) backoff(round int) {
	if round >= Rounds-1 {
		return
	}
	Sleep(jitter(time.Duration(round+1) * casBackoff))
}

// casBackoff is the step of the CAS loop's wait. A record is a small file and a push is
// milliseconds, so the first retry waits about that and the fifth about five times it,
// which is inside any --timeout this tool accepts.
const casBackoff = 50 * time.Millisecond

// Sleep is time.Sleep, named here so a test can hold the loop still.
var Sleep = time.Sleep

// fetchAndReset moves this checkout to the branch's remote tip. reset --hard leaves
// untracked files alone, so the outbox, the state and the logs survive it.
func (r *Records) fetchAndReset() error {
	if _, err := r.Git.Run("fetch", r.Remote, r.Branch); err != nil {
		return err
	}
	if _, err := r.Git.Run("checkout", "-B", r.Branch, "FETCH_HEAD"); err != nil {
		return err
	}
	_, err := r.Git.Run("reset", "--hard", "FETCH_HEAD")
	return err
}

// confirm is the fetch that makes an outbox item deliverable: the record's path at the
// REMOTE tip, holding the outbox's bytes. Until this says yes, the item stays.
func (r *Records) confirm(items []Item) bool {
	if _, err := r.Git.Run("fetch", r.Remote, r.Branch); err != nil {
		return false
	}
	for _, it := range items {
		out, err := r.Git.Run("show", "FETCH_HEAD:"+it.Path)
		if err != nil || out != string(it.Body) {
			return false
		}
	}
	return true
}

// deliveredOK removes THE ITEMS THIS FLUSH DELIVERED, by name, and only then: delivered
// means seen at the remote tip with these bytes.
//
// It used to sweep the whole outbox directory. The outbox is written BEFORE the checkout
// lock is taken (see Deliver), which is deliberate -- the bytes are durable before
// anything can reset a tree under them -- so a second verb on this lane can have its
// record sitting in the outbox, still waiting for the lock, while this flush is pushing.
// The sweep deleted it: a read or a gate a person had already been told was recorded
// would never reach the branch, and nothing would say so. What this flush carried is
// what this flush removes.
func (r *Records) deliveredOK(taken []string) error {
	for _, name := range taken {
		_ = os.Remove(filepath.Join(r.Lane, OutboxDir, name))
	}
	return nil
}

// Pull brings the lane branch's records in, --ff-only, under the checkout lock. It is
// what run and status do before they fold, and the number of record files it brought in
// is RUN PASS's pulled=.
func (r *Records) Pull() (int, error) {
	release, err := r.LockCheckout()
	if err != nil {
		return 0, err
	}
	defer release()
	if err := r.flush(); err != nil && !errors.Is(err, ErrNotDelivered) {
		return 0, err
	}
	before := r.count()
	if _, err := r.Git.Run("fetch", r.Remote, r.Branch); err != nil {
		return 0, err
	}
	if _, err := r.Git.Run("merge", "--ff-only", "FETCH_HEAD"); err != nil {
		return 0, err
	}
	after := r.count()
	if after < before {
		return 0, nil
	}
	return after - before, nil
}

// count is how many record files are in the checkout, for pulled=.
func (r *Records) count() int {
	n := 0
	for _, dir := range []string{ReadsDir, GatesDir} {
		_ = filepath.WalkDir(filepath.Join(r.Lane, dir), func(p string, d os.DirEntry, err error) error {
			if err == nil && d != nil && !d.IsDir() && strings.HasSuffix(p, ".json") {
				n++
			}
			return nil
		})
	}
	return n
}

// FetchTip fetches the lane branch and returns the tip's sha WITHOUT touching the
// checkout: it is what dry-run does, so that its plan is over a named, refreshed snapshot
// and it still cannot write.
func (r *Records) FetchTip() (string, error) {
	release, err := r.LockCheckout()
	if err != nil {
		return "", err
	}
	defer release()
	if _, err := r.Git.Run("fetch", r.Remote, r.Branch); err != nil {
		return "", err
	}
	return r.Git.Out("rev-parse", "FETCH_HEAD")
}

// FoldProblem is a record file the fold REFUSED. It is never skipped and never repaired:
// the unreadable file may be the hold or the newer red, so the entry whose directory
// holds it is blocked for the pass, and a file whose path names no entry stops the pass
// before any entry is read.
type FoldProblem struct {
	File   string
	Entry  string // "" means the scope is indeterminate
	Reason string
}

// Folded is what a fold produced.
type Folded struct {
	Reads    map[string][]Read
	Gates    []Gate
	Problems []FoldProblem
	Files    int
}

// Fold rebuilds reads and gates from the record files in the lane's checkout. The lists
// are the fold and the files are the truth, so a record that is in the branch is in the
// next fold, on every machine, with the sha its reader supplied.
//
// IT RUNS UNDER THE CHECKOUT LOCK, because it reads the checkout's work tree and rule 22
// puts the pull AND THE FOLD there with the CAS loop. A concurrent flush does `reset
// --hard` in that window: without the lock a fold can read a tree the reset has emptied
// and the restore has not yet refilled, which is the outbox's own window read from the
// other side.
func (r *Records) Fold() (*Folded, error) {
	release, err := r.LockCheckout()
	if err != nil {
		return nil, err
	}
	defer release()
	return foldFiles(func(dir string) ([]foldFile, error) { return readDirFiles(r.Lane, dir) })
}

// FoldReadOnly folds the record files in the checkout WITHOUT TAKING THE CHECKOUT LOCK,
// because rule 23 says what packet is: "it is derived from the fold and the host, writes
// nothing and takes no lock."
//
// A verb that takes the lock can be REFUSED by it, and packet was: a reader asking for
// their own packet while the coordinator's pass held the checkout got exit 2 and a lock
// refusal, which is the coordinator's answer to a writer and not an answer a reader of a
// report should ever see. Nothing here writes, so there is nothing to serialise; what a
// concurrent flush can cost this fold is a record that lands a moment later, and the
// packet is a snapshot either way. Every WRITING path still goes through Fold.
//
// WHAT A CONCURRENT FLUSH CAN ALSO COST IT IS HALF A FILE. The flush's restore is a
// truncate-then-write over the record's path, so a fold with no lock can catch a record
// mid-write and parse it as a PROBLEM -- and a problem blocks its entry, which here means
// a reader told their entry is broken when nothing is, over a file that was whole a
// millisecond later. Rule 23 keeps the lock off this path, so the answer is not a lock: a
// file that parses as a problem is READ AGAIN, once, and only a file that refuses BOTH
// times is a refusal this fold reports. A partial read is never a silent wrong packet
// either way -- the first read's refusals that the second read resolves are dropped, and
// the rest are surfaced by Packet (rule 23) exactly as Run surfaces them.
func (r *Records) FoldReadOnly() (*Folded, error) {
	list := func(dir string) ([]foldFile, error) { return readDirFiles(r.Lane, dir) }
	first, err := foldFiles(list)
	if err != nil {
		return nil, err
	}
	if len(first.Problems) == 0 {
		return first, nil
	}
	Sleep(reReadPause)
	second, err := foldFiles(list)
	if err != nil {
		return nil, err
	}
	return mergeFolds(first, second), nil
}

// reReadPause is the wait between the two reads: long enough for one truncate-and-write of
// a small file to finish, short enough that a reader asking for a packet does not notice
// it, and paid only when something refused.
const reReadPause = 50 * time.Millisecond

// mergeFolds joins the two reads of one tree. A RECORD FILE IS IMMUTABLE (rule 22: "one
// immutable file per submission, never edited and never replaced"), so the two reads
// cannot disagree about a file's content -- they can only disagree about whether the file
// was whole yet. So: a path either read parsed is that record, and a problem survives only
// while NEITHER read could parse that path.
func mergeFolds(a, b *Folded) *Folded {
	out := &Folded{Reads: map[string][]Read{}, Files: max(a.Files, b.Files)}
	parsed := map[string]bool{}
	for _, f := range []*Folded{a, b} {
		for _, list := range f.Reads {
			for _, r := range list {
				parsed[r.File] = true
			}
		}
		for _, g := range f.Gates {
			parsed[g.File] = true
		}
	}
	seen := map[string]bool{}
	for _, f := range []*Folded{b, a} {
		for entry, list := range f.Reads {
			for _, r := range list {
				if seen[r.File] {
					continue
				}
				seen[r.File] = true
				out.Reads[entry] = append(out.Reads[entry], r)
			}
		}
		for _, g := range f.Gates {
			if seen[g.File] {
				continue
			}
			seen[g.File] = true
			out.Gates = append(out.Gates, g)
		}
		for _, pr := range f.Problems {
			if parsed[pr.File] || seen[pr.File] {
				continue
			}
			seen[pr.File] = true
			out.Problems = append(out.Problems, pr)
		}
	}
	for _, list := range out.Reads {
		sort.SliceStable(list, func(i, j int) bool { return list[i].At < list[j].At })
	}
	sort.SliceStable(out.Gates, func(i, j int) bool { return out.Gates[i].At < out.Gates[j].At })
	sort.Slice(out.Problems, func(i, j int) bool { return out.Problems[i].File < out.Problems[j].File })
	return out
}

// FoldTip folds the record files of the FETCHED tip in memory, writing neither the state
// nor the checkout. It is dry-run's fold.
func (r *Records) FoldTip(tip string) (*Folded, error) {
	release, err := r.LockCheckout()
	if err != nil {
		return nil, err
	}
	defer release()
	out, err := r.Git.Out("ls-tree", "-r", "--name-only", tip)
	if err != nil {
		return nil, err
	}
	byDir := map[string][]string{}
	for _, p := range strings.Split(out, "\n") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		switch {
		case strings.HasPrefix(p, ReadsDir+"/"):
			byDir[ReadsDir] = append(byDir[ReadsDir], p)
		case strings.HasPrefix(p, GatesDir+"/"):
			byDir[GatesDir] = append(byDir[GatesDir], p)
		}
	}
	return foldFiles(func(dir string) ([]foldFile, error) {
		var out []foldFile
		for _, p := range byDir[dir] {
			if !strings.HasSuffix(p, ".json") {
				continue
			}
			body, err := r.Git.Run("show", tip+":"+p)
			if err != nil {
				return nil, err
			}
			out = append(out, foldFile{Path: p, Body: []byte(body)})
		}
		return out, nil
	})
}

type foldFile struct {
	Path string
	Body []byte
}

func readDirFiles(lane, dir string) ([]foldFile, error) {
	root := filepath.Join(lane, dir)
	var out []foldFile
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".json") {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(lane, p)
		if err != nil {
			return err
		}
		out = append(out, foldFile{Path: filepath.ToSlash(rel), Body: body})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func foldFiles(list func(string) ([]foldFile, error)) (*Folded, error) {
	f := &Folded{Reads: map[string][]Read{}}
	reads, err := list(ReadsDir)
	if err != nil {
		return nil, err
	}
	for _, file := range reads {
		entry, ok := entryOfPath(file.Path)
		f.Files++
		if !ok {
			f.Problems = append(f.Problems, FoldProblem{File: file.Path, Reason: "this path names no entry, so the record's scope is indeterminate"})
			continue
		}
		var rec Read
		if err := strictDecode(file.Body, &rec); err != nil {
			f.Problems = append(f.Problems, FoldProblem{File: file.Path, Entry: entry, Reason: oneLineOf(err.Error())})
			continue
		}
		if err := ValidRead(rec); err != nil {
			f.Problems = append(f.Problems, FoldProblem{File: file.Path, Entry: entry, Reason: oneLineOf(err.Error())})
			continue
		}
		rec.File = file.Path
		f.Reads[entry] = append(f.Reads[entry], rec)
	}
	gates, err := list(GatesDir)
	if err != nil {
		return nil, err
	}
	for _, file := range gates {
		entry, ok := entryOfPath(file.Path)
		f.Files++
		if !ok {
			f.Problems = append(f.Problems, FoldProblem{File: file.Path, Reason: "this path names no entry, so the record's scope is indeterminate"})
			continue
		}
		var rec Gate
		if err := strictDecode(file.Body, &rec); err != nil {
			f.Problems = append(f.Problems, FoldProblem{File: file.Path, Entry: entry, Reason: oneLineOf(err.Error())})
			continue
		}
		if err := ValidGate(rec); err != nil {
			f.Problems = append(f.Problems, FoldProblem{File: file.Path, Entry: entry, Reason: oneLineOf(err.Error())})
			continue
		}
		rec.File = file.Path
		f.Gates = append(f.Gates, rec)
	}
	for _, list := range f.Reads {
		sort.SliceStable(list, func(i, j int) bool { return list[i].At < list[j].At })
	}
	sort.SliceStable(f.Gates, func(i, j int) bool { return f.Gates[i].At < f.Gates[j].At })
	return f, nil
}

// entryOfPath reads the entry out of a record's path: <dir>/<entry>/<name>. A file
// directly under reads/ names no entry.
func entryOfPath(p string) (string, bool) {
	parts := strings.Split(p, "/")
	if len(parts) != 3 || parts[1] == "" {
		return "", false
	}
	return EntryFromDir(parts[1]), true
}

func strictDecode(body []byte, into any) error {
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return err
	}
	return nil
}

// Apply folds the record files into the state's lists, replacing both WHOLESALE: the
// files are the truth and the lists are the fold, so a record removed from the branch is
// removed from the fold and a record added is added.
func (s *State) Apply(f *Folded) {
	for _, e := range s.Entries() {
		e.Reads = f.Reads[e.ID()]
		if e.Reads == nil {
			e.Reads = []Read{}
		}
	}
	s.Gates = append([]Gate{}, f.Gates...)
}

// GitIgnore is what init writes into the lane branch. THE TRACKED FILES ARE THE RECORDS
// AND NOTHING ELSE: a git status in a lane that is not mid-verb is clean.
const GitIgnore = `# nova-merge: the tracked files are the records and nothing else (rule 22).
/state.json
/state.json.tmp
/log
/repo/
/outbox/
/slots/
/stop
*.lock
*.log
`
