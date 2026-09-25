// Package benchcount is the per-bench landed and useful counts for the
// one-second sprint table (nova-tools #2687).
//
// bench-row already writes queue, working, done, ok and fail on bench:<host>
// every second. The fold's consumer reads the cards:done stream and, once a
// second, writes three more fields on that same hash: landed, useful and
// usd_per_useful. This package is that count. It does not render the table,
// and it does not ask GitHub whether a pull request merged or an issue exists.
// The stream is the whole record.
//
// A card counts as landed only after an entry whose event is landed. A pull
// request (event pullreq, or pr as the #2619 writers spell it) is not a landing.
//
// A card counts as useful under #2680, once: it has landed, or a pullreq or
// landed entry has shown a verified defect (defect=verified), or a receipted
// issue (issue set and receipt=receipted). A card that has more than one of
// those is still one useful card. The defect and the receipt are read only
// off pullreq and landed entries (pr is the pullreq kind #2619 writes). Cut,
// then ok, then landed is the order the funnel walks; cut and ok do not by
// themselves make a card useful.
//
// usd_per_useful is the bench's card spend divided by its useful cards, so the
// cost per useful card is one HGET. With no useful card the field is a dash.
// The other fields bench-row owns are left as they are, including the key's TTL.
package benchcount

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// Stream is the one card stream. The fold reads it; this package does not
	// open a second one.
	Stream = "cards:done"

	// Interval is how often the fold's consumer applies the stream. The
	// package does not sleep; the consumer calls Pass on its own tick.
	Interval = time.Second

	// FunnelOrder is the order a card's counts walk, and the order the test requires.
	FunnelOrder = "cut, then ok, then landed"

	// BenchPrefix is the hash bench-row already writes. The fold adds fields
	// to it and does not mint a second key per host.
	BenchPrefix = "bench:"

	FieldLanded       = "landed"
	FieldUseful       = "useful"
	FieldUSDPerUseful = "usd_per_useful"
)

const (
	kindCut     = "cut"
	kindQueued  = "queued" // the launcher's name for a card that was cut onto the stream
	kindOK      = "ok"
	kindPullReq = "pullreq"
	kindPR      = "pr"
	kindLanded  = "landed"

	defectVerified = "verified"
	receipted      = "receipted"
)

// Entry is one cards:done record: the stream id and the string fields XADD stored.
type Entry struct {
	ID     string
	Fields map[string]string
}

// Counts is one bench after the fold. Cut and OK are the funnel beside the
// hash; the hash itself gains Landed, Useful and the cost, not a second ok.
type Counts struct {
	Cut    int
	OK     int
	Landed int
	Useful int
	USD    float64
}

// CostPerUseful is the bench's spend over its useful cards, six digits, or a
// dash when the bench has no useful card (a ratio over nothing is not zero).
func (c Counts) CostPerUseful() string {
	if c.Useful <= 0 {
		return "-"
	}
	return strconv.FormatFloat(c.USD/float64(c.Useful), 'f', 6, 64)
}

// Fold is the consumer's memory: one card per label, each stream id applied once.
type Fold struct {
	seen  map[string]struct{}
	cards map[string]*card
}

type card struct {
	bench     string
	cut       bool
	ok        bool
	landed    bool
	verified  bool
	receipted bool
	usd       float64
}

// New is an empty fold. A replay starts another one; the two agree.
func New() *Fold {
	return &Fold{
		seen:  map[string]struct{}{},
		cards: map[string]*card{},
	}
}

// Apply folds one entry. A stream id already applied is a no-op, so a
// redelivery does not count a card twice and does not add its spend twice.
func (f *Fold) Apply(e Entry) {
	if e.ID != "" {
		if _, ok := f.seen[e.ID]; ok {
			return
		}
		f.seen[e.ID] = struct{}{}
	}
	if e.Fields == nil {
		return
	}
	label := strings.TrimSpace(e.Fields["label"])
	if label == "" {
		return
	}
	c := f.cards[label]
	if c == nil {
		c = &card{}
		f.cards[label] = c
	}
	// The first bench a card names is the bench that ran it. A later landed
	// entry is often the lander's host; it must not move the card.
	if c.bench == "" {
		c.bench = normalizeHost(e.Fields["bench"])
	}
	if usd, ok := parseUSD(e.Fields["usd"]); ok {
		c.usd += usd
	}
	kind := strings.TrimSpace(e.Fields["event"])
	switch kind {
	case kindCut, kindQueued:
		c.cut = true
	case kindOK:
		c.ok = true
	case kindPullReq, kindPR:
		noteUseful(c, e.Fields)
	case kindLanded:
		c.landed = true
		noteUseful(c, e.Fields)
	}
}

