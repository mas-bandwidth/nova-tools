//go:build functional

package main

import (
	"bytes"
	"context"
	"net"
	"regexp"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// TestIdemResolveLinesAndExits is the `nova-sprint idem resolve` contract
// (#2930 rev 5): exit 0 with the RESOLVED line, exit 1 with the STATE line
// and nothing written (a stale --was, a key that is not ambiguous, a
// replay), exit 2 with one refuse line on stderr when Redis is unreachable,
// the function library is not loaded, or on a usage error.
func TestIdemResolveLinesAndExits(t *testing.T) {
	addr, client := sprintRedis(t)
	ctx := context.Background()
	const s = "control-idem0001"
	idem, unresolved, log := "s:"+s+":idem", "s:"+s+":unresolved", "s:"+s+":log"
	keyA, keyB := "pr:ctl-org/ctl-repo:nova/"+s+"/a-a1", "pr:ctl-org/ctl-repo:nova/"+s+"/b-a1"
	wasA, wasB := "ambiguous:harvest-a:1000", "ambiguous:harvest-b:2000"
	idemMust(t, client.HSet(ctx, idem, keyA, wasA, keyB, wasB).Err())
	idemMust(t, client.HSet(ctx, unresolved, "pr-ambiguous:ctl-org/ctl-repo:nova/"+s+"/a-a1", "1-0",
		"pr-ambiguous:ctl-org/ctl-repo:nova/"+s+"/b-a1", "2-0").Err())
	const url = "https://github.test/ctl-org/ctl-repo/pull/7"

	idemRun := func(args ...string) (int, string, string) {
		t.Helper()
		var out, errOut bytes.Buffer
		code := run(append([]string{"idem"}, args...), &out, &errOut)
		return code, out.String(), errOut.String()
	}
	resolve := func(extra ...string) (int, string, string) {
		return idemRun(append([]string{"resolve", "--redis", addr, "--sprint", s, "--who", "ctl-friend"}, extra...)...)
	}
	unchanged := func(what string, run func() (int, string, string), want int, line string) {
		t.Helper()
		before, items := snapshotHash(t, client, idem), snapshotHash(t, client, unresolved)
		n, _ := client.XLen(ctx, log).Result()
		code, out, errOut := run()
		if code != want || out != line || errOut != "" {
			t.Fatalf("%s: exit %d stdout %q stderr %q, want exit %d stdout %q", what, code, out, errOut, want, line)
		}
		if !sameMap(before, snapshotHash(t, client, idem)) || !sameMap(items, snapshotHash(t, client, unresolved)) {
			t.Fatalf("%s wrote to the idem or unresolved hash", what)
		}
		if after, _ := client.XLen(ctx, log).Result(); after != n {
			t.Fatalf("%s wrote a receipt: log %d -> %d", what, n, after)
		}
	}

	// Exit 1: a stale --was writes nothing.
	unchanged("stale --was", func() (int, string, string) {
		return resolve("--key", keyA, "--was", wasA+"0", "--url", url)
	}, 1, "STATE sprint="+s+" key="+keyA+" value="+wasA+"\n")
	unchanged("absent key", func() (int, string, string) {
		return resolve("--key", "pr:ctl-org/ctl-repo:none", "--was", wasA, "--none")
	}, 1, "STATE sprint="+s+" key=pr:ctl-org/ctl-repo:none value=absent\n")

	// Exit 0: --url records the URL, one receipt, the item deleted.
	code, out, errOut := resolve("--key", keyA, "--was", wasA, "--url", url)
	line := regexp.MustCompile(`^RESOLVED sprint=` + regexp.QuoteMeta(s) + ` key=` + regexp.QuoteMeta(keyA) +
		` url=` + regexp.QuoteMeta(url) + ` receipt=([0-9]+-[0-9]+)\n$`)
	m := line.FindStringSubmatch(out)
	if code != 0 || m == nil || errOut != "" {
		t.Fatalf("resolve --url: exit %d stdout %q stderr %q", code, out, errOut)
	}
	if got, _ := client.HGet(ctx, idem, keyA).Result(); got != url {
		t.Fatalf("idem %s = %q, want %q", keyA, got, url)
	}
	if n, _ := client.HExists(ctx, unresolved, "pr-ambiguous:ctl-org/ctl-repo:nova/"+s+"/a-a1").Result(); n {
		t.Fatal("resolve --url left the pr-ambiguous item")
	}
	if msgs, _ := client.XRange(ctx, log, m[1], m[1]).Result(); len(msgs) != 1 || msgs[0].Values["to"] != "resolved" || msgs[0].Values["actor"] != "ctl-friend" {
		t.Fatalf("receipt %s = %+v", m[1], msgs)
	}
	// A replay is a STATE, not a second write.
	unchanged("replay", func() (int, string, string) {
		return resolve("--key", keyA, "--was", wasA, "--url", url)
	}, 1, "STATE sprint="+s+" key="+keyA+" value="+url+"\n")

	// Exit 0: --none deletes the key and the item.
	code, out, _ = resolve("--key", keyB, "--was", wasB, "--none")
	if code != 0 || !regexp.MustCompile(`^RESOLVED sprint=\S+ key=\S+ url=none receipt=[0-9]+-[0-9]+\n$`).MatchString(out) {
		t.Fatalf("resolve --none: exit %d stdout %q", code, out)
	}
	if n, _ := client.HExists(ctx, idem, keyB).Result(); n {
		t.Fatal("resolve --none left the key")
	}

	// Exit 2: usage errors, one refuse line on stderr, stdout empty.
	refused := func(what string, code int, out, errOut string) {
		t.Helper()
		if code != 2 || out != "" || strings.Count(errOut, "\n") != 1 ||
			!strings.HasPrefix(errOut, "nova-sprint idem") || !strings.HasSuffix(errOut, "; run: nova-sprint help\n") {
			t.Fatalf("%s: exit %d stdout %q stderr %q, want exit 2 and one refuse line", what, code, out, errOut)
		}
	}
	for _, c := range []struct {
		name string
		args []string
	}{
		{"both url and none", []string{"--key", keyA, "--was", wasA, "--url", url, "--none"}},
		{"neither url nor none", []string{"--key", keyA, "--was", wasA}},
		{"missing was", []string{"--key", keyA, "--url", url}},
		{"missing key", []string{"--was", wasA, "--url", url}},
		{"positional", []string{"--key", keyA, "--was", wasA, "--url", url, "extra"}},
		{"unknown flag", []string{"--key", keyA, "--was", wasA, "--url", url, "--force"}},
		{"whitespace was", []string{"--key", keyA, "--was", "   ", "--url", url}},
		{"whitespace url", []string{"--key", keyA, "--was", wasA, "--url", "   "}},
	} {
		code, out, errOut := resolve(c.args...)
		refused(c.name, code, out, errOut)
		if !strings.HasPrefix(errOut, "nova-sprint idem resolve: ") {
			t.Fatalf("%s: stderr %q", c.name, errOut)
		}
	}
	code, out, errOut = idemRun()
	refused("no subverb", code, out, errOut)
	code, out, errOut = idemRun("open")
	refused("unknown subverb", code, out, errOut)

	// Exit 2: Redis unreachable (a port nothing listens on).
	l, err := net.Listen("tcp", "127.0.0.1:0")
	idemMust(t, err)
	dead := l.Addr().String()
	idemMust(t, l.Close())
	var o, e bytes.Buffer
	code = run([]string{"idem", "resolve", "--redis", dead, "--sprint", s, "--key", keyA, "--was", wasA, "--url", url, "--who", "ctl-friend"}, &o, &e)
	refused("redis unreachable", code, o.String(), e.String())

	// Exit 2: the function library is not loaded (never loaded by a friend).
	idemMust(t, client.Do(ctx, "FUNCTION", "FLUSH").Err())
	idemMust(t, client.HSet(ctx, idem, keyB, wasB).Err())
	code, out, errOut = resolve("--key", keyB, "--was", wasB, "--none")
	refused("library not loaded", code, out, errOut)
	if got, _ := client.HGet(ctx, idem, keyB).Result(); got != wasB {
		t.Fatalf("library not loaded changed the key: %q", got)
	}
}

// TestIdemResolvePaddedValidInputs verifies that flags with leading/trailing
// whitespace are trimmed and that stdout prints normalized values for
// RESOLVED (both --url and --none) and STATE lines.
func TestIdemResolvePaddedValidInputs(t *testing.T) {
	addr, client := sprintRedis(t)
	ctx := context.Background()
	const s = "control-idem-pad01"
	idem, unresolved := "s:"+s+":idem", "s:"+s+":unresolved"
	keyA, keyB := "pr:ctl-org/ctl-repo:nova/"+s+"/a-a1", "pr:ctl-org/ctl-repo:nova/"+s+"/b-a1"
	wasA, wasB := "ambiguous:harvest-a:1000", "ambiguous:harvest-b:2000"
	idemMust(t, client.HSet(ctx, idem, keyA, wasA, keyB, wasB).Err())
	idemMust(t, client.HSet(ctx, unresolved, "pr-ambiguous:ctl-org/ctl-repo:nova/"+s+"/a-a1", "1-0",
		"pr-ambiguous:ctl-org/ctl-repo:nova/"+s+"/b-a1", "2-0").Err())
	const url = "https://github.test/ctl-org/ctl-repo/pull/42"

	resolve := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := run(append([]string{"idem", "resolve"}, args...), &out, &errOut)
		return code, out.String(), errOut.String()
	}

	// 1. Padded RESOLVED with --url: normalized sprint, key, url in output.
	code, out, errOut := resolve(
		"--redis", "  "+addr+"  ",
		"--sprint", "  "+s+"  ",
		"--key", "  "+keyA+"  ",
		"--who", "  ctl-friend  ",
		"--was", "  "+wasA+"  ",
		"--url", "  "+url+"  ",
	)
	if code != 0 || errOut != "" {
		t.Fatalf("resolve padded --url failed: exit %d stderr %q", code, errOut)
	}
	wantResolved := regexp.MustCompile(`^RESOLVED sprint=` + regexp.QuoteMeta(s) +
		` key=` + regexp.QuoteMeta(keyA) +
		` url=` + regexp.QuoteMeta(url) +
		` receipt=[0-9]+-[0-9]+\n$`)
	if !wantResolved.MatchString(out) {
		t.Fatalf("resolve padded --url stdout = %q, want matching %s", out, wantResolved)
	}

	// 2. Padded STATE (replay/stale was): normalized sprint, key in output.
	code, out, errOut = resolve(
		"--redis", "  "+addr+"  ",
		"--sprint", "  "+s+"  ",
		"--key", "  "+keyA+"  ",
		"--who", "  ctl-friend  ",
		"--was", "  stale-was  ",
		"--url", "  "+url+"  ",
	)
	if code != 1 || errOut != "" {
		t.Fatalf("resolve padded STATE failed: exit %d stderr %q", code, errOut)
	}
	wantState := "STATE sprint=" + s + " key=" + keyA + " value=" + url + "\n"
	if out != wantState {
		t.Fatalf("resolve padded STATE stdout = %q, want %q", out, wantState)
	}

	// 3. Padded RESOLVED with --none: normalized sprint, key in output.
	code, out, errOut = resolve(
		"--redis", "  "+addr+"  ",
		"--sprint", "  "+s+"  ",
		"--key", "  "+keyB+"  ",
		"--who", "  ctl-friend  ",
		"--was", "  "+wasB+"  ",
		"--none",
	)
	if code != 0 || errOut != "" {
		t.Fatalf("resolve padded --none failed: exit %d stderr %q", code, errOut)
	}
	wantNone := regexp.MustCompile(`^RESOLVED sprint=` + regexp.QuoteMeta(s) +
		` key=` + regexp.QuoteMeta(keyB) +
		` url=none receipt=[0-9]+-[0-9]+\n$`)
	if !wantNone.MatchString(out) {
		t.Fatalf("resolve padded --none stdout = %q, want matching %s", out, wantNone)
	}
}

func snapshotHash(t *testing.T, client *redis.Client, key string) map[string]string {
	t.Helper()
	h, err := client.HGetAll(context.Background(), key).Result()
	idemMust(t, err)
	return h
}

func sameMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func idemMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
