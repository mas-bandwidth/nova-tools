// Package hook says whether an identity is hooked in: the one record, in the
// bus Redis, of the role an identity hooked in as, which pushes that role
// needs, and which of them are live and proven now (docs/SPEC-HOOK.md; the
// machine is tla/Hook.tla). Every tool that sends, reads or takes work as an
// identity asks Gate first and acts only on a yes; a no carries the
// breadcrumb, the exact command lines that hook the identity in.
//
// The record's truth lives in Redis alone. A push is live while its holder,
// the process that receives that push, renews a lease key with a TTL; a dead
// holder's lease expires on its own and the gate refuses. A push is proven
// while a proof key with a TTL stands: a session answered the challenge its
// hook printed, or a daemon holder proved its own round trip. This package is
// its own module, with the Redis client its one dependency, so every tool
// that gates (nova-tools and nova-sprint alike) reads the same record.
package hook

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Push is one stream of notifications an identity must be hooked into.
type Push string

// The pushes (docs/SPEC-HOOK.md, roles and pushes).
const (
	Bus    Push = "bus"    // the identity's bus stream (bus2:to:<name>), pushed into its session
	Sprint Push = "sprint" // the sprint server's judgments and pushes to the coordinator
	Work   Push = "work"   // the work the sprint deals the identity
)

// Pushes is every push, in the order the record's keys list them.
var Pushes = []Push{Bus, Sprint, Work}

// Role is what an identity hooks in as; it names the pushes it needs.
type Role string

// The roles (docs/SPEC-HOOK.md, roles and pushes).
const (
	Coordinator Role = "coordinator" // the seat: the bus and the sprint
	Friend      Role = "friend"      // a friend working the sprint: the bus and its work
	Comms       Role = "comms"       // a friend on the bus that takes no sprint work
	Member      Role = "member"      // a machine's member loop: its work
	Reader      Role = "reader"      // a machine's reader loop: its reads
)

// Roles is every role, in the order help lists them.
var Roles = []Role{Coordinator, Friend, Comms, Member, Reader}

// needs is each role's pushes.
var needs = map[Role][]Push{
	Coordinator: {Bus, Sprint},
	Friend:      {Bus, Work},
	Comms:       {Bus},
	Member:      {Work},
	Reader:      {Work},
}

// PushesOf is the pushes role needs, nil for no role.
func PushesOf(r Role) []Push { return slices.Clone(needs[r]) }

// ParseRole is the role s names, or why it is none.
func ParseRole(s string) (Role, error) {
	r := Role(s)
	if _, ok := needs[r]; ok {
		return r, nil
	}
	return "", fmt.Errorf("--role %q is no role; it wants one of %s", s, roleList())
}

func roleList() string {
	var out []string
	for _, r := range Roles {
		out = append(out, string(r))
	}
	return strings.Join(out, ", ")
}

// The clocks of the record (docs/SPEC-HOOK.md, the clocks; tla/Hook.tla).
const (
	// LeaseTTL is how long a push stays live after its holder last renewed it.
	LeaseTTL = 90 * time.Second
	// RenewEvery is how often a holder renews: three renewals inside one lease.
	RenewEvery = 30 * time.Second
	// ChallengeEvery is how often a session's hook asks it again.
	ChallengeEvery = 30 * time.Minute
	// AnswerBound is how long a challenge waits for its answer.
	AnswerBound = 5 * time.Minute
	// ProofTTL is how long a proof stands: a challenge's cadence and its answer's bound.
	ProofTTL = ChallengeEvery + AnswerBound
)

// The keys (docs/SPEC-HOOK.md, the key layout), all in the bus Redis.
const (
	prefix = "hook:"
	// NamesKey is the set of every name that ever hooked in.
	NamesKey = prefix + "names"
)

// RecordKey is name's record: a hash of role, state, epoch, armed, unhooked, by.
func RecordKey(name string) string { return prefix + "rec:" + name }

// LiveKey is the lease of name's push: a string with a TTL its holder renews.
func LiveKey(name string, p Push) string { return prefix + "live:" + name + ":" + string(p) }

// ProofKey is the proof of name's push: a string with a TTL.
func ProofKey(name string, p Push) string { return prefix + "proof:" + name + ":" + string(p) }

// ChallengeKey is name's outstanding challenge: a hash of epoch, sum, pushes, with a TTL.
func ChallengeKey(name string) string { return prefix + "chal:" + name }

// The record's states and a push's (docs/SPEC-HOOK.md, the states).
const (
	StateNone     = "none"     // no record: never hooked in
	StateUnhooked = "unhooked" // unhooked by nova-hook unhook
	StateExpired  = "expired"  // hooked, and no push of the role is live
	StatePartial  = "partial"  // hooked, some pushes of the role live and some not
	StateLive     = "live"     // hooked, every push live, not every one proven
	StateProven   = "proven"   // hooked, every push live and proven: the gate's one yes

	PushDown   = "down"   // no live lease: its holder is not running
	PushArmed  = "armed"  // a live lease and no proof
	PushProven = "proven" // a live lease and a proof
)

