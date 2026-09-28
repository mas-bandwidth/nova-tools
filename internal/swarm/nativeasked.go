package swarm

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// A CARD THAT ENDED BY ASKING (nova-tools #2548).
//
// THE RECEIPT. Canary run 3, 2026-09-22 02:05Z, route openrouter/openai/gpt-5-nano: the
// worker wrote its test file into a phantom nested path, committed nothing, and ended its
// last turn with `Would you like me to proceed with moving RESULT.md into the nested repo
// and finalize the commit?`. Nobody answers a headless card.
//
// THE MECHANISM THE ISSUE TITLE NAMED IS NOT REAL, and this is not a fix for it. The
// runtime dogfood of 2026-09-22 (rowan-new reports/dogfood-runtime-2026-09-22.md, (b))
// ran that shape on both benches -- a card whose STEP 2 forbids writing `RESULT.md` until
// an operator approves and tells the model to ask and wait -- and measured 5.22 s on hulk
// and 5.49 s on the Studio: `opencode run` is non-interactive, it finishes the turn and
// exits 0, and the slot is freed at once. The 1200 s wait the issue measured belongs to
// the canary's own poll for a terminal RESULT line, not to `native`. So there is no slot
// to free here and nothing to interrupt.
//
// WHAT IS REAL IS THE VERDICT. Both dogfood runs ended `NATIVE INCOMPLETE ... why=no-result`
// -- the token reserved for a MODEL THAT CHOSE TO PUBLISH NOTHING -- with nothing anywhere
// recording that the card ended by asking a question. A card that asked and a card that
// crashed into silence are then the same row to every reader: the requeue cannot retry the
// asker once on another route, and the ledger cannot count which models ask. The two are
// different faults with different remedies, exactly as `harness-silent` and `no-result` are
// (issue #591), and the report the card never wrote is where the difference goes.
//
// THIS FILE IS THE DETECTION AND THE VERDICT, AND NOTHING ELSE. Glenn's steer channel --
// post the question to the bus, wait a bounded time for a typed answer, feed it back as one
// more user turn -- is a later card (tools-06b) and is deliberately not built here. Neither
// is the never-ask sentence in the card preamble, which is Emma's #2588/METHODS.

// AskedResultName is the report a run writes FOR a card that ended by asking. It is
// `RESULT.md` for the same reason the blocked report is: that is the one file every gather
// already looks for, and a card that ended by asking published nothing there.
const AskedResultName = BlockedResultName

// AskedTailBytes bounds what the question test reads of a capture. A card's capture is a
// whole transcript and can be megabytes; the question is the LAST thing in it, and no
// reader needs a megabyte read to answer a yes/no.
const AskedTailBytes = 64 << 10

// askedOpeners are the openings that are a question to the operator whether or not the
// model ended the line with a mark -- `(?i)^(would you like|should i|do you want|shall i|
// can you confirm)`, compared in lower case rather than by a regexp so the test is one
// pass over a line this package already has in hand.
var askedOpeners = []string{
	"would you like",
	"should i",
	"do you want",
	"shall i",
	"can you confirm",
}

// askedTrailers are the decorations a model wraps its last sentence in -- bold, a quote, a
// parenthesis -- which are trimmed off the end before the question mark is looked for, so
// `**Would you like me to proceed?**` is the question it plainly is.
const askedTrailers = "*_`\"')]} \t"

// AskedRun is what one finished native run offers the question test: the tail of the card's
// own capture, the child's exit code, whether a report was found where the gather looks for
// one, and whether ./repo holds commits past its base. Every field is something the run
// already knows when the child is gone.
type AskedRun struct {
	Capture   string // the tail of <job>/harness-output.log
	RC        int    // the child's exit code
	Published bool   // a RESULT.md was found where the gather looks for one
	Committed bool   // ./repo holds commits past its base
}

// Asked reports the question a run ended on, and false when it ended any other way.
//
// FOUR CONDITIONS, ALL OF THEM NECESSARY, and each one is a different card this must not
// claim:
//
//   - the last thing the card said is a question. A card that ended on a sentence did not
//     ask, whatever else it did.
//   - the child exited 0. A crash is a crash: a non-zero exit is already its own end
//     (`rc=<n>`), and a model that asked and then fell over is not waiting for an answer.
//   - no `RESULT.md` was found where the gather looks for one. A card that published owns
//     its report, and a question in its last turn is prose in a finished card.
//   - `./repo` holds no commit past its base. A model that asked rhetorically and then went
//     on and committed the work did the work; the question was narration, not a stop.
func Asked(run AskedRun) (string, bool) {
	if run.RC != 0 || run.Published || run.Committed {
		return "", false
	}
	line := lastSpokenLine(run.Capture)
	if line == "" || !isQuestion(line) {
		return "", false
	}
	return line, true
}

