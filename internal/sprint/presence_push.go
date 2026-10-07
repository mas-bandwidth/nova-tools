package sprint

import (
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"time"
)

// SeatPushSet is the one seat push record (SPEC-SPRINT section 8): judgments
// retain their nonce proof; the three native watches add their successful beats.
// Four fixed records fit in one process and one existing store key.
type SeatPushSet struct {
	PushRecord
	Watches map[string]SeatWatchProof `json:"watches,omitempty"`
}

type SeatWatchProof struct {
	At         time.Time `json:"at,omitzero"`
	Epoch      uint64    `json:"epoch"`
	Generation uint64    `json:"generation"`
	Target     string    `json:"target"`
	Session    string    `json:"session,omitempty"`
	Failed     string    `json:"failed,omitempty"`
}

type SeatPushLine struct {
	Source  string        `json:"source"`
	At      time.Time     `json:"at,omitzero"`
	Period  time.Duration `json:"period"`
	Live    bool          `json:"live"`
	Why     string        `json:"why,omitempty"`
	Command string        `json:"command"`
}

// SeatPushPeriods defines each native observer's beat, never supplied by a caller.
func SeatPushPeriods() map[string]time.Duration {
	return map[string]time.Duration{"bus": time.Minute, "friends": 10 * time.Minute, "transitions": time.Minute}
}

// SeatPushLines judges the set at the seat's current epoch/generation and target.
// A future beat, failed pass, old seat or changed target proves nothing.
func SeatPushLines(set SeatPushSet, ok bool, epoch, generation uint64, now time.Time) []SeatPushLine {
	rows := []SeatPushLine{{Source: "judgments", At: set.Proven, Period: PushProofEvery, Live: PushLive(set.PushRecord, ok, now), Why: PushWhy(set.Name, set.PushRecord, ok, now), Command: PushSetup(set.Name, set.PushRecord, ok)}}
	if set.Proven.After(now) {
		rows[0].Live = false
		rows[0].Why = "proof is after the store clock"
	}
	for _, source := range []string{"bus", "friends", "transitions"} {
		p, found := set.Watches[source]
		row := SeatPushLine{Source: source, At: p.At, Period: SeatPushPeriods()[source], Command: SeatPushArm(source, set.Name)}
		switch {
		case p.Failed != "":
			row.Why = p.Failed
		case !found || p.At.IsZero():
			row.Why = "no successful beat"
		case p.Epoch != epoch || p.Generation != generation:
			row.Why = "proof belongs to an old seat or sprint epoch"
		case p.Target != set.Target || p.Session != set.Session:
			row.Why = "proof belongs to another delivery target"
		case p.At.After(now):
			row.Why = "proof is after the store clock"
		case now.Sub(p.At) > 3*row.Period:
			row.Why = fmt.Sprintf("last proof is %s old, past %s", now.Sub(p.At).Truncate(time.Second), 3*row.Period)
		default:
			row.Live = true
		}
		rows = append(rows, row)
	}
	return rows
}

// SeatPushArm is the native command for one missing push (SPEC-SPRINT section 8).
func SeatPushArm(source, name string) string {
	switch source {
	case "bus":
		return "nova-bus recv --as " + oneline.ShellWord(name) + " --forever --exec " + oneline.ShellWord("nova-sprint seat deliver --actor "+oneline.ShellWord(name))
	case "friends":
		return "nova-sprint friends watch --actor " + oneline.ShellWord(name)
	case "transitions":
		return "nova-sprint status watch --actor " + oneline.ShellWord(name)
	}
	return "nova-sprint help seat"
}
