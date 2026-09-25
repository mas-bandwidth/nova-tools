// Package verbs is `nova-sprint verbs unused` (nova-tools #3160): the verbs the
// dev build ships that nobody ran in the window, read mechanically from the
// binaries' own help, the use receipts on the sprint streams in Redis and the
// non-author dogfood receipts, and appended as one entry to verbs:unused:log.
// `verbs unused --check` reads the newest two entries and says whether the
// count fell.
//
// Nothing here reaches GitHub or writes a file. The one write is a
// compare-and-append on verbs:unused:log (WATCH, MULTI, XADD, EXEC), so every
// entry's `resolved` describes the entry its `prev` names. Sprints are found
// through `sprints` and `sprint:order`; nothing is read with KEYS or SCAN.
//
// It imports internal/dogfood and internal/nsprint/capacity and never
// internal/nsprint/fold, so the fold can run it without an import cycle.
package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/dogfood"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// LogKey is the stream every run appends one entry to.
const LogKey = "verbs:unused:log"

const (
	// LogMax is the approximate cap on LogKey (XADD MAXLEN ~).
	LogMax = 10000
	// DefaultDays is the window when --days is not given.
	DefaultDays = 14
	// Tries is how many times the compare-and-append runs before it gives up.
	Tries = 3
	page  = 1000
)

// Exit codes. Check uses ExitOK, ExitFail and ExitRefused; Unused uses
// ExitOK, ExitRefused and ExitFenced.
const (
	ExitOK      = 0
	ExitFail    = 1
	ExitRefused = 2
	ExitFenced  = 3
)

// VerbSummary is the verb's line on nova-sprint help.
const VerbSummary = "verbs unused --store <host:port> --tools <dir of nova-* binaries built at dev> --repo <nova-tools clone at dev> --receipts <dogfood receipts dir> [--days 14] | verbs unused --check --store <host:port> --repo <clone>: verbs with no use and no non-author dogfood receipt in the window, one entry on verbs:unused:log; --check says whether the count fell"

// Config is one `verbs unused` run.
type Config struct {
	Tools    string // directory of nova-* binaries built at the dev tip
	Repo     string // the nova-tools clone at dev: authorship and dev_sha
	Receipts string // the dogfood receipts directory
	Days     int    // the window, in days back from Now
	// Now is read once per run; nil is time.Now.
	Now func() time.Time
	// Git answers the authorship reads; nil is dogfood.GitRunner.
	Git dogfood.Runner

	// beforeExec runs between the tip read and EXEC; tests use it to move
	// the tip under the append.
	beforeExec func(context.Context) error
}

// Main is `nova-sprint verbs unused ...`.
func Main(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "unused" {
		return refuse(stderr, "verbs takes one sub-verb, unused: verbs unused --store <host:port> --tools <dir> --repo <clone> --receipts <dir> [--days 14], or verbs unused --check --store <host:port> --repo <clone>")
	}
	fs := flag.NewFlagSet("verbs unused", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	addr := fs.String("store", "", "")
	tools := fs.String("tools", "", "")
	repo := fs.String("repo", "", "")
	receipts := fs.String("receipts", "", "")
	days := fs.String("days", strconv.Itoa(DefaultDays), "")
	check := fs.Bool("check", false, "")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(stderr, err.Error())
	}
	var problems []string
	if fs.NArg() > 0 {
		problems = append(problems, "extra arguments "+strings.Join(fs.Args(), " "))
	}
	if *addr == "" {
		problems = append(problems, "--store <host:port> is the sprint's Redis")
	}
	if *repo == "" {
		problems = append(problems, "--repo names the nova-tools clone at dev")
	}
	n, err := strconv.Atoi(*days)
	if err != nil || n <= 0 {
		problems = append(problems, fmt.Sprintf("--days %q is not a positive integer", *days))
	}
	if !*check {
		if *tools == "" {
			problems = append(problems, "--tools names the directory of nova-* binaries built at dev")
		}
		if *receipts == "" {
			problems = append(problems, "--receipts names the dogfood receipts directory")
		}
	}
	if len(problems) > 0 {
		return refuse(stderr, strings.Join(problems, "; "))
	}
	st, err := store.Open(ctx, *addr)
	if err != nil {
		return refuse(stderr, "store why="+err.Error())
	}
	defer st.Close()
	if *check {
		return Check(ctx, st.Client(), *repo, stdout, stderr)
	}
	return Unused(ctx, st.Client(), Config{Tools: *tools, Repo: *repo, Receipts: *receipts, Days: n}, stdout, stderr)
}

