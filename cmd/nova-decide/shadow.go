package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/rogpeppe/go-internal/lockedfile"
)

const shadowStateLimit = 256 << 10

// shadowInput is the label-free feed of SPEC-NOVA-DECIDE section 15. An op
// names historical evidence already recorded; new work uses the full tuple.
type shadowInput struct {
	Task          string `json:"task"`
	Head          string `json:"head"`
	PromptVersion string `json:"prompt_version"`
	Op            string `json:"op,omitempty"`
	Card          string `json:"card,omitempty"`
	Diff          string `json:"diff,omitempty"`
	Rule          string `json:"rule,omitempty"`
	Schema        string `json:"schema,omitempty"`
	State         string `json:"state,omitempty"`
}

type shadowWork struct {
	shadowInput
	ID          string
	SchemaValue decide.Schema
	StateValue  string
	Inputs      map[string]string
}

type shadowRow struct {
	Task          string `json:"task"`
	Head          string `json:"head"`
	PromptVersion string `json:"prompt_version"`
	Schema        string `json:"schema"`
	StateHash     string `json:"state_sha256"`
	Backend       string `json:"backend"`
	Stage         string `json:"stage"`
	At            string `json:"at"`
	ElapsedNS     *int64 `json:"elapsed_ns,omitempty"`
	RawResponse   string `json:"raw_response,omitempty"`
	Error         string `json:"error,omitempty"`
	Imported      bool   `json:"imported,omitempty"`
}

type shadowJournal struct {
	Budget int                  `json:"budget"`
	Rows   map[string]shadowRow `json:"rows"`
}

type shadowTruth struct {
	Task          string `json:"task"`
	Head          string `json:"head"`
	PromptVersion string `json:"prompt_version"`
	Label         string `json:"label"`
	Note          string `json:"note,omitempty"`
}

func shadowVerb(w world) tool.Verb {
	return tool.Verb{
		Name: "shadow", Usage: "shadow --manifest <file> --backend <jev|fixed> [--answers <file>] --record <file> [--budget <n>] [--max <n>] [--truth <file>] [--timeout <d>] [--dry-run]",
		Example: "shadow --manifest " + fixture + "shadow.json --backend fixed --answers " + fixture + "read-answers.json --record ./decisions.jsonl --budget 100 --max 1",
		Effect:  tool.Delivery + "; asks unseen evidence and records it locally, with no authority action",
		Detail:  "A label-free JSON array names task, full head, prompt_version and card/diff (or schema/state) files. New calls consume --budget across restarts. --max bounds calls now. A reserved request without a saved response stays uncertain and is never called again. --truth is read after decisions only, and attaches outcomes. Repeating the same line resumes from the record and its .shadow.json journal.",
		DryRun:  true,
		Flags: func(f *tool.Flags) {
			f.Required("manifest", "the label-free evidence manifest, a JSON file")
			w.asking(f)
			f.Int("budget", 100, "the total provider-call allowance, including existing record decisions (1 to 1000)")
			f.Int("max", 1, "the most new calls in this invocation (1 to 100)")
			f.String("truth", "", "the separate outcome manifest, read only after asking finishes")
			f.Check(func(c *tool.Call) {
				if c.Int("budget") < 1 || c.Int("budget") > 1000 {
					c.Problem("--budget wants an allowance from 1 to 1000")
				}
				if c.Int("max") < 1 || c.Int("max") > 100 {
					c.Problem("--max wants a call limit from 1 to 100")
				}
			})
		}, Run: w.shadow,
	}
}

// shadowJSON bounds input before decoding and refuses fields outside its type;
// a truth field in an evidence row cannot silently become part of a request.
func shadowJSON(path string, dst any, limits ...int64) error {
	limit := int64(1 << 20)
	if len(limits) > 0 {
		limit = limits[0]
	}
	raw, err := shadowFile(path, limit)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(dst); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("%s: wants one JSON value", path)
	}
	return nil
}

func shadowFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err == nil && int64(len(raw)) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes; prepare a bounded evidence file", path, limit)
	}
	return raw, err
}

func shadowID(task, head, version string) string {
	raw, _ := json.Marshal([]string{task, head, version}) // strings always marshal
	return "shadow-" + decide.Sum(raw)
}

var shadowHead = regexp.MustCompile(`^[0-9a-f]{40}$`)

