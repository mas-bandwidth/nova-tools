// The line verb: typed lines (SCORE, HOLD, REPAIR, SPEC, SPEC-WRITTEN,
// CLOSE, ...) as Redis records (nova-tools #3595), the store `read post`
// writes through. Registered through registry.go; main.go is untouched.
//
//	nova-sprint line post   --repo <r> --n <n> (--line <typed line> | --kind <K> --who <w> --head <sha>
//	                        [--score N] [--gates ci:ok,base:ok,scope:ok] [--body-file <f>]) [--mirror <dir>] [--redis <addr>]
//	nova-sprint line list   --repo <r> --n <n> [--head <sha>] [--redis <addr>]
//	nova-sprint line import --repo <r> --n <n> --comments-file <f|-> [--redis <addr>]
//
// post is one ns_line_post call: the record pr:<name>:<n>:line:<head>:<who>:<kind>
// (keyed by who: a second line of a kind by one reader at one head replaces
// it), the PR's lines log and the stream lander's reads. The ci and base
// gates are measured from Redis and the scope gate from the mirror diff
// against PATHS (default mirror ~/nova-bench/mirror/<repo>.git); a typed
// gate that disagrees with a measured one, or a SCORE over 7 with a measured
// gate not ok, is refused and nothing is written. list prints the lines at
// one head (default the record's head) oldest first, one read-only call.
// import copies the typed lines of a GitHub comments array (the output of
// `gh api repos/<o>/<r>/issues/<n>/comments`) into the store, idempotent on
// the comment id; it never calls GitHub itself. Zero GitHub calls in every
// subverb.
//
// Exit 0 done; 1 refused with the remedy on the line (no record, a gate
// that disagrees); 2 usage, before Redis is touched.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/line"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "line",
		Summary: "post|list|import: typed lines as Redis records keyed by PR, head, who and kind, gates measured (ci, base, scope), zero GitHub calls",
		Run:     runLine,
	})
}

const lineUsage = "want post --repo <r> --n <n> (--line <typed line> | --kind <K> --who <w> --head <sha> [--score N] [--gates <g>] [--body-file <f>]) [--mirror <dir>] [--redis <addr>], list --repo <r> --n <n> [--head <sha>] [--redis <addr>] or import --repo <r> --n <n> --comments-file <f|-> [--redis <addr>]"

