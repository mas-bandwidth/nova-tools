package lifecycle

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const (
	Ready     = "READY"
	Claimed   = "CLAIMED"
	Starting  = "STARTING"
	Started   = "STARTED"
	Returned  = "RETURNED"
	Harvested = "HARVESTED"
	Unknown   = "UNKNOWN"
)

const (
	WhyNoStart        = "no-start"
	WhyLostReply      = "lost-reply"
	WhyTimeout        = "timeout"
	WhyMalformedStart = "malformed-start"
)

const (
	ExecutionNeverAdmitted = "never-admitted"
	ExecutionTerminated    = "terminated"
	ExecutionCompleted     = "completed"
)

// MaxAttempts is the hard default and maximum automatic budget (initial included).
const MaxAttempts = 2

const (
	dirName      = "lifecycle"
	eventsName   = "events.jsonl"
	cardsDir     = "cards"
	attemptsDir  = "attempts"
	lockName     = "lock"
	idBytes      = 16
	idHexLen     = 32
	maxIdentLen  = 128
	stdoutName   = "stdout"
	stderrName   = "stderr"
	exitName     = "exit"
	startedName  = "started"
	executorName = "executor"
)

// Limits is the attempt budget recorded on every event.
type Limits struct {
	Attempts int `json:"attempts"`
	Max      int `json:"max"`
}

// Event is one version-one lifecycle event. Required identities are explicitly
// null until they exist. Readers ignore unknown fields.
type Event struct {
	Card        string  `json:"card"`
	Attempt     *string `json:"attempt"`
	Prior       *string `json:"prior"`
	New         string  `json:"new"`
	Rev         int     `json:"rev"`
	Generation  *int    `json:"generation"`
	Bench       *string `json:"bench"`
	Route       *string `json:"route"`
	Source      *string `json:"source"`
	Job         *string `json:"job"`
	Lease       *string `json:"lease"`
	Limits      *Limits `json:"limits"`
	At          string  `json:"at"`
	Idempotency string  `json:"idempotency"`
	Raised      *bool   `json:"raised,omitempty"`
	Reason      *string `json:"reason,omitempty"`
	Worker      *string `json:"worker,omitempty"`
	FenceEpoch  int     `json:"fence_epoch,omitempty"`
	Execution   *string `json:"execution,omitempty"`
	Nonce       *string `json:"nonce,omitempty"`
	ExitAttest  *string `json:"exit_attest,omitempty"`
}

// Projection is the rebuildable current card state at lifecycle/cards/<card>.json.
type Projection struct {
	Card       string  `json:"card"`
	State      string  `json:"state"`
	Attempt    *string `json:"attempt"`
	Rev        int     `json:"rev"`
	Generation *int    `json:"generation"`
	Bench      *string `json:"bench"`
	Route      *string `json:"route"`
	Source     *string `json:"source"`
	Job        *string `json:"job"`
	Lease      *string `json:"lease"`
	Worker     *string `json:"worker,omitempty"`
	Limits     *Limits `json:"limits"`
	FenceEpoch int     `json:"fence_epoch"`
	Raised     bool    `json:"raised"`
	Reason     *string `json:"reason,omitempty"`
	Reserved   bool    `json:"reserved"`
	Attempts   int     `json:"attempts"`
	Execution  *string `json:"execution,omitempty"`
	Nonce      *string `json:"nonce,omitempty"`
	ExitAttest *string `json:"exit_attest,omitempty"`
}

// StartedReceipt is the typed LAUNCH STARTED acknowledgement. Wrong card,
// attempt, job, lease or generation is malformed and cannot start.
type StartedReceipt struct {
	Card       string
	Attempt    string
	Job        string
	Lease      string
	Bench      string
	Route      string
	Generation int
	Worker     string
	At         time.Time
}

// Line is the typed acknowledgement the spec names.
func (r StartedReceipt) Line() string {
	return fmt.Sprintf("LAUNCH STARTED card=%s attempt=%s job=%s lease=%s bench=%s route=%s generation=%d worker=%s at=%s",
		oneline.Field(r.Card),
		oneline.Field(r.Attempt),
		oneline.Field(r.Job),
		oneline.Field(r.Lease),
		oneline.Field(r.Bench),
		oneline.Field(r.Route),
		r.Generation,
		oneline.Field(r.Worker),
		oneline.Field(stamp(r.At)),
	)
}

// ParseStarted parses the typed LAUNCH STARTED line. Any other shape is malformed.
func ParseStarted(line string) (StartedReceipt, error) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 11 || fields[0] != "LAUNCH" || fields[1] != "STARTED" {
		return StartedReceipt{}, fmt.Errorf("%w: not a typed LAUNCH STARTED acknowledgement", ErrMalformed)
	}
	kv := make(map[string]string, len(fields)-2)
	for _, f := range fields[2:] {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" || v == "" {
			return StartedReceipt{}, fmt.Errorf("%w: field %q", ErrMalformed, f)
		}
		kv[k] = v
	}
	for _, k := range []string{"card", "attempt", "job", "lease", "bench", "route", "generation", "worker", "at"} {
		if kv[k] == "" {
			return StartedReceipt{}, fmt.Errorf("%w: missing %s", ErrMalformed, k)
		}
	}
	gen, err := strconv.Atoi(kv["generation"])
	if err != nil {
		return StartedReceipt{}, fmt.Errorf("%w: generation", ErrMalformed)
	}
	at, err := time.Parse(time.RFC3339, kv["at"])
	if err != nil {
		return StartedReceipt{}, fmt.Errorf("%w: at", ErrMalformed)
	}
	return StartedReceipt{
		Card:       kv["card"],
		Attempt:    kv["attempt"],
		Job:        kv["job"],
		Lease:      kv["lease"],
		Bench:      kv["bench"],
		Route:      kv["route"],
		Generation: gen,
		Worker:     kv["worker"],
		At:         at.UTC(),
	}, nil
}

func (r StartedReceipt) validate() error {
	if r.Card == "" || r.Attempt == "" || r.Job == "" || r.Lease == "" || r.Bench == "" || r.Route == "" || r.Worker == "" || r.At.IsZero() {
		return fmt.Errorf("%w: LAUNCH STARTED requires card, attempt, job, lease, bench, route, worker and at", ErrMalformed)
	}
	return nil
}

// UnknownWhy is the deadline/malformed-start record. Streams are retained
// under lifecycle/attempts/<attempt>/.
type UnknownWhy struct {
	Attempt string
	Why     string
	Stdout  []byte
	Stderr  []byte
	Exit    *int
}

func stamp(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format(time.RFC3339)
}

func validID(s string) bool {
	if s == "" || len(s) > maxIdentLen {
		return false
	}
	if s == "." || s == ".." || strings.Contains(s, "..") {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

func validWhy(why string) bool {
	switch why {
	case WhyNoStart, WhyLostReply, WhyTimeout, WhyMalformedStart:
		return true
	}
	return false
}

func strptr(s string) *string { return &s }

func intptr(n int) *int { return &n }

func boolptr(b bool) *bool { return &b }

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func cloneLimits(in *Limits) *Limits {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneString(in *string) *string {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneInt(in *int) *int {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
