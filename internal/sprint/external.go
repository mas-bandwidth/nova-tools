package sprint

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// External operands: layer 3 of the processor (docs/SPEC-ISA.md, the one wait kind;
// tla/CardISA.tla, Ext and IsaTick). DEPENDS-ON takes, beside card ids, three external
// forms: `pr <repo>#<n> merged`, `<branch> contains <sha>`
// and `after <RFC3339>`. A card that names one is admitted waiting with its operands on
// FieldExternal; the tick's resolve asks each distinct operand once a tick (tick_external.go)
// and moves the card to ready, writing FieldExternalMet, the first tick every operand holds
// and its needs have landed; the lifecycle refuses any other move of it to ready
// (externalUnmet). Today's hand-polled wait
// (a card admitted held and released by hand when a pull request merges) is that one
// operand of wait.

// FieldExternal is a primary's external operands, as DEPENDS-ON wrote them, comma separated.
const FieldExternal = "external"

// FieldExternalMet is the stamp of the tick that saw every external operand of the primary
// hold: the wait is over, and the field stays as the record.
const FieldExternalMet = "external_met"

// FieldExternalAsk is why the tick's last ask of one of the primary's operands failed;
// absent when the last ask answered.
const FieldExternalAsk = "external_ask"

var (
	externalPRRef  = regexp.MustCompile(`^((?:[A-Za-z0-9][A-Za-z0-9._-]*/)?[A-Za-z0-9][A-Za-z0-9._-]*)#([0-9]+)$`)
	externalBranch = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	externalSHA    = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
)

// parseExternal reads the same three operand forms the card lint checks. Add and lint
// tests hold the two private parsers to the same accepted and refused entries.
func parseExternal(entry string) (op swarm.DependsOperand, external bool, err error) {
	f := strings.Fields(entry)
	switch {
	case len(f) > 0 && f[0] == swarm.OperandPRMerged:
		op.Form = swarm.OperandPRMerged
	case len(f) > 0 && f[0] == swarm.OperandAfter:
		op.Form = swarm.OperandAfter
	case len(f) > 1 && f[1] == swarm.OperandContains:
		op.Form = swarm.OperandContains
	default:
		return op, false, nil
	}
	op.Text = strings.Join(f, " ")
	switch op.Form {
	case swarm.OperandPRMerged:
		if len(f) != 3 || f[2] != "merged" {
			return op, true, fmt.Errorf("%q wants three words, pr <owner/repo>#<n> merged", op.Text)
		}
		m := externalPRRef.FindStringSubmatch(f[1])
		if m == nil {
			return op, true, fmt.Errorf("%q names no pull request: %q is not <owner/repo>#<n>", op.Text, f[1])
		}
		op.Repo = m[1]
		op.N, err = strconv.Atoi(m[2])
		if err != nil || op.N < 1 {
			return op, true, fmt.Errorf("%q is not a positive pull request number", f[1])
		}
	case swarm.OperandAfter:
		if len(f) != 2 {
			return op, true, fmt.Errorf("%q wants two words, after <RFC3339>", op.Text)
		}
		if op.At, err = time.Parse(time.RFC3339, f[1]); err != nil {
			return op, true, fmt.Errorf("%q: %q is not an RFC3339 time (2026-10-06T12:00:00Z)", op.Text, f[1])
		}
	case swarm.OperandContains:
		if len(f) != 3 {
			return op, true, fmt.Errorf("%q wants three words, <branch> contains <sha>", op.Text)
		}
		if !externalBranch.MatchString(f[0]) || strings.Contains(f[0], "..") {
			return op, true, fmt.Errorf("%q: %q is not a branch name", op.Text, f[0])
		}
		if !externalSHA.MatchString(f[2]) {
			return op, true, fmt.Errorf("%q: %q is not a commit, 7 to 40 lowercase hex digits", op.Text, f[2])
		}
		op.Branch, op.SHA = f[0], f[2]
	}
	return op, true, nil
}

// ExternalWaits is the external operands a primary still waits for, each as its wait says
// it (swarm.DependsOperand.Waits): none once FieldExternalMet is written.
func ExternalWaits(c *Card) []string {
	if c == nil || c.F(FieldExternalMet) != "" {
		return nil
	}
	var out []string
	for _, e := range Split(c.F(FieldExternal)) {
		op, _, _ := parseExternal(e) // ignored: add stored it parsed
		out = append(out, op.Waits())
	}
	return out
}

// IsExternalEntry says a needs entry opens like an external operand: a well-formed one or
// one add refuses (ExternalOf), never a card id.
func IsExternalEntry(entry string) bool {
	_, external, _ := parseExternal(entry)
	return external
}

