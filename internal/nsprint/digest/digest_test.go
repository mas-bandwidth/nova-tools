package digest_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/digest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

var (
	since = time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC)
	until = time.Date(2026, 9, 23, 4, 20, 0, 0, time.UTC)
)

// load starts a throwaway redis-server and runs testdata/digest/redis.txt
// (tab-separated arguments, \n a newline), leaving out every line holding a
// skip string.
func load(t *testing.T, skip ...string) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = c.Close() })
	raw, err := os.ReadFile(filepath.Join("testdata", "digest", "redis.txt"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
next:
	for _, line := range strings.Split(string(raw), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		for _, s := range skip {
			if strings.Contains(line, s) {
				continue next
			}
		}
		var args []any
		for _, a := range strings.Split(line, "\t") {
			args = append(args, strings.ReplaceAll(a, `\n`, "\n"))
		}
		if err := c.Do(ctx, args...).Err(); err != nil {
			t.Fatalf("%s: %v", line, err)
		}
	}
	return c
}

func render(t *testing.T, c redis.Cmdable, o digest.Options) string {
	t.Helper()
	d, err := digest.Read(context.Background(), c, o)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := digest.Render(&b, d); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func want(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "digest", "want.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func window() digest.Options { return digest.Options{Since: since, Until: until} }

// TestDigestGolden: the fixture holds, per section, a record at since
// (listed), one at until (not listed) and decoys (before since, other
// events and moves); the output is want.txt byte for byte, whether the
// events repo is named by --repo or only by the window's landing.
func TestDigestGolden(t *testing.T) {
	c := load(t)
	w := want(t)
	if got := render(t, c, window()); got != w {
		t.Fatalf("digest differs from want.txt\n--- got\n%s--- want\n%s", got, w)
	}
	o := window()
	o.Repos = []string{"mas-bandwidth/nova-tools"}
	if got := render(t, c, o); got != w {
		t.Fatalf("with --repo the digest differs\n--- got\n%s--- want\n%s", got, w)
	}
	empty := redis.NewClient(&redis.Options{Addr: testutil.Start(t)})
	t.Cleanup(func() { _ = empty.Close() })
	got := render(t, empty, window())
	if !strings.HasSuffix(got, "landed:\nlanded none\nholds:\nhold none\nreads:\nread none\n") || !strings.Contains(got, " repos=-\n") {
		t.Fatalf("an empty store is not three none sections:\n%s", got)
	}
	checkGrammar(t, got)
}

// sectionOf is the section of line i of text.
func sectionOf(text string, i int) string {
	sec := ""
	for j, l := range strings.Split(text, "\n") {
		switch l {
		case "landed:", "holds:", "reads:":
			sec = l
		}
		if j == i {
			return sec
		}
	}
	return sec
}

func firstDiff(a, b string) int {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range al {
		if i >= len(bl) || al[i] != bl[i] {
			return i
		}
	}
	return len(al)
}

// TestDigestEachSectionFails: a digest missing any one section's in-window
// record differs from the golden, first inside that section.
func TestDigestEachSectionFails(t *testing.T) {
	w := want(t)
	for _, tc := range []struct{ section, skip string }{
		{"landed:", "LANDED mas-bandwidth/nova-tools#3901"},
		{"landed:", "batch\tb7\t"},
		{"landed:", "landed: CLOSE by glenn"},
		{"holds:", "route pr fix"},
		{"reads:", "read: SCORE by stella"},
	} {
		t.Run(tc.skip, func(t *testing.T) {
			o := window()
			o.Repos = []string{"mas-bandwidth/nova-tools"} // line 1 stays the golden's
			got := render(t, load(t, tc.skip), o)
			if got == w {
				t.Fatalf("without %q the digest still equals want.txt", tc.skip)
			}
			if s := sectionOf(w, firstDiff(w, got)); s != tc.section {
				t.Fatalf("first difference is in %q, want %q\n%s", s, tc.section, got)
			}
		})
	}
}

// TestDigestSourceTrimmed: a stream that lost entries at or after since
// says so in every section it feeds and never prints "none"; a deletion
// before since is not a trim of the window.
func TestDigestSourceTrimmed(t *testing.T) {
	ctx := context.Background()
	t.Run("ws_log_trimmed", func(t *testing.T) {
		c := load(t)
		minID := fmt.Sprintf("%d", since.UnixMilli()+1500)
		if err := c.XTrimMinID(ctx, digest.WSLog, minID).Err(); err != nil {
			t.Fatal(err)
		}
		got := render(t, c, window())
		for _, key := range []string{"landed", "hold", "read"} {
			if !strings.Contains(got, "\n"+key+" TRIMMED source ws:log first=") {
				t.Fatalf("no %s TRIMMED line for ws:log:\n%s", key, got)
			}
		}
		if strings.Contains(got, " none\n") {
			t.Fatalf("a trimmed section printed none:\n%s", got)
		}
		checkGrammar(t, got)
	})
	t.Run("deleted_before_since", func(t *testing.T) {
		c := load(t)
		if err := c.XDel(ctx, digest.WSLog, fmt.Sprintf("%d-0", since.UnixMilli()-1000)).Err(); err != nil {
			t.Fatal(err)
		}
		if got := render(t, c, window()); got != want(t) {
			t.Fatalf("a deletion before since changed the digest:\n%s", got)
		}
	})
	t.Run("events_trimmed", func(t *testing.T) {
		c := load(t)
		key := digest.EventsKey("mas-bandwidth/nova-tools")
		id := fmt.Sprintf("%d-0", since.UnixMilli()+7500)
		if err := c.XDel(ctx, key, id).Err(); err != nil {
			t.Fatal(err)
		}
		got := render(t, c, window())
		line := "landed TRIMMED source " + key + " max-deleted=" + id + "\n"
		if !strings.Contains(got, "landed:\n"+line) {
			t.Fatalf("no %q right after the landed header:\n%s", line, got)
		}
		if strings.Contains(got, "hold TRIMMED") {
			t.Fatalf("an event stream trim reached holds:\n%s", got)
		}
		checkGrammar(t, got)
	})
}

// checkGrammar: every line matches exactly one expression of
// digest.Grammar, the sections come in order, facts belong to their section
// and run oldest first, and "none" stands alone.
func checkGrammar(t *testing.T, text string) {
	t.Helper()
	if err := grammarErr(text); err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
}

var atRx = regexp.MustCompile(` at=(\S+)`)

func grammarErr(text string) error {
	if !strings.HasSuffix(text, "\n") {
		return fmt.Errorf("output does not end in a newline")
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	headers := []string{"landed:", "holds:", "reads:"}
	keys := map[string]string{"landed:": "landed", "holds:": "hold", "reads:": "read"}
	sec, next, facts, trims, none, lastAt := "", 0, 0, 0, false, ""
	for i, l := range lines {
		var forms []string
		for name, rx := range digest.Grammar {
			if rx.MatchString(l) {
				forms = append(forms, name)
			}
		}
		if len(forms) != 1 {
			return fmt.Errorf("line %d %q matches %v, want exactly one form", i+1, l, forms)
		}
		form := forms[0]
		if (i == 0) != (form == "header") {
			return fmt.Errorf("line %d %q: the header is line 1 and only line 1", i+1, l)
		}
		if i == 0 {
			continue
		}
		if keys[l] != "" {
			if next >= len(headers) || l != headers[next] {
				return fmt.Errorf("line %d %q: sections are landed:, holds:, reads: in order", i+1, l)
			}
			next++
			sec, facts, trims, none, lastAt = l, 0, 0, false, ""
			continue
		}
		if sec == "" || !strings.HasPrefix(l, keys[sec]+" ") {
			return fmt.Errorf("line %d %q is outside its section %q", i+1, l, sec)
		}
		if none {
			return fmt.Errorf("line %d %q follows %s none", i+1, l, keys[sec])
		}
		switch form {
		case "trimmed":
			if facts > 0 {
				return fmt.Errorf("line %d %q: status lines come before facts", i+1, l)
			}
			trims++
		case "none":
			if facts > 0 || trims > 0 {
				return fmt.Errorf("line %d %q: none only for a section with no facts and no status", i+1, l)
			}
			none = true
		default:
			at := atRx.FindStringSubmatch(l)[1]
			if at < lastAt {
				return fmt.Errorf("line %d %q is older than the fact before it", i+1, l)
			}
			lastAt = at
			facts++
		}
	}
	if next != len(headers) {
		return fmt.Errorf("%d of %d section headers", next, len(headers))
	}
	return nil
}

// TestDigestLineGrammar: want.txt keeps the grammar, and the grammar pins
// field order and the none form; DIGEST_PROBE=<file> checks a saved output.
func TestDigestLineGrammar(t *testing.T) {
	w := want(t)
	checkGrammar(t, w)
	if p := os.Getenv("DIGEST_PROBE"); p != "" {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		checkGrammar(t, string(raw))
	}
	t.Run("pinned", func(t *testing.T) {
		swapped := regexp.MustCompile(`merge_sha=(\S+) members=(\S+)`).ReplaceAllString(w, "members=$2 merge_sha=$1")
		if swapped == w {
			t.Fatal("want.txt has no landing to swap")
		}
		if grammarErr(swapped) == nil {
			t.Fatal("merge_sha and members swapped still passes the grammar")
		}
		i := strings.Index(w, "\nholds:")
		if grammarErr(w[:i+1]+"landed none"+w[i:]) == nil {
			t.Fatal("landed none after a fact still passes the grammar")
		}
		if grammarErr(strings.Replace(w, "reads:\n", "", 1)) == nil {
			t.Fatal("a missing section header still passes the grammar")
		}
	})
}

// rtHook counts round trips (Process and ProcessPipeline calls) and keeps
// every command sent.
type rtHook struct {
	rts  int
	cmds []redis.Cmder
}

func (h *rtHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *rtHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		h.rts++
		h.cmds = append(h.cmds, cmd)
		return next(ctx, cmd)
	}
}
func (h *rtHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.rts++
		h.cmds = append(h.cmds, cmds...)
		return next(ctx, cmds)
	}
}

var eventsRx = regexp.MustCompile(`^land:\S+:events$`)

// TestDigestBoundedReads: the golden is two round trips (the streams, then
// the PR records with the derived event stream); 2,500 more in-window ws:log
// receipts add two pages, so four. Only ws:log, land:<repo>:events and
// pr:<name>:<n> are read, every XRANGE carries COUNT 1000, nothing is
// SCANned, and the package has no GitHub client.
func TestDigestBoundedReads(t *testing.T) {
	ctx := context.Background()
	check := func(t *testing.T, c *redis.Client, rts int) {
		t.Helper()
		h := &rtHook{}
		c.AddHook(h)
		render(t, c, window())
		if h.rts != rts {
			t.Fatalf("%d round trips, want %d", h.rts, rts)
		}
		for _, cmd := range h.cmds {
			args := cmd.Args()
			name := strings.ToUpper(fmt.Sprint(args[0]))
			key := ""
			if len(args) > 1 {
				key = fmt.Sprint(args[1])
			}
			switch name {
			case "XINFO":
				key = fmt.Sprint(args[2])
			case "XRANGE":
				if n := len(args); n < 6 || strings.ToUpper(fmt.Sprint(args[n-2])) != "COUNT" || fmt.Sprint(args[n-1]) != "1000" {
					t.Fatalf("XRANGE without COUNT 1000: %v", args)
				}
			case "HMGET":
				if !strings.HasPrefix(key, "pr:") {
					t.Fatalf("HMGET of %s: only pr records are read", key)
				}
				continue
			default:
				t.Fatalf("command %v: the digest reads with XINFO, XRANGE and HMGET only", args)
			}
			if key != digest.WSLog && !eventsRx.MatchString(key) {
				t.Fatalf("%s %s: only ws:log and land:<repo>:events are streams the digest reads", name, key)
			}
		}
	}
	t.Run("golden", func(t *testing.T) { check(t, load(t), 2) })
	t.Run("paged", func(t *testing.T) {
		c := load(t, "read: SCORE by emma at 12345678") // the receipt at until, so the pages fit below it
		pipe := c.Pipeline()
		for i := 0; i < 2500; i++ {
			pipe.XAdd(ctx, &redis.XAddArgs{Stream: digest.WSLog, ID: fmt.Sprintf("%d-%d", since.UnixMilli()+9000, i+1),
				Values: []any{"id", "t-x", "from", "ready", "to", "working", "why", "take"}})
		}
		if _, err := pipe.Exec(ctx); err != nil {
			t.Fatal(err)
		}
		check(t, c, 4)
		if got := render(t, c, window()); got != want(t) {
			t.Fatalf("paged digest differs from want.txt:\n%s", got)
		}
	})
	t.Run("no_github", func(t *testing.T) {
		files, _ := filepath.Glob("*.go")
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte(`"net/`+`http"`)) || bytes.Contains(raw, []byte("github"+"Token")) {
				t.Fatalf("%s reaches GitHub; the digest reads Redis only", f)
			}
		}
	})
}
