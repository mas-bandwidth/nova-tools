package main

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
)

// freshRun is one run on a fresh harness: a --file store under the test's own
// directory, a fake Redis of its own, an empty environment. No socket opens.
var freshRun = testkit.Main(func(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr, newHarness().deps())
}).Run

// A dry run is the real run's own plan: add, set, remove, migrate and apply
// refuse with --dry-run exactly where they do without, with the same exit and
// status words, and the dry run changes no byte of the --file store. apply
// --dry-run records nothing and still wants --as, because the real run does.
func TestADryRunRefusesWhereTheRealRunWould(t *testing.T) {
	t.Parallel()
	// store is a migrated --file store under root, with each line run on it first.
	store := func(t *testing.T, root string, lines ...[]string) string {
		file := filepath.Join(root, "try.json")
		r := freshRun("migrate", "--file", file)
		require.Equal(t, 0, r.Code, "setup migrate: %+v", r)
		for _, l := range lines {
			r := freshRun(append(l, "--as", "a1", "--file", file)...)
			require.Equal(t, 0, r.Code, "setup %v: %+v", l, r)
		}
		return file
	}
	m1 := []string{"machine", "add", "m1", "--user", "u", "--seat", "s", "--slots", "8", "--width", "4"}
	at := func(verb []string, lines [][]string, rest ...string) func(*testing.T, string) ([]string, []string) {
		return func(t *testing.T, root string) ([]string, []string) {
			return verb, append(rest, "--file", store(t, root, lines...))
		}
	}
	redis := []string{"--redis", "127.0.0.1:6379"}
	testkit.DryRunAgrees(t, freshRun, []testkit.DryCase{
		{Name: "apply without --as", Setup: at([]string{"apply"}, nil, append(redis, "--kind", "machine")...)},
		{Name: "apply without --as or --kind", Setup: at([]string{"apply"}, nil, redis...)},
		{Name: "apply with an unknown --kind and no --as", Setup: at([]string{"apply"}, nil, append(redis, "--kind", "nothing")...)},
		{Name: "apply with the fleet's endpoints unset", Setup: at([]string{"apply"}, nil, append(redis, "--as", "a1")...)},
		{Name: "apply one kind", Setup: at([]string{"apply"}, [][]string{m1}, append(redis, "--kind", "machine", "--as", "a1")...)},
		{Name: "apply on a store migrate has not made", Setup: func(t *testing.T, root string) ([]string, []string) {
			return []string{"apply"}, append(redis, "--kind", "machine", "--as", "a1", "--file", filepath.Join(root, "none.json"))
		}},
		{Name: "add without --as", Setup: at([]string{"machine", "add", "m1"}, nil, "--user", "u", "--seat", "s", "--slots", "8")},
		{Name: "add a name taken", Setup: at([]string{"machine", "add", "m1"}, [][]string{m1}, "--user", "u", "--seat", "s", "--slots", "8", "--as", "a1")},
		{Name: "add naming no row", Setup: at([]string{"loop", "add", "l1"}, nil, "--machine", "nope", "--argv", `["x"]`, "--as", "a1")},
		{Name: "add", Setup: at([]string{"machine", "add", "m2"}, [][]string{m1}, "--user", "u", "--seat", "s", "--slots", "8", "--as", "a1")},
		{Name: "set a row not there", Setup: at([]string{"machine", "set", "nope"}, nil, "--width", "2", "--as", "a1")},
		{Name: "set without --as", Setup: at([]string{"machine", "set", "m1"}, [][]string{m1}, "--width", "2")},
		{Name: "remove a row not there", Setup: at([]string{"machine", "remove", "nope"}, nil, "--as", "a1")},
		{Name: "remove a row another names", Setup: at([]string{"machine", "remove", "m1"}, [][]string{m1, {"fleet", "set", "--coordinator", "m1"}}, "--as", "a1")},
		{Name: "remove without --as", Setup: at([]string{"machine", "remove", "m1"}, [][]string{m1})},
		{Name: "migrate with an argument", Setup: at([]string{"migrate"}, nil, "again")},
		{Name: "migrate a file not made yet", Setup: func(t *testing.T, root string) ([]string, []string) {
			return []string{"migrate"}, []string{"--file", filepath.Join(root, "new.json")}
		}},
	})
}
