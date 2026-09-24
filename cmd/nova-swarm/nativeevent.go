package main

// THE CARD-END WRITER of the cards:done stream (nova-tools #2563 item 1, "no writer emits
// yet"). `native` is the verb every card on every bench actually runs through, and the end
// of a card is the one moment where the label, the bench, the model, the tokens, the dollars
// and the verdict are all known at once. One entry is written here, from the usage row the
// run just appended, and the run is otherwise unchanged.
//
// AN EMIT MAY NEVER FAIL A CARD. events.Writer returns no error on purpose: a store that is
// down, a password that is absent or an address that was never configured leaves the card
// exactly as it was and costs one line on stderr at most. A bench that has never been given
// NOVA_REDIS_BENCH_PASSWORD runs cards in silence, which is the state most of the fleet is
// in until #2559's launcher hands it over.
//
// THE NUMBERS COME FROM THE ROW, NOT FROM A SECOND READING. writeNativeUsage folds the
// provider's store into one usage.tsv row and the event carries that row's own cells, so the
// stream and the file can never disagree about one card's spend. A cell the provider never
// reported is a dash in the row and ABSENT in the entry -- no tokens_in, tokens_out or usd
// field at all -- because an absent cost is not a zero cost (Johnny, #2587).

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/events"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// cardEndEvent builds the one entry a finished card writes, or reports false when this run
// has nothing to say. The verdict is the NATIVE line's own -- a run the line calls
// INCOMPLETE never earned `ok` -- refined by the report itself.
//
// ASKED IS NOT CLASSIFIED HERE YET. #2563 carries the kind and #2548 is the card that gives
// `ASKED` a meaning in a RESULT; until it exists there is nothing to read, and guessing at
// one would put a word in the stream that no reader could check. A card that asked is `ok`
// or `fail` on its report's own words today, and this is the one place that changes when
// #2548 lands.
func cardEndEvent(cfg nativeRunConfig, res nativeRunResult, verdict, bench string) (events.Event, bool) {
	label := strings.TrimSpace(cfg.label)
	if label == "" {
		return events.Event{}, false
	}
	kind := events.OK
	if verdict != "OK" || resultIsFailed(res.job) {
		kind = events.Fail
	}
	row := res.usage
	e := events.Event{
		Label:     label,
		Bench:     bench,
		Model:     modelOf(cfg, row),
		Route:     strings.TrimSpace(row["provider"]),
		Kind:      kind,
		Attempt:   usageInt(row, "attempt"),
		TokensIn:  usageInt64(row, "tokens_in"),
		TokensOut: usageInt64(row, "tokens_out"),
		USD:       usageFloat(row, "usd"),
	}
	return e, true
}

// emitCardEnd opens the store, writes the one entry and closes. It is called after the
// NATIVE line is printed, so the card's own receipt is on stdout before anything is asked of
// a network.
//
// THE WRITE IS BOUNDED AS WELL AS THE DIAL, and the bound is the TOTAL of the two. Inside
// OpenWriter, `opt.Timeout` bounds only the connection; an XADD to a store that ACCEPTED the
// connection and then stopped answering would otherwise wait forever -- and it would wait
// holding this card's bench slot lease, on a card that has already finished and already
// printed its receipt. Two of those and the bench is a seat short for the rest of the
// sprint. One deadline over both steps is deliberate: what must be capped is what the card
// end COSTS, not what each half of it costs. A dial that eats the whole budget leaves the
// write a deadline in the past, which is one EVENT SKIPPED line and a return -- exactly what
// a store that is not answering should cost. The caller's context still governs: this only
// ever shortens it.
func emitCardEnd(parent context.Context, opt events.WriterOptions, cfg nativeRunConfig, res nativeRunResult, verdict, bench string) {
	e, ok := cardEndEvent(cfg, res, verdict, bench)
	if !ok {
		return
	}
	bound := opt.Timeout
	if bound <= 0 {
		bound = defaultCardEventTimeout
	}
	ctx, cancel := context.WithTimeout(parent, bound)
	defer cancel()
	w := events.OpenWriter(ctx, opt)
	defer w.Close()
	w.Send(ctx, e)
}

