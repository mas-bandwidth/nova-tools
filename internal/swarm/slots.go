package swarm

// Bench slot leases: a bench-wide lease store with shares, reserve, expiry and
// live-pid fencing (docs/SPEC-SWARM.md, "Bench slot leases").
//
// The store is <store>/slots with one directory per lease. A lease directory
// is published by renaming a fully-written staging directory into place, so a
// directory in the store always arrives WITH its `lease` file already inside:
// a concurrent take scanning the store can never meet a half-written take and
// reap it (nova-tools#1868). A lease is never inferred from a count but from
// the directories on disk. Each lease directory holds a file `lease`
// with lines `owner=`, `pid=`, `label=`, `until=<RFC3339>`. Shares live in
// <store>/shares.tsv with rows `capacity\t<n>`, `reserve\t<n>` and
// `<owner>\t<n>`.
//
// Expiry is fenced by liveness: before granting, take reaps every lease whose
// until= is past AND whose pid is not alive (Alive, signal 0). A lease past
// until= with a live pid is DRIFT: it stays and counts as held, because the
// process it names is still running and its slot is not free.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// SlotLease is one bench slot lease: the directory name is the id.
type SlotLease struct {
	ID    string
	Owner string
	Pid   int
	Label string
	Until time.Time
}

// State reports live, expired or DRIFT at now: past until= with a live pid is
// DRIFT, past until= with a dead pid is expired, anything else is live.
func (l SlotLease) State(now time.Time) string {
	if l.Until.After(now) {
		return "live"
	}
	if Alive(l.Pid, "") {
		return "DRIFT"
	}
	return "expired"
}

// Line is the `slots list` row for this lease.
func (l SlotLease) Line(now time.Time) string {
	label := l.Label
	if strings.TrimSpace(label) == "" {
		label = "-"
	}
	return fmt.Sprintf("SLOT %s owner=%s pid=%d label=%s until=%s state=%s",
		oneline.Field(l.ID), oneline.Field(l.Owner), l.Pid,
		oneline.Field(label), oneline.Field(l.Until.UTC().Format(time.RFC3339)),
		oneline.Field(l.State(now)))
}

func slotStoreDir(store string) string { return filepath.Join(store, "slots") }

func slotLeaseFile(store, id string) string {
	return filepath.Join(slotStoreDir(store), id, "lease")
}

// loadSlotShares reads <store>/shares.tsv: rows `capacity\t<n>`,
// `reserve\t<n>` and `<owner>\t<n>`. Capacity is required; reserve defaults
// to 0; an owner with no row holds share 0.
func loadSlotShares(store string) (capacity, reserve int, shares map[string]int, err error) {
	shares = map[string]int{}
	raw, err := os.ReadFile(filepath.Join(store, "shares.tsv"))
	if err != nil {
		return 0, 0, nil, fmt.Errorf("shares.tsv: %w", err)
	}
	haveCapacity := false
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 2 {
			return 0, 0, nil, fmt.Errorf("shares.tsv line %d: wants two tab-separated fields", i+1)
		}
		key := strings.TrimSpace(fields[0])
		n, aerr := strconv.Atoi(strings.TrimSpace(fields[1]))
		if aerr != nil || n < 0 {
			return 0, 0, nil, fmt.Errorf("shares.tsv line %d: wants a count of 0 or more", i+1)
		}
		switch key {
		case "capacity":
			capacity, haveCapacity = n, true
		case "reserve":
			reserve = n
		case "":
			return 0, 0, nil, fmt.Errorf("shares.tsv line %d: owner is empty", i+1)
		default:
			shares[key] = n
		}
	}
	if !haveCapacity {
		return 0, 0, nil, fmt.Errorf("shares.tsv: no capacity row; refusing to guess how wide the bench is")
	}
	return capacity, reserve, shares, nil
}

