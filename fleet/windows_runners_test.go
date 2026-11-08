package main

import (
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Unit tests of the Windows side of a WSL machine (roles/github-runner/tasks/wsl-windows*.yml, files/runner-windows.ps1,
// the windows-runners play; README "Windows runners of a WSL machine") and of the CI rules' unchain rule. They read the
// embedded YAML and scripts as data: nothing is run.

const roleDir = "roles/github-runner"

// commandModules are the modules that start a program on the machine or the control node.
var commandModules = []string{"ansible.builtin.command", "ansible.builtin.shell", "ansible.builtin.raw", "ansible.builtin.script",
	"command", "shell", "raw", "script", "ansible.windows.win_shell", "ansible.windows.win_command"}

// roleTasks is every task of the role's task files, block, rescue and always flattened, with its file.
func roleTasks(t *testing.T) map[string][]map[string]any {
	t.Helper()
	out := map[string][]map[string]any{}
	entries, err := fs.ReadDir(tree, roleDir+"/tasks")
	if err != nil {
		t.Fatal(err)
	}
	var flat func(file string, tasks []map[string]any)
	flat = func(file string, tasks []map[string]any) {
		for _, task := range tasks {
			out[file] = append(out[file], task)
			for _, k := range []string{"block", "rescue", "always"} {
				if inner, ok := task[k].([]any); ok {
					var ts []map[string]any
					for _, i := range inner {
						ts = append(ts, i.(map[string]any))
					}
					flat(file, ts)
				}
			}
		}
	}
	for _, e := range entries {
		b, err := fs.ReadFile(tree, roleDir+"/tasks/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		var tasks []map[string]any
		if err := yaml.Unmarshal(b, &tasks); err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		flat(e.Name(), tasks)
	}
	return out
}

func text(t *testing.T, v any) string {
	t.Helper()
	b, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestRunnerTokenNeverOnACommandLine: in every task file of the role, a task that runs a program names the token only
// as its stdin (args.stdin): never in its command line, its environment or a file, and such a task is no_log.
func TestRunnerTokenNeverOnACommandLine(t *testing.T) {
	t.Parallel()
	carriers := 0
	for file, tasks := range roleTasks(t) {
		for _, task := range tasks {
			name, _ := task["name"].(string)
			for _, m := range []string{"ansible.builtin.copy", "copy", "ansible.windows.win_copy", "ansible.builtin.template", "ansible.builtin.lineinfile"} {
				if task[m] != nil && strings.Contains(text(t, task[m]), "github_runner_token") {
					t.Errorf("%s %q writes the token into a file", file, name)
				}
			}
			if strings.Contains(text(t, task["environment"]), "github_runner_token") {
				t.Errorf("%s %q puts the token in an environment", file, name)
			}
			for _, m := range commandModules {
				if task[m] == nil {
					continue
				}
				args, _ := task["args"].(map[string]any)
				stdin := strings.Contains(text(t, args["stdin"]), "github_runner_token")
				rest := map[string]any{}
				for k, v := range args {
					if k != "stdin" {
						rest[k] = v
					}
				}
				if strings.Contains(text(t, task[m])+text(t, rest), "github_runner_token") {
					t.Errorf("%s %q names the token on its command line", file, name)
				}
				if stdin {
					carriers++
					if task["no_log"] != true {
						t.Errorf("%s %q carries the token on stdin without no_log: true", file, name)
					}
					if v, _ := task["vars"].(map[string]any); v["ansible_pipelining"] != true && !strings.HasPrefix(m, "ansible.windows.") {
						t.Errorf("%s %q carries the token without ansible_pipelining: the module would be a file on the machine", file, name)
					}
				}
			}
		}
	}
	if carriers < 4 { // unix and windows register and retire, and the two WSL ones
		t.Errorf("found %d tasks that hand the token on stdin; the test reads no tasks", carriers)
	}
	ps, err := fs.ReadFile(tree, roleDir+"/files/runner-windows.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(ps)
	if !strings.Contains(s, "[Console]::In.ReadLine()") || !strings.Contains(s, "$env:ACTIONS_RUNNER_INPUT_TOKEN = $tok") ||
		strings.Contains(s, "--token") || strings.Contains(s, "$Token") {
		t.Error("runner-windows.ps1 must read the token from stdin into ACTIONS_RUNNER_INPUT_TOKEN only, never --token")
	}
}

// TestWindowsRetireV1IsOptInAndGuarded: retiring v1 runs only with nova_windows_retire_v1 (default false), and its
// first task refuses unless every v2 Windows runner is registered and online; nothing else includes it.
func TestWindowsRetireV1IsOptInAndGuarded(t *testing.T) {
	t.Parallel()
	b, err := fs.ReadFile(tree, roleDir+"/defaults/main.yml")
	if err != nil {
		t.Fatal(err)
	}
	var defaults map[string]any
	if err := yaml.Unmarshal(b, &defaults); err != nil {
		t.Fatal(err)
	}
	if defaults["nova_windows_retire_v1"] != false {
		t.Errorf("nova_windows_retire_v1 defaults to %v, want false", defaults["nova_windows_retire_v1"])
	}
	tasks := roleTasks(t)
	includes := 0
	for file, ts := range tasks {
		for _, task := range ts {
			if strings.Contains(text(t, task["ansible.builtin.include_tasks"]), "wsl-windows-retire-v1.yml") {
				includes++
				if file != "wsl-windows.yml" || task["when"] != "nova_windows_retire_v1 | bool" {
					t.Errorf("%s includes the v1 retirement with when %v; only wsl-windows.yml may, when nova_windows_retire_v1 | bool", file, task["when"])
				}
			}
		}
	}
	if includes != 1 {
		t.Errorf("the v1 retirement is included %d times, want once", includes)
	}
	retire := tasks["wsl-windows-retire-v1.yml"]
	if len(retire) == 0 {
		t.Fatal("no wsl-windows-retire-v1.yml")
	}
	guard, ok := retire[0]["ansible.builtin.assert"].(map[string]any)
	if !ok {
		t.Fatalf("the first task of the v1 retirement is %q, want the assert that v2 is up", retire[0]["name"])
	}
	that := text(t, guard["that"])
	for _, want := range []string{
		"github_runner_win_registered_n | int == github_runner_win_want | length",
		"github_runner_win_online_n | int == github_runner_win_want | length",
		"github_runner_win_unread | length == 0",
		"nova_windows_runners | default([]) | length > 0",
	} {
		if !strings.Contains(that, want) {
			t.Errorf("the v1 retirement's guard lacks %q", want)
		}
	}
	if retire[0]["when"] != nil {
		t.Error("the v1 retirement's guard is conditional; it must always run, under --check too")
	}
	// then the coverage and busy guards, unconditional, before anything that changes the machine
	var guards []string
	for _, task := range retire {
		if a, ok := task["ansible.builtin.assert"].(map[string]any); ok && task["when"] == nil {
			guards = append(guards, text(t, a["that"]))
			continue
		}
		if task["ansible.builtin.include_tasks"] == "wsl-windows-retire-v1-runners.yml" || task["ansible.builtin.shell"] != nil {
			break
		}
	}
	if g := strings.Join(guards, "\n"); len(guards) != 3 || !strings.Contains(g, "github_runner_v1_uncovered | length == 0") || !strings.Contains(g, "github_runner_v1_busy | length == 0") {
		t.Errorf("before the first change the v1 retirement asserts v2 up, v2 covering every v1 target and no v1 runner busy; it asserts %q", guards)
	}
	// the online and registered counts it reads are set before it, in wsl-windows.yml, under --check too
	if !strings.Contains(text(t, tasks["wsl-windows.yml"]), "github_runner_win_online_n:") {
		t.Error("wsl-windows.yml no longer sets github_runner_win_online_n")
	}
}

// TestWindowsRetireV1Keeps: what v1's retirement moves aside never includes what CI or WSL still needs, and every item
// is moved aside, never deleted.
func TestWindowsRetireV1Keeps(t *testing.T) {
	t.Parallel()
	b, err := fs.ReadFile(tree, roleDir+"/defaults/main.yml")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Runners []struct{ Dir, Name, URL string } `yaml:"github_runner_windows_v1_runners"`
		Labels  map[string][]string               `yaml:"github_runner_windows_v1_labels"`
		Items   []string                          `yaml:"github_runner_windows_v1_items"`
		Tasks   []string                          `yaml:"github_runner_windows_v1_tasks"`
		Kept    []string                          `yaml:"github_runner_windows_v1_kept"`
	}
	if err := yaml.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, r := range d.Runners {
		dirs = append(dirs, r.Dir)
	}
	if want := []string{`C:\actions-runner-rocketnet\1`, `C:\actions-runner-rocketnet\2`, `C:\actions-runner-rocketnet\3`, `C:\actions-runner-rgd\1`, `C:\actions-runner-rgd\2`}; !slices.Equal(dirs, want) {
		t.Errorf("v1 runners %q, want %q", dirs, want)
	}
	for _, i := range d.Items {
		l := strings.ToLower(i)
		if strings.HasPrefix(l, `c:\tools`) || l == `c:\rowan-jobs` || l == `c:\rowan-jobs\*` || strings.HasPrefix(l, `c:\actions-runner\`) || l == `c:\actions-runner` {
			t.Errorf("v1 items name %s: CI's tools, the job scratch and v2's runners are kept", i)
		}
	}
	for _, r := range d.Runners {
		if r.Name == "" || len(d.Labels[r.URL]) == 0 {
			t.Errorf("v1 runner %s needs its GitHub name (the busy check) and its target's workflow labels (the coverage check)", r.Dir)
		}
	}
	if !slices.Equal(d.Labels["https://github.com/mas-bandwidth/rocketnet"], []string{"rocketnet-windows"}) ||
		!slices.Equal(d.Labels["https://github.com/robotgamedevelopers"], []string{"fleet"}) {
		t.Errorf("v1 labels %v: rocketnet's workflows select rocketnet-windows, robotgamedevelopers' select fleet", d.Labels)
	}
	if !slices.Equal(d.Tasks, []string{"nova-wsl-keepalive"}) {
		t.Errorf("v1 tasks %q: only the duplicate keepalive goes; WSL2 keepalive (Ubuntu-24.04) stays", d.Tasks)
	}
	kept := strings.Join(d.Kept, "\n")
	for _, k := range []string{`C:\tools`, "fleet-rocketnet-lan-proof-udp-40000", "rocketnet load_split", "job-scratch", "WSL2 keepalive (Ubuntu-24.04)"} {
		if !strings.Contains(kept, k) {
			t.Errorf("the kept lines do not name %s", k)
		}
	}
	ps, err := fs.ReadFile(tree, roleDir+"/files/runner-windows.ps1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ps), "Remove-Item -Recurse") || strings.Contains(strings.ToLower(string(ps)), "netfirewallrule") {
		t.Error("runner-windows.ps1 deletes a tree or touches a firewall rule; v1 is moved aside, and the firewall is kept")
	}
}

// TestRunnerWindowsRepairs: register repairs a half registration (its .runner and credentials moved into the dir's own
// retired-<date>, its service removed, then --replace); an interrupted unpack is moved aside and unpacked again; ensure
// never fails on a missing service (the counts make the run fail, and the next one repairs). The functional
// TestWindowsRunnersPlayRepairsHalfRegistrations runs which runners the play sends to register.
func TestRunnerWindowsRepairs(t *testing.T) {
	t.Parallel()
	b, err := fs.ReadFile(tree, roleDir+"/files/runner-windows.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`$to = Join-Path $Dir "retired-$(Get-Date -Format yyyyMMdd)"`,
		`foreach ($f in '.runner', '.credentials', '.credentials_rsaparams', '.service') {`,
		`"REPAIR $Dir -> $to"`,
		`'--replace', '--runasservice'`,
		`"PARTIAL $Dir -> $(Move-Aside $Dir`,
		`if (-not $s) { "NO-SERVICE $Dir"; exit 0 }`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("runner-windows.ps1 lacks %q", want)
		}
	}
	if strings.Contains(s, "ALREADY-REGISTERED") || strings.Contains(s, "exit 6 }\n  }\n  'retire'") {
		t.Error("register must repair a half registration, not skip it")
	}
	st, err := fs.ReadFile(tree, roleDir+"/tasks/wsl-windows-state.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"selectattr('dir', 'in', github_runner_win_state.stdout_lines | select('match', '^REG ')",
		"selectattr('dir', 'in', github_runner_win_state.stdout_lines | select('match', '^SVC ')",
		"selectattr('name', 'in', github_runner_gh_lines"} {
		if !strings.Contains(string(st), want) {
			t.Errorf("a whole runner needs .runner, its service and GitHub's entry; wsl-windows-state.yml lacks %q", want)
		}
	}
}

// TestWindowsRunnersPlay: the play reaches only machines whose record names windows_runners, through the role's file.
func TestWindowsRunnersPlay(t *testing.T) {
	t.Parallel()
	b, err := fs.ReadFile(tree, "windows-runners.yml")
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); !strings.Contains(s, "tasks_from: wsl-windows.yml") || !strings.Contains(s, "when: nova_windows_runners | default([]) | length > 0") {
		t.Error("windows-runners.yml must include wsl-windows.yml only where nova_windows_runners is non-empty")
	}
	for _, f := range []string{"wsl-windows.yml", "wsl-windows-state.yml", "wsl-windows-github.yml", "wsl-windows-register.yml", "wsl-windows-retire-v1.yml", "wsl-windows-retire-v1-runners.yml"} {
		if _, err := fs.Stat(tree, path.Join(roleDir, "tasks", f)); err != nil {
			t.Errorf("%s is not embedded", f)
		}
	}
}

// TestCIRulesUnchainRule: both scripts drop a chained hook under C:\rowan-jobs\ (v1's ci-hold) or naming a missing file,
// and never chain such a hook (the functional TestCIRulesEnvEdit runs it).
func TestCIRulesUnchainRule(t *testing.T) {
	t.Parallel()
	read := func(f string) string {
		b, err := fs.ReadFile(tree, roleDir+"/files/ci-rules/"+f)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	ps := read("nova-ci-rules.ps1")
	for _, want := range []string{
		`$unchain = @('C:\rowan-jobs\')`,
		`return -not ($p -and (Test-Path -LiteralPath $p -PathType Leaf))`,
		`if ($v -eq $ours[$k] -or -not (Test-Gone $v)) { $old[$k] = $v }`,
		`if (Test-Gone $v) { continue }`,
	} {
		if !strings.Contains(ps, want) {
			t.Errorf("nova-ci-rules.ps1 lacks %q", want)
		}
	}
	sh := read("nova-ci-rules.sh")
	for _, want := range []string{
		`case $1 in [Cc]:'\rowan-jobs\'* | /mnt/[Cc]/rowan-jobs/*) return 0 ;; esac`,
		`[ ! -f "$1" ]`,
		`ACTIONS_RUNNER_HOOK_JOB_*=* | NOVA_CI_CHAIN_JOB_*=*) hook_gone "${l#*=}" && continue ;;`,
	} {
		if !strings.Contains(sh, want) {
			t.Errorf("nova-ci-rules.sh lacks %q", want)
		}
	}
}
