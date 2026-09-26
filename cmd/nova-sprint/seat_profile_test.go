package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
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
	t.Cleanup(func() { seatcred.Select("") })
	dir := t.TempDir()
	store := filepath.Join(dir, "no-store")
	writeSeats(t, dir, "coordinator\t10.1.2.3:6380\tcoordinator\tNOVA_REDIS_COORDINATOR_PASSWORD\t"+store+"\t/k/studio.key\n")

	for _, c := range []struct {
		args []string
		env  map[string]string
		seat string
	}{
		{args: []string{"--seat", "coordinator", "table"}, env: map[string]string{SprintSeatEnv: "x", seatcred.SeatEnv: "y"}, seat: "coordinator"},
		{args: []string{"table"}, env: map[string]string{SprintSeatEnv: "coordinator", seatcred.SeatEnv: "y"}, seat: "coordinator"},
		{args: []string{"table"}, env: map[string]string{seatcred.SeatEnv: "coordinator"}, seat: "coordinator"},
	} {
		set := map[string]string{}
		rest, err := selectSeat(c.args, profileEnv(dir, c.env), func(k, v string) error { set[k] = v; return nil })
		if err != nil || len(rest) != 1 || rest[0] != "table" || seatcred.Selected() != c.seat {
			t.Fatalf("%v %v: rest %v seat %q err %v", c.args, c.env, rest, seatcred.Selected(), err)
		}
		for _, k := range seatAddrEnvs {
			if set[k] != "10.1.2.3:6380" {
				t.Fatalf("%v: %s = %q; want the row's address", c.args, k, set[k])
			}
		}
		// The row's store is what Active opens: its refusal names it.
		if _, ok, err := seatcred.Active(); !ok || err == nil || !strings.Contains(err.Error(), "seat coordinator") || !strings.Contains(err.Error(), store) {
			t.Fatalf("%v: Active err %v; want the row's store named", c.args, err)
		}
	}

	set := map[string]string{}
	if _, err := selectSeat([]string{"table"}, profileEnv(dir, nil), func(k, v string) error { set[k] = v; return nil }); err != nil || seatcred.Selected() != "" || len(set) != 0 {
		t.Fatalf("no seat: selected %q set %v err %v; want nothing selected or set", seatcred.Selected(), set, err)
	}
}

// TestSelectSeatMissingRowNamesTheFile is #4330's DONE-WHEN: a seat with no
// row, which is not a nova-secrets seat either, is refused naming seats.tsv.
func TestSelectSeatMissingRowNamesTheFile(t *testing.T) {
	t.Cleanup(func() { seatcred.Select("") })
	dir := t.TempDir()
	path := writeSeats(t, dir, "bench\th:1\tbench\tNOVA_REDIS_BENCH_PASSWORD\t/s\t/k/air.key\n")
	set := map[string]string{}
	if _, err := selectSeat([]string{"--seat", "coordinator", "table"}, profileEnv(dir, nil), func(k, v string) error { set[k] = v; return nil }); err != nil {
		t.Fatalf("selectSeat: %v; a missing row is refused at first use, when the #4052 seat does not open either", err)
	}
	if len(set) != 0 {
		t.Fatalf("a missing row set %v", set)
	}
	_, ok, err := seatcred.Active()
	if !ok || err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "no row") || !strings.Contains(err.Error(), "as a nova-secrets seat") {
		t.Fatalf("Active err %v; want a refusal naming %s and the nova-secrets seat", err, path)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	selectSeat([]string{"--seat", "coordinator"}, profileEnv(dir, nil), func(string, string) error { return nil })
	if _, _, err := seatcred.Active(); err == nil || !strings.Contains(err.Error(), path+" does not exist") {
		t.Fatalf("no file: %v; want a refusal naming %s", err, path)
	}
}

// TestRunRefusesAMalformedProfile: a bad row is refused before any verb
// runs, printed with file:line.
func TestRunRefusesAMalformedProfile(t *testing.T) {
	t.Cleanup(func() { seatcred.Select("") })
	dir := t.TempDir()
	path := writeSeats(t, dir, "coordinator\t10.1.2.3\n")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	t.Setenv(SprintSeatEnv, "")
	t.Setenv(seatcred.SeatEnv, "")
	var out, errOut bytes.Buffer
	if code := run([]string{"--seat", "coordinator", "version"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), path+":1") {
		t.Fatalf("exit %d stderr %q; want 2 and %s:1 named", code, errOut.String(), path)
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
	t.Cleanup(func() { seatcred.Select("") })
	for _, k := range append([]string{SprintSeatEnv, seatcred.SeatEnv}, seatAddrEnvs...) {
		t.Setenv(k, "")
	}
	for _, c := range []struct {
		args []string
		why  string
	}{
		{[]string{"redis", "--redis", "127.0.0.1:1"}, "no command"},
		{[]string{"redis", "PING"}, "no address"},
	} {
		var out, errOut bytes.Buffer
		if code := run(c.args, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), c.why) {
			t.Fatalf("%v: exit %d stderr %q; want 2 and %q", c.args, code, errOut.String(), c.why)
		}
	}
}
