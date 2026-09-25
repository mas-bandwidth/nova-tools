package harvest

// body.go writes the card PR's title and body from the card record (#3712).
// Glenn 2026-09-24 11:25 PM: "rely on the model as little as possible": the
// wrapper fills every field it knows and the model only does the work. The
// body is built from s:<S>:card:<L> and s:<S>:card:<L>:result:a<n> alone (no
// model, no bench file) plus the paths git reports for base_sha..pushed_sha in
// the harvest clone, so nova-decide review's mechanical checks (internal/prereview)
// and the lander read typed lines:
//
//	BASE: / base-sha: / PATHS: (the card's, verbatim: prereview's paths
//	check bounds the changed files by it) / CHANGED: (base_sha..pushed_sha)
//	/ DEPENDS-ON: / DONE-WHEN: / STREAM: / WHO: (the card's WHO, any when
//	it names none; #3488, #3929)
//	Closes #<n> (origin an issue of the card's repo) or ORIGIN: <origin>
//	SELF-CHECK: <w_check> (<TEST>)      prereview's selfcheck on a non-cell PR
//	RESULT line 1 / line 2 / note       prereview's donewhen reads line 2
//	facts, with files: <paths>          prereview's claims reads files:
//	the Claude Code line

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// Record is the card hash and its attempt's result hash, as Redis holds them.
type Record struct {
	Card   map[string]string
	Result map[string]string
}

// ReadRecord reads s:<S>:card:<label> and its result:a<attempt> in one
// pipeline.
func ReadRecord(ctx context.Context, st *store.Store, sprint, label, attempt string) (Record, error) {
	key := "s:" + sprint + ":card:" + label
	pipe := st.Client().Pipeline()
	c := pipe.HGetAll(ctx, key)
	r := pipe.HGetAll(ctx, key+":result:a"+attempt)
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return Record{}, fmt.Errorf("%s: read record: %w", label, err)
	}
	return Record{Card: c.Val(), Result: r.Val()}, nil
}

// TitleMax is how much of the TASK or DONE-WHEN sentence the title carries.
const TitleMax = 70

// Title is `<label>: <TASK or DONE-WHEN, first 70 chars>`; a record with
// neither keeps the old `<label>: nova-sprint <S> card <label> attempt <n>`.
func Title(sprint string, c Card, rec Record) string {
	s := strings.TrimSpace(rec.Card["task"])
	if s == "" {
		s = strings.TrimSpace(rec.Card["done_when"])
	}
	if s == "" {
		return fmt.Sprintf("%s: nova-sprint %s card %s attempt %s", c.Label, sprint, c.Label, c.Attempt)
	}
	if r := []rune(s); len(r) > TitleMax {
		s = strings.TrimSpace(string(r[:TitleMax]))
	}
	return c.Label + ": " + s
}

// ClaudeLine is the body's last line.
const ClaudeLine = "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

// issueURLRE is a GitHub issue URL: owner, name, number.
var issueURLRE = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/issues/([0-9]+)/?$`)

// fullRepo is owner/name; a bare name is under mas-bandwidth.
func fullRepo(repo string) string {
	repo = strings.TrimSuffix(strings.TrimSpace(repo), ".git")
	if repo == "" || strings.Contains(repo, "/") {
		return repo
	}
	return "mas-bandwidth/" + repo
}

// originLine is `Closes #<n>` when origin is an issue of the card's own repo,
// else `ORIGIN: <origin>` (none when the record has none).
func originLine(repo, origin string) string {
	origin = strings.TrimSpace(origin)
	if m := issueURLRE.FindStringSubmatch(origin); m != nil && strings.EqualFold(m[1]+"/"+m[2], fullRepo(repo)) {
		return "Closes #" + m[3]
	}
	return "ORIGIN: " + or(origin, "none")
}

func or(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return strings.TrimSpace(s)
}

// oneLineField keeps a record value on its body line.
func oneLineField(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// DeclaredPaths is the body's PATHS line: the card's declared PATHS from the
// record, verbatim (what the card was allowed to touch), so a card that wrote
// outside them fails nova-decide review's paths check instead of hiding it.
func DeclaredPaths(rec Record) string {
	return or(rec.Card["paths"], "none")
}

// ChangedPaths is the body's CHANGED line: the paths base_sha..pushed_sha
// changed as git in the harvest clone reported them, else the wrapper's own
// w_paths (the same range, read at card end), else unknown.
func ChangedPaths(rangePaths []string, rec Record) string {
	if len(rangePaths) > 0 {
		return strings.Join(rangePaths, " ")
	}
	return or(oneLineField(rec.Result["w_paths"]), "unknown")
}

// Body is the PR body built from the record alone. rangePaths are the paths
// changed in base_sha..pushed_sha (nil when git could not say).
func Body(sprint, bench string, c Card, rec Record, rangePaths []string) string {
	card, res := rec.Card, rec.Result
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	base := or(card["base"], c.Base)
	changed := ChangedPaths(rangePaths, rec)
	line("BASE: %s", or(base, "-"))
	line("base-sha: %s", or(card["base_sha"], "-"))
	line("PATHS: %s", DeclaredPaths(rec))
	line("CHANGED: %s", changed)
	line("DEPENDS-ON: %s", or(oneLineField(card["depends_on"]), "none"))
	line("DONE-WHEN: %s", or(oneLineField(card["done_when"]), "-"))
	line("STREAM: %s", or(oneLineField(card["stream"]), "none"))
	line("WHO: %s", or(oneLineField(card["who"]), "any"))
	line("%s", originLine(or(card["repo"], c.Repo), card["origin"]))
	line("")
	line("SELF-CHECK: %s (%s)", or(res["w_check"], "not-run"), or(oneLineField(card["test"]), "none"))
	line("")
	// The model's two lines: line 1 as the result record parsed it (the
	// card's contract line), line 2 as the wrapper recorded it.
	line("%s", or(oneLineField(res["line1"]), "RESULT: "+c.Label))
	line("%s", or(oneLineField(res["w_line2"]), "-"))
	if note := strings.TrimSpace(res["w_note"]); note != "" {
		line("")
		line("%s", note)
	}
	line("")
	line("sprint: %s", sprint)
	line("card: %s", c.Label)
	line("identity: %s", or(c.Identity, card["identity"]))
	line("attempt: %s", or(c.Attempt, card["attempt"]))
	line("bench: %s", or(bench, card["bench"]))
	line("tier: %s route: %s model: %s", or(res["w_tier"], "-"), or(res["w_route"], "-"), or(res["w_model"], "-"))
	line("wall_ms: %s", or(res["w_wall_ms"], "-"))
	line("pushed_sha: %s", or(c.PushedSHA, card["pushed_sha"]))
	line("results: %s", or(c.Results, card["results"]))
	line("files: %s", changed)
	line("")
	line("%s", ClaudeLine)
	return b.String()
}
