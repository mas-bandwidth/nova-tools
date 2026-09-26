//go:build functional

package main

// Cold-read probes for #4430 at 1abff1357 (who=rowan-opus). Each runs the
// verb through run() (the binary's main) on a throwaway store, then runs the
// next action the failure printed and asserts it resolves the fixture.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

func r4430Run(args ...string) (int, string, string) {
	var o, e bytes.Buffer
	code := run(args, &o, &e)
	return code, o.String(), e.String()
}

func r4430Keys(t *testing.T, c *redis.Client) string {
	t.Helper()
	ks, err := c.Keys(context.Background(), "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(ks)
	var b strings.Builder
	for _, k := range ks {
		typ := c.Type(context.Background(), k).Val()
		b.WriteString(k + "=" + typ + ";")
		if typ == "hash" {
			h := c.HGetAll(context.Background(), k).Val()
			fs := make([]string, 0, len(h))
			for f, v := range h {
				fs = append(fs, f+":"+v)
			}
			sort.Strings(fs)
			b.WriteString(strings.Join(fs, ",") + ";")
		}
	}
	return b.String()
}

// (a) --policy a directory, an unreadable file, a malformed number: exit 2
// before evaluation, the path named, and not one key written or changed.
func TestRead4430PolicyRefusalLeavesStoreUnchanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	unreadable := filepath.Join(dir, "unreadable.yml")
	if err := os.WriteFile(unreadable, []byte("repo: nova-tools\nbases:\n  dev:\n    readers: 1\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.yml")
	if err := os.WriteFile(bad, []byte("repo: nova-tools\nbases:\n  dev:\n    readers: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"dir": dir, "unreadable": unreadable, "readers-x": bad} {
		c, _ := sdStore(t)
		before := r4430Keys(t, c)
		code, out, errOut := r4430Run("land", "eval", "--redis", c.Options().Addr, "--sprint", sdSprint, "--repo", "nova-tools", "--policy", path, "--mirror", "none")
		if code != 2 || out != "" || !strings.Contains(errOut, path) || !strings.Contains(errOut, "nothing evaluated, store unchanged") {
			t.Errorf("%s: exit=%d stdout=%q stderr=%q", name, code, out, errOut)
		}
		if name == "readers-x" && !strings.Contains(errOut, "field readers") {
			t.Errorf("%s: no field named: %q", name, errOut)
		}
		if after := r4430Keys(t, c); after != before {
			t.Errorf("%s: store changed:\n before %s\n after  %s", name, before, after)
		}
	}
}

// (b) hold show, as a sequence: absent -> the printed ingest -> still
// absent? WRONGTYPE -> the printed inspect runs -> repair -> absent.
func TestRead4430HoldShowSequence(t *testing.T) {
	t.Parallel()
	c, _ := sdStore(t)
	ctx := context.Background()
	addr := c.Options().Addr
	key := "s:" + sdSprint + ":prunit:nova-tools:7"
	if err := c.HSet(ctx, key, "wrong", "type").Err(); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := r4430Run("hold", "show", "--redis", addr, "--sprint", sdSprint, "nova-tools#7")
	for _, want := range []string{"GET " + key, "WRONGTYPE", "whether the unit exists is unknown", "TYPE " + key, addr} {
		if code != 2 || !strings.Contains(errOut, want) {
			t.Fatalf("wrongtype: exit=%d stderr=%q lacks %q", code, errOut, want)
		}
	}
	if typ := c.Type(ctx, key).Val(); typ != "hash" {
		t.Fatalf("inspect TYPE %s = %q", key, typ)
	}
	if h := c.HGetAll(ctx, key).Val(); h["wrong"] != "type" {
		t.Fatalf("inspect HGETALL %s = %v", key, h)
	}
	c.Del(ctx, key)
	code, _, errOut = r4430Run("hold", "show", "--redis", addr, "--sprint", sdSprint, "nova-tools#7")
	// Round 2: the absent receipt names land eval, not hold ingest (see below).
	if code != 2 || !strings.Contains(errOut, "no unit for nova-tools#7") || !strings.Contains(errOut, "nova-sprint land eval") || strings.Contains(errOut, "unknown") {
		t.Fatalf("absent after repair: exit=%d stderr=%q", code, errOut)
	}
	// The absent receipt's remedy. Round 2 (Rowan): the reader's copy ran
	// `hold ingest`, which resolves through the same key and refuses
	// NORECORD without a unit; a unit is made only by land eval
	// (ns_unit_head via HeadFromGit from the PR's branch), so the receipt
	// now names land eval and this step runs it: the unit on branch card-7
	// carries pr=7, its prunit key was the one clobbered and deleted above,
	// and the reconcile over the bench mirror remakes it.
	up, mirror := filepath.Join(t.TempDir(), "up"), filepath.Join(t.TempDir(), "mirror.git")
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	os.MkdirAll(up, 0o755)
	git(up, "init", "-q", "-b", "dev")
	commit := func(file string) string {
		os.WriteFile(filepath.Join(up, file), []byte(file), 0o644)
		git(up, "add", file)
		git(up, "commit", "-q", "-m", file)
		return git(up, "rev-parse", "HEAD")
	}
	base := commit("base.txt")
	git(up, "checkout", "-q", "-b", "card-7")
	h1 := commit("a.go")
	if _, err := land.CallUnitHead(ctx, c, land.UnitHeadParams{Sprint: sdSprint, Unit: "unit-7", Repo: "nova-tools", Base: "dev", Branch: "card-7", Head: h1, BaseSHA: base, PR: "7"}); err != nil {
		t.Fatal(err)
	}
	git(filepath.Dir(mirror), "clone", "-q", "--bare", up, mirror)
	// One pass has seen card-7 at h1 (ns_ref_seen), the way a bench's eval
	// loop has before any repair; then the key is clobbered and deleted,
	// the branch moves, and the printed remedy runs. The mirror fetch is
	// gated on FETCH_HEAD's age (FetchEach, 60 s at the CLI), so the first
	// pass's FETCH_HEAD is aged past it: the remedy fetches for itself.
	if pcode, pout, perr := r4430Run("land", "eval", "--redis", addr, "--sprint", sdSprint, "--repo", "nova-tools", "--mirror", mirror); pcode != 0 {
		t.Fatalf("first pass: exit=%d %q %q", pcode, pout, perr)
	}
	c.Del(ctx, key) // the clobbered-then-deleted prunit key: the unit stays
	h2 := commit("a2.go")
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(filepath.Join(mirror, "FETCH_HEAD"), old, old); err != nil {
		t.Fatal(err)
	}
	icode, iout, ierr := r4430Run("land", "eval", "--redis", addr, "--sprint", sdSprint, "--repo", "nova-tools", "--mirror", mirror)
	code, out, errOut := r4430Run("hold", "show", "--redis", addr, "--sprint", sdSprint, "nova-tools#7")
	t.Logf("printed remedy: land eval exit=%d stdout=%q stderr=%q; then hold show exit=%d stdout=%q stderr=%q", icode, iout, ierr, code, out, errOut)
	if icode != 0 || code != 0 || !strings.Contains(out, "unit=unit-7 head="+h2) {
		t.Errorf("the absent receipt's remedy (land eval) does not make the unit: hold show exit=%d %q %q", code, out, errOut)
	}
	// A dead store: exit 6.
	dead := "127.0.0.1:1"
	code, _, errOut = r4430Run("hold", "show", "--redis", dead, "--sprint", sdSprint, "nova-tools#7")
	if code != 6 {
		t.Errorf("dead store: exit=%d stderr=%q", code, errOut)
	}
}

// (c) card session with a missing wrapper, then the printed next action
// (fix the wrapper, card session --as bench:b retakes the copy).
func TestRead4430CardSessionThenItsNextAction(t *testing.T) {
	t.Parallel()
	c, _ := sdStore(t)
	ctx := context.Background()
	addr := c.Options().Addr
	bench, _ := taskcard.ParseConsumer("bench:b")
	c.SAdd(ctx, "benches", "b")
	c.HSet(ctx, bench.DesiredKey(), "slots", "1")
	if err := taskcard.Enroll(ctx, c, bench, true); err != nil {
		t.Fatal(err)
	}
	if err := sdCard(t, c, "launch-audit", "audit", ""); err != nil {
		t.Fatal(err)
	}
	dealt, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: bench, N: 1, By: "audit"})
	if err != nil || len(dealt) != 1 || dealt[0].Copy == "" || !taskcard.IsCopy(dealt[0].Copy) {
		t.Fatalf("deal %v %v", dealt, err)
	}
	t.Logf("dealt copy %q", dealt[0].Copy)
	missing := filepath.Join(t.TempDir(), "missing-nova-card")
	code, out, errOut := r4430Run("card", "session", "--redis", addr, "--as", "bench:b", "--actor", "bench:b", "--wrapper", missing)
	t.Logf("session 1: exit=%d stdout=%q stderr=%q", code, out, errOut)
	for _, want := range []string{"copy=" + dealt[0].Copy, missing, "given back (primary to ", "next: "} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr lacks %q: %q", want, errOut)
		}
	}
	tok := c.HGet(ctx, "task:"+dealt[0].Copy, "token").Val()
	if tok != "" && strings.Contains(errOut+out, tok) {
		t.Errorf("token leaked: %q", errOut)
	}
	w := c.HGet(ctx, "task:launch-audit", "where").Val()
	t.Logf("primary where=%s after give-back", w)
	// The next action. Round 2 (Rowan): the reader's copy ran card session
	// again and took nothing, because the give-back leaves the primary in
	// waiting until a deal cuts a new copy; the receipt now prints that
	// deal, naming the primary, and this step runs it verbatim before the
	// session that takes the new copy.
	if !strings.Contains(errOut, "nova-sprint card deal --to bench:b --ids launch-audit --actor bench:b") {
		t.Fatalf("the receipt does not print the deal: %q", errOut)
	}
	dcode, dout, derr := r4430Run("card", "deal", "--redis", addr, "--to", "bench:b", "--ids", "launch-audit", "--actor", "bench:b")
	t.Logf("the printed next action (deal): exit=%d stdout=%q stderr=%q", dcode, dout, derr)
	if dcode != 0 {
		t.Fatalf("the printed deal refused: exit=%d %q %q", dcode, dout, derr)
	}
	wrapper := filepath.Join(t.TempDir(), "nova-card")
	os.WriteFile(wrapper, []byte("#!/bin/sh\ncat >/dev/null\necho ACK\nsleep 1\n"), 0o755)
	code, out, errOut = r4430Run("card", "session", "--redis", addr, "--as", "bench:b", "--actor", "bench:b", "--wrapper", wrapper)
	t.Logf("session 2 (after the printed next action): exit=%d stdout=%q stderr=%q", code, out, errOut)
	if !strings.Contains(out, "worked=1") {
		t.Errorf("the printed next action retakes nothing: primary where=%s, stdout=%q stderr=%q", w, out, errOut)
	}
}

