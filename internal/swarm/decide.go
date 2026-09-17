package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// TRIAGE --decide: one typed abstain reason per finished task, behind a floor.
//
// For each finished task triage builds a state (the harness error line first
// if any, then the usage row, then the last 30 lines of harness.log, secrets
// redacted) and asks TypeSafe Jev two questions: reason, a choice among
// {provider_error, wall, prompt_defect, done, budget}, and needs_human, a
// noul. The TRIAGE REPORT line gains
// ' decide=<reason> conf=<c> needs_human=<p> floor=<f>', and one JSON line per
// task lands in <pool>/decisions.log beside the outcome (rule 8).
//
// A decision below the floor is a suggestion, never an authorization: the
// line says decide=? and the pool's own class stands. A Decide call that
// errors keeps today's behaviour as the fallback: the bare line, no log.

const (
	// DecisionsFile is the per-task decision log beside the pool's outcome.
	DecisionsFile = "decisions.log"
	// DefaultDecideFloor is the confidence floor triage --decide starts at.
	DefaultDecideFloor = 0.9
)

// decideReasons is the typed abstain vocabulary triage --decide asks for.
var decideReasons = map[string]string{
	"provider_error": "the provider failed the run: an error, a refusal, a 429, a timeout",
	"wall":           "the sandbox wall stopped the run: a refused read or write outside the job directory",
	"prompt_defect":  "the task or template sent the worker the wrong way: a plan where work was owed",
	"done":           "the run finished its work: findings written, head closed",
	"budget":         "the run ended at its file or token budget, or at its deadline",
}

// decideQuestions is the one typed decision per finished task.
func decideQuestions() map[string]decide.Question {
	return map[string]decide.Question{
		"reason": {
			Instructions: "Why did this finished swarm task end this way? Answer with the reason whose description fits the harness log and usage row.",
			Choice:       decideReasons,
		},
		"needs_human": {
			Instructions: "Does this task's ending need a person to look at it? Null when the record speaks for itself.",
			Noul:         true,
		},
	}
}

// errorMarks are the substrings that mark a harness log line as the error
// line triage --decide puts first in the state.
var errorMarks = []string{"error", "refused", "denied", "429", "timeout"}

var (
	skPattern     = regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`)
	apiKeyPattern = regexp.MustCompile(`API_KEY=[^\s"']*`)
)

// redactSecrets scrubs bearer-shaped secrets from a state before it is sent:
// sk-... tokens and API_KEY=... assignments. The Jev key itself travels only
// on the Authorization header, never in the body.
func redactSecrets(s string) string {
	s = skPattern.ReplaceAllString(s, "sk-REDACTED")
	return apiKeyPattern.ReplaceAllString(s, "API_KEY=REDACTED")
}

// harnessErrorLine is the last line of the log carrying an error mark, or
// "". The match is case-insensitive so Error, ERROR and error all count.
func harnessErrorLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		lower := strings.ToLower(lines[i])
		for _, mark := range errorMarks {
			if strings.Contains(lower, mark) {
				return lines[i]
			}
		}
	}
	return ""
}

// buildDecideState is the state one Decide call judges: the harness error
// line first if any, then the usage row, then the last 30 lines of
// harness.log, secrets redacted.
func buildDecideState(p *Pool, sc Sidecar) string {
	var logLines []string
	if sc.Job != "" {
		if raw, err := readRegular(filepath.Join(sc.Job, "harness.log")); err == nil {
			body := strings.TrimRight(string(raw), "\n")
			if body != "" {
				logLines = strings.Split(body, "\n")
			}
		}
	}
	row := "-"
	if raw, err := readRegular(p.UsagePath(sc.ID)); err == nil {
		trimmed := strings.TrimRight(string(raw), "\n")
		if trimmed != "" {
			lines := strings.Split(trimmed, "\n")
			row = lines[len(lines)-1]
		}
	}
	var parts []string
	if errLine := harnessErrorLine(logLines); errLine != "" {
		parts = append(parts, errLine)
	}
	parts = append(parts, row)
	if len(logLines) > 30 {
		logLines = logLines[len(logLines)-30:]
	}
	parts = append(parts, logLines...)
	return redactSecrets(strings.Join(parts, "\n"))
}

