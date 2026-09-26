package fold

// Rote is each mind's hand work by class (nova-tools #3110; #2756 v6 sections
// 4.11 and 11.9; the memory ruling reduce-rote-work-daily): measure each mind's
// rote share, name the top class and its mechanism.
//
// Two records feed it, both streams in the one store:
//
//   - rote:log, written by `nova-sprint note --rote <class> --as <mind>`: one
//     entry per piece of hand work that has no verb yet (kind rote, mind,
//     class, what, mech, at).
//   - the control-verb receipts on cap:log and s:<S>:log (#2756 4.6, 4.11):
//     an entry with a verb field and an actor is that mind running a verb.
//   - the moves on ws:log (#2620): an entry with a by field is that mind
//     moving a task by a verb; its verb field names the verb, or move-<to>.
//
// A mind's rote share is notes / (notes + verb receipts). Its top class is the
// class it noted most; the NEXT line is the top class across every mind, the
// next mechanism to build. The fold prints the same lines for the sprint's
// window (opened_at to closed_at), so each fold names the next mechanism.
//
// Each fold also keeps its rote as rote:fold:<S> (HASH) and adds <S> to
// rote:folds (ZSET scored by the window's end in ms), and prints TREND lines
// against the fold before it (#2620): the share then and now, per mind and in
// total, and the class the previous fold named NEXT with its mechanism, so two
// consecutive folds show the share falling and the mechanism that did it.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The streams rote reads. RoteLog holds the hand-work notes; CapLog is the
// capacity and presence receipts (#2756 2.2).
const (
	RoteLog = "rote:log"
	CapLog  = "cap:log"
	WsLog   = "ws:log"
)

// RoteFolds is every folded sprint's rote record by the end of its window
// (score, ms); rote:fold:<S> is the record: mind:<m> = "<verbs> <notes>",
// class:<c> = n across minds, next and next_mech, end.
const (
	RoteFolds      = "rote:folds"
	RoteFoldPrefix = "rote:fold:"
)

// NoteSummary is the `note` verb's line on nova-sprint help.
const NoteSummary = "note --rote <class> --as <mind> --redis <host:port> [--what <text>] [--mech <verb or issue>]: one piece of hand work that has no verb yet, for rote"

// RoteSummary is the `rote` verb's line on nova-sprint help.
const RoteSummary = "rote --redis <host:port> [--since <t>] [--until <t>] [--sprint <S>]: per mind, control-verb receipts by verb and noted hand work by class; the top class is the next mechanism"

var roteName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// RoteNote is one piece of hand work.
type RoteNote struct {
	Mind  string // who did it by hand (rowan, stella, ...)
	Class string // what kind of hand work, a slug (issue-body-repair)
	What  string // optional: the instance, one line
	Mech  string // optional: the verb or issue that will replace it
}

// Check refuses a mind or class that is not a slug.
func (n RoteNote) Check() error {
	if !roteName.MatchString(n.Mind) {
		return fmt.Errorf("--as %q is not a mind name ([a-z0-9._-], starting with a letter or digit)", n.Mind)
	}
	if !roteName.MatchString(n.Class) {
		return fmt.Errorf("--rote %q is not a class name; a class is a slug like issue-body-repair ([a-z0-9._-])", n.Class)
	}
	return nil
}

// Note appends n to rote:log and returns the entry id.
func Note(ctx context.Context, client *redis.Client, n RoteNote, at time.Time) (string, error) {
	if err := n.Check(); err != nil {
		return "", err
	}
	id, err := client.XAdd(ctx, &redis.XAddArgs{Stream: RoteLog, Values: []string{
		"kind", "rote", "mind", n.Mind, "class", n.Class,
		"what", oneline.Escape(n.What), "mech", oneline.Escape(n.Mech),
		"at", at.UTC().Format(time.RFC3339),
	}}).Result()
	if err != nil {
		return "", fmt.Errorf("append to %s: %w", RoteLog, err)
	}
	return id, nil
}

// Count is one verb or one class for one mind (or, on the NEXT line, across
// minds). Mech is the latest mechanism a note of the class named.
type Count struct {
	Name  string
	N     int
	Mech  string
	Minds int
	last  string
}

