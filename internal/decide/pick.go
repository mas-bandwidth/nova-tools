// The friend-child pick: which model and effort a friend's child runs a task
// kind at (Jev 2026-09-25).
//
// A friend's child -- the Claude seat `nova-sprint friend serve` spawns -- runs
// one model at one effort tier. The four efforts are the Anthropic tiers,
// cheapest first: haiku, sonnet, opus, fable. The pick is chosen per task kind:
// Jev picks among the four efforts and the model id follows from the versioned
// table, never from a constant here. Any friend adopts: the table is ONE shared
// source, not one per friend, so every friend running the verb for a kind gets
// the same effort and model.
package decide

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

//go:embed pick.json
var defaultPickJSON []byte

// Efforts are the four effort tiers a friend child may run at, cheapest first.
// They are the closed set a pick decision is chosen from, and a table must name
// a model id for every one of them.
var Efforts = []string{"haiku", "sonnet", "opus", "fable"}

// EffortQuestion is the name of the one choice Jev is asked: which effort tier.
const EffortQuestion = "effort"

// Pick is one friend-child pick: the task kind, the effort tier, and the model
// id that effort maps to. The model is derived from the effort through the
// versioned table, never invented at a call site.
type Pick struct {
	Kind   string
	Effort string
	Model  string
}

// PickTable is the versioned table the pick verb reads: the version, the effort
// tiers and their model ids, and the default effort per task kind. It is DATA
// for the same reason the registry is data: the ladder of models changes by a
// file, not by a rebuild.
type PickTable struct {
	Version int               `json:"version"`
	Models  map[string]string `json:"models"`
	Picks   map[string]string `json:"picks"`
}

// ModelFor returns the model id one effort tier maps to, and whether the table
// holds one at all. An effort with no model id is a refusal, never a guess.
func (t *PickTable) ModelFor(effort string) (string, bool) {
	if t == nil {
		return "", false
	}
	m, ok := t.Models[strings.TrimSpace(effort)]
	return strings.TrimSpace(m), ok && strings.TrimSpace(m) != ""
}

// PickFor returns the table's default pick for one kind: the effort, and the
// model id derived from it through the table.
func (t *PickTable) PickFor(kind string) (Pick, bool) {
	kind = strings.TrimSpace(kind)
	if t == nil || kind == "" {
		return Pick{}, false
	}
	effort, ok := t.Picks[kind]
	if !ok {
		return Pick{}, false
	}
	model, ok := t.ModelFor(effort)
	if !ok {
		return Pick{}, false
	}
	return Pick{Kind: kind, Effort: effort, Model: model}, true
}

// DefaultPickJSON is the embedded table's own bytes, copied so a caller can
// merge into the document rather than into the parsed table.
func DefaultPickJSON() []byte {
	return append([]byte(nil), defaultPickJSON...)
}

// DefaultPick is the embedded table, the one a bench with no pick file runs on.
func DefaultPick() (*PickTable, error) {
	t, err := ParsePick(defaultPickJSON)
	if err != nil {
		return nil, fmt.Errorf("decide: the embedded pick table is broken: %w", err)
	}
	return t, nil
}

// LoadPick reads the pick table a path names. An empty path is the embedded
// default; an unreadable or invalid file is a refusal naming the path.
func LoadPick(path string) (*PickTable, error) {
	if strings.TrimSpace(path) == "" {
		return DefaultPick()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("decide: cannot read the pick table %s: %w", path, err)
	}
	t, err := ParsePick(raw)
	if err != nil {
		return nil, fmt.Errorf("decide: pick table %s: %w", path, err)
	}
	return t, nil
}

// ParsePick parses and validates a pick table. The table must carry a version,
// a model id for every one of the four efforts, and a default effort for every
// pick -- an effort the table does not hold is a refusal, never a guess.
func ParsePick(data []byte) (*PickTable, error) {
	var t PickTable
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("decide: bad pick table: not a JSON object: %w", err)
	}
	if t.Version <= 0 {
		return nil, fmt.Errorf("decide: bad pick table: no version; a versioned table with no version answers nothing")
	}
	if len(t.Models) == 0 {
		return nil, fmt.Errorf("decide: bad pick table: no models; an effort with no model id is not a pick")
	}
	for _, effort := range Efforts {
		if _, ok := t.ModelFor(effort); !ok {
			return nil, fmt.Errorf("decide: bad pick table: effort %s has no model id; the table names %s", effort, strings.Join(Efforts, ", "))
		}
	}
	if len(t.Picks) == 0 {
		return nil, fmt.Errorf("decide: bad pick table: no picks; a table with no default pick answers nothing")
	}
	for kind, effort := range t.Picks {
		if !KnownEffort(effort) {
			return nil, fmt.Errorf("decide: bad pick table: kind %s picks effort %s, which is not one of %s", kind, effort, strings.Join(Efforts, ", "))
		}
	}
	return &t, nil
}