func parseSlotLease(id string, raw []byte) (SlotLease, error) {
	l := SlotLease{ID: id}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch name {
		case "owner":
			l.Owner, seen["owner"] = value, true
		case "pid":
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return l, fmt.Errorf("lease %s: pid is not a number", id)
			}
			l.Pid, seen["pid"] = n, true
		case "label":
			l.Label, seen["label"] = value, true
		case "until":
			stamp, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
			if err != nil {
				return l, fmt.Errorf("lease %s: until is not RFC3339", id)
			}
			l.Until, seen["until"] = stamp, true
		}
	}
	for _, k := range []string{"owner", "pid", "label", "until"} {
		if !seen[k] {
			return l, fmt.Errorf("lease %s: missing %s=", id, k)
		}
	}
	if strings.TrimSpace(l.Owner) == "" {
		return l, fmt.Errorf("lease %s: owner is empty", id)
	}
	return l, nil
}

func readSlotLease(store, id string) (SlotLease, error) {
	raw, err := os.ReadFile(slotLeaseFile(store, id))
	if err != nil {
		return SlotLease{}, err
	}
	return parseSlotLease(id, raw)
}

// ListSlotLeases reads every lease in the store, in id order. A directory
// with no parseable lease file holds no lease and is skipped: it is a
// half-written take (Mkdir landed, the file never did), and take reaps it.
func ListSlotLeases(store string, now time.Time) ([]SlotLease, error) {
	entries, err := os.ReadDir(slotStoreDir(store))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []SlotLease
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		l, err := readSlotLease(store, e.Name())
		if err != nil {
			continue
		}
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SlotHoldings reports how many leases owner holds and the share it holds them
// within. It is the read `status` prints; it never reaps or grants.
func SlotHoldings(store, owner string, now time.Time) (held, share int, err error) {
	_, _, shares, err := loadSlotShares(store)
	if err != nil {
		return 0, 0, err
	}
	leases, err := ListSlotLeases(store, now)
	if err != nil {
		return 0, 0, err
	}
	for _, l := range leases {
		if l.Owner == owner {
			held++
		}
	}
	return held, shares[owner], nil
}

// publishSlotLease stages a complete lease directory beside the slot store
// and renames it into place, so a directory in slots/ always arrives WITH
// its lease file already inside. The staging directory lives beside slots/,
// never inside it, so no take scanning the store ever sees it, and the rename
// is one atomic step on the store's filesystem.
//
// An id that is already held is reported as an existence error for the
// caller's retry loop: the look-then-rename cannot claim the atomicity a bare
// Mkdir had, but the id carries the owner's nanosecond stamp plus 32 random
// bits, so a clash is a retry, never a silent replace.
func publishSlotLease(store, id, body string) error {
	if err := os.MkdirAll(slotStoreDir(store), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(store, ".slot-tmp-*")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "lease"), []byte(body), 0o644); err != nil {
		_ = safepath.RemoveUnder(store, tmp)
		return err
	}
	dest := filepath.Join(slotStoreDir(store), id)
	if _, err := os.Lstat(dest); err == nil {
		_ = safepath.RemoveUnder(store, tmp)
		return &os.PathError{Op: "publish", Path: dest, Err: os.ErrExist}
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = safepath.RemoveUnder(store, tmp)
		return err
	}
	return nil
}

// MakeSlotLease writes one lease directory by publish (atomic) for tests and
// for fixtures: the pid and until are the caller's, not the taker's.
func MakeSlotLease(store, id, owner string, pid int, label string, until time.Time) error {
	if strings.TrimSpace(owner) == "" {
		return fmt.Errorf("owner is required")
	}
	if strings.ContainsAny(owner, "\r\n") || strings.ContainsAny(label, "\r\n") {
		return fmt.Errorf("owner and label are one line")
	}
	body := fmt.Sprintf("owner=%s\npid=%d\nlabel=%s\nuntil=%s\n",
		owner, pid, label, until.UTC().Format(time.RFC3339))
	return publishSlotLease(store, id, body)
}

func slotLeaseID(owner string, now time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_':
			return r
		default:
			return '_'
		}
	}, owner)
	if len(safe) > 32 {
		safe = safe[:32]
	}
	if safe == "" {
		safe = "slot"
	}
	return fmt.Sprintf("%s-%d-%s", safe, now.UnixNano(), hex.EncodeToString(b[:]))
}

// slotHolders renders owner:count pairs in name order for the refusal line.
func slotHolders(counts map[string]int) string {
	var names []string
	for name, n := range counts {
		if n > 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s:%d", name, counts[name]))
	}
	return strings.Join(parts, ",")
}