// Mind is one mind's rote: its verb receipts and its noted hand work.
type Mind struct {
	Name  string
	Verbs []Count // by n, then name
	Notes []Count // by n, then name; Notes[0] is the top class
	VerbN int
	NoteN int
}

// Share is the mind's rote share in whole percent, or "-" with nothing counted.
func (m Mind) Share() string {
	total := m.VerbN + m.NoteN
	if total == 0 {
		return "-"
	}
	return strconv.Itoa((m.NoteN*100+total/2)/total) + "%"
}

// Rote is every mind's rote in a window, and the next mechanism.
type Rote struct {
	Minds []Mind // by name
	Next  Count  // the top class across minds; Name "" when nothing was noted
}

// Window bounds the entries read by their stream ids; a zero end is open.
type Window struct {
	Since, Until time.Time
}

func (w Window) ids() (string, string) {
	start, end := "-", "+"
	if !w.Since.IsZero() {
		start = strconv.FormatInt(w.Since.UnixMilli(), 10) + "-0"
	}
	if !w.Until.IsZero() {
		end = strconv.FormatInt(w.Until.UnixMilli(), 10) + "-" + strconv.FormatUint(^uint64(0), 10)
	}
	return start, end
}

// ReadRote reads rote:log, cap:log and ws:log in the window, and each extra stream (a
// sprint's own log, already bounded by the sprint) whole, 1,000 entries a
// round trip, and counts them per mind.
func ReadRote(ctx context.Context, client *redis.Client, w Window, extra ...string) (Rote, error) {
	type acc struct {
		verbs, notes map[string]*Count
		verbN, noteN int
	}
	minds := map[string]*acc{}
	get := func(name string) *acc {
		a := minds[name]
		if a == nil {
			a = &acc{verbs: map[string]*Count{}, notes: map[string]*Count{}}
			minds[name] = a
		}
		return a
	}
	start, end := w.ids()
	for i, stream := range append([]string{RoteLog, CapLog, WsLog}, extra...) {
		from, to := start, end
		if i >= 3 {
			from, to = "-", "+"
		}
		for {
			msgs, err := client.XRangeN(ctx, stream, from, to, 1000).Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return Rote{}, fmt.Errorf("read %s: %w", stream, err)
			}
			for _, m := range msgs {
				v := func(k string) string { s, _ := m.Values[k].(string); return s }
				if stream == RoteLog {
					if v("kind") != "rote" || v("mind") == "" || v("class") == "" {
						continue
					}
					a := get(v("mind"))
					c := a.notes[v("class")]
					if c == nil {
						c = &Count{Name: v("class")}
						a.notes[c.Name] = c
					}
					c.N++
					a.noteN++
					if mech := v("mech"); mech != "" && m.ID >= c.last {
						c.Mech, c.last = mech, m.ID
					}
					continue
				}
				verb, actor := v("verb"), v("actor")
				if stream == WsLog {
					actor = v("by")
					if verb == "" {
						verb = "move-" + v("to")
					}
				}
				if verb == "" || actor == "" {
					continue // a transition receipt, not a control verb
				}
				a := get(actor)
				c := a.verbs[verb]
				if c == nil {
					c = &Count{Name: verb}
					a.verbs[c.Name] = c
				}
				c.N++
				a.verbN++
			}
			if len(msgs) < 1000 {
				break
			}
			next, ok := after(msgs[len(msgs)-1].ID)
			if !ok {
				break
			}
			from = next
		}
	}

	var r Rote
	across := map[string]*Count{}
	names := make([]string, 0, len(minds))
	for name := range minds {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		a := minds[name]
		m := Mind{Name: name, Verbs: ordered(a.verbs), Notes: ordered(a.notes), VerbN: a.verbN, NoteN: a.noteN}
		for _, c := range m.Notes {
			x := across[c.Name]
			if x == nil {
				x = &Count{Name: c.Name}
				across[c.Name] = x
			}
			x.N += c.N
			x.Minds++
			if c.Mech != "" && c.last >= x.last {
				x.Mech, x.last = c.Mech, c.last
			}
		}
		r.Minds = append(r.Minds, m)
	}
	if all := ordered(across); len(all) > 0 {
		r.Next = all[0]
	}
	return r, nil
}