// shadowWorks seals every input before any ask (SPEC-NOVA-DECIDE section 15).
func shadowWorks(path string) ([]shadowWork, error) {
	var rows []shadowInput
	if err := shadowJSON(path, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 || len(rows) > 100 {
		return nil, fmt.Errorf("%s wants 1 to 100 evidence rows", path)
	}
	tuples, ops := map[string]bool{}, map[string]bool{}
	out := make([]shadowWork, 0, len(rows))
	for _, r := range rows {
		if strings.TrimSpace(r.Task) == "" || len(r.Task) > 512 || strings.TrimSpace(r.PromptVersion) == "" || len(r.PromptVersion) > 128 || len(r.Op) > 512 || !shadowHead.MatchString(r.Head) {
			return nil, fmt.Errorf("%s: each row wants task (at most 512 bytes), full lowercase 40-character head, prompt_version (at most 128 bytes) and op (at most 512 bytes)", path)
		}
		tuple := shadowID(r.Task, r.Head, r.PromptVersion)
		id := tuple
		if r.Op != "" {
			id = r.Op
		}
		if tuples[tuple] || ops[id] {
			return nil, fmt.Errorf("%s: duplicate task/head/prompt_version or op %s; keep one evidence row", path, id)
		}
		tuples[tuple], ops[id] = true, true
		v := shadowWork{shadowInput: r, ID: id, Inputs: map[string]string{"task": r.Task, "head": r.Head, "prompt_version": r.PromptVersion}}
		texts := map[string]string{}
		names := []string{"card", "diff", "rule"}
		paths := map[string]string{"card": r.Card, "diff": r.Diff, "rule": r.Rule, "schema": r.Schema, "state": r.State}
		if r.Schema != "" || r.State != "" {
			if r.Schema == "" || r.State == "" || r.Card != "" || r.Diff != "" || r.Rule != "" {
				return nil, fmt.Errorf("%s: %s wants schema/state together or card/diff together", path, r.Task)
			}
			names = []string{"schema", "state"}
		} else if r.Card == "" || r.Diff == "" {
			return nil, fmt.Errorf("%s: %s wants card and diff files", path, r.Task)
		}
		for _, name := range names {
			p := paths[name]
			if p == "" {
				continue
			}
			if !filepath.IsAbs(p) {
				p = filepath.Join(filepath.Dir(path), p)
			}
			raw, err := shadowFile(p, shadowStateLimit)
			if err != nil {
				return nil, err
			}
			texts[name] = string(raw)
			v.Inputs[name], v.Inputs[name+"_sha256"] = p, decide.Sum(raw)
		}
		v.SchemaValue = decide.ReadSchema()
		v.StateValue = decide.ReadState(texts["card"], texts["diff"], texts["rule"])
		if r.Schema != "" {
			var err error
			v.SchemaValue, err = decide.ParseSchema([]byte(texts["schema"]))
			if err != nil {
				return nil, err
			}
			v.StateValue = texts["state"]
		}
		if len(v.StateValue) > shadowStateLimit {
			return nil, fmt.Errorf("%s: %s state exceeds %d bytes; prepare a bounded input", path, r.Task, shadowStateLimit)
		}
		out = append(out, v)
	}
	return out, nil
}

func (j shadowJournal) save(path string) error {
	raw, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Clean(path), append(raw, '\n'), 0600)
}

// shadowSpent includes both completed decisions and reservations that have no
// decision: a timeout or interrupted request cannot free a paid call's slot.
func shadowSpent(ds []decide.Decision, j shadowJournal) int {
	spent := len(ds)
	for id, r := range j.Rows {
		if !r.Imported && decide.Find(ds, id) == nil {
			spent++
		}
	}
	return spent
}

func shadowMatch(v shadowWork, r shadowRow, b decide.Backend) bool {
	return r.Task == v.Task && r.Head == v.Head && r.PromptVersion == v.PromptVersion && r.Schema == v.SchemaValue.Hash() && r.StateHash == decide.Sum([]byte(v.StateValue)) && r.Backend == b.Name()
}