// SlotUtilisation reports one bench store for `nova-pulse status --slots-store`:
// capacity and reserve from shares.tsv, held and free after reaping expired
// leases with a dead pid (expired leases with a live pid are DRIFT and stay
// held), per-owner held counts and per-owner shares. Free is
// capacity-reserve-held.
func SlotUtilisation(store string, now time.Time) (capacity, reserve, held, free int, heldBy map[string]int, shares map[string]int, err error) {
	capacity, reserve, shares, err = loadSlotShares(store)
	if err != nil {
		return 0, 0, 0, 0, nil, nil, err
	}
	leases, lerr := ListSlotLeases(store, now)
	if lerr != nil {
		return 0, 0, 0, 0, nil, nil, lerr
	}
	heldBy = map[string]int{}
	for _, l := range leases {
		if !l.Until.After(now) && !Alive(l.Pid, "") {
			continue
		}
		heldBy[l.Owner]++
		held++
	}
	free = capacity - reserve - held
	return capacity, reserve, held, free, heldBy, shares, nil
}

// TakeSlotLeases grants k leases to owner when both caps hold after reaping:
// the owner's held+k stays within its share, and the total held+k stays
// within capacity-reserve. Expired leases with a dead pid are reaped first;
// expired leases with a live pid are DRIFT and stay held. New leases carry
// the caller's pid and until=now+dur.
//
// IT RETURNS THE IDS IT GRANTED, not a count, and that is deliberate: the
// count was what let a holder release by owner and label instead of by
// identity, and give away a seat that was never its own (nova-tools#1546,
// Stella's hold on PR #1562). len(ids) is the count for anyone who only
// wanted that; taking a lease without learning which one is now impossible.
// Hand the ids back to ReleaseSlotLeasesByID.
func TakeSlotLeases(store, owner string, k int, dur time.Duration, label string, now time.Time, pid int) (ids []string, held, share, free int, holders string, ok bool, err error) {
	if strings.TrimSpace(owner) == "" {
		return nil, 0, 0, 0, "", false, fmt.Errorf("owner is required")
	}
	if k < 1 {
		return nil, 0, 0, 0, "", false, fmt.Errorf("n is at least 1, got %d", k)
	}
	if dur <= 0 {
		return nil, 0, 0, 0, "", false, fmt.Errorf("for is a positive duration")
	}
	if pid <= 0 {
		return nil, 0, 0, 0, "", false, fmt.Errorf("pid is required")
	}
	if strings.ContainsAny(owner, "\r\n") || strings.ContainsAny(label, "\r\n") {
		return nil, 0, 0, 0, "", false, fmt.Errorf("owner and label are one line")
	}
	// The store is read once here so that a store that was never `slots init`ed says so
	// in its own sentence before this run makes a lock file inside it.
	if _, _, _, err := loadSlotShares(store); err != nil {
		return nil, 0, 0, 0, "", false, err
	}
	// THE WHOLE GRANT IS ONE TRANSACTION (issue #1900). Reap, count, test against the
	// share and the capacity, and make the lease directories -- under the store lock, so
	// that two takers cannot both read total=0 and both win the last seat. Everything
	// read before this point is read again under it.
	unlock, err := takeSlotStoreLock(store)
	if err != nil {
		return nil, 0, 0, 0, "", false, err
	}
	defer unlock()
	capacity, reserve, shares, err := loadSlotShares(store)
	if err != nil {
		return nil, 0, 0, 0, "", false, err
	}
	share = shares[owner]
	entries, err := os.ReadDir(slotStoreDir(store))
	if err != nil && !os.IsNotExist(err) {
		return nil, 0, 0, 0, "", false, err
	}
	counts := map[string]int{}
	total := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		l, rerr := readSlotLease(store, e.Name())
		if rerr != nil {
			// Garbage, never a take in flight: new takes publish complete
			// directories (publishSlotLease), so a directory with no lease
			// file in it holds no lease and no hold. Reap it so garbage
			// never accumulates.
			_ = safepath.RemoveUnder(slotStoreDir(store), filepath.Join(slotStoreDir(store), e.Name()))
			continue
		}
		if !l.Until.After(now) && !Alive(l.Pid, "") {
			_ = safepath.RemoveUnder(slotStoreDir(store), filepath.Join(slotStoreDir(store), e.Name()))
			continue
		}
		counts[l.Owner]++
		total++
	}
	held = counts[owner]
	free = capacity - reserve - total
	holders = slotHolders(counts)
	if held+k > share || total+k > capacity-reserve {
		return nil, held, share, free, holders, false, nil
	}
	if err := os.MkdirAll(slotStoreDir(store), 0o755); err != nil {
		return nil, 0, 0, 0, "", false, err
	}
	until := now.Add(dur).UTC().Format(time.RFC3339)
	for i := 0; i < k; i++ {
		for tries := 0; ; tries++ {
			id := slotLeaseID(owner, now)
			body := fmt.Sprintf("owner=%s\npid=%d\nlabel=%s\nuntil=%s\n", owner, pid, label, until)
			if err := publishSlotLease(store, id, body); err != nil {
				if os.IsExist(err) && tries < 20 {
					continue
				}
				return nil, 0, 0, 0, "", false, err
			}
			ids = append(ids, id)
			break
		}
	}
	held += k
	total += k
	free = capacity - reserve - total
	counts[owner] = held
	holders = slotHolders(counts)
	return ids, held, share, free, holders, true, nil
}

