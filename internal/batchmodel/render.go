package batchmodel

import (
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Expr is a typed TLA+ value. It has no raw-source escape hatch: observations
// and requests from Redis must be quoted and cannot become executable TLA+.
type Expr struct {
	kind   string
	text   string
	items  []Expr
	fields map[string]Expr
	keys   []Expr
	values []Expr
}

func String(v string) Expr { return Expr{kind: "string", text: v} }
func Number(v uint64) Expr { return Expr{kind: "number", text: strconv.FormatUint(v, 10)} }
func Boolean(v bool) Expr {
	if v {
		return Expr{kind: "bool", text: "TRUE"}
	}
	return Expr{kind: "bool", text: "FALSE"}
}

// modelSymbol names one fixed operator exported by BatchMemberTable. It is
// deliberately private; callers cannot inject an arbitrary TLA expression.
func modelSymbol(name string) Expr  { return Expr{kind: "symbol", text: name} }
func Tuple(v ...Expr) Expr          { return Expr{kind: "tuple", items: append([]Expr(nil), v...)} }
func Set(v ...Expr) Expr            { return Expr{kind: "set", items: append([]Expr(nil), v...)} }
func Record(v map[string]Expr) Expr { return Expr{kind: "record", fields: v} }
func Function(keys, values []Expr) Expr {
	return Expr{kind: "function", keys: append([]Expr(nil), keys...), values: append([]Expr(nil), values...)}
}

var tlaField = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// TLA returns a safely rendered TLA+ expression.
func (e Expr) TLA() (string, error) {
	switch e.kind {
	case "string":
		return tlaQuote(e.text)
	case "number", "bool":
		return e.text, nil
	case "symbol":
		switch e.text {
		case "NoValue", "NoScore", "NoPlace":
			return e.text, nil
		}
		return "", fmt.Errorf("unknown model symbol %q", e.text)
	case "tuple", "set":
		parts := make([]string, len(e.items))
		for i, v := range e.items {
			var err error
			parts[i], err = v.TLA()
			if err != nil {
				return "", err
			}
		}
		if e.kind == "tuple" {
			return "<<" + strings.Join(parts, ",") + ">>", nil
		}
		sort.Strings(parts)
		return "{" + strings.Join(parts, ",") + "}", nil
	case "record":
		if len(e.fields) == 0 {
			return "", errors.New("empty TLA record")
		}
		keys := make([]string, 0, len(e.fields))
		for k := range e.fields {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			if !tlaField.MatchString(k) {
				return "", fmt.Errorf("invalid TLA record field %q", k)
			}
			v, err := e.fields[k].TLA()
			if err != nil {
				return "", err
			}
			parts[i] = k + " |-> " + v
		}
		return "[" + strings.Join(parts, ",") + "]", nil
	case "function":
		if len(e.keys) != len(e.values) {
			return "", errors.New("TLA function key/value count differs")
		}
		if len(e.keys) == 0 {
			return "[x \\in {} |-> x]", nil
		}
		parts := make([]string, len(e.keys))
		for i := range e.keys {
			k, err := e.keys[i].TLA()
			if err != nil {
				return "", err
			}
			v, err := e.values[i].TLA()
			if err != nil {
				return "", err
			}
			parts[i] = "(" + k + " :> " + v + ")"
		}
		sort.Strings(parts)
		return "(" + strings.Join(parts, " @@ ") + ")", nil
	default:
		return "", fmt.Errorf("invalid TLA expression kind %q", e.kind)
	}
}

// TLC's string syntax is not Go's. Restrict this finite replay vocabulary to
// printable ASCII and escape only the two characters both grammars share.
// The adapter must refuse an unrepresentable fixture rather than alter bytes.
func tlaQuote(s string) (string, error) {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c > 0x7e {
			return "", fmt.Errorf("byte 0x%02x cannot be rendered losslessly as a TLA string", c)
		}
		if c == '"' || c == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')
	return b.String(), nil
}

// ModelState is the complete observed projection in BatchReplayState order.
// Base variables must be captured independently too; omitting them would let
// a store/model disagreement in a cell or placement pass unnoticed.
type ModelState struct{ Values [18]Expr }

func (s ModelState) TLA() (string, error) { return Tuple(s.Values[:]...).TLA() }

// Cell names a model cell. An absent placement is encoded separately through
// the model's NoPlace sentinel by the adapter.
type Cell struct{ Table, Epoch, Row, Column string }

func (c Cell) expr() (Expr, error) {
	e, err := decimal(c.Epoch)
	if err != nil {
		return Expr{}, err
	}
	if c.Table == "" || c.Row == "" || c.Column == "" {
		return Expr{}, errors.New("incomplete model cell")
	}
	return Tuple(Tuple(String(c.Table), Number(e)), String(c.Row), String(c.Column)), nil
}

// MemberAction is exactly the finite entry record accepted by
// BatchMemberTable.Apply. Nil Revision sets hasRevision=FALSE; the numeric
// revision field then carries a harmless zero placeholder.
type MemberAction struct {
	ID                                          string
	Absent                                      bool
	Revision                                    *uint64
	Source, Target                              Cell
	GuardField, GuardKind                       string
	GuardValues                                 []string
	SetField, SetValue, UnsetField              string
	Score                                       uint64
	ScoreChange, Remove, RemoveSupplied, Change bool
}

// BatchAction is the request argument to the real model action Apply(q).
// ProjectedRevision is the raw request revision minus the fixture baseline.
type BatchAction struct {
	Table, Epoch, OperationID, Actor, Digest string
	Canonical                                []byte
	ProjectedRevision                        uint64
	Members                                  []MemberAction
}

func (a BatchAction) TLA() (string, error) {
	if len(a.Canonical) == 0 || a.Table == "" || a.OperationID == "" || a.Actor == "" || a.Digest == "" {
		return "", errors.New("incomplete model action")
	}
	e, err := decimal(a.Epoch)
	if err != nil {
		return "", err
	}
	if len(a.Members) == 0 || len(a.Members) > 3 {
		return "", errors.New("model action needs one to three members")
	}
	entries := make([]Expr, len(a.Members))
	seen := map[string]bool{}
	for i, m := range a.Members {
		if m.ID == "" || seen[m.ID] {
			return "", fmt.Errorf("duplicate or empty model member %q", m.ID)
		}
		seen[m.ID] = true
		src, err := m.Source.expr()
		if err != nil {
			return "", err
		}
		dst, err := m.Target.expr()
		if err != nil {
			return "", err
		}
		values := make([]Expr, len(m.GuardValues))
		for j, v := range m.GuardValues {
			values[j] = String(v)
		}
		rev := Number(0)
		hasRevision := false
		if m.Revision != nil {
			rev = Number(*m.Revision)
			hasRevision = true
		}
		setField := m.SetField
		if setField == "" {
			setField = "none"
		}
		unsetField := m.UnsetField
		if unsetField == "" {
			unsetField = "none"
		}
		guardField := m.GuardField
		if guardField == "" {
			guardField = "none"
		}
		guardKind := m.GuardKind
		if guardKind == "" {
			guardKind = "none"
		}
		setValue := modelSymbol("NoValue")
		if setField != "none" {
			setValue = String(m.SetValue)
		}
		entries[i] = Record(map[string]Expr{
			"id": String(m.ID), "absent": Boolean(m.Absent), "revision": rev, "hasRevision": Boolean(hasRevision),
			"source": src, "target": dst, "guardField": String(guardField),
			"guardKind": String(guardKind), "guardValues": Set(values...),
			"setField": String(setField), "setValue": setValue, "unsetField": String(unsetField), "score": Number(m.Score),
			"scoreChange": Boolean(m.ScoreChange), "remove": Boolean(m.Remove), "removeSupplied": Boolean(m.RemoveSupplied), "change": Boolean(m.Change),
		})
	}
	q := Record(map[string]Expr{
		"table": Tuple(String(a.Table), Number(e)), "epoch": Number(e), "revision": Number(a.ProjectedRevision),
		"id": String(a.OperationID), "actor": String(a.Actor), "members": Tuple(entries...),
		"bytes": String(hex.EncodeToString(a.Canonical)), "digest": String(a.Digest),
	})
	v, err := q.TLA()
	if err != nil {
		return "", err
	}
	return "Apply(" + v + ")", nil
}

// RenderHarness builds a linear trace over the actual model action. The first
// observation is checked at BatchReplayInit; every later one is checked after
// its named Apply(q), including refusals and identical-operation replays.
func RenderHarness(steps []Step) (string, error) {
	if len(steps) == 0 {
		return "", errors.New("empty batch trace")
	}
	var observed []string
	var cases []string
	initial, err := steps[0].BeforeModel.TLA()
	if err != nil {
		return "", fmt.Errorf("initial state: %w", err)
	}
	observed = append(observed, initial)
	for i, s := range steps {
		if s.Index != uint64(i) {
			return "", fmt.Errorf("step %d has trace index %d", i, s.Index)
		}
		if err := ValidateStep(s); err != nil {
			return "", fmt.Errorf("step %d: %w", i, err)
		}
		if i > 0 && !SameImage(steps[i-1].After.Image, s.Before.Image) {
			return "", fmt.Errorf("step %d is not continuous", i)
		}
		if i > 0 && !reflect.DeepEqual(steps[i-1].AfterModel, s.BeforeModel) {
			return "", fmt.Errorf("step %d model projection is not continuous", i)
		}
		if s.Action.Table != s.Request.Table || s.Action.Epoch != s.Request.Epoch || s.Action.OperationID != s.Request.OperationID || s.Action.Actor != s.Request.Actor || s.Action.Digest != s.Request.Digest || !SameBytes(s.Action.Canonical, s.Request.Canonical) {
			return "", fmt.Errorf("step %d model action identity differs from runtime request", i)
		}
		if len(s.Action.Members) != len(s.Request.Selected) {
			return "", fmt.Errorf("step %d model selection differs", i)
		}
		for j, id := range s.Request.Selected {
			if s.Action.Members[j].ID != id {
				return "", fmt.Errorf("step %d model member order differs", i)
			}
		}
		action, err := s.Action.TLA()
		if err != nil {
			return "", fmt.Errorf("step %d action: %w", i, err)
		}
		state, err := s.AfterModel.TLA()
		if err != nil {
			return "", fmt.Errorf("step %d state: %w", i, err)
		}
		observed = append(observed, state)
		prefix := "  [] "
		if i == 0 {
			prefix = "CASE "
		}
		cases = append(cases, prefix+"step="+strconv.Itoa(i)+" -> "+action)
	}
	cases = append(cases, "  [] OTHER -> UNCHANGED bvars")
	return "---------------- MODULE BatchReceiptReplay ----------------\n" +
		"EXTENDS MCBatchMemberTable, TLC, Sequences\n" +
		"Observed == <<\n" + strings.Join(observed, ",\n") + "\n>>\n" +
		"ActualState == BatchReplayState\n" +
		"MatchesExecution == ActualState=Observed[step+1]\n" +
		"ReplayNext ==\n" + strings.Join(cases, "\n") + "\n" +
		"ReplaySpec == BatchReplayInit /\\ [][ReplayNext]_bvars\n" +
		"=================================================================\n", nil
}

func SameBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// RenderConfig keeps the model's exact finite constants and invariants while
// replacing only its specification and step bound. The caller hashes the
// source/config and runs TLC with this generated harness.
func RenderConfig(modelConfig string, steps int) (string, error) {
	if steps < 1 || strings.Count(modelConfig, "SPECIFICATION BatchSpec") != 1 || strings.Count(modelConfig, "MaxSteps = 3") != 1 {
		return "", errors.New("unexpected batch model config shape")
	}
	cfg := strings.Replace(modelConfig, "SPECIFICATION BatchSpec", "SPECIFICATION ReplaySpec", 1)
	cfg = strings.Replace(cfg, "MaxSteps = 3", "MaxSteps = "+strconv.Itoa(steps), 1)
	return cfg + "\nINVARIANT MatchesExecution\n", nil
}
