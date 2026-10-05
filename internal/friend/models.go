package friend

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// A friend's models (docs/SPEC-FRIEND.md, a friend's models; the owner, 2026-10-05: "how do
// friends know which of THEIR models should be used per-tier?"). Her nova-config row maps
// each tier she serves to the model she runs it on and says what her harness can do; friend
// sync writes the row into her queue file (QueueRow) and each card's tier and model into its
// record (Task), so her daemon knows both without reaching the config store: a one-shot lane
// launches her harness with the card's model, a lane's turn and a session's delivery say it,
// nova-friend whoami prints the row, and nova-friend check names what her harness cannot run.

// QueueRow is her row as friend sync last wrote it into her queue file: the tiers she
// serves, the model per tier, her delivery mode and width, and whether her harness runs
// child agents and whether a child's model can be chosen (yes, unless no).
type QueueRow struct {
	Tiers      []string          `json:"tiers"`
	Models     map[string]string `json:"models,omitempty"`
	Mode       string            `json:"mode,omitempty"`
	Width      int               `json:"width,omitempty"`
	Children   string            `json:"children,omitempty"`
	ChildModel string            `json:"child_model,omitempty"`
}

// How a friend runs a card on its tier's model (internal/config FriendHow, the same rule).
const (
	HowLane    = "lane"    // one-shot: each card a headless process, launched with the model flag
	HowChild   = "child"   // one session whose children take a model: each card in a child on it
	HowSession = "session" // one session, children of one model or none: every card on the session's model
)

// How is how the friend of the row runs a card on its tier's model.
func (r QueueRow) How() string {
	switch {
	case r.Mode == ModeOneShot:
		return HowLane
	case r.Children != "no" && r.ChildModel != "no":
		return HowChild
	}
	return HowSession
}

// Lanes is the cards she works at once: her width, or 1 for a friend in one session with no
// children, whatever her width says.
func (r QueueRow) Lanes() int {
	if r.Mode != ModeOneShot && r.Children == "no" {
		return 1
	}
	return r.Width
}

// ModelsWord is the model per tier she serves, tier=model comma joined, "-" for a tier with
// none; "-" when she serves none.
func (r QueueRow) ModelsWord() string {
	var out []string
	for _, t := range r.Tiers {
		out = append(out, t+"="+dash(r.Models[t]))
	}
	return dash(strings.Join(out, ","))
}

// ModelFlags is the flag each harness takes a model by, on the command a one-shot lane runs
// (`opencode run --model <provider/model>`). A harness not here cannot be given a model per
// card from outside its session.
var ModelFlags = map[string]string{"opencode": "--model"}

// ModelProblem is one thing her row asks of her harness that it cannot do, or leaves
// unfilled: Refuse is true for a row her harness cannot run as written (the check fails),
// false for a warning.
type ModelProblem struct {
	Refuse bool
	Why    string
}

// ModelProblems is what is wrong between her row and her harness (empty: unknown), in
// order: a tier she serves with no model (a warning: it runs on whatever her session is set
// to), a row in one session whose cards all run on the session's model naming more than one
// model, and a one-shot row whose harness opens no lanes or takes no model flag.
func ModelProblems(r QueueRow, harness string) []ModelProblem {
	var out []ModelProblem
	for _, t := range r.Tiers {
		if r.Models[t] == "" {
			out = append(out, ModelProblem{Why: fmt.Sprintf("tier %s has no model: its cards run on whatever model her session is set to; fill it with nova-config friend set <name> --model %s=<model>", t, t)})
		}
	}
	switch r.How() {
	case HowSession:
		var models []string
		for _, t := range r.Tiers {
			if m := r.Models[t]; m != "" {
				models = append(models, m)
			}
		}
		if distinct := slices.Compact(slices.Sorted(slices.Values(models))); len(distinct) > 1 {
			out = append(out, ModelProblem{Refuse: true, Why: fmt.Sprintf("her harness runs every card on her session's model (children %s, child_model %s), and her row names %d models (%s): she can serve only the tiers of one", dash(r.Children), dash(r.ChildModel), len(distinct), strings.Join(distinct, ", "))})
		}
	case HowLane:
		if harness != "" && harness != "unknown" {
			if _, ok := ModelFlags[harness]; !ok {
				out = append(out, ModelProblem{Refuse: true, Why: fmt.Sprintf("her row runs one-shot lanes, and harness %s opens no lane on a chosen model (the harnesses that do: %s)", harness, strings.Join(slices.Sorted(maps.Keys(ModelFlags)), ", "))})
			}
		}
	}
	return out
}

// ReadQueueRow is her row from the queue file of her working directory dir; found is false
// when the file carries none (friend sync has not written it since this release).
func ReadQueueRow(dir string) (row QueueRow, found bool, err error) {
	var q Queue
	ok, err := read(filepath.Join(dir, filepath.FromSlash(QueueFile)), &q)
	if err != nil || !ok || q.Row == nil {
		return QueueRow{}, false, err
	}
	return *q.Row, true, nil
}

// WhoAmILines is what nova-friend whoami prints of her row: one WHOAMI line (her name, her
// tiers, the model per tier, how she runs a card, mode, width and lanes, her abilities, her
// directory, her delivery session), then one line per problem.
func WhoAmILines(friend, dir, session string, r QueueRow, harness string) []string {
	lines := []string{fmt.Sprintf("WHOAMI friend=%s tiers=%s models=%s how=%s mode=%s width=%d lanes=%d children=%s child_model=%s dir=%s session=%s",
		friend, dash(strings.Join(r.Tiers, ",")), r.ModelsWord(), r.How(), dash(r.Mode), r.Width, r.Lanes(), dash(r.Children), dash(r.ChildModel), dir, dash(session))}
	for _, p := range ModelProblems(r, harness) {
		word := "WARN"
		if p.Refuse {
			word = "REFUSED"
		}
		lines = append(lines, "WHOAMI "+word+" "+p.Why)
	}
	return lines
}

// RunLine is what a card's turn says first about its model: run it on that model, in a
// child when she runs children of a chosen model; "" when the card carries none.
func RunLine(c Card) string {
	if c.Model == "" {
		return ""
	}
	return fmt.Sprintf("This card runs on %s (tier %s): this lane was launched on it; your REPORT.md names it in a line Model: %s.", c.Model, dash(c.Tier), c.Model)
}

// modelKey carries in a context the model the harness is launched with for one turn.
type modelKey struct{}

// WithModel is ctx carrying the model a lane's turn launches the harness with; "" carries
// none.
func WithModel(ctx context.Context, model string) context.Context {
	if model == "" {
		return ctx
	}
	return context.WithValue(ctx, modelKey{}, model)
}

// ModelOf is the model ctx carries for a turn, "" when none.
func ModelOf(ctx context.Context) string {
	m, _ := ctx.Value(modelKey{}).(string)
	return m
}

// modelArgs is the harness's model flag and the model ctx carries, before the rest of args;
// args as they were when ctx carries none.
func modelArgs(ctx context.Context, harness string, args []string) []string {
	m, flag := ModelOf(ctx), ModelFlags[harness]
	if m == "" || flag == "" {
		return args
	}
	return append([]string{args[0], flag, m}, args[1:]...)
}
