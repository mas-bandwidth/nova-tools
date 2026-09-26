//go:build functional

package card_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

const (
	sprint  = "control-2731"
	baseSHA = "0123456789abcdef0123456789abcdef01234567"
)

func TestCardPushRefusesWithoutDoneWhen(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	fields := validCard(srv.URL + "/acme/public.git")
	for _, key := range []string{"BASE", "BASE-SHA", "PATHS", "DEPENDS-ON", "DONE-WHEN"} {
		t.Run(key, func(t *testing.T) {
			f := fields
			f.label = "miss-" + key
			f.omit = map[string]bool{key: true}
			res := card.Push(ctx, client, sprint, f.render())
			if res.Code != 2 || res.Stdout != "" {
				t.Fatalf("exit %d stdout %q stderr %q, want exit 2 and no write", res.Code, res.Stdout, res.Stderr)
			}
			if !strings.Contains(res.Stderr, "missing "+key) {
				t.Fatalf("stderr %q, want missing %s", res.Stderr, key)
			}
			for _, other := range []string{"BASE", "BASE-SHA", "PATHS", "DEPENDS-ON", "DONE-WHEN"} {
				// "missing BASE-SHA" is not "missing BASE": the key ends where a key character does not follow
				if other != key && regexp.MustCompile(`missing `+regexp.QuoteMeta(other)+`([^A-Z-]|$)`).MatchString(res.Stderr) {
					t.Fatalf("stderr %q also says missing %s", res.Stderr, other)
				}
			}
			assertAbsent(t, ctx, client, f.label)
		})
	}

	t.Run("empty", func(t *testing.T) {
		f := fields
		f.label = "empty-done-when"
		f.emptyDone = true
		res := card.Push(ctx, client, sprint, f.render())
		if res.Code != 2 || !strings.Contains(res.Stderr, "missing DONE-WHEN") {
			t.Fatalf("empty DONE-WHEN: exit %d stderr %q, want exit 2 naming DONE-WHEN", res.Code, res.Stderr)
		}
		assertAbsent(t, ctx, client, f.label)
	})

	fields.label = "has-done-when"
	res := card.Push(ctx, client, sprint, fields.render())
	if res.Code != 0 || res.Stderr != "" || !strings.Contains(res.Stdout, "place=pool") {
		t.Fatalf("a card with DONE-WHEN was refused: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	assertPlace(t, ctx, client, fields.label, true, false)
}

func TestCardPushRefusesPrivateRepo(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	cases := []struct {
		name string
		path string
		want string
	}{
		{name: "not-found", path: "/acme/hidden.git", want: "private repo acme/hidden"},
		{name: "unauthorized", path: "/acme/locked.git", want: "private repo acme/locked"},
		{name: "forbidden", path: "/acme/forbidden.git", want: "private repo acme/forbidden"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := validCard(srv.URL + tc.path)
			f.label = "repo-" + tc.name
			res := card.Push(ctx, client, sprint, f.render())
			if res.Code != 2 || res.Stdout != "" || !strings.Contains(res.Stderr, tc.want) {
				t.Fatalf("exit %d stdout %q stderr %q, want exit 2 and %q", res.Code, res.Stdout, res.Stderr, tc.want)
			}
			assertAbsent(t, ctx, client, f.label)
		})
	}

	t.Run("probe-is-not-private", func(t *testing.T) {
		f := validCard(srv.URL + "/acme/broken.git")
		f.label = "repo-broken"
		res := card.Push(ctx, client, sprint, f.render())
		if res.Code != 2 || res.Stdout != "" || !strings.Contains(res.Stderr, "probe: HTTP 500") || strings.Contains(res.Stderr, "private repo") {
			t.Fatalf("probe failure: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
		}
		assertAbsent(t, ctx, client, f.label)
	})

	f := validCard(srv.URL + "/acme/public.git")
	f.label = "repo-public"
	res := card.Push(ctx, client, sprint, f.render())
	if res.Code != 0 || res.Stderr != "" || !strings.Contains(res.Stdout, "place=pool") {
		t.Fatalf("public repo was refused: exit %d stdout %q stderr %q", res.Code, res.Stdout, res.Stderr)
	}
	assertPlace(t, ctx, client, f.label, true, false)
}

// TestPrivateRepoRedirectIsRefused is the negative control for a forge that
// answers a private repository with 302 and a login page that returns 200.
// Following that redirect would accept the page's 200 as a public repository.
func TestPrivateRepoRedirectIsRefused(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	var loginHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/redirected":
			http.Redirect(w, r, "/login", http.StatusFound)
		case "/login":
			loginHits++
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	f := validCard(srv.URL + "/acme/redirected.git")
	f.label = "repo-redirect"
	res := card.Push(ctx, client, sprint, f.render())
	if res.Code != 2 || res.Stdout != "" || !strings.Contains(res.Stderr, "private repo acme/redirected") {
		t.Fatalf("exit %d stdout %q stderr %q, want exit 2 and private repo acme/redirected", res.Code, res.Stdout, res.Stderr)
	}
	if strings.Contains(res.Stderr, "probe:") {
		t.Fatalf("redirect classified as a probe failure, not private: %q", res.Stderr)
	}
	if loginHits != 0 {
		t.Fatalf("privacy probe followed the redirect and read the login page (%d hits)", loginHits)
	}
	assertAbsent(t, ctx, client, f.label)
}

func TestDependentWaitsUntilParentMerged(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := newRedis(t)
	srv := repoServer(t)
	repo := srv.URL + "/acme/public.git"
	mergeParent := strings.Repeat("b", 40)
	mergeOther := strings.Repeat("c", 40)

	parent := validCard(repo)
	parent.label = "parent-2731"
	other := validCard(repo)
	other.label = "other-2731"
	child := validCard(repo)
	child.label = "child-2731"
	child.depends = "parent-2731"
	bystander := validCard(repo)
	bystander.label = "bystander-2731"
	bystander.depends = "other-2731"

	mustPush(t, ctx, client, parent.render(), "pool")
	mustPush(t, ctx, client, other.render(), "pool")
	mustPush(t, ctx, client, child.render(), "waiting")
	mustPush(t, ctx, client, bystander.render(), "waiting")
	assertPlace(t, ctx, client, "parent-2731", true, false)
	assertPlace(t, ctx, client, "other-2731", true, false)
	assertPlace(t, ctx, client, "child-2731", false, true)
	assertPlace(t, ctx, client, "bystander-2731", false, true)
	if got := client.HGet(ctx, keyCard("child-2731"), "depends_on").Val(); got != "parent-2731" {
		t.Fatalf("child depends_on = %q, want parent-2731", got)
	}
	if got := client.HGet(ctx, keyCard("child-2731"), "state").Val(); got != "queued" {
		t.Fatalf("a waiting card's state = %q, want queued", got)
	}

	// The parent exists and is not merged. Release must not copy waiting into the pool.
	mustRelease(t, ctx, client, 0, 2)
	assertPlace(t, ctx, client, "child-2731", false, true)
	assertPlace(t, ctx, client, "bystander-2731", false, true)

	mustLand(t, ctx, client, "parent-2731", mergeParent)
	if got := client.HGet(ctx, keyCard("parent-2731"), "state").Val(); got != "landed" {
		t.Fatalf("parent state = %q, want landed", got)
	}
	if got := client.HGet(ctx, keyCard("parent-2731"), "merge_sha").Val(); got != mergeParent {
		t.Fatalf("parent merge_sha = %q", got)
	}
	// Landing the parent does not itself deal the child. Release does, and only that child.
	assertPlace(t, ctx, client, "child-2731", false, true)
	assertPlace(t, ctx, client, "bystander-2731", false, true)
	mustRelease(t, ctx, client, 1, 1)
	assertPlace(t, ctx, client, "child-2731", true, false)
	assertPlace(t, ctx, client, "bystander-2731", false, true)
	if got := client.HGet(ctx, keyCard("child-2731"), "state").Val(); got != "queued" {
		t.Fatalf("released child state = %q, want queued", got)
	}

	mustLand(t, ctx, client, "other-2731", mergeOther)
	assertPlace(t, ctx, client, "bystander-2731", false, true)
	mustRelease(t, ctx, client, 1, 0)
	assertPlace(t, ctx, client, "bystander-2731", true, false)
	assertPlace(t, ctx, client, "child-2731", true, false)
}

type cardFix struct {
	label     string
	base      string
	baseSHA   string
	paths     string
	depends   string
	done      string
	repoURL   string
	omit      map[string]bool
	emptyDone bool
}

func validCard(repoURL string) cardFix {
	return cardFix{
		label:   "card-2731",
		base:    "dev",
		baseSHA: baseSHA,
		paths:   "internal/nsprint/card/push.go, internal/nsprint/card/lint.go",
		depends: "none",
		done:    "go test ./internal/nsprint/card -run TestCardPushRefusesWithoutDoneWhen exits 2 when DONE-WHEN is absent and passes when the line is present",
		repoURL: repoURL,
	}
}

func (f cardFix) render() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "RESULT: %s sha=0123456789ab\n", f.label)
	write := func(key, val string) {
		if f.omit[key] {
			return
		}
		if key == "DONE-WHEN" && f.emptyDone {
			b.WriteString("DONE-WHEN:\n")
			return
		}
		fmt.Fprintf(&b, "%s: %s\n", key, val)
	}
	write("KIND", "fix")
	write("BASE", f.base)
	write("BASE-REPO", f.repoURL)
	write("BASE-SHA", f.baseSHA)
	write("PATHS", f.paths)
	write("DEPENDS-ON", f.depends)
	write("DONE-WHEN", f.done)
	return []byte(b.String())
}