// after is the stream id just past id, for the next page.
func after(id string) (string, bool) {
	ms, seq, ok := strings.Cut(id, "-")
	if !ok {
		return "", false
	}
	n, err := strconv.ParseUint(seq, 10, 64)
	if err != nil || n == ^uint64(0) {
		return "", false
	}
	return ms + "-" + strconv.FormatUint(n+1, 10), true
}

func ordered(m map[string]*Count) []Count {
	out := make([]Count, 0, len(m))
	for _, c := range m {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return oneline.Field(s)
}

// PrintRote prints each mind's VERB and NOTE lines, its MIND line, and the
// NEXT line, every line led by prefix ("ROTE", or "FOLD ROTE" in the fold).
func PrintRote(out io.Writer, prefix string, r Rote) {
	for _, m := range r.Minds {
		mind := oneline.Field(m.Name)
		for _, c := range m.Verbs {
			fmt.Fprintf(out, "%s VERB mind=%s verb=%s n=%d\n", prefix, mind, oneline.Field(c.Name), c.N)
		}
		for _, c := range m.Notes {
			fmt.Fprintf(out, "%s NOTE mind=%s class=%s n=%d mech=%s\n", prefix, mind, oneline.Field(c.Name), c.N, dash(c.Mech))
		}
		top, topN := "-", 0
		if len(m.Notes) > 0 {
			top, topN = oneline.Field(m.Notes[0].Name), m.Notes[0].N
		}
		fmt.Fprintf(out, "%s MIND mind=%s verbs=%d notes=%d share=%s top=%s top_n=%d\n", prefix, mind, m.VerbN, m.NoteN, m.Share(), top, topN)
	}
	fmt.Fprintf(out, "%s NEXT class=%s n=%d minds=%d mech=%s\n", prefix, dash(r.Next.Name), r.Next.N, r.Next.Minds, dash(r.Next.Mech))
}

// foldRote prints the sprint's rote section: the notes and the control-verb
// receipts between opened_at and closed_at, and the sprint's own log, then the
// trend against the fold before it.
func foldRote(ctx context.Context, client *redis.Client, sprint string, head map[string]string, out io.Writer) error {
	var w Window
	for _, f := range []struct {
		field string
		to    *time.Time
	}{{"opened_at", &w.Since}, {"closed_at", &w.Until}} {
		if v := head[f.field]; v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return fmt.Errorf("s:%s %s=%q is not an RFC 3339 time", sprint, f.field, v)
			}
			*f.to = t
		}
	}
	if w.Until.IsZero() {
		w.Until = time.Now()
	}
	_, err := FoldRote(ctx, client, sprint, w, out)
	return err
}

// FoldRote is the fold's rote section for one sprint's window (w.Until set):
// it prints the rote, keeps it as rote:fold:<sprint> on rote:folds, and prints
// the TREND lines against the record before it by window end. A re-run writes
// the same record and prints the same lines.
func FoldRote(ctx context.Context, client *redis.Client, sprint string, w Window, out io.Writer) (Rote, error) {
	if w.Until.IsZero() {
		return Rote{}, fmt.Errorf("the rote record of %s needs the window's end", sprint)
	}
	r, err := ReadRote(ctx, client, w, "s:"+sprint+":log")
	if err != nil {
		return Rote{}, err
	}
	prefix := "FOLD ROTE sprint=" + sprint
	PrintRote(out, prefix, r)

	end := w.Until.UnixMilli()
	prevs, err := client.ZRevRangeByScore(ctx, RoteFolds, &redis.ZRangeBy{
		Max: "(" + strconv.FormatInt(end, 10), Min: "-inf", Count: 1}).Result()
	if err != nil {
		return r, fmt.Errorf("read %s: %w", RoteFolds, err)
	}
	pipe := client.Pipeline()
	var prevCmd *redis.MapStringStringCmd
	if len(prevs) > 0 {
		prevCmd = pipe.HGetAll(ctx, RoteFoldPrefix+prevs[0])
	}
	pipe.HSet(ctx, RoteFoldPrefix+sprint, roteFields(r, end))
	pipe.ZAdd(ctx, RoteFolds, redis.Z{Score: float64(end), Member: sprint})
	if _, err := pipe.Exec(ctx); err != nil {
		return r, fmt.Errorf("keep the rote record of %s: %w", sprint, err)
	}
	if prevCmd == nil {
		t := r.total()
		fmt.Fprintf(out, "%s TREND prev=- now=%s dir=-\n", prefix, t.Share())
		return r, nil
	}
	printTrend(out, prefix, prevs[0], readFold(prevCmd.Val()), r)
	return r, nil
}

