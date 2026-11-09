package main

import (
	"io/fs"
	"path"
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
	"command", "shell", "raw", "script"}

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
			for _, m := range []string{"ansible.builtin.copy", "copy", "ansible.builtin.template", "ansible.builtin.lineinfile"} {
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
					if v, _ := task["vars"].(map[string]any); v["ansible_pipelining"] != true {
						t.Errorf("%s %q carries the token without ansible_pipelining: the module would be a file on the machine", file, name)
					}
				}
			}
		}
	}
	if carriers < 3 { // unix register and retire, and the WSL register
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

// TestRunnerWindowsShipsNoMachine: the role ships no machine's names or paths; register resets the directory's ACL first; the firewall is never touched.
func TestRunnerWindowsShipsNoMachine(t *testing.T) {
	t.Parallel()
	for _, f := range []string{"defaults/main.yml", "files/runner-windows.ps1"} {
		b, err := fs.ReadFile(tree, roleDir+"/"+f)
		if err != nil {
			t.Fatal(err)
		}
		for _, v1 := range []string{"captain", "actions-runner-r", "rowan-jobs", "ci-hold", "nova-wsl-keepalive", "robotgamedevelopers", "mas-bandwidth/rocketnet", "nova-retired"} {
			if strings.Contains(string(b), v1) {
				t.Errorf("%s names %s: nothing of one machine ships", f, v1)
			}
		}
	}
	b, err := fs.ReadFile(tree, roleDir+"/files/runner-windows.ps1")
	if err != nil {
		t.Fatal(err)
	}
	ps := string(b)
	reg := ps[strings.Index(ps, "  'register' {"):]
	reg = reg[:strings.Index(reg, "\n  }\n")]
	if !strings.Contains(reg, "icacls.exe $Dir /reset /T /C /Q") || !strings.Contains(ps, `-replace '[^\x20-\x7E]', '?'`) {
		t.Error("register resets the runner directory's ACL first, and config.cmd's lines reach ansible as ASCII")
	}
	if strings.Contains(strings.ToLower(ps), "netfirewallrule") {
		t.Error("runner-windows.ps1 touches a firewall rule; the firewall is kept")
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
	for _, f := range []string{"wsl-windows.yml", "wsl-windows-state.yml", "wsl-windows-github.yml", "wsl-windows-register.yml"} {
		if _, err := fs.Stat(tree, path.Join(roleDir, "tasks", f)); err != nil {
			t.Errorf("%s is not embedded", f)
		}
	}
}

// TestCIRulesUnchainRule: both scripts drop a chained hook naming a missing file,
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
		`[ ! -f "$1" ]`,
		`ACTIONS_RUNNER_HOOK_JOB_*=* | NOVA_CI_CHAIN_JOB_*=*) hook_gone "${l#*=}" && continue ;;`,
	} {
		if !strings.Contains(sh, want) {
			t.Errorf("nova-ci-rules.sh lacks %q", want)
		}
	}
}

// TestWindowsRunnerScriptHasNoRetire: no task calls runner-windows.ps1 with -Action retire (the native Windows runner
// path went in #6309; a WSL machine's runners are retired by unix-retire.yml), so the script accepts no retire action.
func TestWindowsRunnerScriptHasNoRetire(t *testing.T) {
	t.Parallel()
	b, err := fs.ReadFile(tree, roleDir+"/files/runner-windows.ps1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "'retire'") {
		t.Error("runner-windows.ps1 still accepts -Action retire, which nothing calls")
	}
}
