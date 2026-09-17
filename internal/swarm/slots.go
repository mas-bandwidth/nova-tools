package swarm

// Bench slot leases: a bench-wide lease store with shares, reserve, expiry and
// live-pid fencing (docs/SPEC-SWARM.md, "Bench slot leases").
//
// The store is <store>/slots with one directory per lease. The directory is
// made by os.Mkdir, which is atomic: two takers racing for the last slot
// cannot both win the same name, and a lease is never inferred from a count
// but from the directories on disk. Each lease directory holds a file `lease`
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

// MakeSlotLease writes one lease directory by Mkdir (atomic) for tests and
// for fixtures: the pid and until are the caller's, not the taker's.
func MakeSlotLease(store, id, owner string, pid int, label string, until time.Time) error {
	if strings.TrimSpace(owner) == "" {
		return fmt.Errorf("owner is required")
	}
	if strings.ContainsAny(owner, "\r\n") || strings.ContainsAny(label, "\r\n") {
		return fmt.Errorf("owner and label are one line")
	}
	dir := filepath.Join(slotStoreDir(store), id)
	if err := os.MkdirAll(slotStoreDir(store), 0o755); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("owner=%s\npid=%d\nlabel=%s\nuntil=%s\n",
		owner, pid, label, until.UTC().Format(time.RFC3339))
	if err := os.WriteFile(slotLeaseFile(store, id), []byte(body), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	return nil
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

// TakeSlotLeases grants k leases to owner when both caps hold after reaping:
// the owner's held+k stays within its share, and the total held+k stays
// within capacity-reserve. Expired leases with a dead pid are reaped first;
// expired leases with a live pid are DRIFT and stay held. New leases carry
// the caller's pid and until=now+dur.
func TakeSlotLeases(store, owner string, k int, dur time.Duration, label string, now time.Time, pid int) (granted, held, share, free int, holders string, ok bool, err error) {
	if strings.TrimSpace(owner) == "" {
		return 0, 0, 0, 0, "", false, fmt.Errorf("owner is required")
	}
	if k < 1 {
		return 0, 0, 0, 0, "", false, fmt.Errorf("n is at least 1, got %d", k)
	}
	if dur <= 0 {
		return 0, 0, 0, 0, "", false, fmt.Errorf("for is a positive duration")
	}
	if pid <= 0 {
		return 0, 0, 0, 0, "", false, fmt.Errorf("pid is required")
	}
	if strings.ContainsAny(owner, "\r\n") || strings.ContainsAny(label, "\r\n") {
		return 0, 0, 0, 0, "", false, fmt.Errorf("owner and label are one line")
	}
	capacity, reserve, shares, err := loadSlotShares(store)
	if err != nil {
		return 0, 0, 0, 0, "", false, err
	}
	share = shares[owner]
	entries, err := os.ReadDir(slotStoreDir(store))
	if err != nil && !os.IsNotExist(err) {
		return 0, 0, 0, 0, "", false, err
	}
	counts := map[string]int{}
	total := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		l, rerr := readSlotLease(store, e.Name())
		if rerr != nil {
			// A half-written take: no lease file, no owner, no hold.
			// Reap it so garbage never accumulates.
			_ = os.RemoveAll(filepath.Join(slotStoreDir(store), e.Name()))
			continue
		}
		if !l.Until.After(now) && !Alive(l.Pid, "") {
			_ = os.RemoveAll(filepath.Join(slotStoreDir(store), e.Name()))
			continue
		}
		counts[l.Owner]++
		total++
	}
	held = counts[owner]
	free = capacity - reserve - total
	holders = slotHolders(counts)
	if held+k > share || total+k > capacity-reserve {
		return 0, held, share, free, holders, false, nil
	}
	if err := os.MkdirAll(slotStoreDir(store), 0o755); err != nil {
		return 0, 0, 0, 0, "", false, err
	}
	until := now.Add(dur).UTC().Format(time.RFC3339)
	for i := 0; i < k; i++ {
		for tries := 0; ; tries++ {
			id := slotLeaseID(owner, now)
			if err := os.Mkdir(filepath.Join(slotStoreDir(store), id), 0o755); err != nil {
				if os.IsExist(err) && tries < 20 {
					continue
				}
				return 0, 0, 0, 0, "", false, err
			}
			body := fmt.Sprintf("owner=%s\npid=%d\nlabel=%s\nuntil=%s\n", owner, pid, label, until)
			if err := os.WriteFile(slotLeaseFile(store, id), []byte(body), 0o644); err != nil {
				_ = os.RemoveAll(filepath.Join(slotStoreDir(store), id))
				return 0, 0, 0, 0, "", false, err
			}
			break
		}
	}
	held += k
	total += k
	free = capacity - reserve - total
	counts[owner] = held
	holders = slotHolders(counts)
	return k, held, share, free, holders, true, nil
}

// ReleaseSlotLeases removes owner's leases: all of them with all=true, or
// only the ones carrying label otherwise. It reports the removed count and
// the owner's leases still held.
func ReleaseSlotLeases(store, owner, label string, all bool) (released, held int, err error) {
	if strings.TrimSpace(owner) == "" {
		return 0, 0, fmt.Errorf("owner is required")
	}
	entries, err := os.ReadDir(slotStoreDir(store))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
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
		if rerr := os.RemoveAll(filepath.Join(slotStoreDir(store), e.Name())); rerr != nil {
			return released, held, rerr
		}
		released++
	}
	return released, held, nil
}