// total is every mind's verbs and notes summed.
func (r Rote) total() Mind {
	var t Mind
	for _, m := range r.Minds {
		t.VerbN += m.VerbN
		t.NoteN += m.NoteN
	}
	return t
}

// classes is each class's notes across minds.
func (r Rote) classes() map[string]int {
	c := map[string]int{}
	for _, m := range r.Minds {
		for _, n := range m.Notes {
			c[n.Name] += n.N
		}
	}
	return c
}

// roteFields is rote:fold:<S>: mind:<m> = "<verbs> <notes>", class:<c> = n,
// next, next_mech and end (ms).
func roteFields(r Rote, end int64) []string {
	f := []string{"end", strconv.FormatInt(end, 10), "next", r.Next.Name, "next_mech", r.Next.Mech}
	for _, m := range r.Minds {
		f = append(f, "mind:"+m.Name, strconv.Itoa(m.VerbN)+" "+strconv.Itoa(m.NoteN))
	}
	for c, n := range r.classes() {
		f = append(f, "class:"+c, strconv.Itoa(n))
	}
	return f
}

// readFold is the Rote a rote:fold:<S> record holds: each mind's counts and
// the NEXT class with its count and mechanism.
func readFold(h map[string]string) Rote {
	var r Rote
	for k, v := range h {
		if name, ok := strings.CutPrefix(k, "mind:"); ok {
			vs, ns, _ := strings.Cut(v, " ")
			vn, _ := strconv.Atoi(vs)
			nn, _ := strconv.Atoi(ns)
			r.Minds = append(r.Minds, Mind{Name: name, VerbN: vn, NoteN: nn})
		}
	}
	sort.Slice(r.Minds, func(i, j int) bool { return r.Minds[i].Name < r.Minds[j].Name })
	if next := h["next"]; next != "" {
		n, _ := strconv.Atoi(h["class:"+next])
		r.Next = Count{Name: next, N: n, Mech: h["next_mech"]}
	}
	return r
}

// shareDir is falling, rising or flat for the share then (a) and now (b),
// or - when either counted nothing.
func shareDir(a, b Mind) string {
	ta, tb := a.VerbN+a.NoteN, b.VerbN+b.NoteN
	if ta == 0 || tb == 0 {
		return "-"
	}
	return countDir(a.NoteN*tb, b.NoteN*ta)
}

func countDir(was, now int) string {
	switch {
	case now < was:
		return "falling"
	case now > was:
		return "rising"
	}
	return "flat"
}

// printTrend prints the total share then and now, each mind's, and the class
// the previous fold named NEXT: its count then and now and its mechanism, so a
// falling class names the mechanism that did it.
func printTrend(out io.Writer, prefix, prevName string, prev, now Rote) {
	p := oneline.Field(prevName)
	pt, nt := prev.total(), now.total()
	fmt.Fprintf(out, "%s TREND prev=%s was=%s now=%s dir=%s\n", prefix, p, pt.Share(), nt.Share(), shareDir(pt, nt))
	minds := map[string][2]Mind{}
	for _, m := range prev.Minds {
		x := minds[m.Name]
		x[0] = m
		minds[m.Name] = x
	}
	for _, m := range now.Minds {
		x := minds[m.Name]
		x[1] = m
		minds[m.Name] = x
	}
	names := make([]string, 0, len(minds))
	for name := range minds {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		x := minds[name]
		fmt.Fprintf(out, "%s TREND mind=%s prev=%s was=%s now=%s dir=%s\n", prefix, oneline.Field(name), p, x[0].Share(), x[1].Share(), shareDir(x[0], x[1]))
	}
	if prev.Next.Name != "" {
		n := now.classes()[prev.Next.Name]
		fmt.Fprintf(out, "%s TREND class=%s prev=%s was=%d now=%d dir=%s mech=%s\n", prefix, oneline.Field(prev.Next.Name), p, prev.Next.N, n, countDir(prev.Next.N, n), dash(prev.Next.Mech))
	}
}