// noteUseful reads #2680's other two facts. Only a pullreq or a landed entry
// can carry them; an ok entry that says the same words is not the rule.
func noteUseful(c *card, fields map[string]string) {
	if strings.TrimSpace(fields["defect"]) == defectVerified {
		c.verified = true
	}
	issue := strings.TrimSpace(fields["issue"])
	if issue != "" && strings.TrimSpace(fields["receipt"]) == receipted {
		c.receipted = true
	}
}

// Bench is one host's counts. An unknown host is the zero value.
func (f *Fold) Bench(host string) Counts {
	return f.Counts()[normalizeHost(host)]
}

// Counts is every bench that has at least one card, keyed by the host.
func (f *Fold) Counts() map[string]Counts {
	out := map[string]Counts{}
	for _, c := range f.cards {
		if c.bench == "" {
			continue
		}
		row := out[c.bench]
		if c.cut {
			row.Cut++
		}
		if c.ok {
			row.OK++
		}
		if c.landed {
			row.Landed++
		}
		// Once per card. Landed, a verified defect, or a receipted issue.
		if c.landed || c.verified || c.receipted {
			row.Useful++
		}
		row.USD += c.usd
		out[c.bench] = row
	}
	return out
}

// Pass applies a batch and writes every bench hash. The fold's consumer calls
// it once per Interval with the entries it just read. Counts are absolute, so
// a second pass of the same ids does not climb.
func (f *Fold) Pass(ctx context.Context, rdb *redis.Client, entries []Entry) error {
	for _, e := range entries {
		f.Apply(e)
	}
	return Write(ctx, rdb, f.Counts())
}

// Write sets landed, useful and usd_per_useful on bench:<host>. Every host is
// one HSET in one pipeline, so the whole write is one round trip however many
// benches there are (nova-tools #3271). It does not delete the key, expire it,
// or write the fields bench-row owns.
func Write(ctx context.Context, rdb *redis.Client, counts map[string]Counts) error {
	hosts := make([]string, 0, len(counts))
	for host := range counts {
		if host != "" {
			hosts = append(hosts, host)
		}
	}
	if len(hosts) == 0 {
		return nil
	}
	sort.Strings(hosts)
	pipe := rdb.Pipeline()
	cmds := make([]*redis.IntCmd, len(hosts))
	for i, host := range hosts {
		c := counts[host]
		cmds[i] = pipe.HSet(ctx, BenchPrefix+host,
			FieldLanded, strconv.Itoa(c.Landed),
			FieldUseful, strconv.Itoa(c.Useful),
			FieldUSDPerUseful, c.CostPerUseful(),
		)
	}
	_, execErr := pipe.Exec(ctx)
	for i, cmd := range cmds {
		if err := cmd.Err(); err != nil {
			return fmt.Errorf("bench %s: %w", hosts[i], err)
		}
	}
	return execErr
}

// ReadStream reads the whole fixture stream in id order. XRANGE, not a GitHub poll.
func ReadStream(ctx context.Context, rdb *redis.Client, stream string) ([]Entry, error) {
	if strings.TrimSpace(stream) == "" {
		stream = Stream
	}
	msgs, err := rdb.XRange(ctx, stream, "-", "+").Result()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(msgs))
	for _, m := range msgs {
		fields := make(map[string]string, len(m.Values))
		for k, v := range m.Values {
			fields[k] = fmt.Sprint(v)
		}
		out = append(out, Entry{ID: m.ID, Fields: fields})
	}
	return out, nil
}

// FoldStream folds a fixture stream from the store. A second call over the
// same stream is the same counts.
func FoldStream(ctx context.Context, rdb *redis.Client, stream string) (*Fold, error) {
	entries, err := ReadStream(ctx, rdb, stream)
	if err != nil {
		return nil, err
	}
	f := New()
	for _, e := range entries {
		f.Apply(e)
	}
	return f, nil
}

// CostPerUseful is one HGET of bench:<host> usd_per_useful. A missing field
// is a dash, the same as a bench with nothing useful yet.
func CostPerUseful(ctx context.Context, rdb *redis.Client, host string) (string, error) {
	v, err := rdb.HGet(ctx, BenchPrefix+normalizeHost(host), FieldUSDPerUseful).Result()
	if err == redis.Nil {
		return "-", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

func parseUSD(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0, false
	}
	return f, true
}
