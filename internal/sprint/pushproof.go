package sprint

import (
	"strings"
	"time"
)

// The seat's push proof (docs/SPEC-SPRINT.md, "The push proof"; the owner,
// 2026-10-05: "Your inbox MUST push to you." and "it won't work until the AI
// does this"): the seat is held only by a session the push loop can reach. The
// push record names the AI's harness and its deliver target; the push loop
// (inbox --wait --push seat) delivers a check carrying a fresh nonce into the
// session through the harness's adapter, and the seat is live only while a
// pong carrying that nonce came back from inside the session within
// PushProofLive. Without a live proof every coordinator verb is refused with
// one line (PushDown), and coordinator <name> refuses a name with none.

// PushProofEvery is how often the push loop proves the seat again by the same
// round trip; PushAnswerBound is how long a check waits for its pong before
// the loop asks again; PushProofLive is how long a pong stands for the seat.
const (
	PushProofEvery  = 10 * time.Minute
	PushAnswerBound = 5 * time.Minute
	PushProofLive   = PushProofEvery + PushAnswerBound
	PushRetry       = time.Minute // how soon a failed delivery is tried again
)

// PushCheckPrefix opens the check the push loop delivers: the session sees it
// as the first words of its turn.
const PushCheckPrefix = "NOVA SPRINT PUSH CHECK "

// PushRecord is one name's push record: the harness and its deliver target (a
// directory, and a session where the harness names one), the check last
// delivered (its nonce and when), the last delivery failure, and the last pong
// that carried a delivered nonce.
type PushRecord struct {
	Name    string    `json:"name"`
	Harness string    `json:"harness"`
	Target  string    `json:"target"`
	Session string    `json:"session,omitempty"`
	Nonce   string    `json:"nonce,omitempty"`
	Sent    time.Time `json:"sent,omitzero"`
	Failed  string    `json:"failed,omitempty"`
	Proven  time.Time `json:"proven,omitzero"`
	PongOf  string    `json:"pong_of,omitempty"`
}

// PushSetup is the command that records name's push target, installs the push
// loop and starts the proof: harness is the record's, else a placeholder.
func PushSetup(name string, rec PushRecord, ok bool) string {
	h, target := "<harness>", "<session dir>"
	if ok && rec.Harness != "" {
		h, target = rec.Harness, rec.Target
	}
	return "nova-sprint seat install --actor " + orDash(name) + " --harness " + h + " --target " + target
}

// PushLive says the record holds a pong no older than PushProofLive and the
// last delivery did not fail: a push that fails is down now, whatever it
// proved before.
func PushLive(rec PushRecord, ok bool, now time.Time) bool {
	return ok && rec.Failed == "" && !rec.Proven.IsZero() && rec.PongOf != "" && now.Sub(rec.Proven) <= PushProofLive
}

// PushWhy is why name's seat is not proven, "" when it is: no record, no check
// delivered, the last delivery failed, the check unanswered, or the proof
// older than PushProofLive.
func PushWhy(name string, rec PushRecord, ok bool, now time.Time) string {
	switch {
	case PushLive(rec, ok, now):
		return ""
	case !ok || rec.Harness == "":
		return name + " has no push target recorded: the push loop cannot reach the session"
	case rec.Failed != "":
		return "the last push into " + name + "'s " + rec.Harness + " session failed: " + rec.Failed
	case rec.Nonce == "":
		return "no push check has been delivered into " + name + "'s " + rec.Harness + " session yet: is inbox --wait --push seat running?"
	case rec.Proven.IsZero() || rec.PongOf == "":
		return "the push check " + rec.Nonce + " went into " + name + "'s " + rec.Harness + " session and no pong carrying it came back"
	}
	return name + "'s last pong is " + now.Sub(rec.Proven).Truncate(time.Second).String() + " old, past " + PushProofLive.String() + ": the push loop has not proven the session again"
}

// PushDown is the one line every coordinator verb is refused with while name's
// seat has no live proof, "" when it has one: PUSH DOWN, why, and the setup
// command.
func PushDown(name string, rec PushRecord, ok bool, now time.Time) string {
	why := PushWhy(name, rec, ok, now)
	if why == "" {
		return ""
	}
	return "PUSH DOWN: " + why + "; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: " + PushSetup(name, rec, ok)
}

// PushDue says the push loop delivers a new check now: none delivered yet, the
// last one failed PushRetry ago, the last one unanswered past PushAnswerBound, or the proof
// PushProofEvery old.
func PushDue(rec PushRecord, now time.Time) bool {
	switch {
	case rec.Harness == "":
		return false
	case rec.Nonce == "" || rec.Sent.IsZero():
		return true
	case rec.Failed != "":
		return now.Sub(rec.Sent) >= PushRetry
	case rec.PongOf != rec.Nonce:
		return now.Sub(rec.Sent) >= PushAnswerBound
	}
	return now.Sub(rec.Proven) >= PushProofEvery
}

// PushSent is the record after a check with nonce was delivered at now (why ""),
// or failed to be (why the adapter's reason): a failure puts the seat down
// until a check is delivered and answered.
func PushSent(rec PushRecord, nonce, why string, now time.Time) PushRecord {
	rec.Nonce, rec.Sent, rec.Failed = nonce, now, strings.TrimSpace(why)
	return rec
}

// PushPong is the record after a pong carrying nonce, "" why when it counts:
// it must carry the last check delivered, and only once.
func PushPong(rec PushRecord, ok bool, nonce string, now time.Time) (PushRecord, string) {
	switch {
	case !ok || rec.Harness == "":
		return rec, "no push target is recorded for " + orDash(rec.Name) + ": nothing was pushed for a pong to answer"
	case rec.Nonce == "":
		return rec, "no push check has been delivered to " + rec.Name + ": nothing to answer"
	case nonce != rec.Nonce:
		return rec, "the pong carries " + orDash(nonce) + " and the last push check delivered carried " + rec.Nonce + ": only the session's answer to the last check counts"
	case rec.Failed != "":
		return rec, "the push check " + rec.Nonce + " was not delivered (" + rec.Failed + "): a pong to it is no proof"
	case rec.PongOf == nonce:
		return rec, "the pong to " + nonce + " was counted already"
	}
	rec.Proven, rec.PongOf = now, nonce
	return rec, ""
}

// PushCheckText is the check the push loop delivers into name's session: the
// prefix and nonce, then the one command that answers it from inside.
func PushCheckText(name, nonce string) string {
	return PushCheckPrefix + nonce + "\nThe sprint's push loop proves it reaches this session: the seat is held only by a session it can reach, and every coordinator verb is refused until this answer comes back. Answer now, before anything else, with one command, then end this turn: nova-sprint seat pong " + nonce + " --actor " + name + "\n"
}

// NotPushTarget is why a push record may not be written for name, "" is may:
// a name, a harness, and a target directory.
func NotPushTarget(rec PushRecord) string {
	switch {
	case !ValidID(rec.Name):
		return "a push record wants --actor <name>, the seat it pushes to: letters, digits, _ and -: " + orDash(rec.Name)
	case strings.TrimSpace(rec.Harness) == "":
		return "the push loop delivers through the harness's adapter: --harness <name> is required"
	case strings.TrimSpace(rec.Target) == "":
		return "--target <dir> is required: the session's directory, where the harness's adapter delivers"
	}
	return ""
}
