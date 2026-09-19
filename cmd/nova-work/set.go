package main

// `nova-work set check` is the missing half of the reader. `plan check` reads
// `(:plan ...)`, the expander's form; every work set on the bench is written in the
// OTHER one, `(work-set "id" ... :units ((unit ...)))`, so the only checker we had
// was blind to the only file we write. A dogfood run of `ask` on
// work/pitstop-2026-09-17.lisp found the gap: 79 real units, none of them readable
// by `plan check`.
//
// set check reads that form with the SAME bounded reader -- three bounds, no eval,
// a dispatch macro refused at the byte that owes it -- and then validates the
// CONTENT, which is where a work set actually goes wrong: an id written twice, a
// :needs that names a unit nobody defined, a cycle, an owner no registry of minds
// names, a lane no lanes file names, a deadline that is not an instant.
//
// The two exit codes say different things and are never blurred. Exit 2 is a
// refusal: this file could not be read at all, and one line says why. Exit 1 is
// findings: the file was read whole and its content is wrong, one SET line per
// finding, every rule run over every unit in one pass. A checker that stopped at
// the first finding would cost the caller one round trip per defect, so it does not
// stop.
//
// --ready is the other half: the mechanical ready set, derived from the language
// rather than maintained by hand. A unit is done when it says so -- `:done`, or a
// `:status` of closed, done, landed or merged -- or when --done names it; ready
// when it is not done and every need is done. Nothing here asks anyone's judgment,
// which is the point: the coordinator asks the file what can be pulled, and the
// file answers the same way every time.
//
// Every path comes from a flag. --minds and --lanes have no defaults and no
// discovery: without them those two rules are OFF rather than run against a guessed
// file, and the SET OK line still prints the counts.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/jobs"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// cmdSet dispatches the set verbs. There is one, check, and an unknown sub-verb is
// a refusal naming the one that exists rather than a banner.
func cmdSet(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "check" {
		return refuse(stderr, " set", "the verb is set check --file <path.lisp> [--minds <file>] [--lanes <file.tsv>] [--done <ids>] [--ready]")
	}
	return cmdSetCheck(args[1:], stdout, stderr)
}

func cmdSetCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("set check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	file := fs.String("file", "", "the work set to read (required)")
	mindsFile := fs.String("minds", "", "the registry of minds an :owner must name")
	lanesFile := fs.String("lanes", "", "the lanes file a :lane must name")
	doneList := fs.String("done", "", "comma-separated unit ids that are done")
	ready := fs.Bool("ready", false, "print the mechanical ready set")
	def := worklang.DefaultLimits()
	maxBytes := fs.Int("max-bytes", def.MaxBytes, "byte ceiling")
	maxDepth := fs.Int("max-depth", def.MaxDepth, "nesting depth ceiling")
	maxNodes := fs.Int("max-nodes", def.MaxNodes, "atom ceiling")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " set check", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " set check", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*file) == "" {
		return refuse(stderr, " set check", "--file is required; refusing to guess")
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return refuse(stderr, " set check", oneline.Err(err))
	}
	limits := worklang.Limits{MaxBytes: *maxBytes, MaxDepth: *maxDepth, MaxNodes: *maxNodes}
	ws, err := worklang.ParseWorkSet(*file, data, limits)
	if err != nil {
		return refuse(stderr, " set check", oneline.Err(err))
	}

	opts := worklang.Options{Done: splitIDs(*doneList)}
	if strings.TrimSpace(*mindsFile) != "" {
		names, err := readNames(*mindsFile)
		if err != nil {
			return refuse(stderr, " set check", oneline.Err(err))
		}
		opts.Minds, opts.MindsFile = fold(names), *mindsFile
	}
	if strings.TrimSpace(*lanesFile) != "" {
		names, err := readLanes(*lanesFile)
		if err != nil {
			return refuse(stderr, " set check", oneline.Err(err))
		}
		opts.Lanes, opts.LanesFile = names, *lanesFile
	}

	findings, counts := ws.Check(opts)
	for _, f := range findings {
		writeFinding(stdout, f)
	}
	if *ready {
		writeReady(stdout, ws, opts.Done)
	}
	// The summary prints whether or not there were findings: a caller that wants
	// the shape of the set gets it in the same breath as the defects, and
	// units = ready + blocked + done closes the arithmetic.
	fmt.Fprintf(stdout, "SET OK units=%d ready=%d blocked=%d owned=%d\n",
		counts.Units, counts.Ready, counts.Blocked, counts.Owned)
	if len(findings) > 0 {
		return 1
	}
	return 0
}

