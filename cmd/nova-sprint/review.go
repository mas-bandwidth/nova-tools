// review.go: `nova-sprint review post` (nova-tools #4072), the one way out of
// review. A card whose consumer copy fails (a bench crash, wall, refusal or
// SLOTS REFUSED; a friend's stub PR, a read under 8 twice, a child's
// non-zero exit, an abandoned lease) moves to ws:<stream>:review with the
// evidence on its record (internal/nsprint/fn/lua/02_card_move.lua, TM.finish
// and TM.evidence); this verb applies the typed verdict through the one move
// function (ns_cm_review):
//
//	review post --id <primary> --verdict recut|redeal|reassign:<consumer>|drop --why <text>
//	            [--to <consumer>] [--redis <addr>] [--actor <a>]
//
// recut returns the card to waiting, redeal to ready, reassign cuts its copy
// on the named consumer (--to, or reassign:<consumer>), drop moves it to
// landed with outcome=dropped and closes its origin issue with the REVIEW
// line. One receipt line; exit 0 applied, 1 refused (nothing written) or the
// issue close failed, 2 usage or Redis.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// reviewForge closes a dropped card's origin issue; tests swap it (CI-NET:
// no host in a test).
var reviewForge = func(st *store.Store) reconcile.IssueCloser { return ghIssueCloser{verb: "review", rdb: st.Client()} }

func init() {
	register(Verb{Name: "review", Summary: "review post: the typed verdict that moves a failed card out of review (#4072)",
		Run: runReview})
}

func runReview(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] != "post" {
		return refuse(errOut, "review", "wants post: review post --id <primary> --verdict recut|redeal|reassign:<consumer>|drop --why <text>")
	}
	fs := verbflag.New("review post")
	addr := fs.String("redis", "", "Redis address (else NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR)")
	actor := fs.String("actor", "", "who posts the verdict (else NOVA_FRIEND, else nova-sprint)")
	id := fs.String("id", "", "the primary in review")
	verdict := fs.String("verdict", "", "recut | redeal | reassign:<consumer> | drop")
	to := fs.String("to", "", "the consumer a reassign deals to (bench:<b> or friend:<f>)")
	why := fs.String("why", "", "the reason, on the REVIEW line")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, "review post", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "review post", "takes flags, not positional arguments")
	}
	v, err := reviewVerdict(*verdict, *to)
	if err != nil {
		return refuse(errOut, "review post", err.Error())
	}
	if *id == "" || strings.TrimSpace(*why) == "" {
		return refuse(errOut, "review post", "wants --id <primary> and --why <text>")
	}
	if *actor == "" {
		*actor = os.Getenv(seatEnv)
	}
	if seat := os.Getenv(seatEnv); seat != "" && *actor != seat {
		return refuse(errOut, "review post", fmt.Sprintf("--actor %s is not the seat (%s=%s)", *actor, seatEnv, seat))
	}
	if *actor == "" {
		*actor = "nova-sprint"
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	st, err := store.Open(ctx, taskAddr(*addr))
	if err != nil {
		return refuse(errOut, "review post", err.Error())
	}
	defer func() { _ = st.Close() }()
	c := st.Client()
	r, err := taskcard.Review(ctx, c, *id, v, *why, *actor)
	if err != nil {
		if w, ok := taskcard.IsRefused(err); ok {
			fmt.Fprintf(out, "REVIEW POST REFUSED id=%s why=%s ms=%d\n", *id, quoteField(w), time.Since(start).Milliseconds())
			return 1
		}
		return refuse(errOut, "review post", err.Error())
	}
	line := fmt.Sprintf("REVIEW POST id=%s verdict=%s to=%s copy=%s", r.ID, r.Verdict, r.To, dash(r.Copy))
	code := 0
	if r.Verdict == "drop" {
		rec, err := c.HMGet(ctx, taskcard.Key(r.ID), "origin", "ref", "repo", "review").Result()
		if err != nil {
			return refuse(errOut, "review post", err.Error())
		}
		get := func(i int) string { s, _ := rec[i].(string); return s }
		repo, n, ok := reviewIssue(get(0), get(1), get(2))
		var suffix string
		suffix, code = reviewClose(ctx, reviewForge(st), ok, repo, n, get(3))
		line += suffix
	}
	fmt.Fprintf(out, "%s ms=%d\n", line, time.Since(start).Milliseconds())
	return code
}

// reviewClose closes a dropped card's origin issue (when it has one) and
// answers the receipt's issue words and the exit code: a close the forge
// refused is `closed=no err=<why>`, exit 1 (the why used to be dropped).
func reviewClose(ctx context.Context, forge reconcile.IssueCloser, ok bool, repo string, n int, comment string) (string, int) {
	if !ok {
		return " issue=- closed=no", 0
	}
	if err := forge.CloseIssue(ctx, repo, n, comment); err != nil {
		return fmt.Sprintf(" issue=%s#%d closed=no err=%s", repo, n, quoteField(err.Error())), 1
	}
	return fmt.Sprintf(" issue=%s#%d closed=yes", repo, n), 0
}

// reviewVerdict is the typed verdict: recut, redeal, drop, or
// reassign:<consumer> (the consumer from --to or after the colon).
func reviewVerdict(verdict, to string) (string, error) {
	name, target, _ := strings.Cut(verdict, ":")
	if name == "reassign" {
		if target == "" {
			target = to
		}
		if _, err := taskcard.ParseConsumer(target); err != nil {
			return "", fmt.Errorf("--verdict reassign names its consumer: reassign:<consumer> or --to <consumer> (%v)", err)
		}
		return "reassign:" + target, nil
	}
	for _, v := range taskcard.Verdicts {
		if v == name && target == "" && v != "reassign" {
			if to != "" {
				return "", fmt.Errorf("--to belongs to --verdict reassign")
			}
			return v, nil
		}
	}
	return "", fmt.Errorf("--verdict %q is not recut|redeal|reassign:<consumer>|drop", verdict)
}

// reviewIssue is the issue a dropped card closes: its origin (an issue URL
// or owner/name#n, with or without an issue: prefix), else its ref (a bare
// name#n takes the owner of its repo, else the default owner).
func reviewIssue(origin, ref, repo string) (string, int, bool) {
	for _, s := range []string{origin, strings.TrimPrefix(origin, "issue:"), ref} {
		if r, n, ok := reconcile.OriginIssue(s); ok {
			return r, n, true
		}
	}
	name, num, ok := strings.Cut(strings.TrimPrefix(ref, "issue:"), "#")
	if !ok || name == "" || strings.Contains(name, "/") {
		return "", 0, false
	}
	owner := prkey.DefaultOwner
	if o, _, ok := strings.Cut(repo, "/"); ok && o != "" {
		owner = o
	}
	return reconcile.OriginIssue(owner + "/" + name + "#" + num)
}
