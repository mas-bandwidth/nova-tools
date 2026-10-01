package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reportEnv(t *testing.T, cardEnv string) (env, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()
	home := t.TempDir()
	if cardEnv != "" {
		dir := filepath.Join(home, "nova-bench", "launch")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "card.env"), []byte(cardEnv), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var out, errb bytes.Buffer
	return env{stdout: &out, stderr: &errb, getenv: func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}}, &out, &errb, home
}

var goodReceiptArgs = []string{"--repo", "o/r", "--sha", "abc", "--run-id", "5", "--pr", "", "--workflow", "ci", "--conclusion", "success"}

const goodCardEnv = `# the bench
NOVA_BENCH_SEAT=seat1
export NOVA_BENCH_SOPS="$HOME/nova-bench/sops.yaml"
NOVA_CARD_REDIS='10.0.0.2:6399'
`

func TestReportRunExecsNovaSecretsUnderTheBenchSeatWithThisTreesWriter(t *testing.T) {
	t.Parallel()
	e, _, _, home := reportEnv(t, goodCardEnv)
	r := &fakeCmdRunner{}
	if code := reportRun(e, r, goodReceiptArgs); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(r.calls) != 1 {
		t.Fatalf("commands %q", r.lines())
	}
	c := r.calls[0]
	if want := filepath.Join(home, ".local", "bin", "nova-secrets"); c.Name != want {
		t.Fatalf("ran %s, want %s", c.Name, want)
	}
	want := strings.Join([]string{
		"exec", "--store", filepath.Join(home, "nova-bench", "secrets"), "--as", "seat1",
		"--key", filepath.Join(home, ".config", "nova-secrets", "seat1.key"), "--sops", filepath.Join(home, "nova-bench", "sops.yaml"),
		"--only", "NOVA_REDIS_BENCH_PASSWORD", "--require", "NOVA_REDIS_BENCH_PASSWORD", "--",
		"/usr/bin/env", "NOVA_SPRINT_REDIS_USER=bench", "NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_BENCH_PASSWORD",
		"go", "run", "./cmd/nova-ci", "github", "receipt", "--from-runner", "--redis", "10.0.0.2:6399",
		"--repo", "o/r", "--sha", "abc", "--run-id", "5", "--pr", "", "--workflow", "ci", "--conclusion", "success",
	}, " ")
	if got := strings.Join(c.Args, " "); got != want {
		t.Fatalf("arguments\n got: %s\nwant: %s", got, want)
	}
}

// The law the workflow step used to carry: the writer is this tree's nova-ci,
// the one installed binary is the nova-secrets wrapper, and the password rides
// nova-secrets, never a flag.
func TestReportRunCarriesNoPasswordAndProbesNoInstalledWriter(t *testing.T) {
	t.Parallel()
	e, out, errb, _ := reportEnv(t, goodCardEnv)
	r := &fakeCmdRunner{}
	reportRun(e, r, goodReceiptArgs)
	all := strings.Join(r.calls[0].Args, " ") + out.String() + errb.String()
	for _, never := range []string{"--password", "nova-sprint", "command -v", "curl", "api.github.com"} {
		if strings.Contains(all, never) {
			t.Errorf("the command or its output names %q: %s", never, all)
		}
	}
	if strings.Count(strings.Join(r.calls[0].Args, " "), "/usr/bin/env") != 1 {
		t.Errorf("arguments %q", r.calls[0].Args)
	}
}

func TestReportRunWithNoCardEnvIsRefusedNamingTheFile(t *testing.T) {
	t.Parallel()
	e, _, errb, home := reportEnv(t, "")
	r := &fakeCmdRunner{}
	if code := reportRun(e, r, goodReceiptArgs); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if want := filepath.Join(home, "nova-bench", "launch", "card.env"); !strings.Contains(errb.String(), "no "+want+" on this runner") {
		t.Fatalf("stderr %q", errb)
	}
	if len(r.calls) != 0 {
		t.Fatalf("ran %q with no bench file", r.lines())
	}
}

func TestReportRunRefusesEachMissingName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"NOVA_BENCH_SEAT", "NOVA_BENCH_SOPS", "NOVA_CARD_REDIS"} {
		var kept []string
		for _, l := range strings.Split(goodCardEnv, "\n") {
			if !strings.Contains(l, name) {
				kept = append(kept, l)
			}
		}
		e, _, errb, _ := reportEnv(t, strings.Join(kept, "\n"))
		r := &fakeCmdRunner{}
		if code := reportRun(e, r, goodReceiptArgs); code != 1 || !strings.Contains(errb.String(), "card.env names no "+name) {
			t.Errorf("%s missing: exit %d stderr %q", name, code, errb)
		}
		if len(r.calls) != 0 {
			t.Errorf("%s missing: ran %q", name, r.lines())
		}
	}
}

func TestReportRunAnEmptyValueCountsAsNamingNothing(t *testing.T) {
	t.Parallel()
	e, _, errb, _ := reportEnv(t, "NOVA_BENCH_SEAT=\nNOVA_BENCH_SOPS=x\nNOVA_CARD_REDIS=y\n")
	if code := reportRun(e, &fakeCmdRunner{}, goodReceiptArgs); code != 1 || !strings.Contains(errb.String(), "names no NOVA_BENCH_SEAT") {
		t.Fatalf("exit %d stderr %q", code, errb)
	}
}

func TestReportRunPropagatesTheWritersExitCodeAndPrintsNoSecret(t *testing.T) {
	t.Parallel()
	e, out, errb, _ := reportEnv(t, goodCardEnv)
	r := &fakeCmdRunner{answer: func(cmdSpec) (string, int, error) { return "", 7, nil }}
	if code := reportRun(e, r, goodReceiptArgs); code != 7 {
		t.Fatalf("exit %d, want the writer's 7", code)
	}
	if out.Len() != 0 || errb.Len() != 0 {
		t.Fatalf("printed %q / %q", out, errb)
	}
}

func TestReportRunRefusesMissingOrUnknownFlags(t *testing.T) {
	t.Parallel()
	e, _, errb, _ := reportEnv(t, goodCardEnv)
	if code := reportRun(e, &fakeCmdRunner{}, goodReceiptArgs[:10]); code != 2 || !strings.Contains(errb.String(), "--conclusion is required") {
		t.Errorf("missing flag: exit %d stderr %q", code, errb)
	}
	if code := reportRun(e, &fakeCmdRunner{}, append([]string{"--job", "x"}, goodReceiptArgs...)); code != 2 {
		t.Errorf("unknown flag: exit %d", code)
	}
}

func TestReadCardEnvQuotingAndComments(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "card.env")
	body := "# c\n\nA=1\nexport B=\"two $X\"\nC='three $X'\nD=${X}/four\nnot a line\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readCardEnv(p, func(k string) string {
		if k == "X" {
			return "x"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "1", "B": "two x", "C": "three $X", "D": "x/four"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}
