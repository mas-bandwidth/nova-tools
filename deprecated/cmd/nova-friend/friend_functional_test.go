//go:build functional

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// seededStore is a throwaway redis-server with the nova_sprint library
// loaded and the roster nova-config apply writes: rowan (4 slots, frontier,
// coordinator) and stella (2 slots, reader), stamped at friend revision 1.
// Nothing here reaches a real host.
func seededStore(t *testing.T) (string, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	c.SAdd(ctx, "friends", "rowan", "stella")
	c.HSet(ctx, "friend:rowan:desired", "slots", "4", "machine", "studio", "tiers", "frontier")
	c.HSet(ctx, "friend:rowan:roles", "roles", "coordinator")
	c.HSet(ctx, "friend:stella:desired", "slots", "2", "machine", "studio")
	c.HSet(ctx, "friend:stella:roles", "roles", "reader")
	c.HSet(ctx, configDeclKey, configRevField, "1")
	return addr, c
}

// tool is the binary's own entry point with its own environment.
type tool struct{ env map[string]string }

func (x tool) run(args ...string) (int, string, string) { return runFriend(x.env, args...) }

// TestHereListShowByeAwayBack is the friend's day from the store's side:
// here --once writes the beat with no TTL and reads up, a second live
// session is BUSY, a stale one is taken over, a login is written once,
// away and back flip friend:<f>:down, bye reads down at once, and every
// refusal is one line with its remedy.
func TestHereListShowByeAwayBack(t *testing.T) {
	t.Parallel()

	addr, c := seededStore(t)
	ctx := context.Background()
	me := tool{env: map[string]string{"NOVA_FRIEND": "rowan", "NOVA_SPRINT_REDIS": addr}}

	code, out, errOut := me.run("here", "--once", "--host", "studio", "--session", "s1", "--harness", "claude", "--login", "rowan-claude", "--login", "rowan-claude")
	if code != 0 || out != "FRIEND HERE as=rowan host=studio session=s1 slots=4 taken=0\n" {
		t.Fatalf("here --once: exit %d %q %q", code, out, errOut)
	}
	beat := c.HGetAll(ctx, "friend:rowan:beat").Val()
	if beat["host"] != "studio" || beat["harness"] != "claude" || beat["session"] != "s1" || beat["at"] == "" || beat["ncpu"] == "" {
		t.Fatalf("friend:rowan:beat = %v", beat)
	}
	if ttl := c.TTL(ctx, "friend:rowan:beat").Val(); ttl > 0 {
		t.Fatalf("the beat expires in %s; keys do not expire", ttl)
	}
	if got := c.HGet(ctx, "friends:login", "rowan-claude").Val(); got != "rowan" {
		t.Fatalf("friends:login rowan-claude = %q", got)
	}
	if n := len(c.XRange(ctx, "cap:log", "-", "+").Val()); n != 2 {
		t.Fatalf("cap:log has %d receipts, want friend-login and friend-up", n)
	}

	code, out, _ = me.run("list")
	if code != 0 || out != "FRIEND name=rowan state=up slots=4 tiers=frontier roles=coordinator host=studio working=0 rev=1\n"+
		"FRIEND name=stella state=down slots=2 tiers=- roles=reader host=- working=0 rev=1\nFRIEND LIST friends=2 rev=1\n" {
		t.Fatalf("list: exit %d %q", code, out)
	}
	code, out, _ = me.run("show", "rowan")
	if code != 0 || !regexp.MustCompile(`^FRIEND name=rowan state=up slots=4 tiers=frontier roles=coordinator host=studio working=0 session=s1 harness=claude load=\S+ models=- beat=\d+s away=- rev=1\n$`).MatchString(out) {
		t.Fatalf("show: exit %d %q", code, out)
	}
	if code, _, errOut := me.run("show", "emma"); code != 1 || errOut != "nova-friend show: UNREGISTERED emma: not in the roster; run: nova-config friend add emma --slots <n> --as <you>, then nova-config apply; run: nova-friend help\n" {
		t.Fatalf("show emma: exit %d %q", code, errOut)
	}

	// a second live session of the same name is BUSY; a stale one is taken over
	code, out, errOut = me.run("here", "--once", "--host", "laptop", "--session", "s2")
	if code != 1 || out != "" || !strings.HasPrefix(errOut, "nova-friend here: BUSY rowan: a live session is here already on studio (session s1, beat ") {
		t.Fatalf("a second session: exit %d %q %q", code, out, errOut)
	}
	stale := time.Now().Add(-staleAfter - time.Second).UnixMilli()
	c.HSet(ctx, "friend:rowan:beat", "at", strconv.FormatInt(stale, 10))
	code, out, errOut = me.run("here", "--once", "--host", "laptop", "--session", "s2")
	if code != 0 || out != "FRIEND HERE as=rowan host=laptop session=s2 slots=4 taken=0\n" || !strings.Contains(errOut, "took over a stale session") {
		t.Fatalf("a stale session: exit %d %q %q", code, out, errOut)
	}
	if got := c.HGet(ctx, "friend:rowan:beat", "session").Val(); got != "s2" {
		t.Fatalf("beat session %q after the takeover", got)
	}
	// the here loop of s1 is fenced now: its beat is refused and it says no bye
	st, err := store.OpenSingle(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	s1 := liveHereSteps(st, presence{Name: "rowan", Host: "studio", Session: "s1"}, taskcard.ProcessOwner{})
	if err := s1.beat(ctx); err == nil || !strings.Contains(err.Error(), "FENCED: session s2 on laptop holds the beat") {
		t.Fatalf("s1's beat after the takeover: %v", err)
	}
	if err := s1.bye(ctx); err == nil || !strings.Contains(err.Error(), "FENCED") {
		t.Fatalf("s1's bye after the takeover: %v", err)
	}
	if c.Exists(ctx, "friend:rowan:beat").Val() != 1 {
		t.Fatal("a fenced bye deleted the beat")
	}

	// logins: a friend's name, a taken alias, a bad alias
	for _, r := range []struct{ alias, want string }{
		{"stella", "LOGIN-IS-FRIEND rowan: --login stella is a friend's name, not a login alias"},
		{"bad alias", "--login bad alias wants letters, digits and dashes"},
	} {
		if code, _, errOut := me.run("here", "--once", "--session", "s2", "--login", r.alias); code != 1 || errOut != "nova-friend here: "+r.want+"; run: nova-friend help\n" {
			t.Fatalf("--login %s: exit %d %q", r.alias, code, errOut)
		}
	}
	c.HSet(ctx, "friends:login", "stella-ai", "stella")
	if code, _, errOut := me.run("here", "--once", "--session", "s2", "--login", "stella-ai"); code != 1 || errOut != "nova-friend here: LOGIN-TAKEN rowan: --login stella-ai is stella's login already; run: nova-friend help\n" {
		t.Fatalf("a taken alias: exit %d %q", code, errOut)
	}
	emma := tool{env: map[string]string{"NOVA_FRIEND": "emma", "NOVA_SPRINT_REDIS": addr}}
	if code, _, errOut := emma.run("here", "--once"); code != 1 || errOut != "nova-friend here: UNREGISTERED emma: not in the roster; run: nova-config friend add emma --slots <n> --as <you>, then nova-config apply; run: nova-friend help\n" {
		t.Fatalf("an unregistered here: exit %d %q", code, errOut)
	}
	alias := tool{env: map[string]string{"NOVA_FRIEND": "stella-ai", "NOVA_SPRINT_REDIS": addr}}
	if code, _, errOut := alias.run("here", "--once"); code != 1 || !strings.HasPrefix(errOut, "nova-friend here: NAME-IS-LOGIN stella-ai: ") {
		t.Fatalf("a login as a friend: exit %d %q", code, errOut)
	}

	// away and back: the one writer of friend:<f>:down
	code, out, _ = me.run("away", "stella", "--reason", "on holiday")
	if code != 0 || out != "FRIEND AWAY name=stella reason=\"on holiday\" changed=yes\n" {
		t.Fatalf("away: exit %d %q", code, out)
	}
	if code, out, _ := me.run("away", "--reason", "on holiday", "stella"); code != 0 || out != "FRIEND AWAY name=stella reason=\"on holiday\" changed=no\n" {
		t.Fatalf("away again: exit %d %q", code, out)
	}
	if down := c.HGetAll(ctx, "friend:stella:down").Val(); down["reason"] != "on holiday" || down["actor"] != "rowan" {
		t.Fatalf("friend:stella:down = %v", down)
	}
	code, out, _ = me.run("list")
	if code != 0 || !strings.Contains(out, "FRIEND name=stella state=away slots=2 tiers=- roles=reader host=- working=0 rev=1\n") {
		t.Fatalf("list with stella away: %q", out)
	}
	if code, out, _ := me.run("show", "stella"); code != 0 || !strings.Contains(out, ` beat=- away="on holiday" rev=1`) {
		t.Fatalf("show stella away: exit %d %q", code, out)
	}
	if code, out, _ := me.run("back", "stella"); code != 0 || out != "FRIEND BACK name=stella changed=yes\n" {
		t.Fatalf("back: exit %d %q", code, out)
	}
	if code, out, _ := me.run("back", "stella"); code != 0 || out != "FRIEND BACK name=stella changed=no\n" {
		t.Fatalf("back again: exit %d %q", code, out)
	}
	if c.Exists(ctx, "friend:stella:down").Val() != 0 {
		t.Fatal("back left friend:stella:down")
	}
	if code, _, errOut := me.run("away", "emma", "--reason", "x"); code != 1 || !strings.HasPrefix(errOut, "nova-friend away: UNREGISTERED emma: ") {
		t.Fatalf("away emma: exit %d %q", code, errOut)
	}

	// bye reads down at once; a second bye says it was down
	if code, out, _ := me.run("bye"); code != 0 || out != "FRIEND BYE as=rowan was=up\n" {
		t.Fatalf("bye: exit %d %q", code, out)
	}
	if c.Exists(ctx, "friend:rowan:beat").Val() != 0 {
		t.Fatal("bye left the beat")
	}
	if code, out, _ := me.run("list"); code != 0 || !strings.HasPrefix(out, "FRIEND name=rowan state=down slots=4 tiers=frontier roles=coordinator host=- working=0 rev=1\n") {
		t.Fatalf("list after bye: %q", out)
	}
	if code, out, _ := me.run("bye"); code != 0 || out != "FRIEND BYE as=rowan was=down\n" {
		t.Fatalf("bye again: exit %d %q", code, out)
	}
	// a here after bye writes the beat again: registration survived
	if code, out, _ := me.run("here", "--once", "--host", "studio", "--session", "s3"); code != 0 || out != "FRIEND HERE as=rowan host=studio session=s3 slots=4 taken=0\n" {
		t.Fatalf("here after bye: exit %d %q", code, out)
	}
}

// TestHereLoopOnTheStore drives the live steps two ticks on a store, then
// ends them as a signal would: the beat is written each tick with no TTL,
// and the bye at the end is fenced on the session and deletes the beat.
func TestHereLoopOnTheStore(t *testing.T) {
	t.Parallel()

	addr, c := seededStore(t)
	ctx := context.Background()
	st, err := store.OpenSingle(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	p := presence{Name: "rowan", Host: "studio", Harness: "claude", Session: "loop1"}
	if _, err := hereRegister(ctx, st, p); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	steps := liveHereSteps(st, p, taskcard.ProcessOwner{})
	c.HSet(ctx, "friend:rowan:beat", "at", "1")
	fails := 0
	for i := 0; i < 2; i++ {
		if code, end := hereCycle(ctx, p, steps, &fails, &out, &errOut); code != 0 || end != keepGoing {
			t.Fatalf("tick %d: code %d end %d; stderr %s", i, code, end, errOut.String())
		}
	}
	if at := c.HGet(ctx, "friend:rowan:beat", "at").Val(); at == "1" || at == "" {
		t.Fatalf("the ticks did not write the beat: at=%q", at)
	}
	if ttl := c.TTL(ctx, "friend:rowan:beat").Val(); ttl > 0 {
		t.Fatalf("the beat expires in %s; keys do not expire", ttl)
	}
	if code := hereDown(ctx, p, steps, 0, &out, &errOut); code != 0 {
		t.Fatalf("down exit %d; stderr %s", code, errOut.String())
	}
	if out.String() != "FRIEND HERE DOWN as=rowan session=loop1\n" || errOut.String() != "" {
		t.Fatalf("stdout %q stderr %q", out.String(), errOut.String())
	}
	if c.Exists(ctx, "friend:rowan:beat").Val() != 0 {
		t.Fatal("the loop's bye left the beat")
	}
}

// TestPullDoneEndToEnd is the copy model from the friend's side: pull takes
// the dealt copy into working and writes the brief, here --pid binds the
// harness and renews the lease, done --ok --pr runs the spec gate and ends
// the copy so the primary moves to review; a copy that is not the friend's
// is NOTMINE, a stale token is FENCED (exit 3).
func TestPullDoneEndToEnd(t *testing.T) {
	t.Parallel()

	addr, c := seededStore(t)
	ctx := context.Background()
	me := tool{env: map[string]string{"NOVA_FRIEND": "rowan", "NOVA_SPRINT_REDIS": addr}}
	rowan := taskcard.Consumer{Kind: "friend", Name: "rowan"}
	stella := taskcard.Consumer{Kind: "friend", Name: "stella"}
	// the friend's checkout: base-sha, then the friend's commit, the head
	// the PR carries; done --ok --pr runs the spec gate in it. The copy's
	// TEST is none with a why, so the gate is CI's answer for a diff of
	// one text file.
	checkout := filepath.Join(t.TempDir(), "q1")
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", checkout, "-c", "user.email=f@example.com", "-c", "user.name=f"}, args...)...)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "base")
	base := git("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(checkout, "work.txt"), []byte("the friend's work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "work.txt")
	git("commit", "-q", "-m", "the work")
	head := git("rev-parse", "HEAD")
	for _, id := range []string{"q1", "q2"} {
		if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: id, Where: "waiting", Stream: "swarm: cards", Kind: "build",
			Title: "quack " + id, Repo: "mas-bandwidth/nova-tools", Origin: "issue:nova-tools#4233", By: "rowan",
			Fields: []string{"base", "dev", "base_sha", base, "paths", "cmd/nova-friend/pull.go", "test", "none the fixture's diff is one text file",
				"done_when", "go test ./cmd/nova-friend -run TestPullDoneEndToEnd passes", "body", "the issue"}}); err != nil {
			t.Fatal(err)
		}
	}
	d, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: rowan, N: 1, By: "reconciler"})
	if err != nil || len(d) != 1 {
		t.Fatalf("deal to rowan: %v %v", d, err)
	}
	other, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: stella, N: 1, By: "reconciler"})
	if err != nil || len(other) != 1 {
		t.Fatalf("deal to stella: %v %v", other, err)
	}
	mine, theirs := d[0].Copy, other[0].Copy
	dir := t.TempDir()

	code, out, errOut := me.run("pull", "--dir", dir, "--model", "opus-5.5", "--harness", "claude-code", "--child", "c7")
	if code != 0 {
		t.Fatalf("pull exit %d: %s%s", code, out, errOut)
	}
	pulled := regexp.MustCompile(`(?m)^FRIEND PULLED id=(\S+) leg=work token=(\S+) card=(\S+)$`).FindStringSubmatch(out)
	if pulled == nil || pulled[1] != mine || !regexp.MustCompile(`FRIEND PULL as=rowan n=1 free=3 dir=\S+ ms=\d+\n$`).MatchString(out) {
		t.Fatalf("pull receipt:\n%s", out)
	}
	token, path := pulled[2], pulled[3]
	if filepath.Dir(path) != dir {
		t.Fatalf("card written outside --dir: %s", path)
	}
	brief, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\nCOPY: " + mine + "\n", "\nWORKER: model=opus-5.5 harness=claude-code child=c7\n", "\n> the issue\n"} {
		if !strings.Contains(string(brief), want) {
			t.Fatalf("brief lacks %q:\n%s", want, brief)
		}
	}
	rec := c.HGetAll(ctx, taskcard.Key(mine)).Val()
	if rec["where"] != "working" || rec["token"] != token || rec["model"] != "opus-5.5" {
		t.Fatalf("after pull task:%s = %v", mine, rec)
	}
	if code, out, _ := me.run("pull", "--dir", dir); code != 0 || !strings.Contains(out, "FRIEND PULL as=rowan n=0 free=3") {
		t.Fatalf("a second pull with nothing ready: exit %d %s", code, out)
	}
	if code, out, _ := me.run("list"); code != 0 || !strings.Contains(out, "FRIEND name=rowan state=down slots=4 tiers=frontier roles=coordinator host=- working=1 rev=1\n") {
		t.Fatalf("list counts the working copy: %q", out)
	}

	// here --pid binds the harness (the test's parent, a live process) to
	// the copy and the lease renews from the observed owner
	before, _ := strconv.ParseInt(rec["lease_until"], 10, 64)
	c.HSet(ctx, taskcard.Key(mine), "lease_until", strconv.FormatInt(before-1000, 10))
	harness := os.Getppid()
	code, out, errOut = me.run("here", "--once", "--host", "studio", "--session", "s1", "--pid", strconv.Itoa(harness))
	if code != 0 || !strings.Contains(out, "FRIEND HERE OWNER id="+mine+" state=bound as=rowan pid="+strconv.Itoa(harness)+"\n") {
		t.Fatalf("here --pid: exit %d %q %q", code, out, errOut)
	}
	after, _ := strconv.ParseInt(c.HGet(ctx, taskcard.Key(mine), "lease_until").Val(), 10, 64)
	if after < before {
		t.Fatalf("lease not renewed: %d -> %d", before, after)
	}
	if got := c.HGet(ctx, "friend:rowan:beat", "models").Val(); got != "opus-5.5" {
		t.Fatalf("beat models = %q", got)
	}
	if code, out, _ := me.run("show", "rowan"); code != 0 || !strings.Contains(out, " working=1 session=s1 harness=nova-friend ") || !strings.Contains(out, " models=opus-5.5 ") {
		t.Fatalf("show with a copy: %q", out)
	}
	// the same here again binds nothing new and says nothing about it
	if code, out, _ := me.run("here", "--once", "--host", "studio", "--session", "s1", "--pid", strconv.Itoa(harness)); code != 0 || strings.Contains(out, "OWNER") {
		t.Fatalf("here --pid again: exit %d %q", code, out)
	}

	if code, _, errOut := me.run("done", "--id", theirs, "--ok"); code != 1 || !strings.HasPrefix(errOut, "nova-friend done: NOTMINE task:"+theirs+" is friend:stella's copy, not friend:rowan's; ") {
		t.Fatalf("ending stella's copy: exit %d %q", code, errOut)
	}
	if code, _, errOut := me.run("done", "--id", "nope~9", "--ok"); code != 1 || !strings.HasPrefix(errOut, "nova-friend done: NOCOPY task:nope~9 ") {
		t.Fatalf("ending no copy: exit %d %q", code, errOut)
	}
	if code, _, errOut := me.run("done", "--id", mine, "--ok", "--token", "stale@1"); code != 3 || !strings.Contains(errOut, "nova-friend done: FENCED ") {
		t.Fatalf("a stale token: exit %d %q", code, errOut)
	}
	// the spec gate first: no checkout, a checkout at another head, and a
	// TEST the diff carries no test file for are refused before anything
	// is recorded
	if code, _, errOut := me.run("done", "--id", mine, "--ok", "--pr", "nova-tools#4400", "--head", head, "--token", token); code != 1 ||
		!strings.HasPrefix(errOut, "nova-friend done: no-test --ok --pr wants --repo") {
		t.Fatalf("no --repo: exit %d %q", code, errOut)
	}
	if code, _, errOut := me.run("done", "--id", mine, "--ok", "--pr", "nova-tools#4400", "--head", base, "--repo", checkout, "--token", token); code != 1 ||
		!strings.Contains(errOut, "gate could not run: --repo "+checkout+" is at ") {
		t.Fatalf("a checkout at another head: exit %d %q", code, errOut)
	}
	c.HSet(ctx, taskcard.Key(mine), "test", "./x TestX")
	if code, _, errOut := me.run("done", "--id", mine, "--ok", "--pr", "nova-tools#4400", "--head", head, "--repo", checkout, "--token", token); code != 1 ||
		!strings.Contains(errOut, "no-test the diff adds or changes no test file") {
		t.Fatalf("a red gate: exit %d %q", code, errOut)
	}
	if n := c.Exists(ctx, "pr:nova-tools:4400").Val(); n != 0 || c.HGet(ctx, taskcard.Key(mine), "where").Val() != "working" {
		t.Fatal("a refused gate recorded the PR or ended the copy")
	}
	c.HSet(ctx, taskcard.Key(mine), "test", "none the fixture's diff is one text file")
	code, out, errOut = me.run("done", "--id", mine, "--ok", "--pr", "nova-tools#4400", "--head", head, "--repo", checkout, "--token", token)
	if code != 0 || !strings.HasPrefix(out, "GATE TEST: none (the fixture's diff is one text file); no class test required\n") ||
		!strings.Contains(out, "\nFRIEND RECORDED pr=nova-tools#4400 head="+head+" branch=nova/copies/q1-c1-a1\nFRIEND ENDED id="+mine+" primary=q1 from=working to=review next=-\n") ||
		!regexp.MustCompile(`FRIEND DONE as=rowan n=1 ms=\d+\n$`).MatchString(out) {
		t.Fatalf("done exit %d %q %s", code, out, errOut)
	}
	pr := c.HGetAll(ctx, "pr:nova-tools:4400").Val()
	if pr["head"] != head || pr["base"] != "dev" || pr["task"] != "q1" || pr["state"] != "open" {
		t.Fatalf("pr:nova-tools:4400 = %v", pr)
	}
	if n := c.ZCard(ctx, rowan.KeyAt(0, "ok")).Val(); n != 1 {
		t.Fatalf("%s ok = %d", rowan.KeyAt(0, "ok"), n)
	}
	if got := c.HGet(ctx, taskcard.Key("q1"), "where").Val(); got != "review" {
		t.Fatalf("q1 is %s, want review", got)
	}
	if code, out, _ := me.run("done", "--id", mine, "--ok", "--pr", "nova-tools#4400", "--head", head, "--repo", checkout); code != 0 || !strings.Contains(out, "FRIEND ALREADY id="+mine+" ") {
		t.Fatalf("the same end again: exit %d %s", code, out)
	}

	// stella's copy: a fail, from stella's seat, with the reason on the record
	her := tool{env: map[string]string{"NOVA_FRIEND": "stella", "NOVA_SPRINT_REDIS": addr}}
	if code, out, errOut := her.run("pull", "--dir", t.TempDir()); code != 0 || !strings.Contains(out, "FRIEND PULLED id="+theirs+" ") {
		t.Fatalf("stella's pull: exit %d %s%s", code, out, errOut)
	}
	if code, out, _ := her.run("done", "--id", theirs, "--fail", "could not build"); code != 0 || !strings.Contains(out, "FRIEND ENDED id="+theirs+" primary=q2 from=working to=review ") {
		t.Fatalf("stella's fail: exit %d %s", code, out)
	}
	if got := c.HGet(ctx, taskcard.Key("q2"), "review_why").Val(); got != "could not build" {
		t.Fatalf("q2 review_why = %q", got)
	}
	// nothing here bound a here loop or a beat loop key
	if c.Exists(ctx, life.BeatLoopKey("rowan")).Val() != 0 {
		t.Fatal("pull started a nova-sprint beat loop; here is the one presence")
	}
}
