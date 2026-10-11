// eval.go holds the eval verb: its flags, its run and the helpers only it uses.

package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/memindex"
	"github.com/mas-bandwidth/nova-tools/pkg/bounded"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

func cmdEval(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	rf := addRootFlags(fs)
	channels := fs.String("channels", "", "comma-separated retrieval channels (required)")
	k := fs.Int("k", 0, "receipts per query, positive (required)")
	floor := fs.Float64("floor", 0, "minimum recall@k in (0,1] (required)")
	failMax := fs.Int("fail-max", bounded.Default, "MISS lines to print before one MORE line stands for the rest; 0 prints all")
	asJSON := fs.Bool("json", false, "print the result as one JSON object instead of lines")
	given, pos, ok := parse(fs, args, stderr, "root", "channels", "k", "floor")
	if given == nil {
		return 2
	}
	bad := !ok
	if given["k"] && !checkK(*k, "eval", stderr) {
		bad = true
	}
	var names []string
	if given["channels"] {
		if names, ok = channelNames(*channels, "eval", stderr); !ok {
			bad = true
		}
	}
	if given["floor"] && (math.IsNaN(*floor) || math.IsInf(*floor, 0) || *floor <= 0 || *floor > 1) {
		refuse(stderr, " eval", fmt.Sprintf("--floor must be in (0,1] (got %g); a harness that cannot fail is not a measurement", *floor))
		bad = true
	}
	if given["fail-max"] && *failMax < 0 {
		refuse(stderr, " eval", fmt.Sprintf("--fail-max must be a line ceiling of zero or more (got %d); 0 means print them all", *failMax))
		bad = true
	}
	if len(pos) != 1 {
		refuse(stderr, " eval", "name exactly one gold file; refusing to guess")
		bad = true
	}
	if bad {
		return 2
	}
	rows, err := readGold(pos[0])
	if err != nil {
		return refuse(stderr, " eval", oneline.Err(err))
	}

	c, _, ok := rf.build("eval", stderr)
	if !ok {
		return 2
	}
	chans := newChannels(c, names)

	// THE HITS ARE A COUNT: a line per passing row would say "this worked" five hundred
	// times beside the summary that already carries the number. Only the misses -- the
	// rows a reader can act on -- are listed, capped like every other listing here.
	listing := stdout
	if *asJSON {
		listing = io.Discard
	}
	o := result("eval")
	misses := bounded.Capped(listing, *failMax, "EVAL", "miss", failMaxRemedy)
	hits := 0
	var mrr float64
	for _, row := range rows {
		rank := 0
		for i, h := range memindex.Retrieve(c, chans, row.query, *k) {
			for _, e := range row.expected {
				if strings.Contains(h.File, e) {
					rank = i + 1
					break
				}
			}
			if rank != 0 {
				break
			}
		}
		if rank != 0 {
			hits++
			mrr += 1.0 / float64(rank)
			continue
		}
		misses.Line(fmt.Sprintf("EVAL MISS query=%s expected=%s",
			oneline.Field(oneline.Cap(row.query, oneline.TailBytes)),
			oneline.Field(oneline.Cap(strings.Join(row.expected, ","), oneline.TailBytes))))
		o.Item("miss", "query", tool.Text(row.query), "expected", strings.Join(row.expected, ","))
	}
	misses.More()
	recall := float64(hits) / float64(len(rows))
	mrr /= float64(len(rows))
	if *asJSON {
		o.Fact("k", *k).Fact("recall", recall).Fact("floor", *floor).Fact("rows", len(rows)).Fact("hits", hits).
			Fact("misses", misses.Total()).Fact("mrr", mrr).Fact("channels", chanNames(chans))
		if recall < *floor {
			o.Status, o.Exit = tool.Failed, 1
			o.Why = []string{fmt.Sprintf("recall@%d=%.3f is below the floor %.3f", *k, recall, *floor)}
		}
		return o.Cap(*failMax).Render(stdout, true)
	}
	if recall < *floor {
		fmt.Fprintf(stderr, "EVAL FAIL recall@%d=%.3f below floor %.3f (%d/%d, misses=%d shown=%d, mrr=%.3f, channels=%s)\n",
			*k, recall, *floor, hits, len(rows), misses.Total(), misses.Shown(), mrr, chanNames(chans))
		return 1
	}
	fmt.Fprintf(stdout, "EVAL OK recall@%d=%.3f floor=%.3f rows=%d hits=%d misses=%d shown=%d mrr=%.3f channels=%s\n",
		*k, recall, *floor, len(rows), hits, misses.Total(), misses.Shown(), mrr, chanNames(chans))
	return 0
}

// goldRow is one known-answer case: a query, and the paths any one of which
// counts as the right answer.
type goldRow struct {
	query    string
	expected []string
}

// readGold parses the known-answer file: `query<TAB>path[,path]` per line,
// `#` comments and blank lines ignored. Every malformed shape is an error and
// never a skipped row — a row silently dropped, or a row that can never match
// because its expectation side is empty, moves the measured recall without
// moving anything the reader can see.
func readGold(name string) ([]goldRow, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows []goldRow
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		// Trim line endings only. Trimming all whitespace first would eat the
		// TAB on a row whose query or expectation side is empty, and those two
		// malformed shapes would then report as "no TAB" — the wrong finding,
		// and the reason the empty-side rows below are refused at all.
		line := strings.TrimRight(sc.Text(), "\r\n")
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		q, expects, found := strings.Cut(line, "\t")
		if !found {
			return nil, fmt.Errorf("%s line %d has no TAB (format: query<TAB>expected[,expected])", name, lineNo)
		}
		q = strings.TrimSpace(q)
		if q == "" {
			return nil, fmt.Errorf("%s line %d has an empty query", name, lineNo)
		}
		var expected []string
		for _, e := range strings.Split(expects, ",") {
			if e = strings.TrimSpace(e); e != "" {
				expected = append(expected, e)
			}
		}
		if len(expected) == 0 {
			return nil, fmt.Errorf("%s line %d names no expected path — a row that can never hit is a silent drag on recall, not a case", name, lineNo)
		}
		rows = append(rows, goldRow{query: q, expected: expected})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s holds zero rows — a broken harness, not a pass", name)
	}
	return rows, nil
}