// (c') a wrapper that echoes its stdin puts the copy's token in the launch
// error: the receipt and the give-back's recorded why carry <token>, never it.
func TestRead4430LaunchFailureScrubsTheToken(t *testing.T) {
	t.Parallel()
	c, _ := sdStore(t)
	ctx := context.Background()
	bench, _ := taskcard.ParseConsumer("bench:b")
	c.SAdd(ctx, "benches", "b")
	c.HSet(ctx, bench.DesiredKey(), "slots", "1")
	if err := taskcard.Enroll(ctx, c, bench, true); err != nil {
		t.Fatal(err)
	}
	if err := sdCard(t, c, "scrub", "audit", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Deal(ctx, c, taskcard.DealRequest{To: bench, N: 1, By: "audit"}); err != nil {
		t.Fatal(err)
	}
	var tok string
	s, err := card.OpenCopySession(ctx, c, "b", "bench:b", func(l card.CopyLaunch) error {
		tok = l.Token
		return errors.New("wrapper said: sd-sprint " + l.Copy + " 1 " + l.Token)
	})
	if err != nil || tok == "" || len(s.Failed) != 1 {
		t.Fatalf("session %+v %v token=%q", s, err, tok)
	}
	lines := strings.Join(s.FailureLines("b"), "\n")
	if strings.Contains(lines, tok) || !strings.Contains(lines, "<token>") {
		t.Errorf("receipt carries the token: %q", lines)
	}
	for _, k := range c.Keys(ctx, "*").Val() {
		if c.Type(ctx, k).Val() == "hash" {
			for f, v := range c.HGetAll(ctx, k).Val() {
				if f != "token" && strings.Contains(v, tok) {
					t.Errorf("%s %s carries the token: %q", k, f, v)
				}
			}
		}
	}
}
