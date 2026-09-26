//go:build functional

package main

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

var remedyRx = regexp.MustCompile(` remedy=("(?:[^"\\]|\\.)*")`)

// remedyArgs is a receipt's remedy as argv: the quoted value unquoted and
// split into words, a leading "nova-sprint" dropped.
func remedyArgs(t *testing.T, receipt string) []string {
	t.Helper()
	m := remedyRx.FindStringSubmatch(receipt)
	if m == nil {
		t.Fatalf("no remedy on %q", receipt)
	}
	r, err := strconv.Unquote(m[1])
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Fields(r)
	if len(args) > 0 && args[0] == "nova-sprint" {
		args = args[1:]
	}
	return args
}

// TestPathsRemedyRunsVerbatim is the PATHS gate's receipts through the CLI
// (#4322), each remedy run as printed: a store that lost ws:paths refuses a
// task push (PATHS unbuilt, remedy nova-sprint ws check --repair); the
// repair runs; the push is then refused as an overlap (remedy --join work)
// and the same push with the remedy's flag joins work; task move
// --to-stream of a task whose paths a sibling still holds is refused
// (remedy nova-sprint scope park --stream work), the park runs, the move
// goes through; a stream that took work's paths while it was parked makes
// scope unpark refuse (remedy nova-sprint scope park --stream taker), that
// park runs, the unpark goes through; ws check then finds no overlap. Each
// remedy runs with the store's --redis (the operator's NOVA_SPRINT_REDIS)
// and a park's --checkpoint in the test's own directory.
func TestPathsRemedyRunsVerbatim(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	cp, parks := t.TempDir(), 0
	cli := func(args ...string) (int, string, string) {
		t.Helper()
		args = append(args, "--redis", addr)
		if len(args) > 1 && args[0] == "scope" && args[1] == "park" {
			parks++
			args = append(args, "--checkpoint", cp+"/park"+strconv.Itoa(parks)+".tsv")
		}
		return runCLI(args...)
	}
	push := func(id, stream, paths string, more ...string) (int, string) {
		t.Helper()
		args := append([]string{"push", "--redis", addr, "--actor", "rowan", "--id", id, "--stream", stream, "--title", id,
			"--paths", paths}, more...)
		code, out, errOut := runTaskCLI(args...)
		return code, out + errOut
	}
	if code, out := push("pr-1", "work", "internal/x"); code != 0 {
		t.Fatalf("push pr-1: %d %q", code, out)
	}
	c.Del(ctx, ws.PathsKey)
	c.HDel(ctx, "task:pr-1", ws.PathsField)

	code, out := push("pr-2", "ci", "internal/x/y.go")
	want := `REFUSED PATHS unbuilt stream=work remedy="nova-sprint ws check --repair" id=pr-2 ms=`
	if code != 1 || !strings.HasPrefix(out, want) {
		t.Fatalf("unbuilt: %d %q; want prefix %q", code, out, want)
	}
	if code, out, errOut := cli(remedyArgs(t, out)...); code != 0 || !strings.Contains(out, " stale=1 repaired=1 records=1 ") {
		t.Fatalf("ws check --repair: %d %q %q", code, out, errOut)
	}

	code, out = push("pr-2", "ci", "internal/x/y.go")
	want = `REFUSED PATHS overlap stream=work paths=internal/x,internal/x/y.go remedy="--join work" id=pr-2 ms=`
	if code != 1 || !strings.HasPrefix(out, want) {
		t.Fatalf("overlap: %d %q; want prefix %q", code, out, want)
	}
	if code, out := push("pr-2", "ci", "internal/x/y.go", remedyArgs(t, out)...); code != 0 || c.HGet(ctx, "task:pr-2", "stream").Val() != "work" {
		t.Fatalf("push --join work: %d %q stream %q", code, out, c.HGet(ctx, "task:pr-2", "stream").Val())
	}

	code, out, _ = runTaskCLI("move", "--redis", addr, "--actor", "rowan", "--id", "pr-2", "--to-stream", "docs")
	want = `REFUSED PATHS overlap stream=work paths=internal/x,internal/x/y.go remedy="nova-sprint scope park --stream work" id=pr-2 ms=`
	if code != 1 || !strings.HasPrefix(out, want) || c.HGet(ctx, "task:pr-2", "stream").Val() != "work" {
		t.Fatalf("move --to-stream docs: %d %q; want prefix %q", code, out, want)
	}
	if code, out, errOut := cli(remedyArgs(t, out)...); code != 0 || !strings.Contains(out, "PARKED") {
		t.Fatalf("scope park --stream work: %d %q %q", code, out, errOut)
	}
	if code, out, errOut := runTaskCLI("move", "--redis", addr, "--actor", "rowan", "--id", "pr-2", "--to-stream", "docs"); code != 0 {
		t.Fatalf("move after the park: %d %q %q", code, out, errOut)
	}

	if code, out := push("pr-3", "taker", "internal/x/q"); code != 0 {
		t.Fatalf("push onto taker while work is parked: %d %q", code, out)
	}
	code, out, _ = cli("scope", "unpark", "--stream", "work")
	want = `REFUSED PATHS overlap stream=taker paths=internal/x,internal/x/q remedy="nova-sprint scope park --stream taker" unpark=work ms=`
	if code != 1 || !strings.HasPrefix(out, want) || c.HGet(ctx, "task:pr-1", "where").Val() != "parked" {
		t.Fatalf("unpark work: %d %q; want prefix %q and pr-1 parked", code, out, want)
	}
	if code, out, errOut := cli(remedyArgs(t, out)...); code != 0 {
		t.Fatalf("scope park --stream taker: %d %q %q", code, out, errOut)
	}
	if code, out, errOut := cli("scope", "unpark", "--stream", "work"); code != 0 || !strings.Contains(out, "unparked=1") {
		t.Fatalf("unpark work after taker parked: %d %q %q", code, out, errOut)
	}
	if code, out, errOut := cli("ws", "check"); code != 0 || !strings.Contains(out, " overlaps=0 stale=0 ") {
		t.Fatalf("ws check: %d %q %q", code, out, errOut)
	}
}

