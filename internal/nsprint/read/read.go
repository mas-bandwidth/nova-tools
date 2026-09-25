// Package read builds a friend's read brief from Redis and the bench mirror
// and stores the typed line the read produces (nova-tools#3599, umbrella
// #3594: GitHub is a git remote only).
//
// A read makes zero GitHub calls. The PR record pr:<repo>:<n> (head,
// base, base_sha, paths, done_when, stream, depends_on, branch) and the
// lines already posted (the list pr:<repo>:<n>:lines) come from Redis in one
// pipeline; the CI verdict at head (ci:<repo>:<head>, #3597) in a second,
// because its key needs the head; the diff is `git -C <mirror> diff
// <base_sha>..<head>` against the bench mirror that mirror-refresh keeps at
// refs/pull/*/head. Post appends the typed line to the record's lines list
// in one MULTI and, until #3595 retires PR comments, mirrors it as one REST
// comment unless told not to.
package read

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pr"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/spec"
	"github.com/redis/go-redis/v9"
)

//go:embed tmpl/read.tmpl
var templates embed.FS

// RecordFields are the pr:<repo>:<n> hash fields a read brief carries, in
// the order the pipeline reads them.
var RecordFields = []string{"head", "base", "base_sha", "paths", "done_when", "stream", "depends_on", "branch", "who"}

// Record is one PR record from Redis with the lines and CI read beside it.
type Record struct {
	Repo, N                                                              string
	Head, Base, BaseSHA, Paths, DoneWhen, Stream, DependsOn, Branch, Who string
	Lines                                                                []string
	CI                                                                   map[string]string
}

// Key is the record's hash key, the one PR record key pr:<name>:<n>
// (internal/nsprint/prkey; repo may be owner/name or name); LinesKey the list
// of typed lines under it; CIKey the CI hash at one head (#3597).
func Key(repo, n string) string      { return prkey.KeyText(repo, n) }
func LinesKey(repo, n string) string { return Key(repo, n) + ":lines" }
func CIKey(repo, head string) string { return "ci:" + repo + ":" + head }

// ErrMissing is wrapped when the record has no head: the PR is not in Redis.
var ErrMissing = errors.New("MISSING")

// Load reads the record, its lines and its CI hash: one pipeline for the
// record and the lines, one HGETALL for CI once the head is known.
func Load(ctx context.Context, c *redis.Client, repo, n string) (Record, error) {
	rec := Record{Repo: repo, N: n, CI: map[string]string{}}
	pipe := c.Pipeline()
	hm := pipe.HMGet(ctx, Key(repo, n), RecordFields...)
	lr := pipe.LRange(ctx, LinesKey(repo, n), 0, -1)
	if _, err := pipe.Exec(ctx); err != nil {
		return rec, fmt.Errorf("read %s: %w", Key(repo, n), err)
	}
	vals := hm.Val()
	get := func(i int) string {
		if i < len(vals) && vals[i] != nil {
			if s, ok := vals[i].(string); ok {
				return s
			}
		}
		return ""
	}
	rec.Head, rec.Base, rec.BaseSHA, rec.Paths = get(0), get(1), get(2), get(3)
	rec.DoneWhen, rec.Stream, rec.DependsOn, rec.Branch, rec.Who = get(4), get(5), get(6), get(7), get(8)
	rec.Lines = lr.Val()
	if rec.Head == "" {
		return rec, fmt.Errorf("%w %s: no head; the record is written when the PR is opened (card harvest) or imported", ErrMissing, Key(repo, n))
	}
	ci, err := c.HGetAll(ctx, CIKey(repo, rec.Head)).Result()
	if err != nil {
		return rec, fmt.Errorf("read %s: %w", CIKey(repo, rec.Head), err)
	}
	rec.CI = ci
	return rec, nil
}

// DefaultMirror is the bench mirror of a repo: ~/nova-bench/mirror/<repo>.git.
func DefaultMirror(repo string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, "nova-bench", "mirror", repo+".git")
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// Diff is the mirror's view of the PR: the patch base_sha..head, the files
// it touches, and the head refs/pull/<n>/head resolves to in the mirror
// ("" when the mirror has no such ref yet).
type Diff struct {
	Patch      string
	Files      []string
	MirrorHead string
}

