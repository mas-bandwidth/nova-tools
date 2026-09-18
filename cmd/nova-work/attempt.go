package main

// `nova-work attempt` is the WRITE side of SPEC-WORKLANG's A3 and A4. The
// amendment landed both as readers: the grammar knew what an attempt record is
// and what `uncertain` means, and the only way a real work set could grow one
// was a person typing s-expressions into a document by hand. The first hand
// edit that mis-nests a paren costs the whole file, because the reader refuses
// a set whole rather than half-reading it.
//
//	attempt record  files one attempt on one unit and moves the unit's :state
//	attempt list    reads the attempts back, one line each
//
// record edits the file IN PLACE and splices bytes: every byte outside the one
// edited unit comes back identical, and inside the unit every byte outside the
// edited key does too. A work set is a person's document -- its comments, its
// blank lines and the column its keys line up at are the document -- so the
// writer never re-renders what it is not touching.
//
// Two refusals are the point of the verb rather than an edge of it. An outcome
// that claims to have ended without proving it is refused naming the word to
// write instead (A3: `uncertain`), and a unit already closed, refused or
// abandoned takes no further attempt (A2: a reopened piece of work is a NEW id
// carrying :was). Both are made before a byte is written.

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// cmdAttempt dispatches the attempt verbs. An unknown sub-verb names the two
// that exist rather than printing the banner.
func cmdAttempt(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, " attempt", "the verbs are attempt record and attempt list")
	}
	switch args[0] {
	case "record":
		return cmdAttemptRecord(args[1:], stdout, stderr)
	case "list":
		return cmdAttemptList(args[1:], stdout, stderr)
	default:
		return refuse(stderr, " attempt", fmt.Sprintf(
			"no attempt verb %q; the verbs are attempt record and attempt list", args[0]))
	}
}