// ReleaseSlotLeases removes owner's leases: all of them with all=true, or only the
// ones carrying label otherwise. It reports the removed count and the owner's leases
// still held. It never frees a seat whose holder is still running; see
// ReleaseSlotLeasesForcing.
func ReleaseSlotLeases(store, owner, label string, all bool) (released, held int, err error) {
	released, held, _, err = ReleaseSlotLeasesForcing(store, owner, label, all, false)
	return released, held, err
}

// ReleaseSlotLeasesForcing is the body, with the live-seat fence spelled out.
//
// A LEASE IS NOT A TICKET SOMEBODY ELSE MAY TEAR UP (issue #1902). Deleting a lease
// does not stop the process holding it: the holder keeps running, keeps spending, and
// the seat it is sitting in is handed to the next taker. Johnny freed a live `native`'s
// only seat from outside and watched a second `native` take it -- two cards on a
// capacity-1 bench, both printing NATIVE OK -- and a card given --no-wall did the same
// to a bystander from inside its own shell. `--owner` is an unauthenticated string and
// every owner on a shared bench is the same unix user, so who CALLED release is not a
// fence either. The only fence that means anything is the holder: a lease whose pid is
// ALIVE and is not this process is KEPT and counted in live.
//
// Releasing your OWN lease is always allowed: `run`'s dispatcher and `native`'s cleanup
// give back the seat they are sitting in while their own pid is alive, and that is the
// ordinary end of a run rather than a steal. force is the person's loud override, the
// way --no-wall is the loud way to ask for no containment.
func ReleaseSlotLeasesForcing(store, owner, label string, all, force bool) (released, held, live int, err error) {
	if strings.TrimSpace(owner) == "" {
		return 0, 0, 0, fmt.Errorf("owner is required")
	}
	// Under the store lock (issue #1900), so that a release cannot interleave with a
	// take's count-then-mkdir and leave the count the grant was made against wrong.
	if unlock, lerr := takeSlotStoreLock(store); lerr == nil {
		defer unlock()
	}
	entries, err := os.ReadDir(slotStoreDir(store))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, 0, nil
		}
		return 0, 0, 0, err
	}
	self := os.Getpid()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		l, rerr := readSlotLease(store, e.Name())
		if rerr != nil {
			continue
		}
		if l.Owner != owner {
			continue
		}
		if !all && l.Label != label {
			held++
			continue
		}
		if !force && l.Pid != self && Alive(l.Pid, "") {
			live++
			held++
			continue
		}
		if rerr := safepath.RemoveUnder(slotStoreDir(store), filepath.Join(slotStoreDir(store), e.Name())); rerr != nil {
			return released, held, live, rerr
		}
		released++
	}
	return released, held, live, nil
}