// shadow serializes Reserve through Record across invocations under the sibling
// lock. The decision record remains the existing decide format; Label happens
// only after the whole ask loop (DecideShadow.tla, TruthAfterDecision).
func (w world) shadow(c *tool.Call) *tool.Out {
	works, err := shadowWorks(c.Str("manifest"))
	if err != nil {
		return tool.Refuse(err.Error())
	}
	b, err := w.backend(c)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	record := c.Str("record")
	jp := record + ".shadow.json"
	if c.DryRun() {
		return w.shadowRun(c, b, works, record, jp, true)
	}
	lock, err := lockedfile.OpenFile(record+".shadow.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	out := w.shadowRun(c, b, works, record, jp, false)
	if err = lock.Close(); err != nil {
		return tool.Refuse(err.Error())
	}
	return out
}

func (w world) shadowRun(c *tool.Call, b decide.Backend, works []shadowWork, record, jp string, dry bool) *tool.Out {
	j := shadowJournal{Budget: c.Int("budget"), Rows: map[string]shadowRow{}}
	if err := shadowJSON(jp, &j, 128<<20); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return tool.Refuse(err.Error())
	}
	if j.Budget != c.Int("budget") || j.Rows == nil {
		return tool.Refuse("the shadow journal's budget differs or its rows are absent; use the original --budget and repair the journal from its evidence")
	}
	ds, err := decide.Load(record)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	// Validate every replay before spending on any new row.
	for _, v := range works {
		for id, r := range j.Rows {
			if id != v.ID && r.Task == v.Task && r.Head == v.Head && r.PromptVersion == v.PromptVersion {
				return tool.Refuse("shadow tuple already recorded as " + id + "; retain its original op identity")
			}
		}
		if r, ok := j.Rows[v.ID]; ok && !shadowMatch(v, r, b) {
			return tool.Refuse("shadow input changed for " + v.ID + "; retain its sealed evidence or name a new prompt_version")
		}
		if d := decide.Find(ds, v.ID); d != nil {
			if err := decide.Replays(*d, v.SchemaValue, v.StateValue); err != nil {
				return tool.Refuse(err.Error())
			}
			if d.Backend != b.Name() {
				return tool.Refuse("recorded backend differs for " + v.ID + "; select its original backend")
			}
		} else if v.Op != "" {
			return tool.Refuse("--manifest op " + v.Op + " names no existing decision; omit op for new work's tuple identity")
		}
	}
	spent := shadowSpent(ds, j)
	if spent > j.Budget {
		return tool.Refuse(fmt.Sprintf("record and reservations hold %d calls, above budget %d; reconcile the allowance before asking", spent, j.Budget))
	}
	o := tool.Done().Fact("backend", b.Name()).Fact("rows", len(works)).Fact("authority", "none")
	asked, failed, pending := 0, 0, 0
	for _, v := range works {
		if d := decide.Find(ds, v.ID); d != nil {
			o.Item("decision", "task", v.Task, "head", v.Head, "id", v.ID, "recorded", "existing")
			if !dry {
				r := j.Rows[v.ID]
				if r.Stage == "" {
					r = shadowRow{Task: v.Task, Head: v.Head, PromptVersion: v.PromptVersion, Schema: d.Schema, StateHash: decide.Sum([]byte(v.StateValue)), Backend: d.Backend, At: d.At, Imported: true}
				}
				r.Stage = "recorded"
				j.Rows[v.ID] = r
				if err := j.save(jp); err != nil {
					return tool.Refuse(err.Error())
				}
			}
			continue
		}
		r, held := j.Rows[v.ID]
		if dry {
			o.Item("decision", "task", v.Task, "head", v.Head, "id", v.ID, "recorded", map[bool]string{true: "reserved", false: "no"}[held])
			continue
		}
		if held && r.Stage != "response" {
			failed++
			o.Item("decision", "task", v.Task, "head", v.Head, "id", v.ID, "recorded", r.Stage, "error", tool.Text(r.Error))
			continue
		}
		if !held {
			if asked >= c.Int("max") || spent >= j.Budget {
				pending++
				o.Item("decision", "task", v.Task, "head", v.Head, "id", v.ID, "recorded", "pending")
				continue
			}
			r = shadowRow{Task: v.Task, Head: v.Head, PromptVersion: v.PromptVersion, Schema: v.SchemaValue.Hash(), StateHash: decide.Sum([]byte(v.StateValue)), Backend: b.Name(), Stage: "reserved", At: w.now().UTC().Format(time.RFC3339Nano)}
			j.Rows[v.ID] = r
			if err := j.save(jp); err != nil {
				return tool.Refuse(err.Error())
			}
			spent++
			asked++
		}
		made, err := w.shadowMake(c, b, v, r, &j, jp, record, held)
		if err != nil {
			failed++
			o.Item("decision", "task", v.Task, "head", v.Head, "id", v.ID, "recorded", j.Rows[v.ID].Stage, "error", tool.Text(err.Error()))
			continue
		}
		ds = append(ds, made)
		o.Item("decision", "task", v.Task, "head", v.Head, "id", v.ID, "recorded", "new", "tokens_in", made.Usage.InputTokens, "tokens_out", made.Usage.OutputTokens)
	}
	o.Fact("asked", asked).Fact("spent", spent).Fact("remaining", j.Budget-spent).Fact("pending", pending).Fact("failed", failed)
	if !dry && c.Given("truth") {
		if err := shadowLabels(c.Str("truth"), works, record, w.now()); err != nil {
			return tool.Refuse(err.Error())
		}
	}
	if failed > 0 {
		o.Status, o.Exit, o.Why = tool.Failed, 2, []string{"shadow requests failed or remain uncertain; their reservations are retained and will not call the provider again"}
		o.Remedy = "inspect the shadow journal and reconcile uncertain requests against provider evidence; rerun to finish saved responses and new ready rows"
	}
	return o
}

