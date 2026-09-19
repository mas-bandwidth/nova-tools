package pulse

// The accept gate inside the harvest (T05, #1650; SPEC-TOOLWORK §1 rule 1, §4 rules 1-5).
//
// Between rule 12's line-1 verify and its push, a done card whose kind declares a gate is
// judged by `accept`. The verdict, and nothing a worker wrote, decides what happens next:
//
//	ACCEPT OK      push and open the PR, the ACCEPT OK line first in the body; class=fixed
//	ACCEPT REJECT  push nothing; seen.tsv `rejected`; rejected=<n>; rule 14's requeue-once
//	               path with the reason token as its evidence; class=rejected
//	ACCEPT ABSTAIN push nothing, requeue nothing; one line in <root>/bench.tsv for a person,
//	               except reason=paused, which is nobody's fault and writes no row
//
// There is no --no-gate. A kind whose declared gate is `none` -- and a card with no typed
// header at all, which is every card cut before T06 -- skips the step, and its row says
// `gate=none` so a green row never claims a check that did not run.
//
// Neither verdict is ever asked of a model: `accept=ok` is class=fixed and `accept=reject`
// is class=rejected, both with conf=-, and no decide call is made (§4 rule 2).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// fullSHA is the only thing harvest hands `accept` as its base: a worker can move a ref
// in its own clone, and the gate refuses one (§1 rule 2, cold read of #1721).
var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

// gateResult is one card's verdict as the harvest reads it: the exit code says which of
// the three it is, and the ACCEPT line carries the tokens the row and the OUTCOME print.
type gateResult struct {
	verdict string // ok | reject | abstain | none
	reason  string
	at      string
	head    string // the sha12 the ACCEPT line reported
	sha     string // the job's HEAD as a full sha, resolved ONCE before the gate ran
	base    string
	control string
	cert    string
	line    string // the ACCEPT line itself, first in the PR body on an OK
}

// gateNone is the verdict of a card whose kind declares no gate.
func gateNone() gateResult {
	return gateResult{verdict: "none", reason: "-", at: "-", head: "-", base: "-", control: "-", cert: "-"}
}

// cardKindName is the card's KIND: as written, for the row and the OUTCOME line. A card
// with no typed header -- every card cut before T06 -- has none, and says so.
func cardKindName(cardPath string) string {
	h, err := ReadCardHeader(cardPath)
	if err != nil || h.Kind == "" {
		return "-"
	}
	return h.Kind
}

// short12 is a sha as the typed lines print it. A dash stays a dash.
func short12(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	if s == "" {
		return "-"
	}
	return s
}

// acceptToken reads `key=value` out of an ACCEPT line, "-" when it is not there.
func acceptToken(line, key string) string {
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, key+"="); ok && v != "" {
			return v
		}
	}
	return "-"
}

// cardKind is the card's declared kind and whether the gate runs for it. A card whose
// header cannot be read, or whose KIND: the table does not hold, is ungated here: the
// gate itself refuses an unknown kind, and a harvest that stopped on one would strand
// every card cut before T06 wrote the header.
func cardKind(cardPath string) (Kind, bool) {
	h, err := ReadCardHeader(cardPath)
	if err != nil || h.Kind == "" {
		return Kind{}, false
	}
	k, ok := KindNamed(h.Kind)
	if !ok {
		return Kind{}, false
	}
	return k, k.Gated()
}