func mustPush(t *testing.T, ctx context.Context, client *redis.Client, body []byte, place string) {
	t.Helper()
	res := card.Push(ctx, client, sprint, body)
	if res.Code != 0 || res.Stderr != "" || !strings.Contains(res.Stdout, "place="+place+"\n") {
		t.Fatalf("push place=%s: exit %d stdout %q stderr %q", place, res.Code, res.Stdout, res.Stderr)
	}
}

func mustRelease(t *testing.T, ctx context.Context, client *redis.Client, moved, waiting int) {
	t.Helper()
	res := card.Release(ctx, client, sprint)
	want := fmt.Sprintf("CARD RELEASE sprint=%s moved=%d waiting=%d\n", sprint, moved, waiting)
	if res.Code != 0 || res.Stderr != "" || res.Stdout != want {
		t.Fatalf("release: exit %d stdout %q stderr %q, want stdout %q", res.Code, res.Stdout, res.Stderr, want)
	}
}

func mustLand(t *testing.T, ctx context.Context, client *redis.Client, label, merge string) {
	t.Helper()
	res := card.Land(ctx, client, sprint, label, merge)
	if res.Code != 0 || res.Stderr != "" || !strings.Contains(res.Stdout, "label="+label) {
		t.Fatalf("land %s: exit %d stdout %q stderr %q", label, res.Code, res.Stdout, res.Stderr)
	}
}

