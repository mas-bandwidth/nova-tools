// The hold verb (nova-tools #3092 rev 7): typed DISPOSITION/REPAIR lines
// become records through one strict parser, and the hold router replaces the
// bash bin/hold-to-fix. Records are keyed by the #3139 unit contract
// (s:<S>:u:<unit> via s:<S>:prunit:<repo>:<n>). No GitHub call anywhere.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/disposition"
)

func init() {
	register(Verb{
		Name:    "hold",
		Summary: "ingest a typed DISPOSITION/REPAIR line, show or release holds, run the hold router",
		Run:     runHold,
	})
}

func runHold(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "hold", "want ingest, show, release or route")
	}
	switch args[0] {
	case "ingest":
		return runHoldIngest(ctx, args[1:], out, errOut)
	case "show":
		return runHoldShow(ctx, args[1:], out, errOut)
	case "release":
		return runHoldRelease(ctx, args[1:], out, errOut)
	case "route":
		return runHoldRoute(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "hold", fmt.Sprintf("unknown subverb %s; want ingest, show, release or route", args[0]))
	}
}

func holdClient(addr string) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: lifeAddr(addr)})
}

// holdSplitArgs separates leading positional arguments from flags, so
// `hold release --as x repo#n holder` and `hold release repo#n holder --as x`
// both parse.
func holdSplitArgs(args []string, flagsWithValue map[string]bool) (pos, flags []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if !strings.Contains(name, "=") && flagsWithValue[name] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return pos, flags
}

func parseRepoPR(s string) (string, int, error) {
	i := strings.LastIndexByte(s, '#')
	if i <= 0 {
		return "", 0, fmt.Errorf("want <repo>#<n>, got %q", s)
	}
	n, err := strconv.Atoi(s[i+1:])
	if err != nil || n <= 0 {
		return "", 0, fmt.Errorf("want <repo>#<n>, got %q", s)
	}
	return s[:i], n, nil
}

func runHoldIngest(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs, addr := lifeFlags("hold ingest")
	as := fs.String("as", "", "the friend running the ingest (required)")
	sprint := fs.String("sprint", "", "sprint id (required)")
	repo := fs.String("repo", "", "owner/repo")
	pr := fs.Int("pr", 0, "pull request number")
	url := fs.String("url", "", "comment url")
	bodyFile := fs.String("body-file", "", "the comment body as posted")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "hold ingest", err.Error())
	}
	if fs.NArg() != 0 || *as == "" || *sprint == "" || *repo == "" || *pr <= 0 || *url == "" || *bodyFile == "" {
		return refuse(errOut, "hold ingest", "needs --as, --sprint, --repo, --pr, --url and --body-file")
	}
	body, err := os.ReadFile(*bodyFile)
	if err != nil {
		return refuse(errOut, "hold ingest", err.Error())
	}
	c := holdClient(*addr)
	defer func() { _ = c.Close() }()
	res, err := disposition.Ingest(ctx, c, disposition.IngestRequest{
		Sprint: *sprint, Repo: *repo, PR: *pr, URL: *url, Body: string(body), Actor: *as,
	})
	if err != nil {
		return refuse(errOut, "hold ingest", err.Error())
	}
	fmt.Fprintln(out, res.String())
	return res.Exit()
}

func runHoldRelease(ctx context.Context, args []string, out, errOut io.Writer) int {
	pos, flags := holdSplitArgs(args, map[string]bool{"as": true, "sprint": true, "head": true, "evidence": true, "redis": true})
	fs, addr := lifeFlags("hold release")
	as := fs.String("as", "", "the releasing friend (required)")
	sprint := fs.String("sprint", "", "sprint id (required)")
	head := fs.String("head", "", "the unit's current head (40 hex)")
	evidence := fs.String("evidence", "", "evidence url (required)")
	if err := fs.Parse(flags); err != nil {
		return refuse(errOut, "hold release", err.Error())
	}
	if len(pos) != 2 || *as == "" || *sprint == "" || *head == "" || *evidence == "" {
		return refuse(errOut, "hold release", "usage: hold release --as <f> --sprint <S> <repo>#<n> <holder> --head <sha> --evidence <url>")
	}
	repo, n, err := parseRepoPR(pos[0])
	if err != nil {
		return refuse(errOut, "hold release", err.Error())
	}
	holder := strings.TrimPrefix(pos[1], "h:")
	c := holdClient(*addr)
	defer func() { _ = c.Close() }()
	reply, err := disposition.Release(ctx, c, *sprint, repo, n, holder, *as, *head, *evidence)
	if err != nil {
		return refuse(errOut, "hold release", err.Error())
	}
	fmt.Fprintln(out, strings.Join(reply, " "))
	if len(reply) > 0 && reply[0] == "RELEASED" {
		return 0
	}
	return 2
}

