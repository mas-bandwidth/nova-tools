package digest

import "regexp"

// The line grammar, one expression per line form Render prints. <T> is
// TimeLayout; no value holds a space. TestDigestLineGrammar checks every
// line of every test output against exactly one of them.
var (
	tPat = `\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z`

	Grammar = map[string]*regexp.Regexp{
		"header":        regexp.MustCompile(`^digest since=` + tPat + ` until=` + tPat + ` repos=(-|\S+)$`),
		"landed:":       regexp.MustCompile(`^landed:$`),
		"holds:":        regexp.MustCompile(`^holds:$`),
		"reads:":        regexp.MustCompile(`^reads:$`),
		"landed-stream": regexp.MustCompile(`^landed at=` + tPat + ` repo=\S+ pr=\d+ merge_sha=[0-9a-f-]+ members=\d+ tasks=\d+ by=\S+$`),
		"landed-batch":  regexp.MustCompile(`^landed at=` + tPat + ` repo=\S+ base=\S+ batch=\S+ train_head=[0-9a-f-]+$`),
		"landed-close":  regexp.MustCompile(`^landed at=` + tPat + ` close=\S+#\d+ by=\S+ tasks=\d+$`),
		"hold":          regexp.MustCompile(`^hold at=` + tPat + ` repo=\S+ pr=\d+ head=[0-9a-f]{8} holder=\S+ route=(fix|close|recut) state=(open|answered|\?)$`),
		"read":          regexp.MustCompile(`^read at=` + tPat + ` repo=\S+ pr=\d+ head=[0-9a-f]+ who=\S+ score=(\d+|\?)$`),
		"trimmed":       regexp.MustCompile(`^(landed|hold|read) TRIMMED source \S+ (max-deleted|first)=\d+-\d+$`),
		"none":          regexp.MustCompile(`^(landed|hold|read) none$`),
	}
)
