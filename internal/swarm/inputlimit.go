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

// ProviderErrorMarks are how a harness says THIS IS THE PROVIDER TALKING, and a phrase counts
// only on a line whose own LABEL is one of them, or directly under such a line.
//
// THE PHRASE ALONE IS NOT ENOUGH, because a harness log is a TRANSCRIPT and the sentences in
// the table above now live in this repository's own source, README and spec: a worker reading
// nova-tools quotes `input token limit exceeded` in its RESULT.md, and if it then died of a
// real 429 the whole transcript search would call that death an input limit and refuse it the
// retry a 429 has earned (Fable's read of #150, finding 3). It is the D13 rule one file over:
// a diagnosis that fires on the word for the thing, wherever it appears, is noise in the one
// field a reader was told to trust. Every real specimen carries a mark -- OpenCode's
// `Error: `, Anthropic's `API Error: 400 {"type":"error"...}`, OpenAI's `Error code: 400 -
// {'error': ...}` -- and a harness that puts the mark on its own line above the message is
// why the line ABOVE counts too.
var ProviderErrorMarks = []string{"error", "err", "fatal", "exception", "rejected", "aborted"}

// AND `refused` IS NOT ONE OF THEM, because it is THIS FAMILY'S OWN WORD (the swarm
// dispatcher's check of #150). No provider specimen says a bare `refused`: they say `Error:`,
// `Error code:`, `invalid_request_error`, `exception`. Every tool in this repository, on the
// other hand, ends a verb with it -- `ADD REFUSED:`, `BUS REFUSED:`, `RUN REFUSED reason=…` --
// and a nova-swarm line lands in a harness log whenever a job runs these tools, which is what
// dogfooding IS here. `RUN REFUSED reason=sandbox_probe: … input token limit exceeded` was
// classed: one bare token (`run`) before the mark, and the phrase further along the same line.
// `rejected` stays: it is in no grammar line in this repository, and a provider may say it.

// isOwnEventLine reports whether a line is one of this family's own event lines, which are
// never a provider talking. It is the SHAPE rather than a list of verbs (SPEC.md's two-token
// event prefix): an ALL-CAPS verb with no colon, then an ALL-CAPS word -- `RUN REFUSED`,
// `SANDBOX OK`, `PROBE STEP`, `TRIAGE BATCH`, and every other tool's, including this class's
// own `RUN INPUT-LIMIT … : <the provider's own words>` nested in a job that ran nova-swarm.
// A shape holds for the tool written tomorrow; a list of verbs holds until then.
//
// The FIRST token carries no colon, which is what keeps a provider's own shout out of this:
// `ERROR: RATE LIMIT REACHED: INPUT TOKEN LIMIT EXCEEDED` is not an event line, and neither is
// `API Error: 400 …`, whose second word is not all-caps.
func isOwnEventLine(line string) bool {
	words := strings.Fields(line)
	if len(words) < 2 {
		return false
	}
	return capsToken(words[0], false) && capsToken(words[1], true)
}

