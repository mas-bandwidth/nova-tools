package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// A PROVIDER'S INPUT LIMIT IS ITS OWN FAILURE CLASS (#103).
//
// Dogfood 2026-09-12, the Freddy swarm at n=64 on Mercury 2.5 (OpenCode) reading nova-tools
// v0.12.0: two of forty jobs read a whole spec and a second spec, and OpenCode printed
// `Error: Rate limit reached: input token limit exceeded` and exited 1 after ~215s. The
// launcher and the dispatcher saw rc=1 and nothing else -- the pool said `failed`, and the
// one fact that explains it, that the task was too big for the model, was in a log nobody
// reads forty of.
//
// AND THE WORDS `rate limit` ARE IN THAT SENTENCE, so `RateLimited` claimed it: the slot was
// held for the backoff and the SAME task was launched again, spending another 215 seconds
// and another input to prove the same spec still does not fit. A request that did not fit is
// not a wait. It is named, it is never retried, and the provider's own words ride the line
// so that triage can say "the task was too big for the model" without opening a log.

// InputLimitPhrases is the table this detection reads, and it is a TABLE because every
// provider says this in its own words and the tool meets a new provider every time a worker
// description changes. A description may name more (`input_limit_phrases`), and what it
// names is ADDED to these rather than substituted for them: a caller teaching the tool one
// provider's sentence cannot silently un-teach it another's.
//
// EVERY PHRASE IS REFUSAL-SHAPED, in a provider's own words. The lesson is `refusalMarks`
// (worker.go, dogfood D13): a harness log is a TRANSCRIPT -- the diff the worker read, the
// spec it quoted, its own prose -- and the bare words `input limit` or `context window`
// appear in a clean read of any spec that discusses them. A diagnosis that fires on the
// word for the thing is noise in the one field a reader was told to trust.
var InputLimitPhrases = []string{
	"input token limit exceeded",           // OpenCode/Mercury 2.5, verbatim, 2026-09-12 (#103)
	"prompt is too long",                   // Anthropic: "prompt is too long: N tokens > M maximum"
	"maximum context length",               // OpenAI: "this model's maximum context length is N tokens"
	"context length exceeded",              // the same family, by its error code's own words
	"reduce the length of the messages",    // OpenAI's own remedy sentence
	"input length and `max_tokens` exceed", // the third shape of the same refusal
	"request too large",                    // a request the provider would not read at all
}

// InputLimited reports the provider's own words when the harness log says the request did
// not fit, and whether it said so at all.
//
// The quote is the LINE the phrase was found on, with the terminal paint stripped: OpenCode
// wrote `\x1b[91m\x1b[1mError: \x1b[0mRate limit reached: ...`, and a line that carries the
// escapes through reads as `\x1b[91m` on the one line a person is meant to read. The quote
// is bounded to a tail (oneline.TailBytes), because a harness log is 31KB and this is a
// field on a one-line event.
func InputLimited(log []byte, extra []string) (string, bool) {
	phrases := make([]string, 0, len(InputLimitPhrases)+len(extra))
	for _, p := range append(append([]string{}, InputLimitPhrases...), extra...) {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			phrases = append(phrases, p)
		}
	}
	for _, raw := range strings.Split(string(log), "\n") {
		line := strings.TrimSpace(stripPaint(raw))
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		for _, p := range phrases {
			if strings.Contains(lower, p) {
				return oneline.Cap(line, oneline.TailBytes), true
			}
		}
	}
	return "", false
}

// RetriableRateLimit is the question `finish` asks before it holds a slot and spends a
// second attempt: a 429 the dispatcher should wait out, and NOT an input that did not fit.
// The input limit is the more specific truth about a log that says both, and it is the one
// that decides, because the retry a 429 earns is the one thing an oversized task must not
// get.
func RetriableRateLimit(log []byte, extra []string) bool {
	if _, tooBig := InputLimited(log, extra); tooBig {
		return false
	}
	return RateLimited(log)
}

// InputLimitEnd is the ONE place a job's end becomes `input-limit`, so that the dispatcher's
// own `finish`, the start-up pass that recovers a dead dispatcher's job (rule 17) and the
// supervisor all name it the same way. It returns the end and the provider's words.
//
// WHICH ENDS IT MAY REWRITE is the D5 question asked of the input limit (finish.go,
// rateLimitedOutcome): a limit the harness met, backed off from and then answered past is
// HISTORY -- the completion evidence the supervisor wrote is the outcome -- and a reap's own
// end (the deadline, the budget ceiling, an unreadable usage source) is the supervisor's
// verdict from inside the job and is never written over.
func InputLimitEnd(jobDir, end string, rc int, extra []string) (string, string) {
	if !inputLimitedOutcome(end, rc) {
		return end, ""
	}
	raw, err := os.ReadFile(filepath.Join(jobDir, "harness.log"))
	if err != nil {
		return end, ""
	}
	said, tooBig := InputLimited(raw, extra)
	if !tooBig {
		return end, ""
	}
	return EndInputLimit, said
}

// inputLimitedOutcome is rateLimitedOutcome's question, in its own words and for its own
// class: what the log SAYS is this job's outcome only when the job did not finish and its
// end is not one the supervisor reaped it with.
func inputLimitedOutcome(end string, rc int) bool {
	return rateLimitedOutcome(true, end, rc)
}

// OverMaxInput is the task budget checked BEFORE the launch: `max_input` is the ceiling a
// task names on the prompt this tool hands the harness, and the refusal carries the MEASURED
// size beside it.
//
// IT IS A COUNT OF BYTES, not of tokens, and it is named for what it is: this tool has no
// tokenizer, and a token count it guessed would be a guess about somebody else's provider
// (SPEC.md, no guessing). It bounds the one thing the dispatcher can measure -- the PROMPT,
// which carries the task text -- and not the files the worker then opens; those are
// `--files`, and the class above is what happens when they do not fit anyway.
func OverMaxInput(sc Sidecar, prompt int) (string, bool) {
	if sc.MaxInput <= 0 || prompt <= sc.MaxInput {
		return "", false
	}
	return fmt.Sprintf("the prompt is %d bytes and this task's max_input is %d; re-queue it with a smaller task -- one section instead of a whole spec -- or a worker whose window holds it", prompt, sc.MaxInput), true
}

// PromptOf is the prompt this tool handed the harness, read back from the job directory --
// the one input size the dispatcher can measure, and the thing `max_input` is a ceiling on.
// A prompt that cannot be read is no bytes, which no ceiling refuses: a check the tool
// cannot make is never a refusal it invents.
func PromptOf(jobDir string) []byte {
	raw, err := os.ReadFile(filepath.Join(jobDir, "PROMPT.md"))
	if err != nil {
		return nil
	}
	return raw
}

// stripPaint removes the ANSI escape sequences a harness writes to a terminal, so the
// provider's sentence reads as a sentence on the one line a person is meant to read. Only
// the sequences are removed; every other byte, printable or not, is left for oneline.Escape
// to render, because this function's job is legibility and never sanitation.
func stripPaint(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		// CSI: ESC [ <parameters> <final byte 0x40-0x7e>. Anything else after ESC is one
		// byte of a shorter sequence and is dropped with the ESC.
		j := i + 1
		if j < len(s) && s[j] == '[' {
			j++
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
		}
		i = j
	}
	return b.String()
}
