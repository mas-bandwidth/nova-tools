// The read verb: a friend read takes the PR record, the lines and CI from
// Redis and the diff from the bench mirror, zero GitHub calls (nova-tools
// #3599), and a typed line carries across an identical-diff head move as a
// record, never a re-read (nova-tools #3630). Registered through
// registry.go; main.go is untouched.
//
//	nova-sprint read brief  --repo <r> --n <n> --out <dir> [--mirror <dir>] [--redis <addr>]
//	nova-sprint read brief  --id <task> [--sprint <S>] --out <dir> [--mirror <dir>] [--redis <addr>]
//	nova-sprint read brief  --pr <n> [--repo <r>] [--issue <ref>] [--mirror <dir>] [--no-github] [--redis <addr>]
//	nova-sprint read post   --repo <r> --n <n> --line <typed line> [--mirror <dir>] [--no-github] [--owner <o>] [--redis <addr>]
//	nova-sprint read post   --file <scores.tsv> [--mirror <dir>] [--no-github] [--redis <addr>]
//	nova-sprint read digest --repo <r> --n <n> [--sprint <S>] [--mirror <dir>] [--head <sha>] [--base-ref <ref>] [--redis <addr>]
//	nova-sprint read carry  --repo <r> --n <n> [--sprint <S>] [--mirror <dir>] [--base-ref <ref>] [--redis <addr>]
//
// <r> is owner/name or name: every subverb keys the PR record pr:<name>:<n>
// by the bare name (internal/nsprint/prkey), the key pr record writes.
//
// brief writes the read brief; with --ids it reads the read task
// (task:<id>, or s:<S>:task:<id> with --sprint) for the PR and the exact
// head, so a friend holding a read task needs nothing but its id. brief
// --pr (#4335, #4315) prints the whole read to stdout in one screen: the
// issue, the imported card, DONE-WHEN, the PR body, the files with +/-, the
// check rollup at head and the rubric, from Redis and the mirror, with one
// REST read only for a section Redis has no copy of (named on its SOURCES
// line; --no-github makes it a GAP). post --file posts every row of a
// <repo>\t<n>\t<line> file with a ROW receipt each and prints every
// refusal (read.PostFile). post
// stores the typed line through the line store (internal/nsprint/line,
// #3595: one call, the ci and base gates measured from Redis and the scope
// gate from the mirror diff against PATHS, a typed gate that disagrees
// refused) and mirrors it as one PR comment (REST) unless --no-github,
// until the comment readers read the store. digest
// records the diff identity of the head a line is typed at (diff_sha256 on
// the unit record; the reader runs it at read time). carry compares that
// with the unit's head now, read from the mirror (default
// ~/nova-bench/mirror/<repo>.git), and when the diffs are byte-identical
// after the base merge is normalised copies every typed line to the new
// head with a carried_from receipt; the lander counts a carried read as a
// read (internal/nsprint/land). A changed diff is REFUSED naming the files,
// and a re-read is the one remedy.
//
// Exit 0 done (CARRIED, NOTHING, RECORDED, the brief or the post); 1
// refused with the remedy on the line (REFUSED carry, no record, no
// token); 2 usage, before Redis is touched.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/line"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	register(Verb{
		Name:    "read",
		Summary: "brief: the read brief from Redis and the mirror (zero GitHub calls); post: store a typed line; digest: record a read's diff digest; carry: typed lines across an identical-diff head move",
		Run:     runRead,
	})
}

const readUsage = "want brief --repo <r> --n <n> --out <dir> [--mirror <dir>] [--redis <addr>], brief --ids <task> [--sprint <S>] --out <dir> [--mirror <dir>] [--redis <addr>], brief --pr <n> [--repo <r>] [--issue <ref>] [--mirror <dir>] [--no-github] [--redis <addr>], post --repo <r> --n <n> --line <typed line> [--mirror <dir>] [--no-github] [--owner <o>] [--redis <addr>], post --file <scores.tsv> [--mirror <dir>] [--no-github] [--redis <addr>], digest --repo <r> --n <n> [--sprint <S>] [--mirror <dir>] [--head <sha>] [--base-ref <ref>] [--redis <addr>] or carry --repo <r> --n <n> [--sprint <S>] [--mirror <dir>] [--base-ref <ref>] [--redis <addr>]"