// MirrorDiff runs `git diff <base_sha>..<head>` in the mirror. Both commits
// must already be there: mirror-refresh fetches refs/heads/* and
// refs/pull/*/head every tick, and a read never fetches from GitHub itself.
func MirrorDiff(ctx context.Context, mirror, n, baseSHA, head string) (Diff, error) {
	var d Diff
	if st, err := os.Stat(mirror); err != nil || !st.IsDir() {
		return d, fmt.Errorf("mirror %s is not a directory; run mirror-refresh --once or pass --mirror", mirror)
	}
	for _, sha := range []string{baseSHA, head} {
		if _, err := git(ctx, mirror, "rev-parse", "--verify", "--quiet", sha+"^{commit}"); err != nil {
			return d, fmt.Errorf("mirror %s has no commit %s yet; wait one mirror-refresh tick (refs/pull/%s/head) and retry", mirror, sha, n)
		}
	}
	if out, err := git(ctx, mirror, "rev-parse", "--verify", "--quiet", "refs/pull/"+n+"/head^{commit}"); err == nil {
		d.MirrorHead = strings.TrimSpace(out)
	}
	patch, err := git(ctx, mirror, "diff", baseSHA+".."+head)
	if err != nil {
		return d, err
	}
	names, err := git(ctx, mirror, "diff", "--name-only", baseSHA+".."+head)
	if err != nil {
		return d, err
	}
	d.Patch = patch
	d.Files = strings.Fields(names)
	sort.Strings(d.Files)
	return d, nil
}

// OutsidePaths lists the changed files no PATHS entry covers: an entry
// covers the file it names and every file under it when it is a directory.
// An unparseable PATHS covers nothing, so every file is outside it.
func OutsidePaths(paths string, files []string) []string {
	canon, err := pr.CanonPaths(paths)
	if err != nil {
		canon = nil
	}
	var out []string
	for _, f := range files {
		covered := false
		for _, p := range canon {
			if f == p || strings.HasPrefix(f, p+"/") {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, f)
		}
	}
	return out
}

// briefData is what the template sees.
type briefData struct {
	Record
	Mirror, MirrorHead, DiffFile, Out string
	Files, Outside                    []string
	CILines, Lines                    []string
	Rendered                          string
}

func orDash(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "-"
	}
	return s
}

// Render fills tmpl/read.tmpl from the record and the mirror diff. It
// returns the brief bytes; the caller writes them.
func Render(rec Record, d Diff, mirror, outDir string) ([]byte, error) {
	raw, err := templates.ReadFile("tmpl/read.tmpl")
	if err != nil {
		return nil, err
	}
	t, err := template.New("read").Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, err
	}
	data := briefData{Record: rec, Mirror: mirror, MirrorHead: d.MirrorHead, Out: outDir}
	data.Base, data.BaseSHA, data.Paths = orDash(rec.Base), orDash(rec.BaseSHA), orDash(rec.Paths)
	data.DoneWhen, data.Stream, data.DependsOn, data.Branch = orDash(rec.DoneWhen), orDash(rec.Stream), orDash(rec.DependsOn), orDash(rec.Branch)
	data.DiffFile = filepath.Join(outDir, "diff.patch")
	data.Files = d.Files
	data.Outside = OutsidePaths(rec.Paths, d.Files)
	keys := make([]string, 0, len(rec.CI))
	for k := range rec.CI {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		data.CILines = append(data.CILines, k+"="+strings.Join(strings.Fields(rec.CI[k]), " "))
	}
	for _, l := range rec.Lines {
		data.Lines = append(data.Lines, strings.Join(strings.Fields(l), " "))
	}
	data.Rendered = time.Now().UTC().Format(time.RFC3339)
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Brief is the verb: load, diff, render, write <out>/brief.md and
// <out>/diff.patch, print one receipt. Exit 0 written, 1 refused with the
// remedy named, 2 could not run.
func Brief(ctx context.Context, c *redis.Client, repo, n, mirror, outDir string, stdout, stderr io.Writer) int {
	return brief(ctx, c, repo, n, "", "", mirror, outDir, stdout, stderr)
}