// KnownEffort reports whether the effort is one of the four tiers.
func KnownEffort(effort string) bool {
	for _, e := range Efforts {
		if e == strings.TrimSpace(effort) {
			return true
		}
	}
	return false
}

// effortQuestion is the one choice Jev is asked for a kind: which effort tier.
// The instructions are constants, never templated with evidence.
func effortQuestion() Question {
	return Question{
		Instructions: "Answer which effort tier this task kind's friend child should run at. Choose only from the options given, and choose nothing else.",
		Choice: map[string]string{
			"haiku":  "the cheapest tier: the work is mechanical and its shape is proven",
			"sonnet": "the mid tier: the work needs judgment but not a hard problem",
			"opus":   "the high tier: the work is a hard or sensitive problem",
			"fable":  "the top tier: the work is a design that wants the coordinator's own model",
		},
	}
}

// Where a pick answer came from.
const (
	// PickSourceTable is the table's own default: deterministic, no call.
	PickSourceTable = "table"
	// PickSourceJev is the provider's choice among the four efforts.
	PickSourceJev = "jev"
)

// PickResult is one pick decision: the kind, the chosen effort and model, the
// table version, where the answer came from, and -- where the provider was
// asked -- its confidence and what the call spent. A call that was made is
// accounted for whether it answered or not: its cost travels with the result.
type PickResult struct {
	Pick
	Version       int
	Source        string
	Confidence    float64
	HasConfidence bool
	Calls         int
	Usage         Usage
	Failed        bool
}

// PickWithTable answers a pick from the table alone: deterministic, no call, no
// confidence, the version it came from beside the answer.
func PickWithTable(table *PickTable, kind string) (PickResult, error) {
	p, ok := table.PickFor(kind)
	if !ok {
		return PickResult{}, fmt.Errorf("decide: kind %s has no pick in the table; refusing to guess", kind)
	}
	return PickResult{Pick: p, Version: table.Version, Source: PickSourceTable}, nil
}

// PickJev asks Jev which effort tier a kind's friend child runs at, among the
// table's four efforts. A provider error, an absent answer, or an answer
// outside the closed set falls back to the table's default pick: the answer is
// a suggestion, never an authorization, and the call's usage travels with the
// result either way, because a call was made and it cost what it cost.
func PickJev(ctx context.Context, d Decider, table *PickTable, kind string) (PickResult, error) {
	p, ok := table.PickFor(kind)
	if !ok {
		return PickResult{}, fmt.Errorf("decide: kind %s has no pick in the table; refusing to guess", kind)
	}
	res := PickResult{Pick: p, Version: table.Version, Source: PickSourceTable}
	if d == nil {
		return res, nil
	}
	answers, usage, err := d.Decide(ctx, "task kind: "+kind+"\n", map[string]Question{EffortQuestion: effortQuestion()})
	res.Calls = 1
	res.Usage = usage
	res.Failed = err != nil
	if err != nil {
		return res, nil
	}
	a, ok := answers[EffortQuestion]
	if !ok || !KnownEffort(a.Choice) {
		return res, nil
	}
	model, ok := table.ModelFor(a.Choice)
	if !ok {
		return res, nil
	}
	res.Pick = Pick{Kind: kind, Effort: a.Choice, Model: model}
	res.Source = PickSourceJev
	res.Confidence = a.Confidence
	res.HasConfidence = true
	return res, nil
}

// Line renders one pick receipt line. A pick answered by the table carries a
// dash for confidence, never a fabricated number.
func (r PickResult) Line() string {
	conf := "-"
	if r.HasConfidence {
		conf = fmt.Sprintf("%.2f", r.Confidence)
	}
	return fmt.Sprintf("PICK kind=%s effort=%s model=%s version=%d conf=%s source=%s",
		oneline.Field(r.Kind), oneline.Field(r.Effort), oneline.Field(r.Model), r.Version, conf, oneline.Field(r.Source))
}
