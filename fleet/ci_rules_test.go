package main

// Unit tests of the CI-rules hooks every runner carries (roles/github-runner/files/ci-rules, README "CI rules").
// They run the scripts' pure verbs with fixed inputs: no clock (time is an argument), no sleep, no network, and never
// the started path of a limited event, which would look for a real Runner.Worker.

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const ciRulesDir = "roles/github-runner/files/ci-rules"

type ciShell struct {
	name string
	argv func(script string, args ...string) []string
	ext  string
}

// ciShells is sh always, and pwsh (or powershell) where this machine has one.
func ciShells(t *testing.T) []ciShell {
	t.Helper()
	sh := ciShell{"sh", func(s string, a ...string) []string { return append([]string{"sh", s}, a...) }, ".sh"}
	out := []ciShell{sh}
	for _, ps := range []string{"pwsh", "powershell"} {
		if p, err := exec.LookPath(ps); err == nil {
			out = append(out, ciShell{ps, func(s string, a ...string) []string {
				return append([]string{p, "-NoProfile", "-NonInteractive", "-File", s}, a...)
			}, ".ps1"})
			break
		}
	}
	return out
}

// ciRun runs a verb of the script in dir with stdin and env added; it returns the exit code and stdout.
func ciRun(t *testing.T, sh ciShell, dir, stdin string, env []string, args ...string) (int, string) {
	t.Helper()
	argv := sh.argv(filepath.Join(dir, "nova-ci-rules"+sh.ext), args...)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), string(out)
	} else if err != nil {
		t.Fatalf("%s %v: %v", sh.name, args, err)
	}
	return 0, string(out)
}