func runRead(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "read", readUsage)
	}
	sub := args[0]
	fs := taskFlags("read " + sub)
	redisAddr := fs.String("redis", redisDefault("NOVA_SPRINT_REDIS"), verbflag.HelpRedis)
	repo := fs.String("repo", "", verbflag.HelpRepo)
	n := fs.String("n", "", verbflag.HelpN)
	outDir := fs.String("out", "", "the directory the brief is written to")
	mirror := fs.String("mirror", "", "the bench mirror of the repo (default ~/nova-bench/mirror/<repo>.git)")
	typed := fs.String("line", "", "the typed line to post")
	noGitHub := fs.Bool("no-github", false, "post nothing to GitHub, the record only")
	owner := fs.String("owner", "mas-bandwidth", "the GitHub owner the repo lives under")
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	head := fs.String("head", "", "the head the digest is of (default the record's)")
	baseRef := fs.String("base-ref", "", "the base ref the diff is read against")
	taskIDs := fs.String("ids", "", verbflag.HelpIDs)
	prN := fs.String("pr", "", verbflag.HelpPR)
	issue := fs.String("issue", "", "the issue number")
	file := fs.String("file", "", "the file")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		return refuse(errOut, "read "+sub, readUsage)
	}
	if *redisAddr == "" {
		*redisAddr = os.Getenv("NOVA_REDIS_ADDR")
	}
	if *prN != "" || *issue != "" {
		if sub != "brief" || *prN == "" || *n != "" || *outDir != "" || *typed != "" || *taskIDs != "" || *file != "" || *head != "" || *baseRef != "" || *redisAddr == "" {
			return refuse(errOut, "read "+sub, "--pr and --issue are brief only: want brief --pr <n> [--repo <r>] [--issue <ref>] [--mirror <dir>] [--no-github] [--redis <addr>] (or NOVA_SPRINT_REDIS)")
		}
		return runReadBriefPR(ctx, *redisAddr, *repo, *prN, *issue, *mirror, *noGitHub, out, errOut)
	}
	if *file != "" {
		if sub != "post" || *repo != "" || *n != "" || *typed != "" || *outDir != "" || *taskIDs != "" || *head != "" || *baseRef != "" || *redisAddr == "" {
			return refuse(errOut, "read "+sub, "--file is post only: want post --file <scores.tsv> [--mirror <dir>] [--no-github] [--redis <addr>] (or NOVA_SPRINT_REDIS); a row is <repo>\\t<n>\\t<typed line>")
		}
		return runReadPostFile(ctx, *redisAddr, *file, *mirror, *noGitHub, out, errOut)
	}
	if *taskIDs != "" {
		taskID := oneID(*taskIDs)
		if taskID == "" || sub != "brief" || *repo != "" || *n != "" || *outDir == "" || *typed != "" || *noGitHub || *head != "" || *baseRef != "" || *redisAddr == "" {
			return refuse(errOut, "read "+sub, "--ids is brief only, one task: want brief --ids <task> [--sprint <S>] --out <dir> [--mirror <dir>] [--redis <addr>] (or NOVA_SPRINT_REDIS)")
		}
		return runReadBriefTask(ctx, *redisAddr, *sprint, taskID, *mirror, *outDir, out, errOut)
	}
	// --repo is owner/name or name (internal/nsprint/prkey): the record,
	// the ci hash and the mirror take the bare name; an owner in --repo is
	// the comment mirror's owner and must agree with an explicit --owner.
	repoOwner, repoName, rerr := prkey.Split(*repo)
	if *repo == "" || rerr != nil || *redisAddr == "" {
		return refuse(errOut, "read "+sub, "needs --repo <owner/name|name>, --n <number> and --redis <addr> (or NOVA_SPRINT_REDIS); "+readUsage)
	}
	if strings.Contains(*repo, "/") {
		ownerSet := false
		fs.Visit(func(f *flag.Flag) { ownerSet = ownerSet || f.Name == "owner" })
		if ownerSet && *owner != repoOwner {
			return refuse(errOut, "read "+sub, "--repo "+*repo+" names owner "+repoOwner+" but --owner is "+*owner+"; pass one")
		}
		*owner = repoOwner
	}
	*repo = repoName
	num, err := strconv.Atoi(*n)
	if err != nil || num <= 0 {
		return refuse(errOut, "read "+sub, "--n wants a positive PR number, got "+strconv.Quote(*n))
	}
	switch sub {
	case "brief":
		if *outDir == "" || *typed != "" || *noGitHub {
			return refuse(errOut, "read brief", "want --repo <r> --n <n> --out <dir> [--mirror <dir>] [--redis <addr>]")
		}
	case "post":
		if *typed == "" || *outDir != "" {
			return refuse(errOut, "read post", "want --repo <r> --n <n> --line <typed line> [--mirror <dir>] [--no-github] [--owner <o>] [--redis <addr>]")
		}
	case "digest", "carry":
		if *typed != "" || *outDir != "" || *noGitHub || (sub == "carry" && *head != "") {
			return refuse(errOut, "read "+sub, "want --repo <r> --n <n> [--sprint <S>] [--mirror <dir>] [--base-ref <ref>] [--redis <addr>]"+map[bool]string{true: " [--head <sha>]"}[sub == "digest"])
		}
	default:
		return refuse(errOut, "read", "unknown subverb "+sub+"; "+readUsage)
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "read "+sub, err.Error())
	}
	defer func() { _ = st.Close() }()
	switch sub {
	case "brief":
		return read.Brief(ctx, st.Client(), *repo, *n, *mirror, *outDir, out, errOut)
	case "post":
		var poster *read.Poster
		if !*noGitHub {
			token, terr := envGitHubToken()
			if terr != nil {
				fmt.Fprintf(errOut, "READ POST REFUSED repo=%s n=%s why=%s\n", *repo, *n, oneline.Escape(terr.Error()))
				return 1
			}
			if token == "" {
				fmt.Fprintf(errOut, "READ POST REFUSED repo=%s n=%s why=no GH_TOKEN or GITHUB_TOKEN in the environment and no seat token for the comment mirror; pass --seat with a seats.tsv row naming the seat's GitHub token env (seventh column), or pass --no-github (Redis only)\n", *repo, *n)
				return 1
			}
			poster = &read.Poster{BaseURL: os.Getenv("GITHUB_API_URL"), Owner: *owner, Token: token, Redis: st.Client()}
		}
		return read.PostMeasured(ctx, st.Client(), *repo, *n, *typed, readMirror(*mirror, *repo), poster, out, errOut)
	}
	return runReadCarry(ctx, st, sub, land.ID{Repo: *repo, N: num}, *sprint, *mirror, *head, *baseRef, out, errOut)
}

