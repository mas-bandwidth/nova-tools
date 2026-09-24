package redisq

// The Layer 1 uses of docs/SPEC-REDIS.md (L40-53): the wake doorbell, the
// swarm's slots and locks, the budgets rule 13 keeps, and plan state. Each is
// a thin wrapper over the one contract that layer demands (L33-39): every
// use names a key with an owner prefix and a TTL, and a file fallback; the
// same call adopts the local instance when it is reachable and degrades to
// the fallback when it is not. A fallback is not a second design: it is the
// same contract over a file, so an outage is a latency regression and never
// a lost queue or a changed answer.
//
// Nothing here makes Redis the authority; the record is. Every write lands in
// the fallback file first -- the record -- and then in the instance, whose
// keys carry their TTL so a key that lapses is the file's to answer; every
// read takes the instance's answer when it has one and the file's when it
// does not, which is what makes the kill-and-read round trip of L52-53 (and
// issue2277_test.go) read back the same value.
//
// This is deliberately not the mode-at-start discipline the queue and its
// SlotLeases keep (SPEC-STATE: one mode per bench, never both at once). That
// rule guards the SPEC-STATE lease, whose file mode is a *different fence*
// from its Redis mode. The Layer 1 uses here hold ONE fencing token across
// both stores at once -- the file is the record, the key is the live copy of
// the same claim -- and a take that loses either store undoes the other, so
// a slot is never granted twice and the fence is visible across the stores
// that carry it.
//
// The package does not import the tools whose files the fallbacks are: the
// wake, swarm, budget and plan call sites adopt THESE wrappers, and a
// dependency from here back to them would be a cycle the day they do. The
// fallback formats are therefore written here to the byte the existing
// readers take -- the report file nova-wake's report source watches, the
// lock file the swarm's directory mode takes, the usage row the record
// carries, the .work plan file nova-work reads -- and issue2277_test.go
// parses the fallbacks with those tools' own readers, so the shapes are
// pinned together and cannot drift.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The key namespaces of the Layer 1 uses, beside the lease and cap prefixes
// the package already settles. Every key carries its owner prefix and a TTL
// (SPEC-REDIS rule 2); an unbounded key is a bug.
const (
	DoorbellPrefix = "wake:doorbell:" // wake:doorbell:<owner>:<name>
	BudgetPrefix   = "budget:"        // budget:<owner>:<job>:<column>
	PlanPrefix     = "plan:"          // plan:<owner>:position
)

// reportName is the one file name nova-wake's report source watches, at any
// depth under each --reports directory (internal/wake/report.go ReportName).
// The doorbell's fallback rides that file, so a watcher already polling the
// reports directory sees the ring with no second design of the file.
const reportName = "RESULT.md"

// Ephemeral is the Layer 1 client contract every use wraps: the instance
// (nil when there is none), the fallback's anchor (a reports directory, a
// lock-file root, a pool root, a plan file path), and the two halves of the
// contract -- write the record first, read the instance when it answers.
type Ephemeral struct {
	q    *Queue
	root string
}

// writeKey writes one value under key with its TTL. An instance that will
// not answer is a latency regression and not an error: the fallback file was
// written first and is the record, so the write stands and the call returns
// as if the instance had taken it.
func (e Ephemeral) writeKey(ctx context.Context, key, value string, ttl time.Duration) {
	if e.q == nil {
		return
	}
	_ = e.q.rdb.Set(ctx, key, value, ttl).Err()
}

// readKey reads one value: the instance's when it answers, and ("", false)
// when it does not -- unreachable, or a key whose TTL has lapsed -- which is
// the caller's signal to read the fallback file. The record is the
// authority; a lapsed key is the file's to answer.
func (e Ephemeral) readKey(ctx context.Context, key string) (string, bool) {
	if e.q == nil {
		return "", false
	}
	v, err := e.q.rdb.Get(ctx, key).Result()
	if err != nil {
		return "", false
	}
	return v, true
}