func runHoldRoute(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs, addr := lifeFlags("hold route")
	sprint := fs.String("sprint", "", "sprint id (required)")
	once := fs.Bool("once", false, "run one tick and return")
	reclaim := fs.Int64("reclaim-idle", disposition.DefaultReclaimIdle, "XAUTOCLAIM min-idle in ms")
	consumer := fs.String("consumer", "hold-route", "consumer name in group hold-route")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "hold route", err.Error())
	}
	if fs.NArg() != 0 || *sprint == "" {
		return refuse(errOut, "hold route", "needs --sprint")
	}
	if *reclaim < 1 {
		return refuse(errOut, "hold route", "--reclaim-idle must be at least 1 ms")
	}
	c := holdClient(*addr)
	defer func() { _ = c.Close() }()
	token, err := leaseToken()
	if err != nil {
		return refuse(errOut, "hold route", err.Error())
	}
	cfg := disposition.RouteConfig{Sprint: *sprint, Consumer: *consumer, ReclaimIdle: *reclaim, Actor: "hold-route"}
	tick := func() int {
		ok, err := holdLease(ctx, c, token)
		if err != nil {
			return refuse(errOut, "hold route", err.Error())
		}
		if !ok {
			fmt.Fprintln(out, "HOLDROUTE lease held elsewhere")
			return 0
		}
		lines, err := disposition.RouteOnce(ctx, c, cfg)
		for _, l := range lines {
			fmt.Fprintln(out, l)
		}
		if err != nil {
			return refuse(errOut, "hold route", err.Error())
		}
		return 0
	}
	if *once {
		code := tick()
		dropLease(ctx, c, token)
		return code
	}
	sig, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if code := tick(); code != 0 {
			return code
		}
		select {
		case <-sig.Done():
			dropLease(ctx, c, token)
			return 0
		case <-ticker.C:
		}
	}
}

func leaseToken() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// holdRenewScript and holdReleaseScript are the token-checked lease moves
// (the internal/redisq renewScript/releaseScript shape): the stored token must
// be the caller's or the script is a no-op, so an expired owner can never
// extend or delete a successor's lease.
var holdRenewScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0
`)

var holdReleaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

const holdLeaseTTL = 5 * time.Second

// holdLease takes or renews lease:hold-route (TTL 5 s) for this token. The
// renew is one atomic compare-token script, never GET then PEXPIRE.
func holdLease(ctx context.Context, c *redis.Client, token string) (bool, error) {
	ok, err := c.SetNX(ctx, disposition.LeaseKey, token, holdLeaseTTL).Result()
	if err != nil {
		return false, err
	}
	if ok {
		return true, nil
	}
	n, err := holdRenewScript.Run(ctx, c, []string{disposition.LeaseKey}, token, holdLeaseTTL.Milliseconds()).Int64()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// dropLease deletes lease:hold-route only while this token still holds it.
func dropLease(ctx context.Context, c *redis.Client, token string) bool {
	n, err := holdReleaseScript.Run(ctx, c, []string{disposition.LeaseKey}, token).Int64()
	return err == nil && n == 1
}

func runHoldShow(ctx context.Context, args []string, out, errOut io.Writer) int {
	pos, flags := holdSplitArgs(args, map[string]bool{"sprint": true, "redis": true})
	fs, addr := lifeFlags("hold show")
	sprint := fs.String("sprint", "", "sprint id (required)")
	if err := fs.Parse(flags); err != nil {
		return refuse(errOut, "hold show", err.Error())
	}
	if len(pos) != 1 || *sprint == "" {
		return refuse(errOut, "hold show", "usage: hold show --sprint <S> <repo>#<n>")
	}
	repo, n, err := parseRepoPR(pos[0])
	if err != nil {
		return refuse(errOut, "hold show", err.Error())
	}
	c := holdClient(*addr)
	defer func() { _ = c.Close() }()
	short := disposition.ShortRepo(repo)
	unit, err := c.Get(ctx, fmt.Sprintf("s:%s:prunit:%s:%d", *sprint, short, n)).Result()
	if err != nil {
		return refuse(errOut, "hold show", fmt.Sprintf("no unit for %s#%d", short, n))
	}
	friends, err := c.SMembers(ctx, "friends").Result()
	if err != nil {
		return refuse(errOut, "hold show", err.Error())
	}
	sort.Strings(friends)
	pipe := c.Pipeline()
	u := pipe.HMGet(ctx, "s:"+*sprint+":u:"+unit, "head", "author", "holds_open")
	holds := map[string]*redis.MapStringStringCmd{}
	for _, f := range friends {
		holds[f] = pipe.HGetAll(ctx, "s:"+*sprint+":hold:"+unit+":"+f)
	}
	notes := pipe.HGetAll(ctx, disposition.NoteKey(*sprint, unit))
	owners := pipe.HGetAll(ctx, disposition.OwnerKey(*sprint, unit))
	parks := pipe.HGetAll(ctx, disposition.ParkKey(*sprint))
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return refuse(errOut, "hold show", err.Error())
	}
	uv := u.Val()
	fmt.Fprintf(out, "HOLDS %s#%d unit=%s head=%v author=%v holds_open=%v\n", short, n, unit, uv[0], uv[1], uv[2])
	now := time.Now().UnixMilli()
	for _, f := range friends {
		h := holds[f].Val()
		if len(h) == 0 {
			continue
		}
		state := "open"
		path := "holder's typed line at a later head, or hold release by release_reader when " + f + " is down"
		if h["released_by"] != "" {
			state, path = "released by "+h["released_by"], "-"
		}
		fmt.Fprintf(out, "hold %s head=%s kind=%s %s owner=%q release=%s\n", f, h["head"], h["kind"], state, owners.Val()["h:"+f+":"+h["head"]], path)
	}
	for _, k := range sortedKeys(notes.Val()) {
		fmt.Fprintf(out, "note %s %s owner=%q\n", k, notes.Val()[k], owners.Val()[k])
	}
	prefix := short + ":" + strconv.Itoa(n) + ":"
	for _, k := range sortedKeys(parks.Val()) {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		var p disposition.Park
		_ = json.Unmarshal([]byte(parks.Val()[k]), &p)
		fmt.Fprintf(out, "parked %s id=%s role=%s reason=%s age=%ds\n", strings.TrimPrefix(k, prefix), p.ID, p.Role, p.Reason, (now-p.ParkedAt)/1000)
	}
	return 0
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