func refuse(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "nova-sprint verbs: %s; run: nova-sprint help\n", oneline.Escape(what))
	return ExitRefused
}

// notesShape is what a dogfood receipt's notes must name: the issue or the
// card it was real work on.
var notesShape = regexp.MustCompile(`#[0-9]+|card-[a-z0-9-]+`)

// run is one run's evidence, read before the append.
type run struct {
	inventory map[string]bool
	used      map[string]bool
	dogfood   map[string]string // key -> file:line of the receipt that counted
	listed    []string
}

// resolve says why a key that left the list left it.
func (r *run) resolve(key string) string {
	switch {
	case !r.inventory[key]:
		return "deleted"
	case r.used[key]:
		return "use"
	case r.dogfood[key] != "":
		return "dogfood:" + r.dogfood[key]
	}
	return ""
}

// Unused lists the verbs with no use and no counting dogfood receipt in the
// window and appends the list to LogKey. Exit 0 appended, 2 refused (nothing
// appended), 3 the tip moved under every try (nothing appended).
func Unused(ctx context.Context, client *redis.Client, cfg Config, stdout, stderr io.Writer) int {
	if cfg.Days <= 0 {
		return refuse(stderr, fmt.Sprintf("--days %d is not a positive integer", cfg.Days))
	}
	for _, f := range []struct{ flag, val string }{{"--tools", cfg.Tools}, {"--repo", cfg.Repo}, {"--receipts", cfg.Receipts}} {
		if strings.TrimSpace(f.val) == "" {
			return refuse(stderr, f.flag+" is required")
		}
	}
	now := time.Now
	if cfg.Now != nil {
		now = cfg.Now
	}
	until := now().UTC()
	since := until.Add(-time.Duration(cfg.Days) * 24 * time.Hour)

	verbs, fails, err := dogfood.VerbsFromTools(ctx, cfg.Tools, nil, nil)
	if err != nil {
		return refuse(stderr, "inventory why="+err.Error())
	}
	if len(fails) > 0 {
		return refuse(stderr, fmt.Sprintf("inventory tool=%s why=%s", filepath.Base(fails[0].Subject), fails[0].Reason))
	}
	if len(verbs) == 0 {
		return refuse(stderr, "inventory why=no nova-* binary in "+cfg.Tools+" named a verb")
	}
	r := &run{inventory: map[string]bool{}, used: map[string]bool{}, dogfood: map[string]string{}}
	for _, v := range verbs {
		r.inventory[v.Key()] = true
	}

	authors, err := dogfood.AuthorsFromGit(ctx, cfg.Repo, verbs, cfg.Git, nil)
	if err != nil {
		return refuse(stderr, "authors why="+err.Error())
	}

	receipts, rfails, err := dogfood.ReadReceipts(cfg.Receipts)
	if err != nil {
		return refuse(stderr, "receipts why="+err.Error())
	}
	var skips []string
	for _, f := range rfails {
		if strings.HasPrefix(f.Reason, "unreadable:") {
			return refuse(stderr, "receipts why="+f.Subject+": "+f.Reason)
		}
		skips = append(skips, skipLine(f.Subject, failField(f.Reason)))
	}

	devSHA, err := dogfood.GitRunner(ctx, cfg.Repo, "rev-parse", "HEAD")
	devSHA = strings.TrimSpace(devSHA)
	if err != nil || devSHA == "" {
		return refuse(stderr, fmt.Sprintf("repo why=git rev-parse HEAD in %s: %v", cfg.Repo, err))
	}

	streams, err := readUse(ctx, client, since, until, r)
	if err != nil {
		return refuse(stderr, "store why="+err.Error())
	}

	for _, rc := range receipts {
		key := rc.Key()
		why := ""
		switch {
		case !r.inventory[key]:
			why = "verb"
		case !rc.OK || rc.RecordsAnEdge():
			why = "ok"
		case wrote(authors, key, rc.By):
			why = "by"
		case rc.Time().Before(since) || rc.Time().After(until):
			why = "at"
		case !notesShape.MatchString(rc.Notes):
			why = "notes"
		}
		if why != "" {
			skips = append(skips, skipLine(rc.File, why))
			continue
		}
		if r.dogfood[key] == "" {
			r.dogfood[key] = filepath.Base(rc.File)
		}
	}
	for key := range r.inventory {
		if !r.used[key] && r.dogfood[key] == "" {
			r.listed = append(r.listed, key)
		}
	}
	sort.Strings(r.listed)

	for _, s := range skips {
		fmt.Fprintln(stdout, s)
	}
	fmt.Fprintf(stdout, "VERBS STREAMS n=%d sprints=%d\n", len(streams), len(streams)-1)

	e := entry{At: until, Days: cfg.Days, Since: since, Until: until, DevSHA: devSHA, Verbs: r.listed}
	id, err := appendEntry(ctx, client, e, r.resolve, cfg.beforeExec)
	if errors.Is(err, errPrevMoved) {
		fmt.Fprintf(stderr, "nova-sprint verbs: prev-moved tries=%d; nothing appended; run: nova-sprint help\n", Tries)
		return ExitFenced
	}
	if err != nil {
		return refuse(stderr, "store why="+err.Error())
	}
	fmt.Fprintf(stdout, "VERBS UNUSED count=%d dev_sha=%s id=%s\n", len(r.listed), short(devSHA), id)
	return ExitOK
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// wrote is the dogfood ledger's non-author rule: `by` is the verb's author,
// compared trimmed and case-insensitively; an unplaced verb has no author.
func wrote(a dogfood.Authors, key, by string) bool {
	author := strings.TrimSpace(a.Author(key))
	return author != "" && strings.EqualFold(author, strings.TrimSpace(by))
}

func skipLine(subject, why string) string {
	return fmt.Sprintf("VERBS SKIP receipt=%s why=%s", oneline.Field(filepath.Base(subject)), why)
}

// failField names what a receipt line that did not read failed on: parse for
// a line that is not a receipt, else the field its validation named first.
func failField(reason string) string {
	if strings.HasPrefix(reason, "not a receipt:") {
		return "parse"
	}
	if f := strings.Fields(reason); len(f) > 0 {
		return strings.Trim(f[0], `"`)
	}
	return "parse"
}

// window turns the bounds into stream ids, inclusive at both ends, as the
// fold's Window does.
func window(since, until time.Time) (string, string) {
	return strconv.FormatInt(since.UnixMilli(), 10) + "-0",
		strconv.FormatInt(until.UnixMilli(), 10) + "-" + strconv.FormatUint(^uint64(0), 10)
}

// readUse reads cap:log and every sprint's s:<S>:log in the window, 1,000
// entries a page, every stream's first page in one pipeline, and marks each
// inventory key an entry with a verb and an actor names. It returns the
// streams it read.
func readUse(ctx context.Context, client *redis.Client, since, until time.Time, r *run) ([]string, error) {
	pipe := client.Pipeline()
	open := pipe.SMembers(ctx, "sprints")
	order := pipe.ZRangeByScore(ctx, "sprint:order", &redis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(until.UnixMilli(), 10)})
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read sprints and sprint:order: %w", err)
	}
	names := map[string]bool{}
	for _, s := range append(open.Val(), order.Val()...) {
		names[s] = true
	}
	sprints := make([]string, 0, len(names))
	for s := range names {
		sprints = append(sprints, s)
	}
	sort.Strings(sprints)
	streams := []string{capacity.LogKey}
	for _, s := range sprints {
		streams = append(streams, "s:"+s+":log")
	}

	start, end := window(since, until)
	from := map[string]string{}
	pending := append([]string(nil), streams...)
	for _, s := range pending {
		from[s] = start
	}
	for len(pending) > 0 {
		pipe := client.Pipeline()
		cmds := make([]*redis.XMessageSliceCmd, len(pending))
		for i, s := range pending {
			cmds[i] = pipe.XRangeN(ctx, s, from[s], end, page)
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("read the use streams: %w", err)
		}
		var next []string
		for i, s := range pending {
			msgs := cmds[i].Val()
			for _, m := range msgs {
				str := func(k string) string { v, _ := m.Values[k].(string); return v }
				verb, actor := str("verb"), str("actor")
				if strings.TrimSpace(verb) == "" || strings.TrimSpace(actor) == "" {
					continue // a transition receipt, not a verb run
				}
				tool := str("tool")
				if strings.TrimSpace(tool) == "" {
					tool = "nova-sprint" // cap:log and s:<S>:log are nova-sprint's own
				}
				if key := dogfood.NormalizeKey(tool, verb); r.inventory[key] {
					r.used[key] = true
				}
			}
			if len(msgs) == page {
				if id, ok := after(msgs[len(msgs)-1].ID); ok {
					from[s] = id
					next = append(next, s)
				}
			}
		}
		pending = next
	}
	return streams, nil
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