// AskedEnd asks Asked's question of a finished job on disk: the capture's own tail, the
// gather's own result lookup, and ./repo's own commit count. The rc is the caller's,
// because the run holds it and the job directory does not.
func AskedEnd(jobDir string, rc int) (string, bool) {
	capture, _ := tailBytes(filepath.Join(jobDir, "harness-output.log"), AskedTailBytes)
	_, published := FindCardResult(jobDir)
	_, _, committed := repoCommits(jobDir)
	return Asked(AskedRun{Capture: capture, RC: rc, Published: published, Committed: committed})
}

// WriteAskedResult writes the card's report when it ended by asking and published none. Like
// the blocked report it REFUSES to overwrite a result that exists -- a card that published
// owns its report -- and it says who wrote it on its own line, because a reader must never
// have to guess whether a worker or the machinery wrote what they are reading.
//
// Line 1 is `RESULT: ASKED <the question, one line>`: the word is the verdict a requeue
// reads and the ledger counts, and the question itself is on it, bounded and escaped, so
// the one line a coordinator reads already holds the thing the card is waiting for. The
// report carries NO findings head, so it can be scored `plan-only` and never `ok` or
// `clean`: a report the machinery wrote can never be counted as work a worker did.
func WriteAskedResult(jobDir, task, question string) (string, bool, error) {
	if _, found := FindCardResult(jobDir); found {
		return "", false, nil
	}
	dest := filepath.Join(jobDir, AskedResultName)
	body := strings.Join([]string{
		AskedResultPrefix + oneline.Escape(oneline.Cap(question, oneline.TailBytes)),
		"asked: " + oneline.Field(task) + " ended its last turn with a question, published no report and committed nothing; the card is unattended and nobody answers it, so the run records the question rather than leaving an empty job behind",
		AskedWrittenBy,
		"",
	}, "\n")
	if err := os.WriteFile(dest, []byte(body), 0o644); err != nil {
		return "", false, err
	}
	return dest, true, nil
}

// AskedResultPrefix opens line 1 of that report, and AskedWrittenBy is the line that says
// the machinery wrote it. Both are constants because two readers already depend on them:
// the one that writes the report and the one that refuses to count it as the card's own.
const (
	AskedResultPrefix = "RESULT: ASKED "
	AskedWrittenBy    = "written-by: nova-swarm native (the card published no report of its own)"
)

// AskedReport reports whether the `RESULT.md` at path is the one this file wrote for a card
// that ended by asking, rather than a report a card published.
//
// IT ASKS FOR BOTH MARKS -- the `RESULT: ASKED ` opening and the `written-by:` line -- so
// the answer rests on the whole shape this file writes and not on an opening a card could
// type. It exists so that writing this report does not silently promote a card's verdict:
// the absence of a result is what `no-result` is, and a report the machinery wrote for a
// card that asked is still an absence of the card's own.
func AskedReport(path string) bool {
	raw, err := readRegularBounded(path, AskedTailBytes)
	if err != nil {
		return false
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], AskedResultPrefix) {
		return false
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == AskedWrittenBy {
			return true
		}
	}
	return false
}

// lastSpokenLine is the last thing the CHILD said in a capture: the last non-blank line
// that is not one of the wall's own `SANDBOX ` lines, with the terminal's paint stripped.
// The wall's lines are skipped for the same reason `harness=silent` skips them -- they are
// the machinery's words in the child's file, not the child's.
func lastSpokenLine(capture string) string {
	lines := strings.Split(strings.ReplaceAll(capture, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(stripPaint(lines[i]))
		if line == "" || strings.HasPrefix(line, "SANDBOX ") {
			continue
		}
		return line
	}
	return ""
}

// isQuestion is the one test: a line that ends with `?` once its decoration is trimmed, or
// one that opens with a phrase that is a question to the operator however it ends.
func isQuestion(line string) bool {
	if strings.HasSuffix(strings.TrimRight(line, askedTrailers), "?") {
		return true
	}
	lower := strings.ToLower(line)
	for _, opener := range askedOpeners {
		if strings.HasPrefix(lower, opener) {
			return true
		}
	}
	return false
}