// TestCardCutFromPathsGateOnAStore is card cut --from's doors on a real
// store (#4322): its --dry-run reports the row the gate would refuse (an
// overlap, then PATHS unbuilt on a store that lost ws:paths) and files and
// pushes nothing; after the repair, a cut whose check read a stale view
// (another stream took the path between the check and the push) files the
// issue and is refused by the push's own FCALL, the same receipt printed,
// no task written.
func TestCardCutFromPathsGateOnAStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Push(ctx, client, taskcard.PushRequest{ID: "cf-w", Stream: "work", Title: "w", By: "test",
		Fields: []string{"paths", "internal/nsprint/ws"}}); err != nil {
		t.Fatal(err)
	}
	rows := []byte(pathsRows([3]string{"c1", "fleet", "internal/nsprint/ws/paths.go"}))
	forge := &fakeCutForge{}
	d := cutDepsRedis(forge, client)
	d.StreamPaths = func(ctx context.Context) (ws.GateView, error) { return ws.ReadGateView(ctx, client) }

	code, out := runCutFrom(cutFromOpts{Text: rows, DryRun: true, NoGitHub: true}, d)
	want := `REFUSED PATHS overlap stream=work paths=internal/nsprint/ws,internal/nsprint/ws/paths.go remedy="--join work" row=1 line=2 id=c1` + "\n"
	if code != 1 || !strings.HasPrefix(out, want) || len(forge.titles) != 0 || client.Exists(ctx, "task:c1").Val() != 0 {
		t.Fatalf("dry run: exit %d filed %d out %q; want first line %q", code, len(forge.titles), out, want)
	}
	client.Del(ctx, ws.PathsKey)
	code, out = runCutFrom(cutFromOpts{Text: rows, DryRun: true, NoGitHub: true}, d)
	want = `REFUSED PATHS unbuilt stream=work remedy="nova-sprint ws check --repair" row=1 line=2 id=c1` + "\n"
	if code != 1 || !strings.HasPrefix(out, want) {
		t.Fatalf("dry run unbuilt: exit %d out %q; want first line %q", code, out, want)
	}
	live, stored, cards, err := ws.LivePaths(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ws.RepairPaths(ctx, client, live, stored, cards); err != nil {
		t.Fatal(err)
	}

	d.StreamPaths = func(context.Context) (ws.GateView, error) {
		return ws.GateView{Paths: ws.StreamPaths{}, Open: map[string]bool{}}, nil // read before work took the path
	}
	code, out = runCutFrom(cutFromOpts{Text: rows}, d)
	want = `REFUSED PATHS overlap stream=work paths=internal/nsprint/ws,internal/nsprint/ws/paths.go remedy="--join work" row=1 line=2 id=c1` + "\n"
	if code != 1 || !strings.Contains(out, want) || client.Exists(ctx, "task:c1").Val() != 0 {
		t.Fatalf("lost race: exit %d out %q; want the push's own refusal %q and no task", code, out, want)
	}
}
