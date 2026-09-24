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
//
// A mind's rote share is notes / (notes + verb receipts). Its top class is the
// class it noted most; the NEXT line is the top class across every mind, the
// next mechanism to build. The fold prints the same lines for the sprint's
// window (opened_at to closed_at), so each fold names the next mechanism.

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
)

// NoteSummary is the `note` verb's line on nova-sprint help.
const NoteSummary = "note --rote <class> --as <mind> --store <host:port> [--what <text>] [--mech <verb or issue>]: one piece of hand work that has no verb yet, for rote"

// RoteSummary is the `rote` verb's line on nova-sprint help.
const RoteSummary = "rote --store <host:port> [--since <t>] [--until <t>] [--sprint <S>]: per mind, control-verb receipts by verb and noted hand work by class; the top class is the next mechanism"

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

// ReadRote reads rote:log and cap:log in the window, and each extra stream (a
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
	for i, stream := range append([]string{RoteLog, CapLog}, extra...) {
		from, to := start, end
		if i >= 2 {
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
				if v("verb") == "" || v("actor") == "" {
					continue // a transition receipt, not a control verb
				}
				a := get(v("actor"))
				c := a.verbs[v("verb")]
				if c == nil {
					c = &Count{Name: v("verb")}
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
// receipts between opened_at and closed_at, and the sprint's own log.
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
	r, err := ReadRote(ctx, client, w, "s:"+sprint+":log")
	if err != nil {
		return err
	}
	PrintRote(out, "FOLD ROTE sprint="+sprint, r)
	return nil
}
