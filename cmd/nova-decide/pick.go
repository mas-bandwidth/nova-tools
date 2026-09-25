// The pick verb (Jev 2026-09-25): which friend-child model and effort a task
// kind runs at, chosen from a versioned table by Jev, adopted by any friend.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// runPick is the pick verb: it reads the versioned pick table, asks Jev which
// effort tier the kind's friend child runs at (or answers from the table alone
// with --no-jev), and prints one line. Any friend adopts: the table is one
// shared source, so every friend asking for a kind gets the same answer.
func runPick(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-decide pick", flag.ContinueOnError)
	kind := fs.String("kind", "", "the task kind to pick a friend-child model and effort for")
	lines := fs.String("lines", "", "a file of one task kind per line: pick each, deterministically from the table")
	tablePath := fs.String("table", "", "the versioned pick table; the embedded one when absent")
	baseURL := fs.String("base-url", decide.DefaultBaseURL, "Jev endpoint")
	keyEnv := fs.String("key-env", decide.DefaultKeyEnv, "environment variable holding the key; never a file, never argv")
	noJev := fs.Bool("no-jev", false, "answer from the table alone: no key, no network, deterministic")
	usagePath := fs.String("usage", "", "append what a provider call spent to this usage TSV")
	floor := fs.Float64("floor", decide.DefaultFloor, "confidence floor; below it the pick is a suggestion, never an authorization")
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	observeVerbFlags("pick", fs)
	if err := fs.Parse(args); err != nil {
		if answerHelp(err, stdout, "pick") {
			return 0
		}
		return refuse(stderr, "PICK", "bad-flags", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, "PICK", "bad-flags", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if err := decide.ValidFloor(*floor); err != nil {
		return refuse(stderr, "PICK", "bad-floor",
			fmt.Sprintf("--floor %v is not a confidence; it wants a number between 0 and 1, such as --floor 0.9", *floor))
	}
	if set["lines"] {
		if set["kind"] {
			return refuse(stderr, "PICK", "bad-flags", "--lines and --kind both name what to pick; give one or the other, refusing to guess which")
		}
		if strings.TrimSpace(*lines) == "" {
			return refuse(stderr, "PICK", "bad-arguments", "--lines names the file of kinds; it must not be empty")
		}
		return runPickLines(*lines, *tablePath, stdout, stderr)
	}
	if strings.TrimSpace(*kind) == "" {
		return refuse(stderr, "PICK", "bad-arguments", "--kind is required; refusing to guess the kind. Pass --kind <kind>, or --lines <file> to pick a list")
	}
	if !decide.KnownKind(*kind) {
		return refuse(stderr, "PICK", "unknown-kind", fmt.Sprintf("--kind %s is not one of %s", oneline.Field(*kind), strings.Join(decide.Kinds, ", ")))
	}
	table, err := decide.LoadPick(*tablePath)
	if err != nil {
		return refuse(stderr, "PICK", "bad-table", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	ask := !*noJev
	if ask && strings.TrimSpace(*usagePath) == "" {
		return refuse(stderr, "PICK", "no-accounting",
			"a jev call must be accounted for: --usage is missing; pass --usage ./usage.tsv, or --no-jev to answer from the table alone with no call to account for")
	}
	var res decide.PickResult
	if ask {
		client, err := deciderOpener(*baseURL, *keyEnv)
		if err != nil {
			return refuse(stderr, "PICK", "no-key", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		res, err = decide.PickJev(context.Background(), client, table, *kind)
		if err != nil {
			return refuse(stderr, "PICK", "no-pick", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		if err := appendPickUsage(*usagePath, *kind, res, stderr); err != nil {
			return refuse(stderr, "PICK", "bad-record", "usage: "+oneline.Cap(err.Error(), oneline.TailBytes))
		}
	} else {
		res, err = decide.PickWithTable(table, *kind)
		if err != nil {
			return refuse(stderr, "PICK", "no-pick", oneline.Cap(err.Error(), oneline.TailBytes))
		}
	}
	fmt.Fprintln(stdout, res.Line())
	if res.HasConfidence && res.Confidence < *floor {
		return 3
	}
	return 0
}

// runPickLines picks every kind in a file, one per line, from the table alone:
// deterministic, no call, no confidence. It prints one PICK line per kind in
// file order, then the total. A line whose kind the table does not hold is a
// refusal naming the line, never a guess.
func runPickLines(path, tablePath string, stdout, stderr io.Writer) int {
	table, err := decide.LoadPick(tablePath)
	if err != nil {
		return refuse(stderr, "PICK", "bad-table", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return refuse(stderr, "PICK", "bad-lines", fmt.Sprintf("cannot read the kinds file: %s", oneline.Err(err)))
	}
	count := 0
	for i, line := range strings.Split(string(raw), "\n") {
		kind := strings.TrimSpace(line)
		if kind == "" {
			continue
		}
		res, err := decide.PickWithTable(table, kind)
		if err != nil {
			return refuse(stderr, "PICK", "unknown-kind",
				fmt.Sprintf("line %d names kind %s, which has no pick in the table; refusing to guess", i+1, oneline.Field(kind)))
		}
		fmt.Fprintln(stdout, res.Line())
		count++
	}
	if count == 0 {
		return refuse(stderr, "PICK", "bad-lines", "the kinds file held no kinds; refusing to guess")
	}
	fmt.Fprintf(stdout, "PICK TOTAL kinds=%d version=%d\n", count, table.Version)
	return 0
}

// appendPickUsage writes one usage row for the provider call a pick made, in
// the fleet's own columns. A failed call is a row too, with its cost unknown
// (a dash) and its rc naming the failure -- never a zero and never no row.
func appendPickUsage(path, kind string, res decide.PickResult, stderr io.Writer) error {
	if strings.TrimSpace(path) == "" || res.Calls == 0 {
		return nil
	}
	row := swarm.UsageRow{
		"job":      kind,
		"attempt":  "1",
		"started":  now().UTC().Format(time.RFC3339),
		"ended":    now().UTC().Format(time.RFC3339),
		"rc":       "0",
		"provider": usageProvider,
		"model":    decide.DefaultModel,
	}
	if res.Failed {
		row["rc"] = "2"
	}
	if res.Usage.HasInput {
		row["tokens_in"] = strconv.Itoa(res.Usage.InputTokens)
	}
	if res.Usage.HasOutput {
		row["tokens_out"] = strconv.Itoa(res.Usage.OutputTokens)
	}
	return swarm.AppendCardUsage(path, row)
}