// entry is one verbs:unused:log entry.
type entry struct {
	ID       string
	At       time.Time
	Days     int
	Since    time.Time
	Until    time.Time
	DevSHA   string
	Count    int
	Verbs    []string
	Resolved map[string]string
	Prev     string
}

var errPrevMoved = errors.New("prev-moved")

// appendEntry is the compare-and-append: WATCH the log, read its tip, compute
// resolved against the tip's verbs, then MULTI/XADD/EXEC. A tip that moved in
// between fails EXEC and the whole step reruns, Tries times in all.
func appendEntry(ctx context.Context, client *redis.Client, e entry, resolve func(string) string, beforeExec func(context.Context) error) (string, error) {
	listed := map[string]bool{}
	for _, k := range e.Verbs {
		listed[k] = true
	}
	verbsJSON, err := json.Marshal(nonNil(e.Verbs))
	if err != nil {
		return "", err
	}
	for try := 0; try < Tries; try++ {
		var id string
		err := client.Watch(ctx, func(tx *redis.Tx) error {
			tip, err := tx.XRevRangeN(ctx, LogKey, "+", "-", 1).Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return fmt.Errorf("read %s: %w", LogKey, err)
			}
			prev, resolved := "0", map[string]string{}
			if len(tip) == 1 {
				p, err := parseEntry(tip[0])
				if err != nil {
					return err
				}
				prev = p.ID
				for _, k := range p.Verbs {
					if !listed[k] {
						resolved[k] = resolve(k)
					}
				}
			}
			resolvedJSON, err := json.Marshal(resolved)
			if err != nil {
				return err
			}
			if beforeExec != nil {
				if err := beforeExec(ctx); err != nil {
					return err
				}
			}
			var add *redis.StringCmd
			_, err = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
				add = p.XAdd(ctx, &redis.XAddArgs{Stream: LogKey, MaxLen: LogMax, Approx: true, Values: []string{
					"at", e.At.UTC().Format(time.RFC3339),
					"days", strconv.Itoa(e.Days),
					"since", e.Since.UTC().Format(time.RFC3339),
					"until", e.Until.UTC().Format(time.RFC3339),
					"dev_sha", e.DevSHA,
					"count", strconv.Itoa(len(e.Verbs)),
					"verbs", string(verbsJSON),
					"resolved", string(resolvedJSON),
					"prev", prev,
				}})
				return nil
			})
			if err != nil {
				return err
			}
			id = add.Val()
			return nil
		}, LogKey)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, redis.TxFailedErr) {
			return "", err
		}
	}
	return "", errPrevMoved
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// parseEntry reads one log entry; a field that does not read is an error
// naming the entry, never a guess.
func parseEntry(m redis.XMessage) (entry, error) {
	str := func(k string) string { v, _ := m.Values[k].(string); return v }
	e := entry{ID: m.ID, DevSHA: str("dev_sha"), Prev: str("prev")}
	n, err := strconv.Atoi(str("count"))
	if err != nil {
		return e, fmt.Errorf("%s %s: count %q is not a number", LogKey, m.ID, str("count"))
	}
	e.Count = n
	if err := json.Unmarshal([]byte(str("verbs")), &e.Verbs); err != nil {
		return e, fmt.Errorf("%s %s: verbs is not a JSON array: %v", LogKey, m.ID, err)
	}
	if raw := str("resolved"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &e.Resolved); err != nil {
			return e, fmt.Errorf("%s %s: resolved is not a JSON object: %v", LogKey, m.ID, err)
		}
	}
	return e, nil
}