func assertPlace(t *testing.T, ctx context.Context, client *redis.Client, label string, pool, waiting bool) {
	t.Helper()
	inPool, err := zMember(ctx, client, keyPool(), label)
	if err != nil {
		t.Fatal(err)
	}
	inWaiting, err := client.SIsMember(ctx, keyWaiting(), label).Result()
	if err != nil {
		t.Fatal(err)
	}
	if inPool != pool || inWaiting != waiting {
		t.Fatalf("%s pool=%v waiting=%v, want pool=%v waiting=%v", label, inPool, inWaiting, pool, waiting)
	}
}

func assertAbsent(t *testing.T, ctx context.Context, client *redis.Client, label string) {
	t.Helper()
	n, err := client.Exists(ctx, keyCard(label)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("refused card %s was stored", label)
	}
	assertPlace(t, ctx, client, label, false, false)
}

func keyCard(label string) string { return "s:" + sprint + ":card:" + label }
func keyPool() string             { return "s:" + sprint + ":pool" }
func keyWaiting() string          { return "s:" + sprint + ":waiting" }

func zMember(ctx context.Context, client *redis.Client, key, member string) (bool, error) {
	err := client.ZScore(ctx, key, member).Err()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func repoServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/acme/public":
			w.WriteHeader(http.StatusOK)
		case "/acme/hidden":
			w.WriteHeader(http.StatusNotFound)
		case "/acme/locked":
			w.WriteHeader(http.StatusUnauthorized)
		case "/acme/forbidden":
			w.WriteHeader(http.StatusForbidden)
		case "/acme/broken":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := startPushRedis(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	// The owner converges the server, as ns-deploy does on the fleet: card
	// push never loads the library itself (#3266).
	if err := fn.Load(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	return client
}

func startPushRedis(t *testing.T) string {
	t.Helper()
	return testutil.Start(t)
}