// runReadBriefTask is `read brief --id`: the task hash names the PR and the
// head (read.TargetOf), then the brief is Brief's at that head.
func runReadBriefTask(ctx context.Context, addr, sprint, id, mirror, outDir string, out, errOut io.Writer) int {
	st, err := store.Open(ctx, addr)
	if err != nil {
		return refuse(errOut, "read brief", err.Error())
	}
	defer func() { _ = st.Close() }()
	fields, err := read.TaskFields(ctx, st.Client(), sprint, id)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint read brief: %v\n", err)
		return 2
	}
	if len(fields) == 0 {
		fmt.Fprintf(errOut, "READ BRIEF REFUSED task=%s why=MISSING %s; name the sprint with --sprint <S> for a sprint task\n", id, strings.Join(read.TaskKeys(sprint, id), " and "))
		return 1
	}
	return read.BriefTask(ctx, st.Client(), id, fields, mirror, outDir, out, errOut)
}

// runReadBriefPR is `read brief --pr`: the one-screen brief on stdout
// (read.BriefPR). --repo defaults to nova-tools.
func runReadBriefPR(ctx context.Context, addr, repo, n, issue, mirror string, noGitHub bool, out, errOut io.Writer) int {
	if repo == "" {
		repo = "nova-tools"
	}
	_, name, err := prkey.Split(repo)
	if v, aerr := strconv.Atoi(n); err != nil || aerr != nil || v <= 0 {
		return refuse(errOut, "read brief", "--pr wants a positive PR number and --repo <owner/name|name>, got --pr "+strconv.Quote(n)+" --repo "+strconv.Quote(repo))
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		return refuse(errOut, "read brief", err.Error())
	}
	defer func() { _ = st.Close() }()
	o := read.PRBriefOptions{Repo: repo, N: n, Issue: issue, Mirror: readMirror(mirror, name)}
	if !noGitHub {
		o.GitHub = &read.GitHub{BaseURL: os.Getenv("GITHUB_API_URL"), Token: ghToken(), Redis: st.Client()}
	}
	return read.BriefPR(ctx, st.Client(), o, out, errOut)
}