// brief is Brief at an exact head: head "" reads the record's head; a read
// task's head is the head it was queued at, so when the record has moved
// on the brief still reads the task's head (its CI and its diff) and the
// receipt names the record's head. id is the task, "" when there is none.
func brief(ctx context.Context, c *redis.Client, repo, n, head, id, mirror, outDir string, stdout, stderr io.Writer) int {
	if mirror == "" {
		mirror = DefaultMirror(repo)
	}
	rec, err := Load(ctx, c, repo, n)
	if err != nil {
		if errors.Is(err, ErrMissing) {
			fmt.Fprintf(stderr, "READ BRIEF REFUSED repo=%s n=%s why=%v\n", repo, n, err)
			return 1
		}
		fmt.Fprintf(stderr, "nova-sprint read brief: %v\n", err)
		return 2
	}
	recordHead := ""
	if head != "" && head != rec.Head {
		ci, err := c.HGetAll(ctx, CIKey(repo, head)).Result()
		if err != nil {
			fmt.Fprintf(stderr, "nova-sprint read brief: read %s: %v\n", CIKey(repo, head), err)
			return 2
		}
		recordHead, rec.Head, rec.CI = rec.Head, head, ci
	}
	if rec.BaseSHA == "" {
		fmt.Fprintf(stderr, "READ BRIEF REFUSED repo=%s n=%s why=record has no base_sha; the card names it (BASE/base-sha) and harvest writes it\n", repo, n)
		return 1
	}
	d, err := MirrorDiff(ctx, mirror, n, rec.BaseSHA, rec.Head)
	if err != nil {
		fmt.Fprintf(stderr, "READ BRIEF REFUSED repo=%s n=%s why=%v\n", repo, n, err)
		return 1
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(stderr, "nova-sprint read brief: %v\n", err)
		return 2
	}
	body, err := Render(rec, d, mirror, outDir)
	if err != nil {
		fmt.Fprintf(stderr, "nova-sprint read brief: %v\n", err)
		return 2
	}
	briefPath := filepath.Join(outDir, "brief.md")
	if err := os.WriteFile(filepath.Join(outDir, "diff.patch"), []byte(d.Patch), 0o644); err != nil {
		fmt.Fprintf(stderr, "nova-sprint read brief: %v\n", err)
		return 2
	}
	if err := os.WriteFile(briefPath, body, 0o644); err != nil {
		fmt.Fprintf(stderr, "nova-sprint read brief: %v\n", err)
		return 2
	}
	moved := ""
	if d.MirrorHead != "" && d.MirrorHead != rec.Head {
		moved = " mirror_head=" + short(d.MirrorHead)
	}
	if recordHead != "" {
		moved += " record_head=" + short(recordHead)
	}
	if id != "" {
		moved += " task=" + id
	}
	fmt.Fprintf(stdout, "READ BRIEF repo=%s n=%s head=%s base_sha=%s files=%d outside_paths=%d lines=%d ci=%d out=%s diff=%s github_calls=0%s\n",
		repo, n, short(rec.Head), short(rec.BaseSHA), len(d.Files), len(OutsidePaths(rec.Paths, d.Files)), len(rec.Lines), len(rec.CI), briefPath, filepath.Join(outDir, "diff.patch"), moved)
	return 0
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// Kinds are the typed lines a read may post; the first word of the line.
var Kinds = []string{"SCORE", "HOLD", "REPAIR", "SPEC", "SPEC-WRITTEN", "CLOSE", "JEV-DIFF"}

var (
	whoRx  = regexp.MustCompile(`(^|\s)who=\S+`)
	headRx = regexp.MustCompile(`(^|\s)head=[0-9a-f]{7,40}(\s|$)`)
)

// CheckLine refuses a line that is not a typed line: the first word is one
// of Kinds and the first line carries who=<name> and head=<sha>.
func CheckLine(line string) error {
	first := strings.SplitN(strings.ReplaceAll(line, "\r\n", "\n"), "\n", 2)[0]
	tok := strings.Fields(first)
	if len(tok) == 0 {
		return errors.New("empty line; want <KIND> who=<name> head=<sha> ...")
	}
	ok := false
	for _, k := range Kinds {
		if tok[0] == k {
			ok = true
		}
	}
	if !ok {
		return fmt.Errorf("first word %q is not a typed line; want one of %s", tok[0], strings.Join(Kinds, "|"))
	}
	if !whoRx.MatchString(first) {
		return errors.New("line has no who=<name>")
	}
	if tok[0] == "SPEC" { // a spec issue has no head; its line carries rev= and score= (#3370)
		_, err := spec.ParseLine(first)
		return err
	}
	if !headRx.MatchString(first) {
		return errors.New("line has no head=<sha> (7 to 40 hex)")
	}
	return nil
}

// Poster mirrors one typed line as a PR comment through one REST call.
// It is nil when --no-github is given, and it goes when #3595 lands.
type Poster struct {
	BaseURL string // GITHUB_API_URL or https://api.github.com
	Owner   string
	Token   string
	HTTP    *http.Client
}

// Comment is the one REST call: POST /repos/<owner>/<repo>/issues/<n>/comments.
func (p Poster) Comment(ctx context.Context, repo, n, body string) (int64, error) {
	base := strings.TrimRight(p.BaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	ep := base + "/repos/" + url.PathEscape(p.Owner) + "/" + url.PathEscape(repo) + "/issues/" + url.PathEscape(n) + "/comments"
	payload, _ := json.Marshal(map[string]string{"body": body})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	if p.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.Token)
	}
	client := p.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("github %d", resp.StatusCode)
	}
	var v struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(b, &v)
	return v.ID, nil
}

