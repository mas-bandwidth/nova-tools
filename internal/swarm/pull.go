package swarm

// Pull workers with leases and heartbeats (docs/SPEC-JOBS.md section 3).
//
// `nova-swarm pull` takes a slots take lease from the bench store before it runs
// a card. A card lives in <store>/queue/<name>.card; taking it renames it to
// <store>/taken/<owner>-<name>.card -- atomic within the store, so two workers
// cannot take one card -- and the lease it holds carries the card name as its
// label. A worker renews that lease by `nova-work heartbeat` while the card runs.
//
// A lease past until= whose pid is gone is reaped by the next take: its card
// returns to queue/ and the lease is removed. A lease past until= whose pid is
// still alive is DRIFT -- never reaped and never regranted. The coordinator
// starts no slot without a lease it took from this store.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// queueDirName and takenDirName are the per-bench card directories of section 2:
// the queue a puller reaches into, and the place a taken card is owned.
const (
	queueDirName = "queue"
	takenDirName = "taken"
)

// ErrNoCard is what a pull returns when the bench store's queue/ holds nothing to
// take. It is not a refusal: an empty queue is an idle puller.
var ErrNoCard = errors.New("queue is empty")

// LeaseRefusal is a take that could not grant: the owner's share is spent or the
// bench is at capacity minus reserve. It carries the holder list `slots take`
// prints so the puller can refuse with the same names.
type LeaseRefusal struct {
	Card    string
	Held    int
	Share   int
	Free    int
	Holders string
}

func (e *LeaseRefusal) Error() string {
	holders := e.Holders
	if holders == "" {
		holders = "-"
	}
	return fmt.Sprintf("no lease held=%d share=%d free=%d holders=%s", e.Held, e.Share, e.Free, holders)
}

// PullResult names the card a pull owns and the lease it holds it under.
type PullResult struct {
	Owner string
	Card  string
	Lease string
	Until time.Time
}

// QueuedCards lists the card files in <store>/queue, in name order. A directory
// or a non-.card file is not a card. A missing queue/ is an empty queue, not an
// error: a bench with nothing waiting pulls nothing.
func QueuedCards(store string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(store, queueDirName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".card") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// returnCardForLease puts the card an expired lease names back in queue/. The
// taken file is <store>/taken/<owner>-<label>; the rename is what makes the card
// queueable again. A lease with no label names no card, and a card already back
// in queue/ is left alone. It returns the card name when it moved one.
func returnCardForLease(store string, l SlotLease) string {
	label := strings.TrimSpace(l.Label)
	if label == "" || strings.ContainsAny(label, `/\`) {
		return ""
	}
	taken := filepath.Join(store, takenDirName, l.Owner+"-"+label)
	if _, err := os.Stat(taken); err != nil {
		return ""
	}
	queueDir := filepath.Join(store, queueDirName)
	if err := os.MkdirAll(queueDir, 0o755); err != nil {
		return ""
	}
	if err := os.Rename(taken, filepath.Join(queueDir, label)); err != nil {
		return ""
	}
	return label
}

// ReapExpiredLeases returns the cards of expired leases whose pid is gone to
// queue/ and removes those leases, in lease id order. A lease past until= whose
// pid is alive is DRIFT and is left where it is; a lease still inside its term
// is live and is left too.
func ReapExpiredLeases(store string, now time.Time) ([]string, error) {
	entries, err := os.ReadDir(slotStoreDir(store))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var returned []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		l, rerr := readSlotLease(store, e.Name())
		if rerr != nil {
			// A half-written take: no lease, no owner, no card. Reap it.
			_ = os.RemoveAll(filepath.Join(slotStoreDir(store), e.Name()))
			continue
		}
		if l.Until.After(now) || Alive(l.Pid, "") {
			continue
		}
		if card := returnCardForLease(store, l); card != "" {
			returned = append(returned, card)
		}
		_ = os.RemoveAll(filepath.Join(slotStoreDir(store), e.Name()))
	}
	sort.Strings(returned)
	return returned, nil
}

// PullCard takes the next card from <store>/queue under a fresh one-slot lease.
// It reaps first, so a dead worker's lease returns its card to the queue before
// this pull considers it. With no card it returns ErrNoCard; when the take
// refuses, no card is moved and it returns *LeaseRefusal. It never reaches for a
// card without a lease.
func PullCard(store, owner string, dur time.Duration, now time.Time, pid int) (PullResult, error) {
	if _, err := ReapExpiredLeases(store, now); err != nil {
		return PullResult{}, err
	}
	cards, err := QueuedCards(store)
	if err != nil {
		return PullResult{}, err
	}
	if len(cards) == 0 {
		return PullResult{}, ErrNoCard
	}
	card := cards[0]
	granted, held, share, free, holders, ok, err := TakeSlotLeases(store, owner, 1, dur, card, now, pid)
	if err != nil {
		return PullResult{}, err
	}
	if !ok {
		return PullResult{}, &LeaseRefusal{Card: card, Held: held, Share: share, Free: free, Holders: holders}
	}
	_ = granted
	takenDir := filepath.Join(store, takenDirName)
	if err := os.MkdirAll(takenDir, 0o755); err != nil {
		_, _, _ = ReleaseSlotLeases(store, owner, card, false)
		return PullResult{}, err
	}
	if err := os.Rename(filepath.Join(store, queueDirName, card), filepath.Join(takenDir, owner+"-"+card)); err != nil {
		_, _, _ = ReleaseSlotLeases(store, owner, card, false)
		return PullResult{}, err
	}
	return PullResult{Owner: owner, Card: card, Lease: newestLeaseID(store, owner, card), Until: now.Add(dur)}, nil
}

// newestLeaseID names the lease a take just wrote for owner and label. The store
// holds one per pull and its id sorts last, so the largest id is the fresh one.
func newestLeaseID(store, owner, label string) string {
	leases, err := ListSlotLeases(store, time.Now().UTC())
	if err != nil {
		return ""
	}
	id := ""
	for _, l := range leases {
		if l.Owner == owner && l.Label == label && l.ID > id {
			id = l.ID
		}
	}
	return id
}

// RenewSlotLease extends the until= of the lease owner holds under label by dur,
// and reports how many leases it renewed and the latest new until. It renews by
// owner and label because the heartbeat is its own process: the pid in the lease
// is the pull worker's for liveness, not the renewer's identity. A lease whose
// pid is gone is left for the next take to reap.
func RenewSlotLease(store, owner, label string, dur time.Duration, now time.Time) (renewed int, until time.Time, err error) {
	if strings.TrimSpace(owner) == "" {
		return 0, time.Time{}, fmt.Errorf("owner is required")
	}
	if dur <= 0 {
		return 0, time.Time{}, fmt.Errorf("for is a positive duration")
	}
	entries, err := os.ReadDir(slotStoreDir(store))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, time.Time{}, nil
		}
		return 0, time.Time{}, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		l, rerr := readSlotLease(store, e.Name())
		if rerr != nil || l.Owner != owner || l.Label != label {
			continue
		}
		next := now.Add(dur)
		body := fmt.Sprintf("owner=%s\npid=%d\nlabel=%s\nuntil=%s\n",
			l.Owner, l.Pid, l.Label, next.UTC().Format(time.RFC3339))
		if werr := os.WriteFile(slotLeaseFile(store, e.Name()), []byte(body), 0o644); werr != nil {
			return renewed, until, werr
		}
		renewed++
		until = next
	}
	return renewed, until, nil
}