// resolveBase is the full sha of the base in the JOB's own clone. A ref is never passed
// on: `dev` in a worker's clone is whatever the worker last set it to.
func resolveBase(dir, base string) (string, error) {
	if strings.TrimSpace(base) == "" {
		base = "dev"
	}
	if fullSHA.MatchString(strings.TrimSpace(base)) {
		return strings.TrimSpace(base), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	for _, ref := range []string{base, "origin/" + base} {
		cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--quiet", ref+"^{commit}")
		cmd.Dir = dir
		// The hooks and the fsmonitor are turned off through the ENVIRONMENT, not through
		// -c flags: this reads a worker's own clone, where a hook is the worker's, and an
		// argv a test can read is one nobody has to count -c pairs in.
		cmd.Env = gitHookOffEnv()
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		if sha := strings.TrimSpace(string(out)); fullSHA.MatchString(sha) {
			return sha, nil
		}
	}
	return "", fmt.Errorf("cannot resolve %s to a commit in %s; the gate is handed a full sha, never a ref a worker can move", base, dir)
}

// runGate runs the accept seam for one card and reads its verdict. The gate's own output
// goes to the harvest's stderr as well as being parsed, so a person running the harvest
// sees the ACCEPT line the verdict came from.
func (in HarvestInput) runGate(jobDir, cardPath, label string) gateResult {
	kind, gated := cardKind(cardPath)
	if !gated {
		return gateNone()
	}
	gate := in.Gate
	if gate == nil {
		gate = Accept
	}
	base, err := resolveBase(jobDir, in.Base)
	if err != nil {
		// Nothing about the card is known yet: this is the bench's, so it abstains and
		// the card is not touched. `toolchain` is the abstain token for could-not-run.
		fmt.Fprintf(in.Stderr, "HARVEST NOTE gate could not start label=%s: %s\n", field(label), oneline.Err(err))
		return gateResult{verdict: "abstain", reason: "toolchain", at: "-", head: "-", base: "-", control: "-", cert: "-"}
	}
	// The job's HEAD is resolved ONCE, before the gate: it is the object the gate is
	// about to judge and the object the push will name. A push of `branch:branch`
	// re-resolves the branch in the WORKER's clone at push time, so a worker that
	// commits again between the verdict and the push gets that commit published under an
	// ACCEPT OK that never saw it (the red team of 98e3f3a9, item 9).
	sha, shaErr := resolveBase(jobDir, "HEAD")
	if shaErr != nil {
		fmt.Fprintf(in.Stderr, "HARVEST NOTE gate could not start label=%s: %s\n", field(label), oneline.Err(shaErr))
		return gateResult{verdict: "abstain", reason: "toolchain", at: "-", head: "-", base: "-", control: "-", cert: "-"}
	}
	var buf bytes.Buffer
	code := gate(AcceptInput{
		Job: jobDir, Card: cardPath, Base: base,
		Bench: in.GateBench, Cert: in.Cert, Identities: in.Identities,
		Sandbox: in.Sandbox, Fixtures: in.Fixtures, Root: in.Root, Build: in.Build,
		Stdout: &buf, Stderr: &buf,
	})
	line := acceptLine(buf.String())
	if line != "" {
		fmt.Fprintln(in.Stderr, line)
	}
	r := gateResult{
		reason:  acceptToken(line, "reason"),
		at:      acceptToken(line, "at"),
		head:    acceptToken(line, "head"),
		control: acceptToken(line, "control"),
		base:    base,
		sha:     sha,
		cert:    acceptToken(line, "cert"),
		line:    line,
	}
	switch {
	case code == 0:
		r.verdict, r.reason = "ok", "-"
	case code == 1:
		r.verdict = "reject"
	default:
		r.verdict = "abstain"
		if r.reason == "-" {
			// An ACCEPT REFUSED names no token. It still could not run.
			r.reason = "toolchain"
		}
	}
	_ = kind
	return r
}

// acceptLine is the ACCEPT verdict line in the gate's output: the last one, so a
// selftest's own lines never stand in for the card's.
func acceptLine(out string) string {
	line := ""
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "ACCEPT OK") || strings.HasPrefix(t, "ACCEPT REJECT") ||
			strings.HasPrefix(t, "ACCEPT ABSTAIN") || strings.HasPrefix(t, "ACCEPT REFUSED") {
			line = t
		}
	}
	return line
}

// classForGate is §4 rule 2: what the gate decided is never asked of a model.
func classForGate(v string) (string, bool) {
	switch v {
	case "ok":
		return "fixed", true
	case "reject":
		return "rejected", true
	}
	return "", false
}

// outcome is the typed result of one card (§4 rule 1): one line in the job directory,
// written by the machinery, and one appended row beside the route log.
type outcome struct {
	Label   string `json:"label"`
	Kind    string `json:"kind"`
	Gather  string `json:"gather"`
	Accept  string `json:"accept"`
	Reason  string `json:"reason"`
	Class   string `json:"class"`
	Conf    string `json:"conf"`
	Head    string `json:"head"`
	Base    string `json:"base"`
	Bench   string `json:"bench"`
	Cert    string `json:"cert"`
	Control string `json:"control"`
	PR      string `json:"pr"`
	Took    string `json:"took"`
}

func (o outcome) line() string {
	return fmt.Sprintf("OUTCOME label=%s kind=%s gather=%s accept=%s reason=%s class=%s conf=%s head=%s base=%s bench=%s cert=%s control=%s pr=%s took=%s",
		field(o.Label), field(o.Kind), field(o.Gather), field(o.Accept), field(o.Reason),
		field(o.Class), field(o.Conf), field(o.Head), field(o.Base), field(o.Bench),
		field(o.Cert), field(o.Control), field(o.PR), field(o.Took))
}

// writeOutcome puts the one typed line in the job directory, replacing whatever was
// there: a job that ships its own OUTCOME is a stray-file in the repository (the hygiene
// list) and is not the record here either.
func writeOutcome(jobDir string, o outcome) {
	if strings.TrimSpace(jobDir) == "" {
		return
	}
	_ = os.WriteFile(filepath.Join(jobDir, "OUTCOME"), []byte(o.line()+"\n"), 0o644)
}