// capsToken is one token of that prefix: at least two characters of A-Z, 0-9, `-` or `_` with
// a letter among them, and a trailing colon only where one is allowed.
func capsToken(token string, colonAllowed bool) bool {
	if strings.HasSuffix(token, ":") {
		if !colonAllowed {
			return false
		}
		token = strings.TrimSuffix(token, ":")
	}
	if len(token) < 2 {
		return false
	}
	letters := 0
	for i := 0; i < len(token); i++ {
		switch c := token[i]; {
		case c >= 'A' && c <= 'Z':
			letters++
		case c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return letters > 0
}

// MarkWords is how many tokens may come BEFORE the mark and leave it a label.
//
// A SUBSTRING SEARCH IS NOT A LABEL (the swarm delta eye on #150). `strings.Contains(lower,
// mark)` accepted the word anywhere on the line, so a RESULT.md bullet, a finding row or a
// heading that QUOTED the provider's sentence beside the word `error` was marked and classed
// -- the very line the mark rule was added to exclude, and a job classed this way is never
// retried, so the false mark takes a real 429's retry away.
//
// A harness's label is a stamp, a component name and the mark: `Error: …`,
// `opencode: error: …`, `2026-09-12T12:24:31Z error: …`, `API Error: 400 …`,
// `openai.BadRequestError: Error code: 400 …` -- never more than two tokens before it. Prose
// has more (`- the provider printed error: …` has four), and a quote character before the mark
// means the line is quoting rather than reporting.
//
// AND AT MOST ONE OF THOSE TOKENS IS A BARE WORD. Two alone is not enough: `the harness error
// is …` and `| 3 | red | error | …` are a sentence and a table row, and both put a mark two
// tokens in. A label token is a STAMP (it carries a digit) or a PREFIX (it ends in `:`, `]`,
// `|` or `>`); anything else is a bare word, and a harness writes at most one of them before
// its mark -- the `API` of `API Error:`, the `provider` of `provider error:`. A second bare
// word is prose.
const (
	MarkWords     = 2
	MarkBareWords = 1
)

// quoteRunes are the characters that turn the rest of a line into somebody's quotation: after
// one of these, a mark is a mark somebody WROTE DOWN, and this class is about what a provider
// said.
const quoteRunes = "`\"'\u201c\u201d\u2018\u2019"

// InputLimited reports the provider's own words when the harness log says the request did
// not fit, and whether it said so at all. A phrase counts only on a line whose own label is a
// provider error mark, or on the line directly under one (ProviderErrorMarks, MarkWords): the
// table's sentences appear in transcripts that merely QUOTE them, this repository's own docs
// and a worker's own RESULT.md included.
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
	previousWasAMark := false
	for _, raw := range strings.Split(string(log), "\n") {
		line := strings.TrimSpace(stripPaint(raw))
		if line == "" {
			continue
		}
		if isOwnEventLine(line) {
			// A LINE THIS FAMILY WROTE IS NOT EVIDENCE ABOUT THIS JOB'S PROVIDER, and it is
			// skipped whole: it carries no mark, and it does not inherit the mark of the line
			// above it either.
			previousWasAMark = false
			continue
		}
		lower := strings.ToLower(line)
		mark := carriesAMark(lower)
		marked := previousWasAMark || mark
		previousWasAMark = mark
		if !marked {
			continue
		}
		for _, p := range phrases {
			if strings.Contains(lower, p) {
				return oneline.Cap(line, oneline.TailBytes), true
			}
		}
	}
	return "", false
}

// carriesAMark reports whether this line's own label is a provider error mark: the mark
// begins a word, at most MarkWords tokens precede it, and no quote character comes before it.
func carriesAMark(lower string) bool {
	for _, mark := range ProviderErrorMarks {
		for at := 0; at >= 0 && at < len(lower); {
			i := strings.Index(lower[at:], mark)
			if i < 0 {
				break
			}
			i += at
			at = i + 1
			if !wordStarts(lower, i, len(mark)) {
				continue
			}
			before := lower[:i]
			if strings.ContainsAny(before, quoteRunes) {
				// The line is quoting somebody. A quotation is not a report.
				break
			}
			fields := strings.Fields(before)
			if isALabel(fields) {
				return true
			}
			// THE WORK IS BOUNDED TO THE HEAD OF THE LINE. Every later occurrence of this
			// mark has at least as many tokens before it, so once the count is past the
			// bound this line cannot be labelled by this mark -- a 31KB transcript is
			// scanned across its lines and never across each line twice.
			if len(fields) > MarkWords {
				break
			}
		}
	}
	return false
}

// isALabel reports whether the tokens before a mark are a harness's LABEL and not the start
// of a sentence: at most MarkWords of them, of which at most MarkBareWords is a bare word.
func isALabel(before []string) bool {
	if len(before) > MarkWords {
		return false
	}
	// A LIST MARKER IS PROSE, WHATEVER FOLLOWS IT (Fable's delta read of 110b745). `-` is one
	// bare token and `1.` carries a digit, so a RESULT.md bullet that LEADS with the mark --
	// `- error: the harness said prompt is too long and died`, `1. error: prompt is too long`
	// -- was a label by both bounds and classed the job. A bulleted quotation is the shape a
	// worker writes a finding in, and no harness writes its label under one.
	if len(before) > 0 && isListMarker(before[0]) {
		return false
	}
	bare := 0
	for _, token := range before {
		if labelToken(token) {
			continue
		}
		bare++
	}
	return bare <= MarkBareWords
}

// isListMarker is the head of a bullet or a numbered item: `-`, `*`, `+`, `<n>.`, `<n>)`.
func isListMarker(token string) bool {
	switch token {
	case "-", "*", "+":
		return true
	}
	body := strings.TrimRight(token, ".)")
	if body == token || body == "" {
		return false
	}
	for i := 0; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return false
		}
	}
	return true
}