// shadowMake saves the raw response before decode/append. A crash after Response
// replays those bytes through the existing Jev decoder, not through the network.
func (w world) shadowMake(c *tool.Call, b decide.Backend, v shadowWork, r shadowRow, j *shadowJournal, jp, record string, replay bool) (decide.Decision, error) {
	actual := b
	if jev, ok := b.(decide.Jev); ok {
		send := jev.Send
		jev.Send = func(ctx context.Context, body []byte) ([]byte, error) {
			if replay {
				return []byte(r.RawResponse), nil
			}
			start := w.now()
			raw, err := send(ctx, body)
			elapsed := w.now().Sub(start).Nanoseconds()
			r.ElapsedNS = &elapsed
			r.RawResponse = string(raw)
			r.Stage = "response"
			if len(raw) > 64<<10 {
				r.RawResponse = ""
				err = fmt.Errorf("the shadow response exceeds 65536 bytes (sha256 %s); inspect provider evidence before any retry", decide.Sum(raw))
			}
			if err != nil {
				r.Stage = "failed"
				r.Error = err.Error()
			}
			j.Rows[v.ID] = r
			if saveErr := j.save(jp); saveErr != nil {
				return nil, saveErr
			}
			return raw, err
		}
		actual = jev
	} else if replay {
		return decide.Decision{}, fmt.Errorf("%s: a fixed reservation has no saved provider response; inspect its record", v.ID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.Dur("timeout"))
	defer cancel()
	d, _, err := decide.Make(ctx, actual, v.SchemaValue, v.StateValue, record, v.ID, v.Inputs, w.now())
	if err != nil {
		var backendErr *decide.BackendError
		if errors.As(err, &backendErr) {
			r = j.Rows[v.ID]
			r.Stage = "failed"
			r.Error = err.Error()
			j.Rows[v.ID] = r
			if e := j.save(jp); e != nil {
				return d, e
			}
		}
		return d, err
	}
	r = j.Rows[v.ID]
	r.Stage = "recorded"
	j.Rows[v.ID] = r
	return d, j.save(jp)
}

// shadowLabels reads truth only after asking; label bytes never enter a state.
func shadowLabels(path string, works []shadowWork, record string, at time.Time) error {
	var labels []shadowTruth
	if err := shadowJSON(path, &labels); err != nil {
		return err
	}
	if len(labels) > 100 {
		return fmt.Errorf("%s wants at most 100 truth rows", path)
	}
	ids := map[string]string{}
	for _, v := range works {
		ids[shadowID(v.Task, v.Head, v.PromptVersion)] = v.ID
	}
	seen := map[string]bool{}
	ds, err := decide.Load(record)
	if err != nil {
		return err
	}
	for _, l := range labels {
		tuple := shadowID(l.Task, l.Head, l.PromptVersion)
		id, ok := ids[tuple]
		if !ok || seen[tuple] || l.Label == "" || strings.ContainsAny(l.Label, " \t\r\n,=") {
			return fmt.Errorf("%s wants unique manifest tuples and one-word labels", path)
		}
		seen[tuple] = true
		if d := decide.Find(ds, id); d != nil && d.Outcome != nil && d.Outcome.Label != l.Label {
			return fmt.Errorf("%s: outcome changed for %s; retain its recorded label", path, id)
		}
	}
	for _, l := range labels {
		id := ids[shadowID(l.Task, l.Head, l.PromptVersion)]
		if decide.Find(ds, id) == nil {
			continue
		}
		if _, _, err := decide.Attach(record, decide.Outcome{ID: id, Label: l.Label, Note: l.Note, At: at.UTC().Format(time.RFC3339)}); err != nil {
			return err
		}
	}
	return nil
}