// defaultCardEventTimeout is the bound when the caller named none.
const defaultCardEventTimeout = 5 * time.Second

// resultIsFailed reads the card's OWN report and answers whether it says the card failed.
// Two things make it so, and both are the report's words rather than an inference:
//
//   - THE VERDICT LINE, which is line 2 of every card report in this line of work (line 1 is
//     `RESULT <label> sha=<...> — <title>`; line 2 is the verdict: `RED FAIL ...`, `GREEN
//     ...`, `ABSENT`). BLOCKED, FAILED, FAIL and RED are failures.
//   - WRITTEN-BY, which is how swarm.WriteBlockedResult signs a report the MACHINERY wrote
//     for a card that published none (wallread.go). A report nobody's model wrote is never
//     an `ok`, whatever its first words say, and that report puts BLOCKED on its own line 1 --
//     which is exactly why the signature is checked as well as the verdict line.
//
// A job with no readable report at all is not judged here: the NATIVE verdict already called
// that run INCOMPLETE, and this function only ever adds a failure, never removes one.
func resultIsFailed(jobDir string) bool {
	path, found := swarm.FindCardResult(jobDir)
	if !found {
		return false
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- the path is the run's own job directory
	if err != nil {
		return false
	}
	lines := strings.Split(string(raw), "\n")
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "written-by:") {
			return true
		}
	}
	// Line 2 is the verdict line; a report with one line has none.
	if len(lines) < 2 {
		return false
	}
	return failWord(lines[1])
}

// failWord reports whether a verdict line opens with one of the words that mean the card did
// not do its work. The comparison is on the FIRST word, upper-cased, so `RED FAIL bits(12)
// law: ...` is a failure and a title mentioning "red" further along a sentence is not.
func failWord(line string) bool {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) == 0 {
		return false
	}
	switch strings.ToUpper(strings.Trim(fields[0], ":*#")) {
	case "BLOCKED", "FAILED", "FAIL", "RED":
		return true
	}
	return false
}

// modelOf is the model id the entry carries: the usage row's provider and model rejoined, so
// the stream says what the row says, and `--model` itself when the row carries neither (a
// fast failure whose provider reported nothing keeps dashes).
func modelOf(cfg nativeRunConfig, row swarm.UsageRow) string {
	provider := strings.TrimSpace(row["provider"])
	model := strings.TrimSpace(row["model"])
	if provider != "" && provider != swarm.Dash && model != "" && model != swarm.Dash {
		return provider + "/" + model
	}
	return strings.TrimSpace(cfg.model)
}

// usageInt64 reads one count out of the row. A dash, an empty cell and an unreadable or
// negative number are all ABSENT (nil): nobody measured it, and the entry then carries no
// such field rather than a zero nobody reported.
func usageInt64(row swarm.UsageRow, name string) *int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(row[name]), 10, 64)
	if err != nil || n < 0 {
		return nil
	}
	return &n
}

// usageInt reads attempt, whose absence is the card's first attempt, so it stays a plain int.
func usageInt(row swarm.UsageRow, name string) int {
	if n := usageInt64(row, name); n != nil {
		return int(*n)
	}
	return 0
}

// usageFloat reads the price the same way: a dash is absent, never $0.
func usageFloat(row swarm.UsageRow, name string) *float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(row[name]), 64)
	if err != nil || f < 0 {
		return nil
	}
	return &f
}

// benchName is the `bench=` an entry carries from a card: the machine the card ran ON.
// `native` runs on the bench itself, so its own hostname is the answer and there is no flag
// to get wrong. A hostname that cannot be read leaves the field empty rather than guessed.
var benchName = func() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	// The short name, which is what the sprint table's rows and the fleet registry use.
	if i := strings.Index(h, "."); i > 0 {
		h = h[:i]
	}
	return h
}