// labelToken is a stamp or a prefix: a token carrying a digit (a date, a clock, a pid, a
// code) or one ending in a separator a harness writes its label with.
func labelToken(token string) bool {
	if strings.ContainsAny(token, "0123456789") {
		return true
	}
	return strings.HasSuffix(token, ":") || strings.HasSuffix(token, "]") ||
		strings.HasSuffix(token, "|") || strings.HasSuffix(token, ">")
}

// wordStarts reports whether the match at i is a WHOLE word: a mark that is the tail of a
// longer word is not a label. `openai.badrequesterror` carries no mark; the `Error code:` that
// follows it does. The boundary is any character that is not a letter or a digit, so `_` in
// `invalid_request_error` is a boundary too -- that IS the provider's own field name.
func wordStarts(s string, i, n int) bool {
	if i > 0 && isWordRune(s[i-1]) {
		return false
	}
	end := i + n
	return end >= len(s) || !isWordRune(s[end])
}

func isWordRune(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
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
// AND THE SENTENCE HAS TWO SOURCES, IN THIS ORDER: the harness log, and the supervisor's own
// `exit.json` record (`recorded`) when the log gives nothing. The supervisor classified this
// job from inside it and wrote the words down; a log that cannot be read at finish -- a
// Windows replace window, a directory already reclaimed, a disk that moved -- left the class
// standing with `sc.Limit` empty and `TRIAGE INPUT-LIMIT … : -`, which is the one line this
// class exists to print (Fable's read of #150, finding 2).
func InputLimitEnd(jobDir, end string, rc int, recorded string, extra []string) (string, string) {
	if !inputLimitedOutcome(end, rc) {
		return end, ""
	}
	if raw, err := os.ReadFile(filepath.Join(jobDir, "harness.log")); err == nil {
		if said, tooBig := InputLimited(raw, extra); tooBig {
			return EndInputLimit, said
		}
	}
	// The log said nothing, or could not be read. The CLASS may still stand, because the
	// supervisor named it on the exit record; then its own sentence is the quote.
	if end == EndInputLimit && strings.TrimSpace(recorded) != "" {
		return EndInputLimit, oneline.Cap(strings.TrimSpace(stripPaint(recorded)), oneline.TailBytes)
	}
	return end, ""
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

// PhraseFloor is the shortest a provider's sentence may be, in characters, and it is a FLOOR
// because a phrase is a knife: a job classed `input-limit` is a job that is never retried.
const PhraseFloor = 12

// TooShortForAPhrase is what a description's own phrase must clear, and the reason when it
// does not. `input_limit_phrases: ["limit"]` would class every failed job whose log holds the
// word `limit` and refuse each one its retry, and non-emptiness was the only floor there was
// (Fable's read of #150, finding 4). A provider's refusal is a SENTENCE: PhraseFloor
// characters at least, and a space or a digit in it, so that no single word can be one. The
// table this repo ships clears its own floor, and a test says so.
func TooShortForAPhrase(phrase string) (string, bool) {
	trimmed := strings.TrimSpace(phrase)
	switch {
	case trimmed == "":
		return "it is empty", false
	case len([]rune(trimmed)) < PhraseFloor:
		return fmt.Sprintf("it is %d characters and a provider's sentence is at least %d", len([]rune(trimmed)), PhraseFloor), false
	case !strings.ContainsAny(trimmed, " \t") && !strings.ContainsAny(trimmed, "0123456789"):
		return "it is one word, and one word appears in a transcript that quotes it", false
	}
	return "", true
}

// PromptSize is the size of the prompt this tool handed the harness, read back from the job
// directory -- the one input size the dispatcher can measure, and the thing `max_input` is a
// ceiling on.
// It reports whether it could measure at all, so the two callers can differ where they must:
// the pre-launch check reads an unmeasurable prompt as no bytes, which no ceiling refuses --
// a check the tool cannot make is never a refusal it invents -- and the line prints the dash
// that is rule 12's word for an absence, never a zero.
func PromptSize(jobDir string) (int, bool) {
	fi, err := os.Stat(filepath.Join(jobDir, "PROMPT.md"))
	if err != nil {
		return 0, false
	}
	const maxInt = int64(^uint(0) >> 1)
	if fi.Size() > maxInt {
		return int(maxInt), true
	}
	return int(fi.Size()), true
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