// decisionEntry is one line of <pool>/decisions.log: the task, the pool's own
// class, the decision taken, and the time, so the suggestion sits beside the
// outcome it abstains from overriding (rule 8).
type decisionEntry struct {
	Task       string  `json:"task"`
	Attempt    int     `json:"attempt"`
	Class      string  `json:"class"`
	Decision   string  `json:"decision"`
	Confidence float64 `json:"confidence"`
	NeedsHuman float64 `json:"needs_human"`
	Floor      float64 `json:"floor"`
	Time       string  `json:"time"`
}

// decideAttempt is the attempt this decision belongs to, for the
// idempotency key (task, attempt): the REV beside the retained report first,
// then the usage row's attempt column, then the sidecar's own count.
func decideAttempt(p *Pool, sc Sidecar) int {
	if n, _, err := p.ReadRev(sc.ID); err == nil && n > 0 {
		return n
	}
	if raw, err := readRegular(p.UsagePath(sc.ID)); err == nil {
		lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
		if len(lines) >= 2 {
			head := strings.Split(lines[0], "\t")
			values := strings.Split(lines[1], "\t")
			for i, name := range head {
				if name == "attempt" && i < len(values) {
					var n int
					if _, err := fmt.Sscanf(strings.TrimSpace(values[i]), "%d", &n); err == nil && n > 0 {
						return n
					}
				}
			}
		}
	}
	if sc.Requeued > 0 {
		return 2
	}
	return 1
}

// taskDecider carries one triage run's Decide call, floor and the set of
// (task, attempt) pairs already logged, so a second run adds nothing.
type taskDecider struct {
	do    decideFunc
	floor float64
	pool  *Pool
	now   func() time.Time
	seen  map[string]bool
}

// decideFunc is one typed decision: the seam the httptest fake in tests and
// the Jev client in production both satisfy, so no test ever calls a
// provider over the network.
type decideFunc func(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error)

func newTaskDecider(p *Pool, do decideFunc, floor float64, now func() time.Time) *taskDecider {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	d := &taskDecider{do: do, floor: floor, pool: p, now: now, seen: map[string]bool{}}
	raw, err := readRegular(p.Path(DecisionsFile))
	if err != nil {
		return d
	}
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		var e decisionEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		d.seen[fmt.Sprintf("%s\x00%d", e.Task, e.Attempt)] = true
	}
	return d
}

// decideOne asks the one typed decision for a finished task and renders the
// suffix for its TRIAGE REPORT line. ok is false when the provider errored or
// answered without a reason: the caller then keeps today's bare line.
func (d *taskDecider) decideOne(sc Sidecar, class string) (suffix string, ok bool) {
	state := buildDecideState(d.pool, sc)
	answers, _, err := d.do(context.Background(), state, decideQuestions())
	if err != nil {
		return "", false
	}
	reason, ok := answers["reason"]
	if !ok || reason.Choice == "" {
		return "", false
	}
	human := 0.0
	if h, ok := answers["needs_human"]; ok {
		human = h.Noul
	}
	decision := reason.Choice
	if reason.Confidence < d.floor {
		decision = "?"
	}
	suffix = fmt.Sprintf(" decide=%s conf=%.2f needs_human=%.2f floor=%.2f",
		oneline.Field(decision), reason.Confidence, human, d.floor)
	d.log(sc, class, decision, reason.Confidence, human)
	return suffix, true
}

// log appends one JSON line per task to <pool>/decisions.log, idempotent per
// (task, attempt).
func (d *taskDecider) log(sc Sidecar, class, decision string, conf, human float64) {
	attempt := decideAttempt(d.pool, sc)
	key := fmt.Sprintf("%s\x00%d", sc.ID, attempt)
	if d.seen[key] {
		return
	}
	e := decisionEntry{
		Task: sc.ID, Attempt: attempt, Class: class, Decision: decision,
		Confidence: conf, NeedsHuman: human, Floor: d.floor, Time: Stamp(d.now()),
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return
	}
	f, err := os.OpenFile(d.pool.Path(DecisionsFile), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	_, _ = f.Write(append(raw, '\n'))
	_ = f.Close()
	d.seen[key] = true
}