// stamp is the value of a lease or a proof: the record's epoch it was written
// under, the store's time in ms, and who wrote it.
type stamp struct {
	Epoch int64  `json:"epoch"`
	At    int64  `json:"at"`
	By    string `json:"by"`
}

// parseStamp is the stamp raw holds, ok false when it holds none.
func parseStamp(raw string) (stamp, bool) {
	var s stamp
	if raw == "" || json.Unmarshal([]byte(raw), &s) != nil {
		return stamp{}, false // ignored: a value that is no stamp proves nothing
	}
	return s, true
}

// PushRead is one push's keys as one read found them: the lease and the proof
// values ("" for none) and their remaining TTLs.
type PushRead struct {
	Live, Proof         string
	LiveLeft, ProofLeft time.Duration
}

// PushStatus is one push of a role, judged.
type PushStatus struct {
	Push      Push
	State     string // PushDown, PushArmed, PushProven
	By        string // the lease's holder, "" when down
	LiveLeft  time.Duration
	ProofLeft time.Duration
}

// Status is one identity's record, judged at the store's now.
type Status struct {
	Name     string
	Role     Role
	State    string // StateNone .. StateProven
	Epoch    int64
	Armed    time.Time // when the record was last hooked, zero when never
	Unhooked time.Time // when it was unhooked, zero when it is not
	Pushes   []PushStatus
}

// Judge is the status a record and its pushes' reads say: the state machine,
// a pure function (tla/Hook.tla, GateOK). rec is the record hash (empty: no
// record); reads holds one PushRead per push of Pushes, in that order. A lease
// or proof whose epoch is not the record's counts for nothing.
func Judge(name string, rec map[string]string, reads []PushRead) Status {
	st := Status{Name: name, State: StateNone}
	if len(rec) == 0 {
		return st
	}
	st.Role = Role(rec["role"])
	st.Epoch, _ = strconv.ParseInt(rec["epoch"], 10, 64) // ignored: an epoch that is no number matches no stamp, so nothing counts
	st.Armed = msTime(rec["armed"])
	if rec["state"] != "hooked" {
		st.State = StateUnhooked
		st.Unhooked = msTime(rec["unhooked"])
		return st
	}
	need := needs[st.Role]
	if len(need) == 0 {
		st.State = StateExpired // a role this package does not know needs a push nobody holds
		return st
	}
	live, proven := 0, 0
	for _, p := range need {
		i := slices.Index(Pushes, p)
		ps := PushStatus{Push: p, State: PushDown}
		if i < len(reads) {
			r := reads[i]
			if l, ok := parseStamp(r.Live); ok && l.Epoch == st.Epoch {
				ps.State, ps.By, ps.LiveLeft = PushArmed, l.By, r.LiveLeft
				live++
				if pr, ok := parseStamp(r.Proof); ok && pr.Epoch == st.Epoch {
					ps.State, ps.ProofLeft = PushProven, r.ProofLeft
					proven++
				}
			}
		}
		st.Pushes = append(st.Pushes, ps)
	}
	switch {
	case proven == len(need):
		st.State = StateProven
	case live == len(need):
		st.State = StateLive
	case live > 0:
		st.State = StatePartial
	default:
		st.State = StateExpired
	}
	return st
}

