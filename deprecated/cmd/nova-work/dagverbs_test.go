package main

import (
	"bytes"
	"testing"
)

// The nine socket verbs the spec added (docs/SPEC-WORK.md, the verbs block,
// lines for dep, axis, roadmap create, roadmap configure, roadmap row,
// prioritise, cell, take and release) reach the session through the same
// socket path as every other row of verbFlags: one request line in, one
// reply line out, byte for byte. These nine named tests pin that one path
// each, by name, so a flag added to a row, a row added to socketVerbs and
// missed here, or a row that resolves the verb without dialling the socket
// is a red test in this file before it is a silent gap at the bench.
//
// Each test sets up the S1 stand-in (fakeSession), invokes the verb with
// the spec's own flag set, and pins the byte-for-byte request line the
// session sees alongside the byte-for-byte reply the caller reads. If the
// production path stops dialling the session -- for instance by short-
// circuiting with the OK line the session would have answered -- no request
// ever reaches the socket and awaitRequest fails the test, which is the
// control the card names.

// TestDep: the dep mutation writes a needs edge between two nodes. The
// spec's own flag surface is --session <path> --as <name> --node <id>
// (--add <id> | --remove <id>) --reason <text>.
func TestDep(t *testing.T) {
	socket, requests := fakeSession(t, "DEP OK node=a add=b")
	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"dep", "--session", socket,
		"--as", "Rowan",
		"--node", "a", "--add", "b",
		"--reason", "needs edge",
	}, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("dep exit = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "DEP OK node=a add=b\n"; got != want {
		t.Fatalf("dep stdout = %q, want %q", got, want)
	}
	if got, want := awaitRequest(t, requests),
		"dep --session "+socket+" --as Rowan --node a --add b --reason needs\\x20edge"; got != want {
		t.Fatalf("dep request line = %q, want %q", got, want)
	}
}

// TestAxis: the axis mutation adds a member to a roadmap's axis. The
// spec's flag surface is --session <path> --as <name> --roadmap <id>
// --axis <id> (--add <member> | --remove <member>) --reason <text>.
func TestAxis(t *testing.T) {
	socket, requests := fakeSession(t, "AXIS OK roadmap=R axis=A add=m")
	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"axis", "--session", socket,
		"--as", "Rowan",
		"--roadmap", "R", "--axis", "A",
		"--add", "m",
		"--reason", "axis member",
	}, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("axis exit = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "AXIS OK roadmap=R axis=A add=m\n"; got != want {
		t.Fatalf("axis stdout = %q, want %q", got, want)
	}
	if got, want := awaitRequest(t, requests),
		"axis --session "+socket+" --as Rowan --roadmap R --axis A --add m --reason axis\\x20member"; got != want {
		t.Fatalf("axis request line = %q, want %q", got, want)
	}
}

// TestRoadmapCreate: a fresh roadmap is created under a parent node. The
// spec's flag surface is --session <path> --as <name> --id <id>
// --under <parent-id> [--title <text>] --row-kind <kind>
// --aggregation <policy> --completion-policy all-required-features
// (--axes-none | --axis-id <id> ...) [--permit-root <root-id> ...]
// --reason <text>.
func TestRoadmapCreate(t *testing.T) {
	socket, requests := fakeSession(t, "ROADMAP OK id=R under=p")
	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"roadmap", "create", "--session", socket,
		"--as", "Rowan",
		"--id", "R", "--under", "p",
		"--row-kind", "feature",
		"--aggregation", "all-members",
		"--completion-policy", "all-required-features",
		"--axes-none",
		"--reason", "new roadmap",
	}, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("roadmap create exit = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "ROADMAP OK id=R under=p\n"; got != want {
		t.Fatalf("roadmap create stdout = %q, want %q", got, want)
	}
	want := "roadmap create --session " + socket +
		" --as Rowan --id R --under p --row-kind feature" +
		" --aggregation all-members --completion-policy all-required-features" +
		" --axes-none true --reason new\\x20roadmap"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("roadmap create request line = %q, want %q", got, want)
	}
}

// TestRoadmapConfigure: a roadmap's row-kind, aggregation or completion
// policy can be re-set without re-creating it. The spec's flag surface
// is --session <path> --as <name> --roadmap <id> [--row-kind <kind>]
// [--aggregation <policy>] [--completion-policy all-required-features]
// [--axes-none | --axis-id <id> ...]
// [--permit-root <root-id> ... | --roots-empty] --reason <text>.
func TestRoadmapConfigure(t *testing.T) {
	socket, requests := fakeSession(t, "ROADMAP OK roadmap=R row-kind=epic")
	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"roadmap", "configure", "--session", socket,
		"--as", "Rowan",
		"--roadmap", "R",
		"--row-kind", "epic",
		"--reason", "re-classify",
	}, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("roadmap configure exit = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "ROADMAP OK roadmap=R row-kind=epic\n"; got != want {
		t.Fatalf("roadmap configure stdout = %q, want %q", got, want)
	}
	want := "roadmap configure --session " + socket +
		" --as Rowan --roadmap R --row-kind epic --reason re-classify"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("roadmap configure request line = %q, want %q", got, want)
	}
}