// Post appends the typed line to pr:<repo>:<n>:lines and stamps the record
// (last_line, last_line_at) in one MULTI, or for an event line (EventKinds)
// in one library call that also moves its tasks, then mirrors it as one
// comment when poster is not nil. Exit 0 posted, 1 refused, 2 could not run. The Redis
// write is the record; a comment that fails after it is reported as such.
func Post(ctx context.Context, c *redis.Client, repo, n, line string, poster *Poster, stdout, stderr io.Writer) int {
	if err := CheckLine(line); err != nil {
		fmt.Fprintf(stderr, "READ POST REFUSED repo=%s n=%s why=%v\n", repo, n, err)
		return 1
	}
	if strings.Fields(line)[0] == "SPEC" {
		return postSpec(ctx, c, repo, n, line, poster, stdout, stderr)
	}
	head, err := c.HGet(ctx, Key(repo, n), "head").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		fmt.Fprintf(stderr, "nova-sprint read post: %v\n", err)
		return 2
	}
	if head == "" {
		fmt.Fprintf(stderr, "READ POST REFUSED repo=%s n=%s why=%s has no head; the record is written when the PR is opened or imported\n", repo, n, Key(repo, n))
		return 1
	}
	now := strconv.FormatInt(time.Now().Unix(), 10)
	kind := strings.Fields(line)[0]
	var lines int64
	moves := ""
	if EventKinds[kind] {
		ev, err := postEvent(ctx, c, repo, n, line, now)
		if err != nil {
			fmt.Fprintf(stderr, "nova-sprint read post: %v\n", err)
			return 2
		}
		lines = ev.Lines
		moves = fmt.Sprintf(" tasks_moved=%d copies_cut=%d", ev.Moved, ev.Cut)
		for _, s := range ev.Skipped {
			fmt.Fprintf(stderr, "READ POST SKIPPED repo=%s n=%s %s\n", repo, n, strings.ReplaceAll(s, "\n", " "))
		}
	} else {
		tx := c.TxPipeline()
		rp := tx.RPush(ctx, LinesKey(repo, n), line)
		tx.HSet(ctx, Key(repo, n), "last_line", strings.SplitN(line, "\n", 2)[0], "last_line_at", now)
		if _, err := tx.Exec(ctx); err != nil {
			fmt.Fprintf(stderr, "nova-sprint read post: %v\n", err)
			return 2
		}
		lines = rp.Val()
	}
	if poster == nil {
		fmt.Fprintf(stdout, "READ POST repo=%s n=%s kind=%s lines=%d github_calls=0%s\n", repo, n, kind, lines, moves)
		return 0
	}
	id, err := poster.Comment(ctx, repo, n, line)
	if err != nil {
		fmt.Fprintf(stderr, "READ POST REFUSED repo=%s n=%s kind=%s lines=%d redis=ok github=%v; the line is in Redis, re-run with --no-github or fix the token\n", repo, n, kind, lines, err)
		return 1
	}
	fmt.Fprintf(stdout, "READ POST repo=%s n=%s kind=%s lines=%d github_calls=1 comment=%d%s\n", repo, n, kind, lines, id, moves)
	return 0
}

// EventKinds are the typed lines whose post is an event for the sprint's
// tasks (nova-tools#3779): a CLOSE by a person lands every task naming the
// PR or an issue its record closes; a SCORE moves the PR's read task
// read-<n>-<head8> working -> merging, and ends the reader's read copy of
// every primary of the PR in reading (#4094, #4097): 8+ at the record head
// moves the primary to merging (tasks_moved), under 8 cuts one fix copy on
// the author's queue (copies_cut). Their post is one library call,
// ns_read_post (internal/nsprint/fn/lua/03_task_event.lua): the line and the
// move together or neither.
var EventKinds = map[string]bool{"CLOSE": true, "SCORE": true}

// FunctionReadPost is the library function that posts an event line.
const FunctionReadPost = "ns_read_post"

type event struct {
	Lines   int64
	Moved   int
	Cut     int // the copies the post cut (a fix copy, fresh reads)
	Skipped []string
}

func postEvent(ctx context.Context, c *redis.Client, repo, n, line, now string) (event, error) {
	var ev event
	res, err := c.FCall(ctx, FunctionReadPost, nil, prkey.Name(repo), n, line, now).StringSlice()
	if err != nil {
		return ev, fmt.Errorf("%s: %w (a store whose library predates it: nova-sprint fn load)", FunctionReadPost, err)
	}
	if len(res) < 6 || res[0] != "OK" {
		return ev, fmt.Errorf("%s: %s", FunctionReadPost, strings.Join(res, " "))
	}
	ev.Lines, _ = strconv.ParseInt(res[1], 10, 64)
	ev.Moved, _ = strconv.Atoi(res[3])
	ev.Skipped = res[6:]
	// the cut count (#4094) follows skipped; a library that predates it
	// replies with the notes there
	if len(res) > 6 {
		if k, err := strconv.Atoi(res[6]); err == nil {
			ev.Cut, ev.Skipped = k, res[7:]
		}
	}
	return ev, nil
}