// ciCopy copies the hooks into a fresh directory, as apply does into <runner dir>/.nova-ci-rules.
func ciCopy(t *testing.T, limit string) string {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir(ciRulesDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(ciRulesDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if limit != "" {
		if err := os.WriteFile(filepath.Join(dir, "limit"), []byte(limit+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCIRulesFilesAreEmbedded(t *testing.T) {
	for _, f := range []string{"nova-ci-rules.sh", "job-started.sh", "job-completed.sh", "nova-ci-rules.ps1", "job-started.ps1", "job-completed.ps1"} {
		if _, err := os.Stat(filepath.Join(ciRulesDir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
		if !hasFile(t, ciRulesDir+"/"+f) {
			t.Errorf("%s is not in the embedded tree", f)
		}
	}
}

func hasFile(t *testing.T, p string) bool {
	t.Helper()
	return slices.Contains(files(tree), p)
}

func TestCIRulesPushScope(t *testing.T) {
	for _, sh := range ciShells(t) {
		for ref, want := range map[string]int{
			"refs/heads/main":                     0,
			"refs/heads/sprint/v2":                0,
			"refs/tags/v1.2.3":                    0,
			"refs/heads/sprint/v2-review/card-12": 1,
			"refs/heads/sprint/":                  1,
			"refs/heads/feature":                  1,
			"refs/heads/mainline":                 1,
			"refs/heads/Main":                     1,
			"refs/tags/":                          1,
			"refs/pull/12/merge":                  1,
		} {
			if got, _ := ciRun(t, sh, ciRulesDir, "", nil, "push-ok", ref); got != want {
				t.Errorf("%s push-ok %s = %d, want %d", sh.name, ref, got, want)
			}
		}
	}
}

func TestCIRulesLimitedEvents(t *testing.T) {
	for _, sh := range ciShells(t) {
		for ev, want := range map[string]int{"push": 0, "pull_request": 0, "merge_group": 0, "schedule": 1, "workflow_dispatch": 1, "": 1} {
			if got, _ := ciRun(t, sh, ciRulesDir, "", nil, "limited", ev); got != want {
				t.Errorf("%s limited %q = %d, want %d", sh.name, ev, got, want)
			}
		}
	}
}

func TestCIRulesOverdueAndText(t *testing.T) {
	for _, sh := range ciShells(t) {
		for _, tc := range []struct {
			now, deadline string
			want          int
		}{{"1000", "1120", 1}, {"1119", "1120", 1}, {"1120", "1120", 0}, {"5000", "1120", 0}} {
			if got, _ := ciRun(t, sh, ciRulesDir, "", nil, "overdue", tc.now, tc.deadline); got != tc.want {
				t.Errorf("%s overdue %s %s = %d, want %d", sh.name, tc.now, tc.deadline, got, tc.want)
			}
		}
		for s, want := range map[string]string{"120": "2m", "60": "1m", "90": "90s"} {
			if _, out := ciRun(t, sh, ciRulesDir, "", nil, "limit-text", s); strings.TrimSpace(out) != want {
				t.Errorf("%s limit-text %s = %q, want %q", sh.name, s, out, want)
			}
		}
	}
}

func TestCIRulesFindsTheWorker(t *testing.T) {
	ps := "1 0 init\n100 1 /r/bin/Runner.Listener\n200 100 /r/bin/Runner.Worker\n300 200 bash\n400 300 sh\n500 1 Runner.Worker\n"
	for _, sh := range ciShells(t) {
		if code, out := ciRun(t, sh, ciRulesDir, ps, nil, "ancestor", "400", "Runner.Worker"); code != 0 || strings.TrimSpace(out) != "200" {
			t.Errorf("%s ancestor 400 = %d %q, want 200", sh.name, code, out)
		}
		if code, out := ciRun(t, sh, ciRulesDir, "1 0 init\n10 1 bash\n11 10 sh\n", nil, "ancestor", "11", "Runner.Worker"); code == 0 {
			t.Errorf("%s ancestor with no worker = %q, want a refusal", sh.name, out)
		}
	}
}

// The kill list is everything under the worker but the hooks themselves and what they run.
func TestCIRulesTreeSparesTheHooks(t *testing.T) {
	ps := strings.Join([]string{
		"200 100 /r/bin/Runner.Worker spawnclient",
		"300 200 bash -e /r/_work/_temp/step.sh",
		"301 300 go test ./...",
		"302 301 /tmp/go-build/pkg.test",
		"310 200 bash -e /r/.nova-ci-rules/job-completed.sh",
		"311 310 ps -A",
		"999 1 sshd",
		"998 999 bash",
	}, "\n") + "\n"
	for _, sh := range ciShells(t) {
		_, out := ciRun(t, sh, ciRulesDir, ps, nil, "tree", "200", "/r/.nova-ci-rules")
		if got := strings.Join(strings.Fields(out), " "); got != "300 301 302" {
			t.Errorf("%s tree = %q, want 300 301 302", sh.name, got)
		}
	}
}

func TestCIRulesEnvEdit(t *testing.T) {
	sh := ciShells(t)[0]
	for _, tc := range []struct{ name, in, want string }{
		{"empty", "", "ACTIONS_RUNNER_HOOK_JOB_STARTED=/h/job-started.sh\nACTIONS_RUNNER_HOOK_JOB_COMPLETED=/h/job-completed.sh\n"},
		{"keeps the rest", "LANG=C.UTF-8\nJAVA_HOME=/j\n",
			"LANG=C.UTF-8\nJAVA_HOME=/j\nACTIONS_RUNNER_HOOK_JOB_STARTED=/h/job-started.sh\nACTIONS_RUNNER_HOOK_JOB_COMPLETED=/h/job-completed.sh\n"},
		{"chains a hook that was there", "LANG=C\nACTIONS_RUNNER_HOOK_JOB_STARTED=/x/hold-start.sh\nACTIONS_RUNNER_HOOK_JOB_COMPLETED=/x/hold-stop.sh\n",
			"LANG=C\nNOVA_CI_CHAIN_JOB_COMPLETED=/x/hold-stop.sh\nNOVA_CI_CHAIN_JOB_STARTED=/x/hold-start.sh\n" +
				"ACTIONS_RUNNER_HOOK_JOB_STARTED=/h/job-started.sh\nACTIONS_RUNNER_HOOK_JOB_COMPLETED=/h/job-completed.sh\n"},
	} {
		_, got := ciRun(t, sh, ciRulesDir, tc.in, nil, "env-edit", "/h")
		if sortLines(got) != sortLines(tc.want) {
			t.Errorf("env-edit %s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
		// a second pass changes nothing: our own hooks are never chained to themselves
		if _, again := ciRun(t, sh, ciRulesDir, got, nil, "env-edit", "/h"); sortLines(again) != sortLines(got) {
			t.Errorf("env-edit %s is not idempotent:\n once %q\ntwice %q", tc.name, got, again)
		}
	}
}

func sortLines(s string) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	slices.Sort(l)
	return strings.Join(l, "\n")
}

func TestCIRulesStartedRefusesAPushOutsideTheScope(t *testing.T) {
	for _, sh := range ciShells(t) {
		dir := ciCopy(t, "")
		code, out := ciRun(t, sh, dir, "", []string{"GITHUB_EVENT_NAME=push", "GITHUB_REF=refs/heads/sprint/v2-review/c1", "NOVA_CI_CHAIN_JOB_STARTED="}, "started")
		if code == 0 || !strings.Contains(out, "::error::CI rule: push CI runs only on main, sprint/<stream> and tags, not refs/heads/sprint/v2-review/c1") {
			t.Errorf("%s started on a card branch push = %d %q, want a refusal naming the ref", sh.name, code, out)
		}
	}
}

func TestCIRulesStartedLeavesNightliesUnlimited(t *testing.T) {
	for _, sh := range ciShells(t) {
		for _, ev := range []string{"schedule", "workflow_dispatch"} {
			dir := ciCopy(t, "")
			code, out := ciRun(t, sh, dir, "", []string{"GITHUB_EVENT_NAME=" + ev, "GITHUB_REF=refs/heads/feature", "NOVA_CI_CHAIN_JOB_STARTED="}, "started")
			if code != 0 || !strings.Contains(out, "has no time limit") {
				t.Errorf("%s started on %s = %d %q, want no limit", sh.name, ev, code, out)
			}
			if _, err := os.Stat(filepath.Join(dir, "state", "watchdog.pid")); err == nil {
				t.Errorf("%s started on %s armed a watchdog", sh.name, ev)
			}
		}
	}
}

func TestCIRulesChainedHookFailureFailsTheJob(t *testing.T) {
	sh := ciShells(t)[0]
	dir := ciCopy(t, "")
	hook := filepath.Join(t.TempDir(), "hold.sh")
	if err := os.WriteFile(hook, []byte("echo held; exit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out := ciRun(t, sh, dir, "", []string{"GITHUB_EVENT_NAME=schedule", "GITHUB_REF=refs/heads/main", "NOVA_CI_CHAIN_JOB_STARTED=" + hook}, "started")
	if code != 3 || !strings.Contains(out, "held") {
		t.Errorf("started with a failing chained hook = %d %q, want 3 and its output", code, out)
	}
}

// A job the watchdog stopped fails at completion with the rule's line; the limit text follows the limit file.
func TestCIRulesCompletedFailsAStoppedJob(t *testing.T) {
	for _, sh := range ciShells(t) {
		for limit, text := range map[string]string{"": "2m", "90": "90s"} {
			dir := ciCopy(t, limit)
			env := []string{"NOVA_CI_CHAIN_JOB_COMPLETED="}
			if code, out := ciRun(t, sh, dir, "", env, "completed"); code != 0 || strings.Contains(out, "::error::") {
				t.Errorf("%s completed with no watchdog fired = %d %q, want 0", sh.name, code, out)
			}
			if err := os.MkdirAll(filepath.Join(dir, "state"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "state", "fired"), []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			code, out := ciRun(t, sh, dir, "", env, "completed")
			want := "::error::CI rule: per-commit jobs finish in " + text + "; move longer work to a nightly (schedule) workflow"
			if code == 0 || !strings.Contains(out, want) {
				t.Errorf("%s completed after the watchdog fired = %d %q, want a failure with %q", sh.name, code, out, want)
			}
			if _, err := os.Stat(filepath.Join(dir, "state", "fired")); err == nil {
				t.Errorf("%s completed left the fired marker for the next job", sh.name)
			}
		}
	}
}