// appendOutcomeRow appends the same outcome to <queue>/decide/outcomes.jsonl, beside the
// route log, so the Jev lane measures agreement from rows and not from a feeling (§4
// rule 5). An unnamed queue appends nothing.
func appendOutcomeRow(queue string, o outcome) {
	if strings.TrimSpace(queue) == "" {
		return
	}
	dir := filepath.Join(queue, "decide")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "outcomes.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", raw)
}

// recordRouteOutcome tells the ladder what happened to the unit a decision routed
// (`nova-decide outcome`, dev 1e27d9e0): green for a card that landed, red for one the
// gate sent back, blocked for one nobody could finish. A unit no decision routed is the
// verb's own refusal and is a note here, never a failure of the harvest.
func recordRouteOutcome(in HarvestInput, label, verdict string) {
	queue := strings.TrimSpace(in.Queue)
	if queue == "" {
		return
	}
	log := filepath.Join(queue, "decide", "route.jsonl")
	if _, err := os.Stat(log); err != nil {
		return
	}
	result := ""
	switch verdict {
	case "ok":
		result = "green"
	case "reject":
		result = "red"
	case "abstain":
		result = "blocked"
	default:
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nova-decide", "outcome", "--log", log, "--unit-id", label, "--result", result)
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(in.Stderr, "HARVEST NOTE outcome not recorded label=%s: %s %s\n",
			field(label), oneline.Err(err), oneline.Cap(string(out), 200))
	}
}

// appendBench is the one line a person reads when a bench could not judge a card (§1
// rule 1). reason=paused is nobody's fault and never lands here.
func appendBench(in HarvestInput, c CardRow, reason string) {
	if reason == "paused" {
		return
	}
	f, err := os.OpenFile(filepath.Join(in.Root, "bench.tsv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s\t%s\t%s\n",
		in.Now().UTC().Format(time.RFC3339), c.Label, field(in.GateBench), field(reason))
}

// quarantine is §3 rule 6's second half: a key in a worker's diff means a key reached a
// worker, so the job directory is MOVED to <root>/quarantine/<label> -- not harvested,
// not deleted -- and one HUMAN line is written. The matched text is never in any of it:
// only the card, the shape and the place the gate named.
func quarantine(in HarvestInput, c CardRow, jobDir, at string) string {
	dir := filepath.Join(in.Root, "quarantine")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(in.Stderr, "HARVEST NOTE quarantine failed label=%s: %s\n", field(c.Label), oneline.Err(err))
		return ""
	}
	dest := filepath.Join(dir, c.Label)
	// The destination is a computed path, so the removal goes through safepath: an
	// arbitrary directory is refused rather than deleted (internal/ci's removeall class
	// test). A label that will not resolve under the quarantine is a refusal here, and
	// the job stays where it is.
	if err := safepath.RemoveUnder(dir, dest); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(in.Stderr, "HARVEST NOTE quarantine failed label=%s: %s\n", field(c.Label), oneline.Err(err))
		return ""
	}
	if err := os.Rename(jobDir, dest); err != nil {
		fmt.Fprintf(in.Stderr, "HARVEST NOTE quarantine failed label=%s: %s\n", field(c.Label), oneline.Err(err))
		return ""
	}
	if queue := strings.TrimSpace(in.Queue); queue != "" {
		if err := os.MkdirAll(queue, 0o755); err == nil {
			if f, err := os.OpenFile(filepath.Join(queue, "HUMAN"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
				fmt.Fprintf(f, "HUMAN card=%s reason=secret at=%s quarantine=%s remedy=%s\n",
					field(c.Label), field(at), field(dest),
					field("rotate the key the worker saw, then read the quarantined job by hand"))
				f.Close()
			}
		}
	}
	return dest
}

// gitHookOffEnv is the environment every git read the harvest makes in a WORKER's clone
// runs with: no global or system config, no hooks, no fsmonitor. A hook or an fsmonitor
// in a worker's clone is a program the worker chose, and a read of the base must not be
// able to start it (cold read 1 of #1721, HIGH 1, in its harvest-side form).
func gitHookOffEnv() []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/dev/null",
		"GIT_CONFIG_KEY_1=core.fsmonitor", "GIT_CONFIG_VALUE_1=false",
	)
}

// judgedHead is the sha the gate actually judged, or "" when the job's HEAD moved under
// it. The ACCEPT line carries head=<sha12>; the push must name the full sha the harvest
// resolved before the gate ran, and the two must be the same object. They differ only if
// the worker committed again while the gate was running, and then the verdict is a
// verdict on something that is no longer there (the red team of 98e3f3a9, item 9).
func (g gateResult) judgedHead() string {
	if g.sha == "" {
		return ""
	}
	if g.head == "-" || g.head == "" {
		// The line named no head: nothing to disagree with, and the resolved sha is
		// still the object the gate was pointed at.
		return g.sha
	}
	if !strings.HasPrefix(g.sha, g.head) {
		return ""
	}
	return g.sha
}
