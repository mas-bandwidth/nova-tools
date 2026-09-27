package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

// pullTimeout bounds the store round trips of one pull.
const pullTimeout = 30 * time.Second

// cardsDir is where pull writes briefs when --dir is not given:
// ~/.nova-friend/<name>/cards.
func cardsDir(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("--dir is required (no home directory: %v)", err)
	}
	return filepath.Join(home, ".nova-friend", name, "cards"), nil
}

// runPull is `pull --as <you> [--n <k>] [--dir <d>] [--model <m>] [--harness
// <h>] [--child <id>]`: card work --as friend:<you> (--n k, else every free
// slot) in one call (ns_cm_work, which writes who works the copies onto
// their records), then each copy's brief (card.RenderCopy, the person's
// brief for a friend) written to <dir>/<copy label>.card, one PULLED line
// per copy naming the path, the leg and the token. A brief that cannot be
// written ends its copy as a fail now, never a lease left to lapse. The
// leases of pulled copies renew from the here loop once their owner is
// bound (here --pid).
func runPull(ctx context.Context, e env, args []string, out, errOut io.Writer) int {
	const verb = "pull"
	fs := newFlags(verb)
	redisAddr := fs.String("redis", "", redisHelp)
	as := fs.String("as", "", asHelp)
	n := fs.Int("n", 0, "how many copies to pull (default every free slot)")
	dir := fs.String("dir", "", "where the briefs are written (default ~/.nova-friend/<you>/cards)")
	model := fs.String("model", "", "the model that works the copies, one word, on their records")
	harness := fs.String("harness", "", "the harness that works the copies, one word, on their records")
	child := fs.String("child", "", "the child id that works the copies, one word, on their records")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	who := taskcard.Who{Model: *model, Harness: *harness, Child: *child}
	if err := who.Check(); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if *n < 0 {
		return refuse(errOut, verb, "--n wants a positive count; omit it to fill every free slot")
	}
	name, err := e.actor(*as)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	k := taskcard.Consumer{Kind: "friend", Name: name}
	if *dir == "" {
		if *dir, err = cardsDir(name); err != nil {
			return refuse(errOut, verb, err.Error())
		}
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		return refuse(errOut, verb, "--dir: "+err.Error())
	}
	start := time.Now()
	ms := func() int64 { return time.Since(start).Milliseconds() }
	ctx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()
	st, err := openStore(ctx, e.redis(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()
	c := st.Client()
	w, err := taskcard.WorkAs(ctx, c, k, name, *n, *n == 0, who)
	if err != nil {
		if why, ok := taskcard.IsRefused(err); ok {
			return refused(errOut, verb, why)
		}
		return refuse(errOut, verb, err.Error())
	}
	pipe := c.Pipeline()
	recs := make([]*redis.MapStringStringCmd, len(w.IDs))
	for i, id := range w.IDs {
		recs[i] = pipe.HGetAll(ctx, taskcard.Key(id))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return refuse(errOut, verb, "read copies: "+err.Error())
	}
	code := 0
	pulled := 0
	for i, id := range w.IDs {
		rec := recs[i].Val()
		body, err := card.RenderCopy(card.CopyCardFrom(id, rec))
		path := filepath.Join(*dir, card.CopyLabel(id)+".card")
		if err == nil {
			err = os.WriteFile(path, body, 0o644)
		}
		if err != nil {
			why := err.Error()
			fmt.Fprintf(out, "FRIEND PULLED REFUSED id=%s why=%s\n", id, quoteField(why))
			if _, endErr := taskcard.End(ctx, c, taskcard.EndRequest{IDs: []string{id}, Why: "brief: " + why, Token: w.Tokens[i], By: name}); endErr != nil {
				fmt.Fprintf(out, "FRIEND PULLED FAIL-REFUSED id=%s why=%s\n", id, quoteField(endErr.Error()))
			}
			code = 1
			continue
		}
		pulled++
		fmt.Fprintf(out, "FRIEND PULLED id=%s leg=%s token=%s card=%s\n", id, dash(rec["leg"]), w.Tokens[i], path)
	}
	fmt.Fprintf(out, "FRIEND PULL as=%s n=%d free=%d dir=%s ms=%d\n", name, pulled, w.Free, *dir, ms())
	return code
}
