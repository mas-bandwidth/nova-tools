package sprint

import (
	"slices"
	"time"
)

// A friend's session proof on her beat (docs/SPEC-SPRINT.md section 1, the friends'
// rule; docs/SPEC-FRIEND.md, presence). Her daemon puts a SESSION CHECK with a fresh
// nonce into her session and says so on the beat that follows (friend beat --check
// <nonce> --run <run>); when her session answers it, the next beat names the nonce
// (--pong <nonce> --run <run>). The server keeps the checks asked on her beat record and
// counts an answer as her session's evidence only when it names a nonce her daemon's
// run asked, once, within CheckAnswerWithin of the ask. A bare timestamp, a nonce never
// asked, one asked by another run, one answered already or one asked too long ago is a
// beat with no proof: recorded, said, never evidence. The owner, 2026-10-05 ~9:30 AM
// ET: "there is no value in things that are answered just by the daemon".

// CheckAnswerWithin is how long after the ask an answer to a check proves; MaxAsked is
// how many unanswered checks her beat record keeps, the oldest dropped first.
const (
	CheckAnswerWithin = 15 * time.Minute
	MaxAsked          = 8
)

// AskedCheck is one session check her daemon said it asked: the nonce, the daemon's
// run that asked it (its generation: a restart is another run), and when the server
// recorded the ask.
type AskedCheck struct {
	Nonce string    `json:"nonce"`
	Run   string    `json:"run,omitempty"`
	At    time.Time `json:"at"`
}

// BeatWords are the proof words of one beat: the daemon's run, the check it asked
// (--check) and the check its session answered (--pong), each "" for none.
type BeatWords struct {
	Run, Check, Pong string
}

// NoProof is the word for a beat whose --pong proves nothing.
const NoProof = "beat with no proof"

// ProveBeat is one beat's proof step at now, the server's clock, over the checks her
// record holds: the checks still answerable after it (an ask recorded, a stale one
// dropped, the answered one gone), whether the beat proved her session, and, when its
// --pong proves nothing, why ("beat with no proof: ..."). It reads nothing but its
// arguments.
func ProveBeat(asked []AskedCheck, w BeatWords, now time.Time) (next []AskedCheck, proved bool, why string) {
	for _, a := range asked {
		if age := now.Sub(a.At); age >= 0 && age <= CheckAnswerWithin {
			next = append(next, a)
		}
	}
	if w.Check != "" && !slices.ContainsFunc(next, func(a AskedCheck) bool { return a.Nonce == w.Check && a.Run == w.Run }) {
		next = append(next, AskedCheck{Nonce: w.Check, Run: w.Run, At: now})
		if len(next) > MaxAsked {
			next = next[len(next)-MaxAsked:]
		}
	}
	if w.Pong == "" {
		return next, false, ""
	}
	i := slices.IndexFunc(next, func(a AskedCheck) bool { return a.Nonce == w.Pong && a.Run == w.Run })
	switch {
	case !ValidID(w.Pong):
		return next, false, NoProof + ": --pong names no nonce (" + w.Pong + ")"
	case i < 0:
		return next, false, NoProof + ": nonce " + w.Pong + " was not asked by this daemon's run within " + CheckAnswerWithin.String() + ", or was answered already"
	}
	return slices.Delete(next, i, i+1), true, ""
}
