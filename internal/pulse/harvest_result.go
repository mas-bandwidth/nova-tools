package pulse

// The harvest's result reading: what did this card find, and whose red is it
// (nova-tools #1619, SPEC-DECIDE section 2).
//
// Beside the branch class question the harvest already asks, --decide asks two
// choices in the same provider call: result (clean, defect, skip-precondition,
// blocked-toolchain) and red_owner (row, bench, na). The rule table answers
// with no call: a SKIP first line never started, a CLEAN first line with an
// exit status of 0 found nothing, and output whose de-quoted tail names a
// missing toolchain is the bench's red. A quoted toolchain string is not one:
// fenced blocks are the log's quoting mechanism for expected output, and the
// matcher strips them before it looks. Below the floor, or with no provider
// answer, the pair is unknown and the harvest runs today's path.
//
// It makes no model call of its own: every decision comes through Decider, the
// shipped *decide.Client or a test's fake, so no test reaches the network and
// the key is only ever read by decide.New from the environment.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// harvestResultOptions is the closed set of findings one finished job can be.
var harvestResultOptions = [...]string{"clean", "defect", "skip-precondition", "blocked-toolchain"}

// harvestRedOwnerOptions is the closed set of owners for a red: the work's own,
// the machine's, or nothing red.
var harvestRedOwnerOptions = [...]string{"row", "bench", "na"}

// harvestResultQuestions is the harvest/v1 pair: what the card found, and whose
// red it is. It rides the one class call, which is what the provider's shape is
// for: one item, one call.
func harvestResultQuestions() map[string]decide.Question {
	return map[string]decide.Question{
		"result": {
			Instructions: "Read this finished card from its RESULT.md and its output tail: clean (the card did what it was asked and found nothing wrong); defect (the card reports a wrong behaviour of the thing under test, with a receipt); skip-precondition (the card could not start because something it was told to expect was absent); blocked-toolchain (the card could not run because the bench lacked a tool, a version, disk, network or permission).",
			Choice: map[string]string{
				"clean":             "the card did what it was asked and found nothing wrong",
				"defect":            "the card reports a wrong behaviour of the thing under test, with a receipt",
				"skip-precondition": "the card could not start because something it was told to expect was absent",
				"blocked-toolchain": "the card could not run because the bench lacked a tool, a version, disk, network or permission",
			},
		},
		"red_owner": {
			Instructions: "Whose red is this finished card's: row (the failing thing is the work's own); bench (it is the machine's); na (nothing is red).",
			Choice: map[string]string{
				"row":   "the failing thing is the work's own",
				"bench": "it is the machine's",
				"na":    "nothing is red",
			},
		},
	}
}

// harvestOutputBytes is how much of the card's output the result pair is asked
// over: the last bytes, tail truncation. Bound: 4096 bytes with the class state.
const harvestOutputBytes = 3072

// harvestResultEvidence is the bounded public state the result pair is asked
// over: the RESULT.md first line, its red: line where present, and the last
// harvestOutputBytes of the card's output, tail truncation, after redaction.
// Nothing else is sent: never a secret, never a private body, only the record
// the harvest already read.
func harvestResultEvidence(jobDir string, resultLines []string) string {
	var b strings.Builder
	b.WriteString("RESULT.md first line: " + strings.TrimSpace(firstNonEmpty(resultLines)) + "\n")
	b.WriteString("red: " + harvestRedLine(resultLines) + "\n")
	b.WriteString("OUTPUT TAIL\n")
	b.WriteString(harvestOutputTail(jobDir))
	return b.String()
}

// harvestRedLine is the card's red: line, else "-".
func harvestRedLine(lines []string) string {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(strings.ToLower(t), "red:") {
			return strings.TrimSpace(t[len("red:"):])
		}
	}
	return "-"
}

// harvestOutputTail is the last harvestOutputBytes of the job's harness.log,
// with one [cut n bytes] line when truncated, else "(no output)" when the job
// wrote none. The log is the card's output the rule table and the provider read.
func harvestOutputTail(jobDir string) string {
	raw, err := os.ReadFile(filepath.Join(jobDir, "harness.log"))
	if err != nil || len(raw) == 0 {
		return "(no output)\n"
	}
	if len(raw) > harvestOutputBytes {
		return fmt.Sprintf("[cut %d bytes]\n%s", len(raw)-harvestOutputBytes, raw[len(raw)-harvestOutputBytes:])
	}
	return string(raw)
}

// harvestToolchainPatterns is the shipped toolchain table: output naming one of
// these is a bench red with no call. It is data, not judgment: a red whose
// message names a missing toolchain is the bench's and not the row's.
var harvestToolchainPatterns = []string{
	"command not found",
	"executable file not found",
	"no space left on device",
	"toolchain not available",
}