func cmdAttemptRecord(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("attempt record", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	file := fs.String("file", "", "the work set to edit in place (required)")
	unit := fs.String("unit", "", "the unit the attempt was made on (required)")
	by := fs.String("by", "", "the mind that made it: the :owner of the record (required)")
	rung := fs.String("rung", "", "the rung that ran it; --by when absent")
	outcome := fs.String("outcome", "", "ok | failed | uncertain (also green, red, refused, abandoned)")
	proof := fs.String("proof", "", "the termination proof: a path, a sha or a url")
	usage := fs.String("usage", "", "the usage TSV this attempt spent into")
	pr := fs.Int("pr", 0, "the PR this attempt produced")
	started := fs.String("started", "", "when it started, as 2026-09-18T12:00:00Z; this run's clock when absent")
	limits, bounds := boundFlags(fs)
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " attempt record", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " attempt record", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	for name, value := range map[string]string{"--file": *file, "--unit": *unit, "--by": *by, "--outcome": *outcome} {
		if strings.TrimSpace(value) == "" {
			return refuse(stderr, " attempt record", name+" is required; refusing to guess")
		}
	}
	stamp := strings.TrimSpace(*started)
	if stamp == "" {
		stamp = time.Now().UTC().Format(time.RFC3339)
	} else if _, err := worklang.ParseStamp(stamp); err != nil {
		return refuse(stderr, " attempt record", oneline.Err(err))
	}
	a := worklang.NewAttempt{
		Rung: strings.TrimSpace(*rung), Owner: strings.TrimSpace(*by),
		Started: stamp, Outcome: *outcome, Usage: strings.TrimSpace(*usage), PR: *pr,
	}
	if a.Rung == "" {
		a.Rung = a.Owner
	}
	if p, ok := worklang.ProofOf(*proof); ok {
		a.Proof = p
	}

	// The lock is held across the read, the edit and the write, because those
	// three are one operation: two minds recording attempts on one set at once
	// would each splice into the bytes the other had already replaced.
	release, err := lockSet(*file)
	if err != nil {
		return refuse(stderr, " attempt record", oneline.Err(err))
	}
	defer release()

	data, err := os.ReadFile(*file)
	if err != nil {
		return refuse(stderr, " attempt record", oneline.Err(err))
	}
	out, rec, err := worklang.Record(*file, data, *unit, a, bounds(limits))
	if err != nil {
		return refuse(stderr, " attempt record", oneline.Err(err))
	}
	if err := writeInPlace(*file, out); err != nil {
		return refuse(stderr, " attempt record", oneline.Err(err))
	}
	how := "appended"
	if rec.Closed {
		how = "closed"
	}
	fmt.Fprintf(stdout, "ATTEMPT %s unit=%s n=%d rung=%s started=%s outcome=%s state=%s proof=%s\n",
		oneline.Field(how), oneline.Field(*unit), rec.N, field(rec.Rung), field(rec.Started),
		oneline.Field(rec.Outcome), oneline.Field(rec.State), field(a.Proof.Value))
	return 0
}

func cmdAttemptList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("attempt list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	file := fs.String("file", "", "the work set to read (required)")
	unit := fs.String("unit", "", "the unit whose attempts to read (required)")
	limits, bounds := boundFlags(fs)
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " attempt list", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if fs.NArg() > 0 {
		return refuse(stderr, " attempt list", fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	if strings.TrimSpace(*file) == "" || strings.TrimSpace(*unit) == "" {
		return refuse(stderr, " attempt list", "--file and --unit are both required; refusing to guess")
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return refuse(stderr, " attempt list", oneline.Err(err))
	}
	ws, err := worklang.ParseWorkSet(*file, data, bounds(limits))
	if err != nil {
		return refuse(stderr, " attempt list", oneline.Err(err))
	}
	u, ok := ws.Unit(*unit)
	if !ok {
		return refuse(stderr, " attempt list", fmt.Sprintf("the set holds no unit %q", *unit))
	}
	attempts := u.Attempts()
	for _, a := range attempts {
		fmt.Fprintf(stdout, "ATTEMPT unit=%s n=%d rung=%s owner=%s started=%s outcome=%s proof=%s\n",
			oneline.Field(u.ID), a.N, field(a.Rung), field(a.Owner), field(a.Started),
			field(a.Outcome), field(proofText(a)))
	}
	fmt.Fprintf(stdout, "ATTEMPT OK unit=%s attempts=%d state=%s\n",
		oneline.Field(u.ID), len(attempts), oneline.Field(u.State()))
	return 0
}

// proofText renders an attempt's proof for the line: the value when the record
// is written in the (:kind :k :value "v") shape, and the raw text otherwise, so
// a record a person wrote by hand still reads back.
func proofText(a worklang.Attempt) string {
	if !a.HasProof {
		return ""
	}
	if a.Proof.Kind == worklang.List {
		var kind, value string
		for i := 0; i+1 < len(a.Proof.List); i++ {
			switch {
			case a.Proof.List[i].IsKeyword("kind"):
				kind = a.Proof.List[i+1].Value
			case a.Proof.List[i].IsKeyword("value"):
				value = a.Proof.List[i+1].Value
			}
		}
		if kind != "" && value != "" {
			return kind + ":" + value
		}
		if value != "" {
			return value
		}
		return "yes"
	}
	if text := a.Proof.Text(); text != "" {
		return text
	}
	return a.Proof.Value
}

// boundFlags declares the reader's three bounds on a flag set and hands back a
// reader of them, so every verb that opens a work set spells them once.
func boundFlags(fs *flag.FlagSet) (*[3]int, func(*[3]int) worklang.Limits) {
	def := worklang.DefaultLimits()
	var v [3]int
	fs.IntVar(&v[0], "max-bytes", def.MaxBytes, "byte ceiling")
	fs.IntVar(&v[1], "max-depth", def.MaxDepth, "nesting depth ceiling")
	fs.IntVar(&v[2], "max-nodes", def.MaxNodes, "atom ceiling")
	return &v, func(p *[3]int) worklang.Limits {
		return worklang.Limits{MaxBytes: p[0], MaxDepth: p[1], MaxNodes: p[2]}
	}
}

// writeInPlace replaces the document through a temporary file beside it and one
// rename, so a reader of the set never sees a half-written one and a write that
// dies leaves the original whole. The file keeps the mode it had.
func writeInPlace(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp := path + ".nova-work.tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// lockSet takes the work set's own lock: one writer at a time over one
// document. There is no queue lock to join here -- internal/jobs' Admission is
// an in-process authority over capacity, not a durable lock over a file -- so
// the lock is a directory beside the set, created exclusively by the operating
// system and removed on the way out.
//
// A lock that is already held is a REFUSAL naming the path, never a wait: a
// waiter here would be the barrier A9 removes, and a caller that wants to try
// again asks again.
func lockSet(path string) (func(), error) {
	lock := path + ".lock"
	if err := os.Mkdir(lock, 0o755); err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf(
				"%s is locked by another writer; one writer at a time over one work set. Remove it with: rmdir %s (after checking nothing is mid-edit)",
				path, lock)
		}
		return nil, err
	}
	return func() { os.Remove(lock) }, nil
}