// runReadPostFile is `read post --file`: every row through the same post as
// `read post --line`, one receipt per row (read.PostFile).
func runReadPostFile(ctx context.Context, addr, file, mirror string, noGitHub bool, out, errOut io.Writer) int {
	token := ""
	if !noGitHub {
		if token = ghToken(); token == "" {
			fmt.Fprintf(errOut, "READ POST FILE REFUSED file=%s why=no GH_TOKEN or GITHUB_TOKEN in the environment for the comment mirror; run under nova-secrets exec --only GH_TOKEN, or pass --no-github (Redis only)\n", file)
			return 1
		}
	}
	f, err := os.Open(file)
	if err != nil {
		fmt.Fprintf(errOut, "READ POST FILE REFUSED file=%s why=%v\n", file, err)
		return 1
	}
	defer f.Close()
	st, err := store.Open(ctx, addr)
	if err != nil {
		return refuse(errOut, "read post", err.Error())
	}
	defer func() { _ = st.Close() }()
	post := func(row read.Row, stdout, stderr io.Writer) int {
		var poster *read.Poster
		if !noGitHub {
			poster = &read.Poster{BaseURL: os.Getenv("GITHUB_API_URL"), Owner: row.Owner, Token: token, Redis: st.Client()}
		}
		return read.PostMeasured(ctx, st.Client(), row.Repo, row.N, row.Line, readMirror(mirror, row.Repo), poster, stdout, stderr)
	}
	return read.PostFile(f, file, post, out, errOut)
}

// ghToken is GH_TOKEN, else GITHUB_TOKEN.
func ghToken() string {
	if t := os.Getenv("GH_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GITHUB_TOKEN")
}

// runReadCarry is digest and carry over the unit record (#3630).
func runReadCarry(ctx context.Context, st *store.Store, sub string, id land.ID, sprint, mirror, head, baseRef string, out, errOut io.Writer) int {
	name := "read " + sub
	dir := readMirror(mirror, id.Repo)
	if dir == "" {
		fmt.Fprintf(errOut, "nova-sprint %s: no mirror: pass --mirror <dir> (a bare mirror or a clone that resolves the base ref)\n", name)
		return 1
	}
	c := st.Client()
	sprint, err := devRedSprint(ctx, c, sprint)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", name, err)
		return 1
	}
	if sub == "carry" {
		res, err := line.Carry(ctx, c, sprint, id, dir, baseRef)
		if err != nil {
			fmt.Fprintf(errOut, "nova-sprint %s: %v\n", name, err)
			return 1
		}
		fmt.Fprintln(out, res.Line(id))
		if res.Outcome == line.Refused {
			return 1
		}
		return 0
	}
	unit, err := land.ResolvePR(ctx, c, sprint, id)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", name, err)
		return 1
	}
	ukey := land.UnitKey(sprint, unit)
	u, err := c.HGetAll(ctx, ukey).Result()
	if err != nil || len(u) == 0 {
		fmt.Fprintf(errOut, "nova-sprint %s: MISSING %s\n", name, ukey)
		return 1
	}
	if head == "" {
		head = u["head"]
	}
	if baseRef == "" {
		baseRef = u["base"]
	}
	if baseRef == "" {
		baseRef = "dev"
	}
	if head == "" {
		fmt.Fprintf(errOut, "nova-sprint %s: unit %s has no head; pass --head <sha>\n", name, unit)
		return 1
	}
	d, err := line.DiffDigest(ctx, dir, baseRef, head)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", name, err)
		return 1
	}
	if err := line.Record(ctx, c, ukey, head, d); err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", name, err)
		return 1
	}
	fmt.Fprintf(out, "RECORDED %s head=%s diff_sha256=%s files=%d\n", id, head[:min(8, len(head))], d.SHA256[:16], len(d.Files))
	return 0
}

// readMirror is the mirror directory: the flag, else the bench mirror of
// the repo when present, else "".
func readMirror(flag, repo string) string {
	if flag != "" {
		return flag
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if d := filepath.Join(home, "nova-bench", "mirror", strings.TrimSuffix(repo, ".git")+".git"); isDir(d) {
		return d
	}
	return ""
}