// msTime is the instant a record field of ms since the epoch names, zero when none.
func msTime(s string) time.Time {
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil || ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// Want is what a gate asks of an identity: its name, the pushes the act needs,
// and the role the caller would hook in as when the record names none (the
// breadcrumb's role; "" lists every role that carries the pushes).
type Want struct {
	Name string
	Need []Push
	Role Role
}

// Refusal is the gate's no: the identity is not hooked in for the act. It
// carries the status it judged and the breadcrumb, the exact command lines
// that hook the identity in, runnable as printed.
type Refusal struct {
	Status Status
	Why    string   // one line: what is not live
	Hook   string   // the command that hooks in, the remedy
	Lines  []string // the rest of the breadcrumb, one runnable line each
}

func (r *Refusal) Error() string { return r.Why + "; run: " + r.Hook }

// ErrNotHooked is what Renew, Prove and Challenge answer when the record is
// not hooked under the holder's epoch: unhooked, or hooked again since.
var ErrNotHooked = errors.New("the record is not hooked under this epoch: it was unhooked, or hooked again since")

// Gate is nil when st lets w act: the record is hooked, every push of its role
// is live and proven, and its role carries every push w needs. Otherwise it
// is the Refusal with its breadcrumb (tla/Hook.tla, GateOK).
func (st Status) Gate(w Want) *Refusal {
	var missing []string
	for _, p := range w.Need {
		if st.State != StateNone && st.State != StateUnhooked && !slices.Contains(needs[st.Role], p) {
			missing = append(missing, string(p))
		}
	}
	if st.State == StateProven && len(missing) == 0 {
		return nil
	}
	role := st.Role
	if st.State == StateNone || len(missing) > 0 || role == "" {
		role = w.Role
	}
	r := &Refusal{Status: st}
	switch {
	case st.State == StateNone:
		r.Why = fmt.Sprintf("%s is not hooked in: no hook record (never hooked); nothing on the bus, friend comms or the sprint works until it is", w.Name)
	case st.State == StateUnhooked:
		r.Why = fmt.Sprintf("%s is not hooked in: unhooked at %s; nothing on the bus, friend comms or the sprint works until it hooks in again", w.Name, st.Unhooked.Format(time.RFC3339))
	case len(missing) > 0:
		r.Why = fmt.Sprintf("%s is hooked in as %s, which carries no %s push; this act needs it, so hook in under a role that does", w.Name, st.Role, strings.Join(missing, ","))
	default:
		r.Why = fmt.Sprintf("%s is not hooked in: %s as %s (%s); every push of the role must be live and proven", w.Name, st.State, st.Role, st.pushWords())
	}
	r.Hook, r.Lines = Breadcrumb(w.Name, role, w.Need, st)
	return r
}

// pushWords is each push's state: "bus=proven sprint=down".
func (st Status) pushWords() string {
	var out []string
	for _, p := range st.Pushes {
		out = append(out, string(p.Push)+"="+p.State)
	}
	return strings.Join(out, " ")
}

// Words is the status as one line's words: state, role, epoch and each push.
func (st Status) Words() string {
	s := "state=" + st.State
	if st.Role != "" {
		s += fmt.Sprintf(" role=%s epoch=%d", st.Role, st.Epoch)
	}
	if w := st.pushWords(); w != "" {
		s += " " + w
	}
	return s
}

// Breadcrumb is the command that hooks name in as role, and the lines that
// say who holds each push and how to see and end the hook, each runnable as
// printed (docs/SPEC-HOOK.md, the breadcrumb). With no role it names every
// role that carries need.
func Breadcrumb(name string, role Role, need []Push, st Status) (string, []string) {
	roles := []Role{role}
	if role == "" {
		roles = nil
		for _, r := range Roles {
			if containsAll(needs[r], need) {
				roles = append(roles, r)
			}
		}
	}
	hook := HookCommand(name, roles[0])
	var lines []string
	for _, r := range roles[1:] {
		lines = append(lines, "or, as "+string(r)+": "+HookCommand(name, r))
	}
	for _, r := range roles {
		if role == "" {
			break // no role known: the hook command of each role is the breadcrumb; its holders follow once one is chosen
		}
		for _, p := range needs[r] {
			if st.pushState(p) == PushProven {
				continue
			}
			lines = append(lines, fmt.Sprintf("the %s push of a %s is held by: %s", p, r, HeldBy(name, r, p)))
		}
	}
	lines = append(lines,
		"see it: nova-hook status --as "+name,
		"unhook: nova-hook unhook --as "+name)
	return hook, lines
}

// HookCommand is the line that hooks name in as role.
func HookCommand(name string, r Role) string {
	return "nova-hook hook --as " + name + " --role " + string(r)
}

// HeldBy is who holds push p of a role for name: the command that runs it.
func HeldBy(name string, r Role, p Push) string {
	switch {
	case p == Sprint:
		return "nova-sprint hook --as " + name + " (under its own Monitor; it renews the sprint push while its subscription is proven)"
	case p == Bus && (r == Friend || r == Comms):
		return "the friend daemon, nova-friend run --as " + name + " ... (its agent com.nova.friend-" + name + "; it hooks in at start), or a session's own " + HookCommand(name, r) + " under a Monitor"
	case p == Bus:
		return HookCommand(name, r) + " under the session's Monitor (each line it prints is a push; it prints HOOK CHALLENGE, answered with nova-hook prove --as " + name + " --nonce <nonce>)"
	case r == Friend:
		return "the friend daemon's lanes, nova-friend run --as " + name + " ... (its agent com.nova.friend-" + name + "; it hooks in at start)"
	case r == Reader:
		return "the reader loop, nova-swarm member --as " + name + " --reader (it hooks in at start)"
	}
	return "the member loop, nova-swarm member --as " + name + " (it hooks in at start)"
}

// pushState is p's state in st, PushDown when st holds none.
func (st Status) pushState(p Push) string {
	for _, ps := range st.Pushes {
		if ps.Push == p {
			return ps.State
		}
	}
	return PushDown
}

func containsAll(have, want []Push) bool {
	for _, p := range want {
		if !slices.Contains(have, p) {
			return false
		}
	}
	return true
}