// writeFileAtomic writes the whole file and renames it into place, so a
// watcher never meets a half-written report and a reader never a half row.
// It is the whole-file write the worker contract keeps for every RESULT.md
// and the shape DirQueue.Add keeps for every card.
func writeFileAtomic(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	// CreateTemp makes its file 0600; the record's own files are the mode the
	// caller names, and the rename keeps it.
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// oneName guards a part a fallback path or an owner-prefixed key is built
// from: it is one name, never a path and never a second line, because the
// Conventions guess no paths and a key that could escape its prefix is not
// an owner-prefixed key.
func oneName(part, what string) error {
	if strings.TrimSpace(part) == "" {
		return fmt.Errorf("%s is required", what)
	}
	if strings.ContainsAny(part, "/\\\r\n\t") || part == "." || part == ".." {
		return fmt.Errorf("%s is one name, not a path: %q", what, part)
	}
	return nil
}

// ---------------------------------------------------------------- the doorbell

// Doorbell is the wake use: a change signal a blocking watcher can be woken
// by. Through the instance a ring is the key wake:doorbell:<owner>:<name>
// carrying the signal with a TTL; the fallback is the report file nova-wake
// already watches -- one RESULT.md under the reports directory its --reports
// flag polls, at any depth. (The entry half of that source is a forge poll,
// not a file; the report file is the file half the doorbell rides.)
type Doorbell struct {
	Ephemeral
}

// NewDoorbell fixes the doorbell's instance (nil for none) and the reports
// directory nova-wake watches.
func NewDoorbell(q *Queue, reportsDir string) *Doorbell {
	return &Doorbell{Ephemeral{q: q, root: reportsDir}}
}

// DoorbellKey is the instance key one doorbell rings.
func DoorbellKey(owner, name string) string { return DoorbellPrefix + owner + ":" + name }

// ReportPath is the report file one doorbell's ring lands in, at depth under
// the reports directory nova-wake walks.
func (d *Doorbell) ReportPath(owner, name string) string {
	return filepath.Join(d.root, owner, name, reportName)
}

// Ring writes one change signal: the report file first (the record, and a
// change nova-wake's watcher sees on its next poll), then the instance key
// with its TTL. The signal is one non-empty line, because the watcher's
// state value is compared byte for byte and a value that cannot round-trip
// is not state.
func (d *Doorbell) Ring(ctx context.Context, owner, name, signal string, ttl time.Duration) error {
	if err := oneName(owner, "the doorbell's owner"); err != nil {
		return err
	}
	if err := oneName(name, "the doorbell's name"); err != nil {
		return err
	}
	if signal == "" || strings.ContainsAny(signal, "\r\n") {
		return errors.New("a wake signal is one non-empty line")
	}
	if ttl <= 0 {
		return errors.New("the doorbell's TTL is a positive duration")
	}
	if err := writeFileAtomic(d.ReportPath(owner, name), []byte(signal), 0o644); err != nil {
		return err
	}
	d.writeKey(ctx, DoorbellKey(owner, name), signal, ttl)
	return nil
}

// Read returns the signal one doorbell last rang: the instance's copy when
// it answers, the report file's when it does not. The two hold the same
// bytes because Ring wrote both; a doorbell never rung reads as the empty
// signal.
func (d *Doorbell) Read(ctx context.Context, owner, name string) (string, error) {
	if signal, ok := d.readKey(ctx, DoorbellKey(owner, name)); ok {
		return signal, nil
	}
	b, err := os.ReadFile(d.ReportPath(owner, name))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ------------------------------------------------------- slots and locks, fenced

// Claim is one held slot or lock through both stores: the fenced lease the
// package already settles, and the lock file the claim degrades to.
type Claim struct {
	*Lease        // Key the instance key, Token the fencing token, Until the lapse
	File   string // the lock file the swarm's directory mode takes
}

// SwarmLocks is the swarm use: which worker holds which slot and which lane
// holds which lock. Through the instance a claim is the fenced lease the
// package already settles -- swarm:lease:<store>:<slot>, taken with SET NX
// PX and renewed and released only by the Lua that compares the stored
// fencing token; the fallback is the lock file the swarm's directory mode
// already takes, <root>/<store>/<slot>, holding the same token with the same
// take/renew/release contract. One claim, one token, both stores: the file
// is written first, so a claim is never only in Redis, and a take that loses
// either store undoes the other.
type SwarmLocks struct {
	Ephemeral
}

// NewSwarmLocks fixes the claims' instance (nil for none) and the root the
// lock files live under, one directory per store.
func NewSwarmLocks(q *Queue, root string) *SwarmLocks {
	return &SwarmLocks{Ephemeral{q: q, root: root}}
}

// LockPath is the lock file one claim degrades to.
func (s *SwarmLocks) LockPath(store, slot string) string {
	return filepath.Join(s.root, store, slot)
}

// now is the clock a claim's until= comes from: the queue's injected clock
// when there is one, the wall clock otherwise.
func (s *SwarmLocks) now() time.Time {
	if s.q != nil {
		return s.q.Now()
	}
	return time.Now().UTC()
}

// Take claims one slot or lock for ttl: the lock file first -- the record,
// and O_EXCL is the file side of SET NX, so a slot already held is
// (nil, false, nil) with the instance untouched -- then the instance key
// with the same fencing token. An instance that will not answer leaves the
// claim standing on the file; an instance that holds another claim undoes
// the file take, because a slot granted by both stores is a fence no token
// can see across.
func (s *SwarmLocks) Take(ctx context.Context, store, slot string, ttl time.Duration) (*Claim, bool, error) {
	if err := oneName(store, "the claim's store"); err != nil {
		return nil, false, err
	}
	if err := oneName(slot, "the claim's slot"); err != nil {
		return nil, false, err
	}
	if ttl <= 0 {
		return nil, false, errors.New("the claim's TTL is a positive duration")
	}
	token, err := mintToken()
	if err != nil {
		return nil, false, err
	}
	path := s.LockPath(store, slot)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		f.Close()
		return nil, false, err
	}
	if err := f.Close(); err != nil {
		return nil, false, err
	}
	// LeaseKey is pure formatting, so it names the claim even with no
	// instance to hold the key.
	key := s.q.LeaseKey(store, slot)
	lease := &Lease{Key: key, Token: token, Until: s.now().Add(ttl)}
	if s.q != nil {
		ok, err := s.q.rdb.SetNX(ctx, key, token, ttl).Result()
		if err != nil {
			// The instance is unreachable: the claim stands on the lock file.
			return &Claim{Lease: lease, File: path}, true, nil
		}
		if !ok {
			// The instance holds another claim: undo the file take, never
			// two grants of one slot.
			if rerr := os.Remove(path); rerr != nil {
				return nil, false, rerr
			}
			return nil, false, nil
		}
	}
	return &Claim{Lease: lease, File: path}, true, nil
}

// Holder is the fencing token that currently holds store/slot: the
// instance's when it answers, the lock file's when it does not. The two are
// the same token because Take wrote both; a slot never taken reads as the
// empty token.
func (s *SwarmLocks) Holder(ctx context.Context, store, slot string) (string, error) {
	if token, ok := s.readKey(ctx, s.q.LeaseKey(store, slot)); ok {
		return token, nil
	}
	token, err := readToken(s.LockPath(store, slot))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return token, err
}

// Renew extends a claim only while its token still holds it, in both stores:
// the lock file first -- the record, and the rewrite is the file lease's
// heartbeat, the mtime a reaper reads -- then the settled compare-and-release
// script in the instance. A stale token renews nothing anywhere, because the
// record's own token check refuses it before the instance is asked; an
// instance that will not answer leaves the renewal standing on the file.
func (s *SwarmLocks) Renew(ctx context.Context, c *Claim, ttl time.Duration) (bool, error) {
	if c == nil || c.Lease == nil {
		return false, nil
	}
	token, err := readToken(c.File)
	if err != nil || token != c.Token {
		return false, nil
	}
	if err := os.WriteFile(c.File, []byte(c.Token+"\n"), 0o644); err != nil {
		return false, err
	}
	if s.q != nil {
		if _, err := s.q.RenewLease(ctx, c.Lease, ttl); err != nil {
			// The instance is unreachable: the renewal stands on the lock
			// file, and the key lapses with the TTL the settled lease names
			// as its own reaper.
		}
	}
	c.Until = s.now().Add(ttl)
	return true, nil
}

// Release drops a claim only while its token still holds it, in both stores:
// the lock file first -- the record -- then the settled compare-and-release
// script in the instance. An instance that will not answer leaves the key to
// its TTL, which is the reaper the settled lease already names.
func (s *SwarmLocks) Release(ctx context.Context, c *Claim) (bool, error) {
	if c == nil || c.Lease == nil {
		return false, nil
	}
	token, err := readToken(c.File)
	if err != nil || token != c.Token {
		return false, nil
	}
	if err := os.Remove(c.File); err != nil {
		return false, err
	}
	if s.q != nil {
		if _, err := s.q.ReleaseLease(ctx, c.Lease); err != nil {
			// The instance is unreachable: the record's claim is gone.
		}
	}
	return true, nil
}

// -------------------------------------------------------------------- budgets

// Budgets is the budgets use: the spend counters rule 13 keeps -- what the
// job SENT, what it GENERATED and what it REASONED (tokens_in + tokens_out +
// reasoning; cache reads and writes are not work the job asked for). Through
// the instance a spend is the three counter keys budget:<owner>:<job>:<col>,
// each with a TTL; the fallback is the usage row the record carries --
// <root>/usage/<job>.tsv, the sixteen tab-separated columns finalize writes,
// one row per observed spend, folded by summing the way a retried native
// card's rows fold.
type Budgets struct {
	Ephemeral
}

// NewBudgets fixes the budgets' instance (nil for none) and the pool root
// whose usage/ directory carries the rows.
func NewBudgets(q *Queue, poolRoot string) *Budgets {
	return &Budgets{Ephemeral{q: q, root: poolRoot}}
}

// BudgetKey is the instance key one budget column counts under.
func BudgetKey(owner, job, column string) string {
	return BudgetPrefix + owner + ":" + job + ":" + column
}

// UsagePath is the usage file one job's observed spends land in.
func (b *Budgets) UsagePath(job string) string {
	return filepath.Join(b.root, "usage", job+".tsv")
}

// usageColumns are the record's sixteen columns, in the order the record
// writes them; the order is the contract (internal/swarm/usage.go
// UsageColumns), and issue2277_test.go pins the two equal.
var usageColumns = []string{
	"job", "attempt", "from", "started", "ended", "end", "rc", "provider", "model", "repo",
	"tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning", "usd",
}

// budgetColumns are the three columns rule 13 counts (internal/swarm/usage.go
// BudgetColumns).
var budgetColumns = []string{"tokens_in", "tokens_out", "reasoning"}

// dash is the absence the record writes, never a zero: a zero is a
// measurement and a dash is an absence.
const dash = "-"

// Spend records one observed spend of job: the usage row first -- the
// record, one row per observation, the whole file rewritten so a reader
// never meets a half row -- then the three instance counters, each INCRBY'd
// by the observation and re-TTL'd. An instance that will not answer leaves
// the rows to answer alone, and a counter that lapsed is the rows' to
// answer, so a partially counted spend can never pass for a whole one.
func (b *Budgets) Spend(ctx context.Context, owner, job string, tokensIn, tokensOut, reasoning int, ttl time.Duration) error {
	if err := oneName(owner, "the budget's owner"); err != nil {
		return err
	}
	if err := oneName(job, "the budget's job"); err != nil {
		return err
	}
	if tokensIn < 0 || tokensOut < 0 || reasoning < 0 {
		return errors.New("a spend is not negative")
	}
	if ttl <= 0 {
		return errors.New("the budget's TTL is a positive duration")
	}
	observed := map[string]int{"tokens_in": tokensIn, "tokens_out": tokensOut, "reasoning": reasoning}
	row := make([]string, len(usageColumns))
	for i, col := range usageColumns {
		v := dash
		if col == "job" {
			v = job
		} else if n, ok := observed[col]; ok {
			v = strconv.Itoa(n)
		}
		row[i] = v
	}
	path := b.UsagePath(job)
	var body []byte
	if prev, err := os.ReadFile(path); err == nil {
		body = prev // the file ends in its row's newline; the new row appends
	} else if errors.Is(err, os.ErrNotExist) {
		body = []byte(strings.Join(usageColumns, "\t") + "\n")
	} else {
		return err
	}
	body = append(body, []byte(strings.Join(row, "\t")+"\n")...)
	if err := writeFileAtomic(path, body, 0o644); err != nil {
		return err
	}
	if b.q == nil {
		return nil
	}
	for _, col := range budgetColumns {
		key := BudgetKey(owner, job, col)
		if err := b.q.rdb.IncrBy(ctx, key, int64(observed[col])).Err(); err != nil {
			return nil // the instance is unreachable: the rows answer alone
		}
		_ = b.q.rdb.PExpire(ctx, key, ttl).Err()
	}
	return nil
}

// Report is the spend rule 13 counts for job: the three counters' sum when
// the instance answers with all of them, the usage rows' folded sum when it
// does not. The two are the same number because Spend wrote both; a dash in
// a row's column is an absence, not a zero, and is skipped the way
// ProviderUsage skips it.
func (b *Budgets) Report(ctx context.Context, owner, job string) (int, error) {
	if b.q != nil {
		keys := make([]string, len(budgetColumns))
		for i, col := range budgetColumns {
			keys[i] = BudgetKey(owner, job, col)
		}
		if vals, err := b.q.rdb.MGet(ctx, keys...).Result(); err == nil {
			sum, complete := 0, true
			for _, v := range vals {
				if v == nil {
					complete = false
					break
				}
				n, err := strconv.Atoi(fmt.Sprint(v))
				if err != nil {
					complete = false
					break
				}
				sum += n
			}
			if complete {
				return sum, nil
			}
			// A counter that lapsed is the rows' to answer.
		}
		// An instance that will not answer is the rows' to answer.
	}
	return b.foldUsage(job)
}

// foldUsage sums the budget columns of every row the record carries for job,
// the record's own fold arithmetic: numeric columns summed across rows, a
// dash in any row an absence that adds nothing.
func (b *Budgets) foldUsage(job string) (int, error) {
	raw, err := os.ReadFile(b.UsagePath(job))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 2 {
		return 0, nil
	}
	at := map[string]int{}
	for i, name := range strings.Split(lines[0], "\t") {
		at[name] = i
	}
	sum := 0
	for _, line := range lines[1:] {
		values := strings.Split(line, "\t")
		for _, col := range budgetColumns {
			i, ok := at[col]
			if !ok || i >= len(values) {
				continue
			}
			if n, err := strconv.Atoi(strings.TrimSpace(values[i])); err == nil {
				sum += n
			}
		}
	}
	return sum, nil
}

// ----------------------------------------------------------------- plan state

// PlanState is the plan use: where a plan is and what it has done. Through
// the instance the position is the key plan:<owner>:position carrying
// "<position>\t<deed>" with a TTL; the fallback is the plan file on disk --
// the .work file nova-work reads, one (:plan ...) whose current :node
// carries :id (the position), :state (the deed) and :owner. The stored value
// is the two joined by one tab, and the write refuses a tab or a second line
// in either half, because a state value round-trips byte for byte over the
// stored form or it is not state.
type PlanState struct {
	Ephemeral // root is the plan file's own path
}

// NewPlanState fixes the plan use's instance (nil for none) and the plan
// file on disk.
func NewPlanState(q *Queue, planFile string) *PlanState {
	return &PlanState{Ephemeral{q: q, root: planFile}}
}

// PlanKey is the instance key one plan's position lives under.
func PlanKey(owner string) string { return PlanPrefix + owner + ":position" }

// planStates is the closed set of deeds the plan grammar admits, mirrored
// from internal/worklang (units.go knownStates) so a deed the plan file
// cannot carry back is refused here at write time; issue2277_test.go pins
// the mirror and the grammar together.
var planStates = []string{"open", "ready", "live", "blocked", "uncertain", "closed", "refused", "abandoned"}

func isPlanState(deed string) bool {
	for _, s := range planStates {
		if s == deed {
			return true
		}
	}
	return false
}

// At records where the plan is and what it has done: the plan file first
// (the record, whole-written), then the instance key with its TTL. The
// position is one non-empty line; the deed is one of the states the plan
// grammar admits, or the file could not carry it back.
func (p *PlanState) At(ctx context.Context, owner, position, deed string, ttl time.Duration) error {
	if err := oneName(owner, "the plan's owner"); err != nil {
		return err
	}
	if position == "" || strings.ContainsAny(position, "\t\r\n") {
		return errors.New("the plan's position is one non-empty line")
	}
	if !isPlanState(deed) {
		return fmt.Errorf("the plan's deed is one of the states the plan grammar admits: %s", strings.Join(planStates, ", "))
	}
	if ttl <= 0 {
		return errors.New("the plan's TTL is a positive duration")
	}
	body := fmt.Sprintf("(:plan :version 1\n (:node :id %s :kind go-fix :state :%s :owner %s))\n",
		strconv.Quote(position), deed, strconv.Quote(owner))
	if err := writeFileAtomic(p.root, []byte(body), 0o644); err != nil {
		return err
	}
	p.writeKey(ctx, PlanKey(owner), position+"\t"+deed, ttl)
	return nil
}

// Read is where the plan is and what it has done: the instance's copy when
// it answers, the plan file's when it does not. The two are the same bytes
// because At wrote both.
func (p *PlanState) Read(ctx context.Context, owner string) (string, error) {
	if v, ok := p.readKey(ctx, PlanKey(owner)); ok {
		return v, nil
	}
	return p.readPlanFile()
}

// readPlanFile reads the position and deed from the plan file's current
// node: the file At writes, one (:plan ...) with one :node, whose :id and
// :state carry the two halves of the value exactly as they were written.
func (p *PlanState) readPlanFile() (string, error) {
	raw, err := os.ReadFile(p.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	var position, deed string
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, "(:node") {
			continue
		}
		if v, ok := cutQuoted(line, ":id "); ok {
			position = v
		}
		if v, ok := cutKeyword(line, ":state :"); ok {
			deed = v
		}
		break
	}
	if position == "" || deed == "" {
		return "", fmt.Errorf("the plan file %s carries no node position/deed", p.root)
	}
	return position + "\t" + deed, nil
}

// cutQuoted reads the quoted string after key inside line, the way At wrote
// it with strconv.Quote, so every character round-trips.
func cutQuoted(line, key string) (string, bool) {
	i := strings.Index(line, key)
	if i < 0 {
		return "", false
	}
	rest := line[i+len(key):]
	if rest == "" || rest[0] != '"' {
		return "", false
	}
	for j := 1; j < len(rest); j++ {
		switch rest[j] {
		case '\\':
			j++
		case '"':
			v, err := strconv.Unquote(rest[:j+1])
			if err != nil {
				return "", false
			}
			return v, true
		}
	}
	return "", false
}

// cutKeyword reads the bare word after key inside line, the way At wrote it.
func cutKeyword(line, key string) (string, bool) {
	i := strings.Index(line, key)
	if i < 0 {
		return "", false
	}
	rest := line[i+len(key):]
	end := strings.IndexAny(rest, " \t)\n")
	if end < 0 {
		end = len(rest)
	}
	if end == 0 {
		return "", false
	}
	return rest[:end], true
}
