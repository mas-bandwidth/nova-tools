// search.go holds the search verb: its flags, its run and the helpers only it uses.

package main

import (
	"flag"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/memindex"
)

func cmdSearch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "render the retrieval result as JSON")
	whole := fs.Bool("whole", false, "print each hit's whole paragraph instead of its 120-byte snippet, capped and marked when cut")
	rf := addRootFlags(fs)
	channels := fs.String("channels", "", "comma-separated retrieval channels (required)")
	k := fs.Int("k", 0, "receipts per query, positive (required)")
	given, pos, ok := parse(fs, args, stderr, "root", "channels", "k")
	if given == nil {
		return 2
	}
	// One run, every reason. A value is only judged when the flag carrying it
	// was given, so a missing flag says one thing and not two.
	bad := !ok
	if given["k"] && !checkK(*k, "search", stderr) {
		bad = true
	}
	var names []string
	if given["channels"] {
		if names, ok = channelNames(*channels, "search", stderr); !ok {
			bad = true
		}
	}
	if len(pos) == 0 {
		refuse(stderr, " search", "no query words given; refusing to guess")
		bad = true
	}
	if bad {
		return 2
	}
	query := strings.Join(pos, " ")
	c, _, ok := rf.build("search", stderr)
	if !ok {
		return 2
	}
	chans := newChannels(c, names)
	hits := receiptHits(memindex.Retrieve(c, chans, query, *k))
	result := retrievalResult{Verb: "search", Query: query, K: *k, Channels: chanNames(chans), Files: len(c.Files), Chunks: len(c.Chunks), Whole: *whole, Calibration: calibrationHits(c, chans), Candidates: []retrievalCandidate{{Hits: hits}}, Notes: []string{noteLexical}}
	result.render(stdout, *asJSON)
	return 0
}