// Check reads the newest two entries of LogKey and says whether the count
// fell. Exit 0 FALLING, 1 MISSING, BROKEN-CHAIN, NOT FALLING, STALE or
// UNEXPLAINED, 2 refused.
func Check(ctx context.Context, client *redis.Client, repo string, stdout, stderr io.Writer) int {
	if strings.TrimSpace(repo) == "" {
		return refuse(stderr, "--repo names the nova-tools clone at dev")
	}
	msgs, err := client.XRevRangeN(ctx, LogKey, "+", "-", 2).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return refuse(stderr, "store why="+err.Error())
	}
	if len(msgs) < 2 {
		fmt.Fprintf(stdout, "VERBS UNUSED MISSING entries=%d\n", len(msgs))
		return ExitFail
	}
	newest, err := parseEntry(msgs[0])
	if err != nil {
		return refuse(stderr, "store why="+err.Error())
	}
	prev, err := parseEntry(msgs[1])
	if err != nil {
		return refuse(stderr, "store why="+err.Error())
	}
	if newest.Prev != prev.ID {
		fmt.Fprintf(stdout, "VERBS UNUSED BROKEN-CHAIN newest=%s prev=%s want=%s\n", newest.ID, oneline.Field(newest.Prev), prev.ID)
		return ExitFail
	}
	rel, err := relation(ctx, repo, prev.DevSHA, newest.DevSHA)
	if err != nil {
		return refuse(stderr, "repo why="+err.Error())
	}
	fell := newest.Count < prev.Count || newest.Count == 0
	switch {
	case rel == "NEWER" && fell:
		fmt.Fprintf(stdout, "VERBS UNUSED FALLING was=%d now=%d\n", prev.Count, newest.Count)
		return ExitOK
	case rel == "NEWER":
		fmt.Fprintf(stdout, "VERBS UNUSED NOT FALLING was=%d now=%d\n", prev.Count, newest.Count)
		return ExitFail
	case rel == "SAME" && fell:
		now := map[string]bool{}
		for _, k := range newest.Verbs {
			now[k] = true
		}
		code := ExitOK
		for _, k := range prev.Verbs {
			if now[k] {
				continue
			}
			if v := newest.Resolved[k]; v != "use" && !strings.HasPrefix(v, "dogfood:") {
				fmt.Fprintf(stdout, "VERBS UNUSED UNEXPLAINED verb=%s\n", oneline.Quote(k))
				code = ExitFail
			}
		}
		if code == ExitOK {
			fmt.Fprintf(stdout, "VERBS UNUSED FALLING was=%d now=%d via=receipts\n", prev.Count, newest.Count)
		}
		return code
	}
	fmt.Fprintf(stdout, "VERBS UNUSED STALE dev_sha=%s\n", oneline.Field(newest.DevSHA))
	return ExitFail
}

// relation places the newest dev_sha against the previous one: SAME, NEWER
// (the previous is its ancestor) or OTHER.
func relation(ctx context.Context, repo, prev, newest string) (string, error) {
	if prev == "" || newest == "" {
		return "", fmt.Errorf("an entry has no dev_sha (prev %q, newest %q)", prev, newest)
	}
	if prev == newest {
		return "SAME", nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "merge-base", "--is-ancestor", prev, newest)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return "NEWER", nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "OTHER", nil
	}
	return "", fmt.Errorf("git merge-base --is-ancestor %s %s: %v: %s", prev, newest, err, strings.TrimSpace(string(out)))
}
