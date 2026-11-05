package friend

import (
	"regexp"
	"strconv"
	"strings"
)

// A friend lane asks the sprint server with `take --as friend.<name> --lane <n>` (LaneTakeArgv)
// and reads its one LANE line back (ParseLaneHold): the protocol the friend lane client in her
// hook speaks (internal/friend, nova-hook).

// LaneTakeArgv is lane n's ask of the sprint server: what it holds, else its next card. It
// names the worker's row and the lane and nothing else: no card, no epoch.
func LaneTakeArgv(member string, n int) []string {
	return []string{"take", "--as", member, "--lane", strconv.Itoa(n)}
}

// LaneHold is the server's answer to a lane's ask: the lane, the epoch the server holds, and
// the card the lane holds there (Card "" for none) at its generation, its kind (work or read)
// and its job (<id>~<epoch>, .g<gen> from the second generation).
type LaneHold struct {
	Lane  int
	Epoch uint64
	Card  string
	Gen   int
	Kind  string
	Job   string
}

// laneLineRE is the LANE line's head; the words after holds= are key=value fields read in any
// order (docs/SPEC-FRIEND.md, the LANE line): nova-sprint prints rank=<d> between holds= and
// kind=, and a field the lane does not read is skipped, never a refusal.
var laneLineRE = regexp.MustCompile(`LANE (\S+) lane=(\d+) epoch=(\d+) holds=(\S+)(.*)`)

// laneLineGrammar is the LANE line a lane can run, said in an Unparseable refusal.
const laneLineGrammar = "LANE <member> lane=<n> epoch=<e> holds=<card>@<gen> [rank=<d>] kind=<work|read> job=<job>"

// Unparseable is a LANE line that names the lane that asked but that the lane cannot read: a
// field it needs (gen, kind, job) missing or malformed. It is a Refused too. Asking again gets
// the same line from the same server, so the lane says it once and asks no more until the
// daemon restarts (laneAsk), rather than once a TakeRetry forever.
type Unparseable struct {
	Field string // the field the line is missing: gen, kind or job
	Why   string
}

func (u *Unparseable) Error() string { return u.Why }

// Unwrap makes an unparseable line a refusal for errors.As.
func (u *Unparseable) Unwrap() error { return &Refused{Why: u.Why} }

func unparseable(field string) *Unparseable {
	return &Unparseable{Field: field, Why: "the server's LANE line names no " + field + "= the lane can read (a lane runs " + laneLineGrammar + ")"}
}

// ParseLaneHold reads the server's LANE line for member's lane n out of an ask's answer. An
// answer with no such line, or one naming another member or lane, is a refusal: the lane
// runs nothing (the fault of 2026-10-10 21:42Z: a take answered OK and the lane said "took"
// while the server still showed the card ready). A line for this lane that holds a card but
// lacks its generation, kind or job is Unparseable, naming the field (the fault of 2026-10-14
// 03:51Z: rank= between holds= and kind= left a reader-only friend's lanes refusing every read
// card's line, once every TakeRetry, all night).
func ParseLaneHold(member string, n int, out string) (LaneHold, error) {
	for _, line := range strings.Split(out, "\n") {
		m := laneLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lane, _ := strconv.Atoi(m[2])
		if m[1] != member || lane != n {
			return LaneHold{}, &Refused{Why: "the server's LANE line names another member's lane or another lane"}
		}
		epoch, err := strconv.ParseUint(m[3], 10, 64)
		if err != nil {
			return LaneHold{}, &Refused{Why: "the server's LANE line names no epoch"}
		}
		h := LaneHold{Lane: n, Epoch: epoch}
		for _, w := range strings.Fields(m[5]) {
			k, v, _ := strings.Cut(w, "=")
			switch k {
			case "kind":
				h.Kind = v
			case "job":
				h.Job = v
			}
		}
		if m[4] == "-" {
			return h, nil
		}
		id, g, ok := strings.Cut(m[4], "@")
		gen, err := strconv.Atoi(g)
		switch {
		case !ok || id == "" || err != nil || gen < 1:
			return LaneHold{}, unparseable("gen")
		case h.Kind != "work" && h.Kind != "read":
			return LaneHold{}, unparseable("kind")
		case h.Job == "" || !validJob(h.Job):
			return LaneHold{}, unparseable("job")
		}
		h.Card, h.Gen = id, gen
		return h, nil
	}
	// a fixed sentence, never the answer: what the server printed is the record's, not the error's
	return LaneHold{}, &Refused{Why: "the server answered with no LANE line, so the lane holds nothing it can name"}
}

// Refused is the sprint server's refusal of a verb it answered (a non-zero exit), as against
// a server that did not answer: a refused friend cards is a server that does not serve it yet.
type Refused struct{ Why string }

func (r *Refused) Error() string { return r.Why }

// jobRE is a job's name as it may stand in her inbox: a card id, its epoch and its gen
// (sprint.StoredID, friend sync's friendJobOf), one path element and never a dot file.
var jobRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.~-]*$`)

func validJob(job string) bool {
	return len(job) <= 200 && jobRE.MatchString(job) && !strings.Contains(job, "..") && job != "retired"
}