// TestRoadmapRow: an axisless roadmap admits a row member through --add
// or --remove. The spec's flag surface is --session <path> --as <name>
// --roadmap <id> (--add <member> | --remove <member>) --reason <text>,
// and the parenthetical names the axisless-only constraint.
func TestRoadmapRow(t *testing.T) {
	socket, requests := fakeSession(t, "ROW OK roadmap=R add=m")
	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"roadmap", "row", "--session", socket,
		"--as", "Rowan",
		"--roadmap", "R", "--add", "m",
		"--reason", "row member",
	}, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("roadmap row exit = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "ROW OK roadmap=R add=m\n"; got != want {
		t.Fatalf("roadmap row stdout = %q, want %q", got, want)
	}
	want := "roadmap row --session " + socket +
		" --as Rowan --roadmap R --add m --reason row\\x20member"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("roadmap row request line = %q, want %q", got, want)
	}
}

// TestPrioritise: a node's priority rank is set or cleared in either
// scope. The spec's flag surface is --session <path> --as <name>
// --node <id> (--set <rank> | --clear) [--context <self|subtree>]
// --reason <text>.
func TestPrioritise(t *testing.T) {
	socket, requests := fakeSession(t, "PRIORITY OK node=a set=3")
	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"prioritise", "--session", socket,
		"--as", "Rowan",
		"--node", "a", "--set", "3",
		"--reason", "rank it",
	}, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("prioritise exit = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "PRIORITY OK node=a set=3\n"; got != want {
		t.Fatalf("prioritise stdout = %q, want %q", got, want)
	}
	want := "prioritise --session " + socket +
		" --as Rowan --node a --set 3 --reason rank\\x20it"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("prioritise request line = %q, want %q", got, want)
	}
}

// TestCell: a roadmap cell is set to a node ref, or marked out of scope,
// or cleared with --ref -. The spec's flag surface is --session <path>
// --as <name> --roadmap <id> --coord <member,member>
// (--ref <id|-> | --out-of-scope | --in-scope) --reason <text>.
func TestCell(t *testing.T) {
	socket, requests := fakeSession(t, "CELL OK roadmap=R coord=x,y ref=n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"cell", "--session", socket,
		"--as", "Rowan",
		"--roadmap", "R", "--coord", "x,y",
		"--ref", "n",
		"--reason", "cell to node",
	}, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("cell exit = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "CELL OK roadmap=R coord=x,y ref=n\n"; got != want {
		t.Fatalf("cell stdout = %q, want %q", got, want)
	}
	want := "cell --session " + socket +
		" --as Rowan --roadmap R --coord x,y --ref n --reason cell\\x20to\\x20node"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("cell request line = %q, want %q", got, want)
	}
}

// TestTake: a node is taken on a lease whose deadline is --by and whose
// default on timeout is --default. The spec's flag surface is
// --session <path> --as <name> --node <id> --by <duration|stamp>
// --default <release|extend-once|escalate:<name>>.
func TestTake(t *testing.T) {
	socket, requests := fakeSession(t, "TAKE OK node=a by=1h")
	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"take", "--session", socket,
		"--as", "Rowan",
		"--node", "a",
		"--by", "1h",
		"--default", "release",
	}, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("take exit = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "TAKE OK node=a by=1h\n"; got != want {
		t.Fatalf("take stdout = %q, want %q", got, want)
	}
	want := "take --session " + socket +
		" --as Rowan --node a --by 1h --default release"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("take request line = %q, want %q", got, want)
	}
}

// TestRelease: a node's lease is released, optionally handed to a named
// friend with --handed and a new --by / --default. The spec's first
// flag surface is --session <path> --as <name> --node <id>
// [--handed <name> --by <duration|stamp>
// --default <release|extend-once|escalate:<name>>]; the second, an
// allocation release, is the heartbeat's pair and is its own row.
func TestRelease(t *testing.T) {
	socket, requests := fakeSession(t, "RELEASE OK node=a")
	var stdout, stderr bytes.Buffer
	if code := run([]string{
		"release", "--session", socket,
		"--as", "Rowan",
		"--node", "a",
	}, &stdout, &stderr, ""); code != 0 {
		t.Fatalf("release exit = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "RELEASE OK node=a\n"; got != want {
		t.Fatalf("release stdout = %q, want %q", got, want)
	}
	want := "release --session " + socket + " --as Rowan --node a"
	if got := awaitRequest(t, requests); got != want {
		t.Fatalf("release request line = %q, want %q", got, want)
	}
}
