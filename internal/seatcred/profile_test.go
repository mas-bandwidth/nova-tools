package seatcred_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred/seattest"
)

func TestProfilePathIsXDGThenHome(t *testing.T) {
	t.Parallel()

	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if p, err := seatcred.ProfilePath("nova-sprint", env(map[string]string{"XDG_CONFIG_HOME": "/x", "HOME": "/h"})); err != nil || p != "/x/nova-sprint/seats.tsv" {
		t.Fatalf("with XDG_CONFIG_HOME: %q %v", p, err)
	}
	if p, err := seatcred.ProfilePath("nova-sprint", env(map[string]string{"XDG_CONFIG_HOME": "rel", "HOME": "/h"})); err != nil || p != "/h/.config/nova-sprint/seats.tsv" {
		t.Fatalf("relative XDG_CONFIG_HOME is ignored per the XDG spec: %q %v", p, err)
	}
	if _, err := seatcred.ProfilePath("nova-sprint", env(nil)); err == nil || !strings.Contains(err.Error(), "seats.tsv") {
		t.Fatalf("no HOME: %v; want a refusal naming seats.tsv", err)
	}
}

// TestLoadProfileNamesTheFile is #4330's DONE-WHEN clause: a missing seat
// row names the file, and so does every other refusal about it.
func TestLoadProfileNamesTheFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "seats.tsv")
	if _, err := seatcred.LoadProfile(path, "coordinator", "/h"); !errors.Is(err, seatcred.ErrNoProfileRow) || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), seatcred.ProfileColumns) {
		t.Fatalf("missing file: %v; want ErrNoProfileRow naming %s and the columns", err, path)
	}
	good := "# fleet play\n\ncoordinator\tredis.invalid:6380\tcoordinator\tNOVA_REDIS_COORDINATOR_PASSWORD\t~/nova-bench/secrets\t~/.config/nova-secrets/studio.key\n" +
		"bench\tredis.invalid:6380\tbench\tNOVA_REDIS_BENCH_PASSWORD\t/s\t/k/air.key\r\n"
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := seatcred.LoadProfile(path, "coordinator", "/h")
	want := seatcred.Profile{Name: "coordinator", Addr: "redis.invalid:6380", User: "coordinator", SecretEnv: "NOVA_REDIS_COORDINATOR_PASSWORD", Store: "/h/nova-bench/secrets", Key: "/h/.config/nova-secrets/studio.key"}
	if err != nil || p != want || p.AsName() != "studio" {
		t.Fatalf("LoadProfile = %+v %v; want %+v as studio", p, err, want)
	}
	if p, err := seatcred.LoadProfile(path, "bench", "/h"); err != nil || p.Key != "/k/air.key" || p.AsName() != "air" {
		t.Fatalf("CRLF row: %+v %v", p, err)
	}
	if _, err := seatcred.LoadProfile(path, "ghost", "/h"); !errors.Is(err, seatcred.ErrNoProfileRow) || !strings.Contains(err.Error(), "seat ghost") || !strings.Contains(err.Error(), path) {
		t.Fatalf("missing row: %v; want ErrNoProfileRow naming the seat and %s", err, path)
	}

	for _, c := range []struct{ row, why string }{
		{"coordinator\tredis.invalid:6380\tcoordinator", "3 columns"},
		{"bad/name\th:1\tu\tK\t/s\t/k/a.key", "name"},
		{"c\tnoport\tu\tK\t/s\t/k/a.key", "redis addr"},
		{"c\th:1\t\tK\t/s\t/k/a.key", "redis user"},
		{"c\th:1\tu\tlower\t/s\t/k/a.key", "secret env"},
		{"c\th:1\tu\tK\trel/s\t/k/a.key", "store"},
		{"c\th:1\tu\tK\t/s\t/k/a.pem", "key"},
		{"c\th:1\tu\tK\t/s\t/k/a.key\tlower", "github token env"},
		{"c\th:1\tu\tK\t/s\t/k/a.key\tGH\textra", "8 columns"},
	} {
		if err := os.WriteFile(path, []byte("# x\n"+c.row+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := seatcred.LoadProfile(path, "anything", "/h")
		if err == nil || errors.Is(err, seatcred.ErrNoProfileRow) || !strings.Contains(err.Error(), path+":2") || !strings.Contains(err.Error(), c.why) {
			t.Fatalf("row %q: %v; want a refusal naming %s:2 and %q", c.row, err, path, c.why)
		}
	}
	dup := "c\th:1\tu\tK\t/s\t/k/a.key\nc\th:2\tu\tK\t/s\t/k/a.key\n"
	if err := os.WriteFile(path, []byte(dup), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := seatcred.LoadProfile(path, "c", "/h"); err == nil || !strings.Contains(err.Error(), path+":2") || !strings.Contains(err.Error(), "second row") {
		t.Fatalf("duplicate row: %v", err)
	}
}

// TestResolveProfileReadsTheRowsSecret opens a real store through the row:
// the row's user, with the password held under the row's secret env, from the
// seat file its key names -- nothing from the default layout.
func TestResolveProfileReadsTheRowsSecret(t *testing.T) {
	t.Parallel()

	const pw = "profile-test-pw-4330"
	home := seattest.Home(t, "studio", map[string]string{"NOVA_REDIS_COORDINATOR_PASSWORD": pw, "NOVA_REDIS_BENCH_PASSWORD": "not-this-one"})
	// Nothing from the environment: the row names the store and key, and
	// sops is the one on PATH.
	noenv := func(string) string { return "" }
	p := seatcred.Profile{
		Name: "coordinator", Addr: "h:1", User: "coordinator", SecretEnv: "NOVA_REDIS_COORDINATOR_PASSWORD",
		Store: filepath.Join(home, seatcred.DefaultStore), Key: filepath.Join(home, seatcred.DefaultKeyDir, "studio.key"),
	}
	c, err := seatcred.ResolveProfile(p, noenv)
	if err != nil || c.Seat != "coordinator" || c.User != "coordinator" || c.Key != p.SecretEnv || !same(c, pw) {
		t.Fatalf("ResolveProfile = %v %v; want coordinator with the coordinator password", c, err)
	}
	p.SecretEnv = "NOVA_REDIS_GHOST_PASSWORD"
	if _, err := seatcred.ResolveProfile(p, noenv); err == nil || !strings.Contains(err.Error(), "NOVA_REDIS_GHOST_PASSWORD") || !strings.Contains(err.Error(), "--as studio") || strings.Contains(err.Error(), pw) {
		t.Fatalf("absent secret env: %v; want a refusal naming the key and the seal remedy", err)
	}
	p.Key = filepath.Join(home, "nowhere.key")
	if _, err := seatcred.ResolveProfile(p, noenv); err == nil || !strings.Contains(err.Error(), "seat coordinator") {
		t.Fatalf("key for a seat the store lacks: %v", err)
	}
}

func TestSelectWithResolvesThroughTheGivenFunc(t *testing.T) {
	t.Parallel()

	var sel seatcred.Selection
	calls := 0
	sel.SelectWith("coordinator", "h:1", func(s string) (seatcred.Cred, error) {
		calls++
		return seatcred.Cred{Seat: s, User: "u"}, nil
	})
	for i := 0; i < 2; i++ {
		if c, ok, err := sel.Active(); !ok || err != nil || c.User != "u" {
			t.Fatalf("Active = %v %v %v", c, ok, err)
		}
	}
	if calls != 1 || sel.Addr() != "h:1" || sel.Selected() != "coordinator" {
		t.Fatalf("resolved %d times with addr %q seat %q, want once with h:1 as coordinator", calls, sel.Addr(), sel.Selected())
	}
	sel.Select("")
	if _, ok, _ := sel.Active(); ok || sel.Addr() != "" {
		t.Fatal("Select(\"\") left a seat or its address selected")
	}
}

// TestLoadProfileSeventhColumnIsTheGitHubTokenEnv is #4330's second gap: a
// row may name the key of the seat's file that holds its GitHub token; a
// six-column row is still a row, with no token env.
func TestLoadProfileSeventhColumnIsTheGitHubTokenEnv(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "seats.tsv")
	body := "coordinator\th:1\tcoordinator\tNOVA_REDIS_COORDINATOR_PASSWORD\t/s\t/k/studio.key\tGH_GATE_TOKEN\n" +
		"bench\th:1\tbench\tNOVA_REDIS_BENCH_PASSWORD\t/s\t/k/air.key\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, err := seatcred.LoadProfile(path, "coordinator", "/h"); err != nil || p.GitHubEnv != "GH_GATE_TOKEN" {
		t.Fatalf("seven columns: %+v %v; want GitHubEnv GH_GATE_TOKEN", p, err)
	}
	if p, err := seatcred.LoadProfile(path, "bench", "/h"); err != nil || p.GitHubEnv != "" {
		t.Fatalf("six columns: %+v %v; want no GitHubEnv", p, err)
	}
}