// writeReady prints the ready set, each unit carrying the ADMISSION verdict
// beside its readiness. The two are different questions and the line says both:
// `ready` is whether a unit's needs are closed, which the language answers;
// `admit` is whether its resource vector is free, which the kernel answers
// (internal/jobs, SPEC-JOBS section 9).
//
// Asking only the first is what let the loop hand out two cards in one lane and
// two units one file. The pass runs the ready units through one Admission in
// written order, so the units marked `go` are a set that may run TOGETHER --
// one live unit per lane (A6), no two intersecting :writes (A7) -- rather than
// a list each of which could run if the others did not.
//
// A9 is why a held unit does not stop the ones after it: the pass never waits
// and never stops, so a unit whose own vector is free goes whatever the unit
// before it is doing. The line names what held a unit and who holds it, so the
// remedy is a reading rather than a hunt.
func writeReady(stdout io.Writer, ws *worklang.WorkSet, done map[string]bool) {
	// The authority counts no cpu or memory here: `set check` reads a file and
	// knows no bench. What it CAN account for is what the file itself names --
	// the lanes, whose capacity is 1 by A6, and the writes -- so that is what
	// it admits over, and a unit naming a counted resource is reported as held
	// on it rather than silently granted.
	a := jobs.New(nil)
	defer a.Close()
	units := ws.Ready(done)
	for i, ad := range a.Admit(worklang.Requests(units)) {
		u := units[i]
		fmt.Fprintf(stdout, "SET READY unit=%s owner=%s lane=%s deadline=%s",
			oneline.Field(u.ID), field(u.Owner()), field(u.Lane()), field(u.Deadline()))
		if ad.Go {
			fmt.Fprint(stdout, " admit=go on=- by=-\n")
			continue
		}
		fmt.Fprintf(stdout, " admit=held on=%s by=%s\n", field(ad.On()), field(ad.By()))
	}
}

// writeFinding prints one finding as one line: the rule as a bare word a caller
// greps, the unit that owes it, the rule's own field, the byte it lives at and the
// remedy. Absent fields print `-` rather than vanishing, so every SET line of one
// run has the same shape.
func writeFinding(stdout io.Writer, f worklang.Finding) {
	fmt.Fprintf(stdout, "SET %s unit=%s", oneline.Field(f.Rule), field(f.Unit))
	if f.Key != "" {
		fmt.Fprintf(stdout, " %s=%s", oneline.Field(f.Key), oneline.Field(f.Value))
	}
	fmt.Fprintf(stdout, " at=%d remedy=%s\n", f.Offset, oneline.Quote(f.Remedy))
}

// field is one value of a SET line: escaped, or `-` when there is none.
func field(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return oneline.Field(s)
}

// splitIDs reads --done: a comma-separated list of unit ids, with blanks dropped. A
// nil map is no list at all, which turns nothing off -- the document's own :done
// and :status still say what is finished.
func splitIDs(list string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(list, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out[part] = true
		}
	}
	return out
}

// fold lowers a set of names, because a work set writes a friend's name the way a
// person does (Emma) and a registry writes a mind's the way a machine does (emma).
// The fold is the only liberty taken: a name neither spelling holds is a finding.
func fold(names []string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			out[strings.ToLower(n)] = true
		}
	}
	return out
}

// mindsFile is the JSON a registry of minds is written as. Both shapes are read
// because both exist and both are registries of who can be given work: `minds` is
// the decide lane's ladder (internal/decide/registry.json), `participants` is the
// bus's roster. The shape is READ, not guessed at from the file's name.
type mindsFile struct {
	Minds        []struct{ Name string } `json:"minds"`
	Participants []struct{ Name string } `json:"participants"`
}

// readNames reads --minds in either form. A file whose first byte is `{` is one of
// the two JSON registries; anything else is a plain list, one name per line, `#` a
// comment and a tab cutting the name from whatever follows it. A JSON file that
// holds neither key is refused naming both rather than read as an empty registry:
// an empty registry would fail every owner, which is a worse answer than a refusal.
func readNames(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if firstByte(raw) != '{' {
		return readTable(raw), nil
	}
	var f mindsFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s is not a registry of minds: %w", path, err)
	}
	var out []string
	for _, m := range f.Minds {
		out = append(out, m.Name)
	}
	for _, p := range f.Participants {
		out = append(out, p.Name)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s names no minds: a registry carries a \"minds\" or a \"participants\" list", path)
	}
	return out, nil
}

// readLanes reads --lanes: the lanes file SPEC-WORKLANG already fixes, `<name>\t<path
// prefixes>` per line, `#` a comment and a blank line skipped (the same table
// nova-pulse fill holds one live card per). Unlike fill's, an unreadable file here
// is an error rather than an empty table: fill launches a card without a lane, but a
// checker with no lanes would report every lane as unknown.
func readLanes(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, name := range readTable(raw) {
		out[name] = true
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s names no lanes; a lanes file is <name>\\t<path prefixes> per line", path)
	}
	return out, nil
}

// readTable is the first column of a tab-separated table: one name per line, `#` a
// comment, blanks skipped.
func readTable(raw []byte) []string {
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, _, _ := strings.Cut(line, "\t")
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// firstByte is the first byte that is not whitespace and not inside a `;` or `#`
// line comment: what says which of the two grammars a file is written in, read
// rather than guessed at from its name.
func firstByte(raw []byte) byte {
	for i := 0; i < len(raw); i++ {
		switch c := raw[i]; {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f':
		case c == ';' || c == '#':
			for i < len(raw) && raw[i] != '\n' {
				i++
			}
		default:
			return c
		}
	}
	return 0
}
