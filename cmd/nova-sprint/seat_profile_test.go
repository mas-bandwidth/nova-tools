package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/secrets"
)

// profileEnv is a fake environment: a HOME and XDG_CONFIG_HOME under dir, sops
// pointed at a path that is never run, and vars on top.
func profileEnv(dir string, vars map[string]string) func(string) string {
	return func(k string) string {
		if v, ok := vars[k]; ok {
			return v
		}
		switch k {
		case "HOME":
			return filepath.Join(dir, "home")
		case "XDG_CONFIG_HOME":
			return filepath.Join(dir, "xdg")
		case seatcred.SopsEnv:
			return filepath.Join(dir, "no-sops")
		}
		return ""
	}
}

func writeSeats(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "xdg", "nova-sprint", "seats.tsv")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSelectSeatReadsTheRow is #4330: --seat (or NOVA_SPRINT_SEAT, then
// NOVA_SEAT) names a seats.tsv row, whose address becomes every verb's
// --redis default and whose store and key are what the secret is read from.
func TestSelectSeatReadsTheRow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := filepath.Join(dir, "no-store")
	writeSeats(t, dir, "coordinator\tredis.invalid:6380\tcoordinator\tNOVA_REDIS_COORDINATOR_PASSWORD\t"+store+"\t/k/studio.key\n")

	for _, c := range []struct {
		args []string
		env  map[string]string
	}{
		{args: []string{"--seat", "coordinator", "table"}, env: map[string]string{SprintSeatEnv: "x", seatcred.SeatEnv: "y"}},
		{args: []string{"table"}, env: map[string]string{SprintSeatEnv: "coordinator", seatcred.SeatEnv: "y"}},
		{args: []string{"table"}, env: map[string]string{seatcred.SeatEnv: "coordinator"}},
	} {
		var sel seatcred.Selection
		set := map[string]string{}
		rest, err := selectSeat(&sel, c.args, profileEnv(dir, c.env), func(k, v string) error { set[k] = v; return nil })
		if err != nil || len(rest) != 1 || rest[0] != "table" || sel.Selected() != "coordinator" || sel.Addr() != "redis.invalid:6380" {
			t.Fatalf("%v %v: rest %v seat %q addr %q err %v", c.args, c.env, rest, sel.Selected(), sel.Addr(), err)
		}
		for _, k := range seatAddrEnvs {
			if set[k] != "redis.invalid:6380" {
				t.Fatalf("%v: %s = %q; want the row's address", c.args, k, set[k])
			}
		}
		// The row's store is what Active opens: its refusal names it.
		if _, ok, err := sel.Active(); !ok || err == nil || !strings.Contains(err.Error(), "seat coordinator") || !strings.Contains(err.Error(), store) {
			t.Fatalf("%v: Active err %v; want the row's store named", c.args, err)
		}
	}

	var sel seatcred.Selection
	set := map[string]string{}
	if _, err := selectSeat(&sel, []string{"table"}, profileEnv(dir, nil), func(k, v string) error { set[k] = v; return nil }); err != nil || sel.Selected() != "" || len(set) != 0 {
		t.Fatalf("no seat: selected %q set %v err %v; want nothing selected or set", sel.Selected(), set, err)
	}
}