// toolchainHit reports whether the de-quoted output tail names a missing
// toolchain. Fenced blocks are the log's quoting mechanism -- a passing test's
// expected-output block -- and are stripped before the match, so quoted output
// is never a bench red. A permission denied names the bench only outside the
// job directory: inside it the card denied itself.
func toolchainHit(jobDir, tail string) bool {
	text := strings.ToLower(stripQuotedBlocks(tail))
	for _, p := range harvestToolchainPatterns {
		if strings.Contains(text, p) {
			return true
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "permission denied") && !strings.Contains(line, strings.ToLower(jobDir)) {
			return true
		}
	}
	return false
}

// stripQuotedBlocks removes ``` fenced regions, the log's quoting mechanism for
// expected output, including a trailing unclosed fence.
func stripQuotedBlocks(s string) string {
	var b strings.Builder
	in := false
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			in = !in
			continue
		}
		if !in {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// harvestExitCode is the supervisor's own exit status for the job from
// exit.json, -1 when the job wrote none or it does not parse. It is read
// without the slot attestation, which the harvest does not hold this late: the
// CLEAN rule pairs it with the CLEAN first line, itself card-written, so the
// rule is no weaker than its other half. An absent fact asks the provider.
func harvestExitCode(jobDir string) int {
	raw, err := os.ReadFile(filepath.Join(jobDir, "exit.json"))
	if err != nil {
		return -1
	}
	var rec struct {
		RC int `json:"rc"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return -1
	}
	return rec.RC
}

// harvestResult is one finished job's finding: the result, whose red it is, and
// whether the rule table answered with no call.
type harvestResult struct {
	result string
	owner  string
	ruled  bool
}

// harvestResultByRule answers the result pair from the rule table with no call:
// a SKIP first line never started, a CLEAN first line with an exit status of 0
// found nothing, and a toolchain pattern in the de-quoted output tail is the
// bench's red. Anything else is for the provider.
func harvestResultByRule(jobDir string, resultLines []string) (harvestResult, bool) {
	line1 := strings.TrimSpace(firstNonEmpty(resultLines))
	if strings.HasPrefix(line1, "SKIP") {
		return harvestResult{result: "skip-precondition", owner: "na", ruled: true}, true
	}
	if strings.HasPrefix(line1, "CLEAN") && harvestExitCode(jobDir) == 0 {
		return harvestResult{result: "clean", owner: "na", ruled: true}, true
	}
	if toolchainHit(jobDir, harvestOutputTail(jobDir)) {
		return harvestResult{result: "blocked-toolchain", owner: "bench", ruled: true}, true
	}
	return harvestResult{}, false
}

// decideHarvest asks the harvest's questions in one provider call: the branch
// class it already asked, and beside it the harvest/v1 result pair. The rule
// table is consulted first and answers with no call; a provider error, a
// missing answer, an option outside either closed set or a confidence below the
// floor leaves that pair unknown, and the harvest runs today's path.
func (in HarvestInput) decideHarvest(jobDir, branch string, resultLines []string) (harvestClass, harvestResult) {
	class := harvestClass{kind: "unknown", below: "class"}
	res := harvestResult{result: "unknown", owner: "unknown"}
	if in.Decider == nil {
		return class, res
	}
	questions := harvestClassQuestions()
	state := harvestClassState(jobDir, branch, resultLines)
	if ruled, ok := harvestResultByRule(jobDir, resultLines); ok {
		res = ruled
	} else {
		for name, q := range harvestResultQuestions() {
			questions[name] = q
		}
		state += harvestResultEvidence(jobDir, resultLines)
	}
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	answers, _, err := in.Decider.Decide(ctx, state, questions)
	if err != nil {
		fmt.Fprintf(in.Stderr, "HARVEST NOTE class decision unavailable: %s\n", oneline.Err(err))
		return class, res
	}
	if a, ok := answers["class"]; ok && a.Type == "choice" {
		class.conf = a.Confidence
		if a.Confidence >= in.Floor {
			for _, opt := range harvestClassOptions {
				if a.Choice == opt {
					class.kind, class.below = opt, "-"
					if opt == "already-fixed" {
						class.test = resultNamedTest(resultLines)
					}
					break
				}
			}
		}
	}
	if res.ruled {
		return class, res
	}
	ra, ok := answers["result"]
	oa, ook := answers["red_owner"]
	if !ok || !ook || ra.Type != "choice" || oa.Type != "choice" {
		return class, res
	}
	if ra.Confidence < in.Floor || oa.Confidence < in.Floor {
		return class, res
	}
	for _, opt := range harvestResultOptions {
		if ra.Choice == opt {
			res.result = opt
			break
		}
	}
	if res.result == "unknown" {
		return class, res
	}
	for _, opt := range harvestRedOwnerOptions {
		if oa.Choice == opt {
			res.owner = opt
			break
		}
	}
	if res.owner == "unknown" {
		res.result = "unknown"
	}
	return class, res
}

// resultFields is the result= red_owner= tail every HARVEST line carries once
// the decision route is on. Unknown runs today's path and names the reader at
// close.
func resultFields(r harvestResult) string {
	return fmt.Sprintf("result=%s red_owner=%s", field(r.result), field(r.owner))
}
