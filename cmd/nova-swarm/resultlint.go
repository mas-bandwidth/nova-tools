package main

import (
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// `nova-swarm result-lint` is the harvest's question before it counts a DONE (issue #2917):
// is line 2 of this RESULT.md a disposition in the grammar, not the template copied, and not
// a `BLOCKED head=<base>` that blocked on nothing? The rules and the record they came from
// are in internal/swarm/resultlint.go.
//
//	RESULT-LINT OK   result=<path> disposition=<DONE|ABSTAIN|BLOCKED|RED|GAP|CONFORMS> done=<yes|no>
//	RESULT-LINT FLAG result=<path> check=<check> line2=<line 2, capped> remedy=<...>
//
// Exit 0 when every result is clean, 1 when any is flagged, 2 on a usage error or when any
// file was unreadable (the others are still linted, one line each). A flagged result is never counted as DONE; a caller that does not know
// this verb (an older binary exits 2 on it) keeps its own count, so a bench is never
// stopped by a binary it has not been handed yet.

// resultLintReadBytes bounds what is read of one RESULT.md. Only lines 1 and 2 are
// linted; a RESULT that folds a REPORT under them is still read in one go.
const resultLintReadBytes = 64 << 10

func cmdResultLint(args []string, stdout, stderr io.Writer) int {
	f := newFlags("result-lint")
	var results []string
	f.fs.Var(stringListValue{&results}, "result", "")
	baseSHA := f.fs.String("base-sha", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	if len(results) == 0 {
		f.add("--result is required; it wants the path to a card's RESULT.md, and may be given more than once; refusing to guess")
	}
	if f.refused(stderr) {
		return 2
	}
	code := 0
	for _, path := range results {
		fh, err := os.Open(path)
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm result-lint: --result wants a readable RESULT.md: %s\n", oneline.Err(err))
			code = 2
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(fh, resultLintReadBytes))
		fh.Close()
		if err != nil {
			fmt.Fprintf(stderr, "nova-swarm result-lint: reading %s: %s\n", oneline.Field(path), oneline.Err(err))
			code = 2
			continue
		}
		r := swarm.LintResult(raw, *baseSHA)
		if len(r.Findings) == 0 {
			if r.CountsAsDone() {
				fmt.Fprintf(stdout, "RESULT-LINT OK result=%s disposition=%s done=yes\n", oneline.Field(path), oneline.Field(r.Disposition))
			} else {
				fmt.Fprintf(stdout, "RESULT-LINT OK result=%s disposition=%s done=no\n", oneline.Field(path), oneline.Field(r.Disposition))
			}
			continue
		}
		if code == 0 {
			code = 1
		}
		for _, fd := range r.Findings {
			fmt.Fprintf(stdout, "RESULT-LINT FLAG result=%s check=%s line2=%s remedy=%s\n",
				oneline.Field(path), oneline.Field(fd.Check),
				oneline.Field(oneline.Cap(fd.Excerpt, oneline.TailBytes)),
				oneline.Escape(swarm.ResultLintRemedies[fd.Check]))
		}
	}
	return code
}