// ReleaseSlotLeasesByID removes EXACTLY the leases named, and only while they are still
// the caller's. It exists because releasing by owner and label is not releasing by
// identity: an owner is a bench and a label is a card's name, and two runs that share both
// -- two slots, two benches, a retry -- would each give away the other's seat
// (nova-tools#1546, Stella's hold on PR #1562). A holder that took leases learns their ids
// from TakeSlotLeases and hands exactly those back here.
//
// The pid is a fence, not bookkeeping. An id whose lease has since been reaped and remade
// by somebody else must not be removed by a stale list, so each lease is RE-READ and left
// alone unless its pid is the one given. An id that is simply gone is not an error: a
// release is allowed to be late, and the caller's job is to stop holding, not to prove
// nobody tidied up first.
//
// `slots release --owner ... --label ...` stays as it is, by owner and label, because a
// PERSON at a prompt wants exactly that: free whatever this owner is holding for that card.
// A person can see the store; a deferred cleanup cannot.
func ReleaseSlotLeasesByID(store string, ids []string, pid int) (released int, err error) {
	if pid <= 0 {
		return 0, fmt.Errorf("pid is required")
	}
	// Under the store lock (issue #1900): the same reason as ReleaseSlotLeases. A store
	// this process cannot lock -- one that is already gone, say -- is not a reason to
	// refuse to stop holding, so the release goes on unlocked rather than erroring.
	if unlock, lerr := takeSlotStoreLock(store); lerr == nil {
		defer unlock()
	}
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}
		l, rerr := readSlotLease(store, id)
		if rerr != nil {
			// Already gone, or never readable: nothing of ours is held under it.
			continue
		}
		if l.Pid != pid {
			// Not ours any more. Someone reaped it and took the id, or the list is
			// stale. Either way this is not a seat we may give away.
			continue
		}
		dir := filepath.Join(slotStoreDir(store), id)
		if rerr := safepath.RemoveUnder(slotStoreDir(store), dir); rerr != nil {
			return released, rerr
		}
		released++
	}
	return released, nil
}

// NoSlotsStoreRefusal is the ONE line printed when a native launch was asked for without a
// bench slot store or without an owner (nova-tools#1546). It lives here, beside the lease
// code, because FOUR places must print the same sentence -- `nova-swarm native` itself, the
// batch that refuses before any card runs, and the two paths that build a native argv --
// and a remedy that drifts between them is a remedy a reader stops trusting.
//
// It names the store, the owner AND the exact command that makes a one-seat store, because
// "pass --slots-store <dir>" on a bench that has never had one is not a remedy, it is a
// second question.
const NoSlotsStoreRefusal = "NATIVE REFUSED reason=no_slots_store: pass --slots-store <dir> --owner <name> (one seat: nova-swarm slots init --store <dir> --owner <name> --capacity 1 --share 1)"

// SlotStoreLockName is the bench store's one lock file, beside shares.tsv and the
// slots/ directory. It is NOT part of the store's format in the sense that matters:
// shares.tsv keeps every byte of its shape, `slots list` still reads directories, and
// a store made by an older `slots init` grows this file the first time a take runs
// against it.
const SlotStoreLockName = "slots.lock"

// SlotStoreWait is how long a take or a release waits for the store lock. Every
// holder does a bounded reap, a count and at most `--n` mkdirs and then releases; a
// wait longer than this is a holder that is stuck, not a bench that is busy.
const SlotStoreWait = 10 * time.Second

// takeSlotStoreLock serialises the whole grant (issue #1900). os.Mkdir is atomic PER
// ID, which is what the comment at the top of this file says; it is not atomic per
// CAPACITY. Two takers who both read total=0 against a capacity-1 store both counted,
// both passed the share test, and both made a directory with a different name: three
// of four 50-way trials over-granted. The count and the mkdir have to be one
// transaction, and the store is a directory on one bench, so an flock on a file inside
// it is the transaction -- the same primitive, and the same dies-with-its-holder
// property, as the pool's slots.lock.
//
// The lock file is only created inside a store that already exists: a take against a
// store that was never `slots init`ed must still say `shares.tsv: ...` rather than
// quietly making a directory.
func takeSlotStoreLock(store string) (func(), error) {
	return takeFileLock(filepath.Join(store, SlotStoreLockName), SlotStoreWait)
}