// TestResolveProfileReadsTheGitHubToken: the row's seventh column names the
// key the token is read from, in the same open of the seat's file; a file
// without it is a refusal kept for the GitHub verb that asks, not for the
// Redis login.
func TestResolveProfileReadsTheGitHubToken(t *testing.T) {
	t.Parallel()

	const pw, tok = "profile-gh-test-pw-4330", "profile-gh-test-token-4330"
	home := seattest.Home(t, "studio", map[string]string{"NOVA_REDIS_COORDINATOR_PASSWORD": pw, "GH_GATE_TOKEN": tok})
	noenv := func(string) string { return "" }
	p := seatcred.Profile{
		Name: "coordinator", Addr: "h:1", User: "coordinator", SecretEnv: "NOVA_REDIS_COORDINATOR_PASSWORD",
		Store: filepath.Join(home, seatcred.DefaultStore), Key: filepath.Join(home, seatcred.DefaultKeyDir, "studio.key"),
		GitHubEnv: "GH_GATE_TOKEN",
	}
	c, err := seatcred.ResolveProfile(p, noenv)
	if err != nil || c.GitHubErr != nil || c.GitHubKey != "GH_GATE_TOKEN" {
		t.Fatalf("ResolveProfile = %v %v %v", c, err, c.GitHubErr)
	}
	got := ""
	if err := c.GitHub.Use(func(v string) error { got = v; return nil }); err != nil || got != tok {
		t.Fatal("the GitHub token is not the one sealed under GH_GATE_TOKEN")
	}
	p.GitHubEnv = "GH_GHOST_TOKEN"
	c, err = seatcred.ResolveProfile(p, noenv)
	if err != nil || c.GitHubErr == nil || !strings.Contains(c.GitHubErr.Error(), "GH_GHOST_TOKEN") || !strings.Contains(c.GitHubErr.Error(), "--as studio") || strings.Contains(c.GitHubErr.Error(), tok) {
		t.Fatalf("absent token env: login err %v, GitHubErr %v; want the login and a GitHub refusal naming the key and the seal remedy", err, c.GitHubErr)
	}
}