// TestSelectSeatMissingRowNamesTheFile is #4330's DONE-WHEN: a seat with no
// row, which is not a nova-secrets seat either, is refused naming seats.tsv.
func TestSelectSeatMissingRowNamesTheFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeSeats(t, dir, "bench\th:1\tbench\tNOVA_REDIS_BENCH_PASSWORD\t/s\t/k/air.key\n")
	var sel seatcred.Selection
	set := map[string]string{}
	if _, err := selectSeat(&sel, []string{"--seat", "coordinator", "table"}, profileEnv(dir, nil), func(k, v string) error { set[k] = v; return nil }); err != nil {
		t.Fatalf("selectSeat: %v; a missing row is refused at first use, when the #4052 seat does not open either", err)
	}
	if len(set) != 0 || sel.Addr() != "" {
		t.Fatalf("a missing row set %v addr %q", set, sel.Addr())
	}
	_, ok, err := sel.Active()
	if !ok || err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "no row") || !strings.Contains(err.Error(), "as a nova-secrets seat") {
		t.Fatalf("Active err %v; want a refusal naming %s and the nova-secrets seat", err, path)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := selectSeat(&sel, []string{"--seat", "coordinator"}, profileEnv(dir, nil), func(string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sel.Active(); err == nil || !strings.Contains(err.Error(), path+" does not exist") {
		t.Fatalf("no file: %v; want a refusal naming %s", err, path)
	}
}

// TestSelectSeatRefusesAMalformedProfile: a bad row is refused before any
// verb runs, with file:line.
func TestSelectSeatRefusesAMalformedProfile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := writeSeats(t, dir, "coordinator\tredis.invalid\n")
	var sel seatcred.Selection
	if _, err := selectSeat(&sel, []string{"--seat", "coordinator", "version"}, profileEnv(dir, nil), func(string, string) error { return nil }); err == nil || !strings.Contains(err.Error(), path+":1") {
		t.Fatalf("err %v; want a refusal naming %s:1", err, path)
	}
}

func TestRedisRawPrintsLikeRedisCLIToAPipe(t *testing.T) {
	t.Parallel()

	var b bytes.Buffer
	printRedisReply(&b, []any{"a", int64(3), nil, []any{"n1", "n2"}, map[any]any{"z": "1", "b": "2"}, 1.5, true})
	if want := "a\n3\n\nn1\nn2\nb\n2\nz\n1\n1.5\n1\n"; b.String() != want {
		t.Fatalf("got %q want %q", b.String(), want)
	}
}

func TestRedisRawRefusesWithoutCommandOrAddress(t *testing.T) {
	t.Parallel()

	noenv := func(string) string { return "" }
	for _, c := range []struct {
		args []string
		why  string
	}{
		{[]string{"--redis", "127.0.0.1:1"}, "no command"},
		{[]string{"PING"}, "no address"},
	} {
		var sel seatcred.Selection
		var out, errOut bytes.Buffer
		if code := redisRaw(context.Background(), &sel, noenv, c.args, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), c.why) {
			t.Fatalf("%v: exit %d stderr %q; want 2 and %q", c.args, code, errOut.String(), c.why)
		}
	}
	var sel seatcred.Selection
	sel.SelectWith("coordinator", "", func(string) (seatcred.Cred, error) { return seatcred.Cred{}, os.ErrNotExist })
	var out, errOut bytes.Buffer
	if code := redisRaw(context.Background(), &sel, noenv, []string{"--redis", "127.0.0.1:1", "PING"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "file does not exist") {
		t.Fatalf("unreadable seat: exit %d stderr %q; want 2 and the seat's refusal printed", code, errOut.String())
	}
}

// TestRedisDefaultIsEnvThenSeat is #4330's first gap: a verb given no --redis
// under a seat dials the seat's address; the environment's address (the one
// order, seatAddrEnvs) still comes first, and with no seat and no environment
// it is "".
func TestRedisDefaultIsEnvThenSeat(t *testing.T) {
	t.Parallel()

	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	var none, sel seatcred.Selection
	sel.SelectWith("coordinator", "seat.invalid:6380", nil)
	for _, c := range []struct {
		sel  *seatcred.Selection
		env  map[string]string
		envs []string
		want string
	}{
		{&none, nil, nil, ""},
		{&sel, nil, nil, "seat.invalid:6380"},
		// the one resolver (#4352 A): every verb reads the same variables in
		// the same order, with no list of its own
		{&sel, map[string]string{"NOVA_SPRINT_REDIS": "env.invalid:1"}, nil, "env.invalid:1"},
		{&none, map[string]string{"NOVA_REDIS_ADDR": "env.invalid:2"}, nil, "env.invalid:2"},
		{&none, map[string]string{"NOVA_REDIS": "env.invalid:3"}, nil, "env.invalid:3"},
		{&none, map[string]string{"NOVA_REDIS_ADDR": "env.invalid:2", "NOVA_SPRINT_REDIS": "env.invalid:1"}, nil, "env.invalid:1"},
		// a verb's own variable (card run's NOVA_CARD_REDIS) comes first
		{&none, map[string]string{"NOVA_CARD_REDIS": "card.invalid:4", "NOVA_SPRINT_REDIS": "env.invalid:1"}, []string{"NOVA_CARD_REDIS"}, "card.invalid:4"},
	} {
		if got := redisDefaultFrom(c.sel, env(c.env), c.envs...); got != c.want {
			t.Fatalf("seat %q env %v envs %v: %q, want %q", c.sel.Selected(), c.env, c.envs, got, c.want)
		}
	}
}

// TestSelectSeatCarriesTheGitHubColumn: a seven-column row's token env is the
// selection's, known without decrypting anything.
func TestSelectSeatCarriesTheGitHubColumn(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeSeats(t, dir, "coordinator\th:1\tcoordinator\tNOVA_REDIS_COORDINATOR_PASSWORD\t/s\t/k/studio.key\tGH_GATE_TOKEN\n"+
		"bench\th:1\tbench\tNOVA_REDIS_BENCH_PASSWORD\t/s\t/k/air.key\n")
	for seat, want := range map[string]string{"coordinator": "GH_GATE_TOKEN", "bench": ""} {
		var sel seatcred.Selection
		if _, err := selectSeat(&sel, []string{"--seat", seat, "land"}, profileEnv(dir, nil), func(string, string) error { return nil }); err != nil || sel.GitHubEnv() != want {
			t.Fatalf("seat %s: GitHubEnv %q err %v; want %q", seat, sel.GitHubEnv(), err, want)
		}
	}
}

// TestSeatGitHubTokenReadsTheSeatOrSaysOnce is #4330's second gap at the
// verb seam: a row naming a token env hands the GitHub verbs the seat's
// token; a row without one is the old behaviour, said once per process; no
// seat says nothing.
func TestSeatGitHubTokenReadsTheSeatOrSaysOnce(t *testing.T) {
	t.Parallel()

	const tok = "seat-gh-unit-token-4330"
	var note bytes.Buffer
	var once sync.Once

	var none seatcred.Selection
	if got, ok, err := seatGitHubToken(&none, &note, &once); got != "" || ok || err != nil || note.Len() != 0 {
		t.Fatalf("no seat: %v %v; note %q", ok, err, note.String())
	}

	var bare seatcred.Selection
	bare.SelectWith("bench", "h:1", func(string) (seatcred.Cred, error) {
		t.Fatal("decrypted for a row with no token column")
		return seatcred.Cred{}, nil
	})
	for i := 0; i < 2; i++ {
		if got, ok, err := seatGitHubToken(&bare, &note, &once); got != "" || ok || err != nil {
			t.Fatalf("no column: %v %v", ok, err)
		}
	}
	if n := strings.Count(note.String(), "\n"); n != 1 || !strings.Contains(note.String(), "seat bench") || !strings.Contains(note.String(), "GH_TOKEN") {
		t.Fatalf("no column: note %q; want one line naming the seat and GH_TOKEN", note.String())
	}

	var sel seatcred.Selection
	sel.SelectProfile(seatcred.Profile{Name: "coordinator", Addr: "h:1", GitHubEnv: "GH_GATE_TOKEN"}, func(s string) (seatcred.Cred, error) {
		return seatcred.Cred{Seat: s, GitHubKey: "GH_GATE_TOKEN", GitHub: secrets.NewSecret(tok)}, nil
	})
	if got, ok, err := seatGitHubToken(&sel, &note, &once); got != tok || !ok || err != nil {
		t.Fatalf("column: ok %v err %v; want the seat's token", ok, err)
	}

	var missing seatcred.Selection
	missing.SelectProfile(seatcred.Profile{Name: "coordinator", Addr: "h:1", GitHubEnv: "GH_GATE_TOKEN"}, func(s string) (seatcred.Cred, error) {
		return seatcred.Cred{Seat: s, GitHubKey: "GH_GATE_TOKEN", GitHubErr: errors.New("seat coordinator: f.yaml holds no GH_GATE_TOKEN")}, nil
	})
	if _, ok, err := seatGitHubToken(&missing, &note, &once); !ok || err == nil || !strings.Contains(err.Error(), "GH_GATE_TOKEN") {
		t.Fatalf("column, file without it: ok %v err %v; want the refusal", ok, err)
	}
}