func runLine(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "line", lineUsage)
	}
	sub := args[0]
	verb := "line " + sub
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", redisDefault("NOVA_SPRINT_REDIS"), "")
	repo := fs.String("repo", "", "")
	n := fs.String("n", "", "")
	typed := fs.String("line", "", "")
	kind := fs.String("kind", "", "")
	who := fs.String("who", "", "")
	head := fs.String("head", "", "")
	score := fs.Int("score", -1, "")
	gates := fs.String("gates", "", "")
	bodyFile := fs.String("body-file", "", "")
	mirror := fs.String("mirror", "", "")
	commentsFile := fs.String("comments-file", "", "")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 {
		return refuse(errOut, verb, lineUsage)
	}
	if *redisAddr == "" {
		*redisAddr = os.Getenv("NOVA_REDIS_ADDR")
	}
	_, name, rerr := prkey.Split(*repo)
	if num, err := strconv.Atoi(*n); err != nil || num <= 0 || rerr != nil || *redisAddr == "" {
		return refuse(errOut, verb, "needs --repo <owner/name|name>, --n <positive number> and --redis <addr> (or NOVA_SPRINT_REDIS); "+lineUsage)
	}
	built := *kind != "" || *who != "" || *score >= 0 || *gates != "" || *bodyFile != ""
	switch sub {
	case "post":
		if *commentsFile != "" || (*typed == "") == !built || (built && (*kind == "" || *who == "" || *head == "")) {
			return refuse(errOut, verb, "want --line <typed line>, or --kind, --who and --head (with --score, --gates, --body-file), not both")
		}
		if built {
			body := ""
			if *bodyFile != "" {
				b, err := os.ReadFile(*bodyFile)
				if err != nil {
					return refuse(errOut, verb, "--body-file: "+err.Error())
				}
				body = string(b)
			}
			*typed = line.Build(*kind, *who, *head, *score, *gates, body)
		}
	case "list":
		if *typed != "" || built || *mirror != "" || *commentsFile != "" {
			return refuse(errOut, verb, "want --repo <r> --n <n> [--head <sha>] [--redis <addr>]")
		}
	case "import":
		if *commentsFile == "" || *typed != "" || built || *head != "" || *mirror != "" {
			return refuse(errOut, verb, "want --repo <r> --n <n> --comments-file <f|-> [--redis <addr>]")
		}
	default:
		return refuse(errOut, "line", "unknown subverb "+sub+"; "+lineUsage)
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()
	c := st.Client()
	switch sub {
	case "post":
		l, err := line.Parse(*typed)
		if err != nil {
			fmt.Fprintf(errOut, "LINE POST REFUSED %s why=%v\n", prkey.KeyText(name, *n), err)
			return 1
		}
		p, err := line.Post(ctx, c, name, *n, *typed, read.MeasureScope(ctx, c, name, *n, l.Head, readMirror(*mirror, name)))
		if err != nil {
			fmt.Fprintf(errOut, "nova-sprint line post: %v\n", err)
			return 1
		}
		switch p.Status {
		case "MISSING":
			fmt.Fprintf(errOut, "LINE POST REFUSED %s why=no record head; the record is written when the PR is opened (pr record)\n", p.Key)
			return 1
		case "REFUSED":
			fmt.Fprintf(errOut, "LINE POST REFUSED %s kind=%s who=%s why=%s; nothing stored\n", prkey.KeyText(name, *n), l.Kind, l.Who, p.Why)
			return 1
		}
		fmt.Fprintf(out, "LINE POST %s kind=%s who=%s head=%s gates=%s measured=%s lines=%d github_calls=0\n",
			p.Key, l.Kind, l.Who, shortSHA(p.Head), dash(p.Gates), dash(p.Measured), p.Lines)
		return 0
	case "list":
		full, recs, err := line.List(ctx, c, name, *n, *head)
		if err != nil {
			fmt.Fprintf(errOut, "LINE LIST REFUSED %s why=%v\n", prkey.KeyText(name, *n), err)
			return 1
		}
		for _, r := range recs {
			fmt.Fprintf(out, "LINE %s who=%s head=%s score=%s gates=%s measured=%s source=%s at=%s\n",
				r["kind"], r["who"], shortSHA(r["head"]), dash(r["score"]), dash(r["gates"]), dash(r["measured"]), dash(r["source"]), r["at"])
		}
		fmt.Fprintf(out, "LINES %s head=%s count=%d\n", prkey.KeyText(name, *n), shortSHA(full), len(recs))
		return 0
	}
	var src io.Reader = os.Stdin
	if *commentsFile != "-" {
		f, err := os.Open(*commentsFile)
		if err != nil {
			return refuse(errOut, verb, "--comments-file: "+err.Error())
		}
		defer f.Close()
		src = f
	}
	comments, err := line.ParseComments(src)
	if err != nil {
		fmt.Fprintf(errOut, "LINE IMPORT REFUSED %s why=%v\n", prkey.KeyText(name, *n), err)
		return 1
	}
	res, err := line.Import(ctx, c, name, *n, comments)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint line import: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "LINE IMPORT %s comments=%d typed=%d imported=%d existed=%d missing=%d github_calls=0\n",
		prkey.KeyText(name, *n), res.Comments, res.Typed, res.Imported, res.Existed, res.Missing)
	if res.Missing > 0 {
		fmt.Fprintf(errOut, "LINE IMPORT REFUSED %s why=no record head; run pr record first\n", prkey.KeyText(name, *n))
		return 1
	}
	return 0
}

// shortSHA is the first 8 of a sha, "-" when empty.
func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return dash(s)
}
