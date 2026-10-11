// check.go holds the check verb: its flags, its run and the helpers only it uses.

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/memindex"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

func cmdCheck(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "render the retrieval result as JSON")
	whole := fs.Bool("whole", false, "print each hit's whole paragraph instead of its 120-byte snippet, capped and marked when cut")
	rf := addRootFlags(fs)
	channels := fs.String("channels", "", "comma-separated retrieval channels (required)")
	k := fs.Int("k", 0, "receipts per candidate, positive (required)")
	given, pos, ok := parse(fs, args, stderr, "root", "channels", "k")
	if given == nil {
		return 2
	}
	bad := !ok
	if given["k"] && !checkK(*k, "check", stderr) {
		bad = true
	}
	var names []string
	if given["channels"] {
		if names, ok = channelNames(*channels, "check", stderr); !ok {
			bad = true
		}
	}
	if len(pos) != 1 {
		refuse(stderr, " check", "name exactly one candidate file, or - for stdin; refusing to guess")
		bad = true
	}
	if bad {
		return 2
	}

	src := stdin
	name := "-"
	if pos[0] != "-" {
		f, err := os.Open(pos[0])
		if err != nil {
			return refuse(stderr, " check", oneline.Err(err))
		}
		defer f.Close()
		src, name = f, pos[0]
	}
	raw, err := io.ReadAll(src)
	if err != nil {
		return refuse(stderr, " check", fmt.Sprintf("reading %s: %s", oneline.Escape(name), oneline.Err(err)))
	}
	var candidates []string
	// The same line-ending normalization memindex.Build does before its own
	// blank-line split: a CRLF candidate file must chunk into the paragraphs
	// its LF twin does, or check queries one giant blob against a corpus that
	// was indexed paragraph by paragraph.
	for _, p := range strings.Split(memindex.NormalizeNewlines(string(raw)), "\n\n") {
		if len(memindex.Tokenize(p)) >= memindex.MinTerms {
			candidates = append(candidates, p)
		}
	}
	if len(candidates) == 0 {
		// Unusable input, not a verdict: a run over nothing must never print
		// a green that a caller reads as "nothing was already known".
		return refuse(stderr, " check", fmt.Sprintf("%s holds no candidate paragraph of at least %d terms; nothing to check", oneline.Escape(name), memindex.MinTerms))
	}

	c, _, ok := rf.build("check", stderr)
	if !ok {
		return 2
	}
	chans := newChannels(c, names)

	result := retrievalResult{Verb: "check", Source: name, K: *k, Channels: chanNames(chans), Files: len(c.Files), Chunks: len(c.Chunks), Whole: *whole, Calibration: calibrationHits(c, chans), Notes: []string{noteLexical, "this verb asserts nothing and never exits 1: it hands you k receipts and the verdict stays yours", "a hit in a dated log class is evidence the event was recorded, not that the lesson was banked — the class on each receipt is the distinction"}}
	for _, cand := range candidates {
		result.Candidates = append(result.Candidates, retrievalCandidate{Text: memindex.Truncate(strings.TrimSpace(cand), 100), Hits: receiptHits(memindex.Retrieve(c, chans, cand, *k))})
	}
	result.render(stdout, *asJSON)
	return 0
}
