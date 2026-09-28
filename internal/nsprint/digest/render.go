package digest

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
)

// TimeLayout is every <T> the digest prints: RFC 3339 UTC with milliseconds.
const TimeLayout = "2006-01-02T15:04:05.000Z"

func stamp(ms int64) string { return time.UnixMilli(ms).UTC().Format(TimeLayout) }

// field values never hold a space: an empty or spaced value prints as -.
func val(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\n") {
		return "-"
	}
	return s
}

type fact struct {
	at   int64
	line string
}

// Render prints the digest in the line grammar (grammar.go): line 1, then
// the sections landed, holds and reads, each a header, any TRIMMED line, its
// facts oldest first (ties by line), or "<key> none".
func Render(w io.Writer, d *Digest) error {
	repos := "-"
	if len(d.Repos) > 0 {
		repos = strings.Join(d.Repos, ",")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "digest since=%s until=%s repos=%s\n", stamp(d.Since.UnixMilli()), stamp(d.Until.UnixMilli()), repos)

	var landed []fact
	for _, l := range d.Landings {
		landed = append(landed, fact{l.At, fmt.Sprintf("landed at=%s repo=%s pr=%d merge_sha=%s members=%d tasks=%d by=%s",
			stamp(l.At), val(prkey.Name(l.Repo)), l.PR, val(l.MergeSHA), l.Members, l.Tasks, val(l.By))})
	}
	for _, x := range d.Batches {
		landed = append(landed, fact{x.At, fmt.Sprintf("landed at=%s repo=%s base=%s batch=%s train_head=%s",
			stamp(x.At), val(prkey.Name(x.Repo)), val(x.Base), val(x.ID), val(x.Train))})
	}
	for _, c := range d.Closes {
		landed = append(landed, fact{c.At, fmt.Sprintf("landed at=%s close=%s by=%s tasks=%d",
			stamp(c.At), val(c.Ref), val(c.By), c.Tasks)})
	}
	trims := []Trim{}
	if d.LogTrim != nil {
		trims = append(trims, *d.LogTrim)
	}
	section(&b, "landed", append(trims, d.EventTrims...), landed)

	var holds []fact
	for _, h := range d.Holds {
		holds = append(holds, fact{h.At, fmt.Sprintf("hold at=%s repo=%s pr=%d head=%s holder=%s route=%s state=%s",
			stamp(h.At), val(h.Repo), h.PR, val(h.Head), val(h.Holder), val(h.Route), val(h.State))})
	}
	section(&b, "hold", trims, holds)

	var reads []fact
	for _, r := range d.Reads {
		reads = append(reads, fact{r.At, fmt.Sprintf("read at=%s repo=%s pr=%d head=%s who=%s score=%s",
			stamp(r.At), val(r.Repo), r.PR, val(r.Head), val(r.Who), val(r.Score))})
	}
	section(&b, "read", trims, reads)
	_, err := io.WriteString(w, b.String())
	return err
}

// headers names each section's header line by its key.
var headers = map[string]string{"landed": "landed:", "hold": "holds:", "read": "reads:"}

func section(b *strings.Builder, key string, trims []Trim, facts []fact) {
	b.WriteString(headers[key] + "\n")
	for _, t := range trims {
		fmt.Fprintf(b, "%s TRIMMED source %s %s\n", key, t.Stream, t.Mark)
	}
	sort.SliceStable(facts, func(i, j int) bool {
		if facts[i].at != facts[j].at {
			return facts[i].at < facts[j].at
		}
		return facts[i].line < facts[j].line
	})
	for _, f := range facts {
		b.WriteString(f.line + "\n")
	}
	if len(facts) == 0 && len(trims) == 0 {
		b.WriteString(key + " none\n")
	}
}