// ExternalOf is the external operands of one card: the external entries of its needs, then
// those of its brief's `Needs:` line, else its DEPENDS-ON: line (the line add reads its
// needs from, whose `pr` form the needs do not carry), each once, in that order. why names
// every entry that opens like an operand and is not one, with the forms.
func ExternalOf(needs []string, brief string) (ops []string, why string) {
	value, found := "", false
	for _, line := range strings.Split(brief, "\n") {
		if key, v, ok := cardhdr.KeyValue(line); ok && key == "Needs" {
			value, found = v, true
			break
		}
	}
	if !found {
		value, _ = swarm.CardHeaderValue([]byte(brief), "DEPENDS-ON")
	}
	var bad []string
	for _, e := range append(append([]string(nil), needs...), Split(value)...) {
		if cut, _, ok := strings.Cut(e, "("); ok {
			e = cut
		}
		op, external, err := parseExternal(e)
		switch {
		case !external:
		case err != nil:
			if !contains(bad, err.Error()) {
				bad = append(bad, err.Error())
			}
		case !contains(ops, op.Text):
			ops = append(ops, op.Text)
		}
	}
	if len(bad) > 0 {
		why = "DEPENDS-ON: " + strings.Join(bad, "; ") + "; an external operand is " + swarm.DependsOperandForms
	}
	return ops, why
}

// ExternalAsk answers whether one external operand holds now: a pull request merged, or a
// branch that contains a commit. repo is the card's REPO: line, the repository a `contains`
// operand and a `pr` operand with no owner are read in. `after` is the tick's clock and is
// never asked.
type ExternalAsk func(ctx context.Context, op swarm.DependsOperand, repo string) (bool, error)

// ExternalAnswers is one tick's answers to the external operands: each operand asked at
// most once however many cards wait on it and however many times the tick's parts plan
// (tla/CardISA.tla, IsaTick and answer). The binding makes one a tick; nil asks nothing,
// and only `after` operands can hold.
type ExternalAnswers struct {
	ask ExternalAsk
	ctx context.Context
	mu  sync.Mutex
	got map[string]externalAnswer
}

type externalAnswer struct {
	holds bool
	err   error
}

// NewExternalAnswers is a tick's answers, asked through ask.
func NewExternalAnswers(ctx context.Context, ask ExternalAsk) *ExternalAnswers {
	return &ExternalAnswers{ask: ask, ctx: ctx, got: map[string]externalAnswer{}}
}

// Asked is how many operands were asked this tick.
func (a *ExternalAnswers) Asked() int {
	if a == nil {
		return 0
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.got)
}

// operandKey is the one name of an operand across the cards of a tick: its text, and for
// an operand read in the card's repository, that repository too. The key is the operand's
// own: one card's answer is never read under another's (MCCardISABrokenOneKey).
func operandKey(op swarm.DependsOperand, repo string) string {
	if op.Form == swarm.OperandPRMerged && strings.Contains(op.Repo, "/") {
		return op.Text
	}
	return repo + " " + op.Text
}

// Holds says the operand holds at now: `after` by the clock, the others by the tick's one
// ask of each.
func (a *ExternalAnswers) Holds(op swarm.DependsOperand, repo string, now time.Time) (bool, error) {
	if op.Form == swarm.OperandAfter {
		return !now.Before(op.At), nil
	}
	if a == nil || a.ask == nil {
		return false, fmt.Errorf("no external source is set for this tick, so %s is not asked", op.Waits())
	}
	key := operandKey(op, repo)
	a.mu.Lock()
	defer a.mu.Unlock()
	if got, ok := a.got[key]; ok {
		return got.holds, got.err
	}
	holds, err := a.ask(a.ctx, op, repo)
	a.got[key] = externalAnswer{holds, err}
	return holds, err
}

// GHAsk is the tree's ExternalAsk: one `gh api` call per operand. A pull request has merged
// when its merged_at is set; a branch contains a commit when the comparison of the branch to
// the commit says the commit is behind it or identical to it.
func GHAsk(ctx context.Context, op swarm.DependsOperand, repo string) (bool, error) {
	var path, query string
	switch op.Form {
	case swarm.OperandPRMerged:
		r := op.Repo
		if !strings.Contains(r, "/") {
			owner, _, ok := strings.Cut(repo, "/")
			if !ok {
				return false, fmt.Errorf("%s names no owner and the card's REPO: line %q names none", op.Text, repo)
			}
			r = owner + "/" + r
		}
		path, query = fmt.Sprintf("repos/%s/pulls/%d", r, op.N), ".merged_at // \"\""
	case swarm.OperandContains:
		if !strings.Contains(repo, "/") {
			return false, fmt.Errorf("%s is read in the card's repository, and its REPO: line %q is not <owner>/<repo>", op.Text, repo)
		}
		path, query = fmt.Sprintf("repos/%s/compare/%s...%s", repo, op.Branch, op.SHA), ".status"
	default:
		return false, fmt.Errorf("%s is not asked of gh", op.Text)
	}
	var out, errs bytes.Buffer
	cmd, cancel := subproc.CommandFor(ctx, subproc.GHBudget, "gh", "api", path, "--jq", query)
	defer cancel()
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("gh api %s: %w: %s", path, err, strings.TrimSpace(errs.String()))
	}
	word := strings.TrimSpace(out.String())
	if op.Form == swarm.OperandPRMerged {
		return word != "" && word != "null", nil
	}
	return word == "behind" || word == "identical", nil
}
