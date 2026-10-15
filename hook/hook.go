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
	"runtime"
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
	Friend:      {Bus},
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
	// ProofTTL is how long a proof stands: a challenge's cadence and twice its
	// answer's bound, so a proof answered at the last moment of one challenge's
	// bound outlives the whole bound of the next (a session answering every
	// challenge inside its bound is never unproven between them).
	ProofTTL = ChallengeEvery + 2*AnswerBound
	// BeatEvery is the heartbeat's default interval: nova-hook serve challenges
	// every hooked name whose role carries the bus push once an interval, and
	// the answer is due within the same interval (tla/HookBeat.tla).
	BeatEvery = time.Minute
	// DownAfter is the heartbeat's default silence before a name goes down:
	// nova-hook serve unhooks a name only once its last answer (or its
	// hooking in, whichever is later) is older than DownAfter by the store's TIME
	// and the challenge of the last pass still stands unanswered; challenges
	// still go out every interval (2026-10-14: a harness that cannot
	// answer mid-turn needs more than two one-minute intervals). The readers'
	// up (Served.Down) uses the same window (tla/HookBeat.tla, DownAfter).
	DownAfter = 5 * time.Minute
)

// The keys (docs/SPEC-HOOK.md, the key layout), all in the bus Redis.
const (
	prefix = "hook:"
	// NamesKey is the set of every name that ever hooked in.
	NamesKey = prefix + "names"
	// ServeKey is the heartbeat server's own stamp: a hash of at, every, by.
	ServeKey = prefix + "serve"
	// EventsKey is the capped stream of the heartbeat's unhooks, one entry each.
	EventsKey = prefix + "events"
	// EventsMax caps EventsKey (approximately, MAXLEN ~).
	EventsMax = 10000
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
	Reason   string    // why it was unhooked, when the heartbeat did it ("no answer in 5m0s (5 challenges missed)")
	BeatAt   time.Time // when the heartbeat server last challenged it, zero when never
	AnswerAt time.Time // when the session last answered a challenge, zero when never
	// Down is why the record reads down at this read whatever its state says
	// (Served.Down: no server, a stale one, or no answer), "" when the evidence
	// stands. No reader trusts the last written state then, and the gate refuses.
	Down   string
	Misses int // challenges in a row left unanswered
	Pushes []PushStatus
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
	st.BeatAt = msTime(rec["beat_at"])
	st.AnswerAt = msTime(rec["answered_at"])
	st.Misses, _ = strconv.Atoi(rec["misses"]) // ignored: no count reads as none missed
	st.Reason = rec["reason"]
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
// the roles the act is for, and the role the caller would hook in as when the
// record names none (the breadcrumb's role; "" offers every role that carries
// the pushes, and the remedy is to look first).
type Want struct {
	Name string
	Need []Push
	// Roles, when set, are the only roles the act is for: a record hooked as
	// another role is refused even when that role carries every push Need
	// names (a friend daemon arming the seat's name as friend opens none of the
	// coordinator's verbs). Empty: any role that carries Need.
	Roles []Role
	Role  Role
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
// is live and proven, its role carries every push w needs, and it is one of
// w.Roles when w names any. Otherwise it is the Refusal with its breadcrumb
// (tla/Hook.tla, Gate and GateMeansHooked).
func (st Status) Gate(w Want) *Refusal {
	hooked := st.State != StateNone && st.State != StateUnhooked
	var missing []string
	for _, p := range w.Need {
		if hooked && !slices.Contains(needs[st.Role], p) {
			missing = append(missing, string(p))
		}
	}
	otherRole := hooked && len(w.Roles) > 0 && !slices.Contains(w.Roles, st.Role)
	if st.State == StateProven && len(missing) == 0 && !otherRole && st.Down == "" {
		return nil
	}
	role := st.Role
	if st.State == StateNone || len(missing) > 0 || role == "" || len(w.Roles) > 0 && !slices.Contains(w.Roles, role) {
		role = w.Role
	}
	r := &Refusal{Status: st}
	switch {
	case st.Down != "" && st.State == StateProven: // proven, without the heartbeat's evidence; any other state says its own why
		r.Why = fmt.Sprintf("%s is not hooked in: %s: up needs a heartbeat server passing (nova-hook serve) and, for a session, its answer to the latest challenge (nova-hook prove); nothing on the bus, friend comms or the sprint works until then", w.Name, st.Down)
	case st.State == StateNone:
		r.Why = fmt.Sprintf("%s is not hooked in: no hook record (never hooked); nothing on the bus, friend comms or the sprint works until it is", w.Name)
	case st.State == StateUnhooked:
		r.Why = fmt.Sprintf("%s is not hooked in: unhooked at %s%s; nothing on the bus, friend comms or the sprint works until it hooks in again", w.Name, st.Unhooked.Format(time.RFC3339), st.because())
	case otherRole:
		r.Why = fmt.Sprintf("%s is hooked in as %s, and this act is only for %s; hook in as one of them", w.Name, st.Role, roleWords(w.Roles))
	case len(missing) > 0:
		r.Why = fmt.Sprintf("%s is hooked in as %s, which carries no %s push; this act needs it, so hook in under a role that does", w.Name, st.Role, strings.Join(missing, ","))
	default:
		r.Why = fmt.Sprintf("%s is not hooked in: %s as %s (%s); every push of the role must be live and proven", w.Name, st.State, st.Role, st.pushWords())
	}
	r.Hook, r.Lines = Breadcrumb(w.Name, role, w.Need, st)
	return r
}

// because is the heartbeat's reason for an unhook as a clause, "" when it gave none.
func (st Status) because() string {
	if st.Reason == "" {
		return ""
	}
	return " by the heartbeat (" + st.Reason + ")"
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

// offerOrder is the order a breadcrumb with no role offers the roles: the
// least first (comms carries the bus alone), then a friend, then the seat;
// the machines' roles last.
var offerOrder = []Role{Comms, Friend, Coordinator, Member, Reader}

// roleUse is what each role is for, as a breadcrumb that offers it says.
var roleUse = map[Role]string{
	Comms:       "on the bus, taking no sprint work",
	Friend:      "a friend working the sprint",
	Coordinator: "the seat",
	Member:      "a machine's member loop",
	Reader:      "a machine's reader loop",
}

// Breadcrumb is the remedy and the lines that say who holds each push and how
// to see and end the hook, each runnable as printed (docs/SPEC-HOOK.md, the
// breadcrumb). With a role, the remedy is the command that hooks name in as
// it. With no role known the breadcrumb never guesses one: the remedy is to
// look (nova-hook status), and the lines offer every role that carries need,
// comms first, then friend, then coordinator.
func Breadcrumb(name string, role Role, need []Push, st Status) (string, []string) {
	var lines []string
	if role == "" {
		for _, r := range offerOrder {
			if containsAll(needs[r], need) {
				lines = append(lines, "hook in as "+string(r)+" ("+roleUse[r]+"): "+HookCommand(name, r))
			}
		}
		lines = append(lines, "unhook: nova-hook unhook --as "+name)
		return "nova-hook status --as " + name, lines
	}
	for _, p := range needs[role] {
		if st.pushState(p) == PushProven {
			continue
		}
		lines = append(lines, fmt.Sprintf("the %s push of a %s is held by: %s", p, role, HeldBy(name, role, p)))
	}
	lines = append(lines,
		"see it: nova-hook status --as "+name,
		"unhook: nova-hook unhook --as "+name)
	return HookCommand(name, role), lines
}

// HookCommand is the line that hooks name in as role.
func HookCommand(name string, r Role) string {
	return "nova-hook hook --as " + name + " --role " + string(r)
}

// AgentLabel is the service that runs a friend's lanes: her daemon
// (com.nova.friend-<name>).
func AgentLabel(name string, r Role) string {
	return "com.nova.friend-" + name
}

// RestartCommand is the line that restarts the service label on goos, runnable
// as printed: launchctl in the user's gui domain on darwin, systemctl --user
// elsewhere (pkg/units.Load names the services the same way).
func RestartCommand(goos, label string) string {
	if goos == "darwin" {
		return "launchctl kickstart -k gui/$(id -u)/" + label
	}
	return "systemctl --user restart " + label
}

// HeldBy is who holds push p of a role for name: the service or the command
// that runs it, each line runnable as printed on this machine.
func HeldBy(name string, r Role, p Push) string {
	switch {
	case p == Sprint:
		return "the sprint's server (nova-sprint run), which holds it for the coordinator while it runs and reaches the bus"
	case p == Bus:
		return HookCommand(name, r) + " under the session's Monitor (each line it prints is a push; it prints HOOK CHALLENGE, answered with nova-hook prove --as " + name + " --nonce <nonce>); supervised service label: nova-hook.client." + name
	case r == Friend:
		label := AgentLabel(name, r)
		return "the friend daemon's lanes, its service " + label + ", which hooks in at its start; restart it: " + RestartCommand(runtime.GOOS, label)
	case r == Reader:
		return "the reader loop, nova-swarm member --as " + name + " --reader (it hooks in at start)"
	}
	return "the member loop, nova-swarm member --as " + name + " (it hooks in at start)"
}

// roleWords is roles as words: "coordinator or comms".
func roleWords(rs []Role) string {
	var out []string
	for _, r := range rs {
		out = append(out, string(r))
	}
	return strings.Join(out, " or ")
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
