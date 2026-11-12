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
		`'--work', '_work', '--replace')`, `$a += '--runasservice'`,
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

// TestWindowsRunnersPlay: the play reaches only machines whose record names windows_runners or windows_admin_runners,
// through the role's file.
func TestWindowsRunnersPlay(t *testing.T) {
	t.Parallel()
	b, err := fs.ReadFile(tree, "windows-runners.yml")
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); !strings.Contains(s, "tasks_from: wsl-windows.yml") || !strings.Contains(s, "when: (nova_windows_runners | default([]) | length > 0) or (nova_windows_admin_runners | default([]) | length > 0)") {
		t.Error("windows-runners.yml must include wsl-windows.yml only where nova_windows_runners or nova_windows_admin_runners is non-empty")
	}
	for _, f := range []string{"wsl-windows.yml", "wsl-windows-state.yml", "wsl-windows-github.yml", "wsl-windows-register.yml"} {
		if _, err := fs.Stat(tree, path.Join(roleDir, "tasks", f)); err != nil {
			t.Errorf("%s is not embedded", f)
		}
	}
}

// TestWSLKeepaliveTask: every WSL machine the play reaches, with or without windows_runners, is held running from boot
// by one scheduled task: S4U as the Windows account that started WSL (no password stored), a boot trigger that repeats
// every minute with IgnoreNew (so a wsl.exe ended by a WSL update, wsl --shutdown or a reboot inside the distro is
// started again), no time limit, and the distro found by this machine's machine-id, never by a name the role ships.
func TestWSLKeepaliveTask(t *testing.T) {
	t.Parallel()
	b, err := fs.ReadFile(tree, "windows-runners.yml")
	if err != nil {
		t.Fatal(err)
	}
	play := string(b)
	i := strings.Index(play, "tasks_from: wsl-keepalive.yml")
	if i < 0 {
		t.Fatal("windows-runners.yml does not include wsl-keepalive.yml")
	}
	if j := strings.Index(play[i:], "\n    - name:"); j >= 0 && strings.Contains(play[i:i+j], "nova_windows_runners") ||
		j < 0 && strings.Contains(play[i:], "nova_windows_runners") {
		t.Error("the keepalive must reach every WSL machine, not only those whose record names windows_runners")
	}
	kb, err := fs.ReadFile(tree, roleDir+"/tasks/wsl-keepalive.yml")
	if err != nil {
		t.Fatal(err)
	}
	k := string(kb)
	for _, want := range []string{"github_runner_wsl_interop", "github_runner_wsl_machine_id_file", "-Action keepalive", "-MachineId", "-User", "</dev/null"} {
		if !strings.Contains(k, want) {
			t.Errorf("wsl-keepalive.yml lacks %q", want)
		}
	}
	ps, err := fs.ReadFile(tree, roleDir+"/files/runner-windows.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(ps)
	ka := s[strings.Index(s, "  'keepalive' {"):]
	ka = ka[:strings.Index(ka, "\n  }\n")]
	for _, want := range []string{
		"<LogonType>S4U</LogonType>",
		"<BootTrigger>",
		"<Interval>PT1M</Interval>",
		"<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>",
		"<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>",
		"<DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>",
		"<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>",
		`\etc\machine-id`,
		"--exec /bin/sleep infinity",
		"Start-ScheduledTask",
		`"NO-DISTRO`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("runner-windows.ps1's keepalive lacks %q", want)
		}
	}
	for _, never := range []string{"-Password", "<Password>", "Password", "SYSTEM", "Ubuntu"} {
		if strings.Contains(ka, never) {
			t.Errorf("the keepalive names %q: no password is stored, the task runs as the account that owns the distro, and no distro name ships", never)
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

// The page file is held at a fixed size only where a run names nova_windows_pagefile_mb, and nothing restarts the
// machine: a size Windows has not applied yet is reported as restart=owed, for a person to pick the moment.
func TestWindowsPagefileIsOptInAndNeverRestarts(t *testing.T) {
	b, err := fs.ReadFile(tree, roleDir+"/files/runner-windows.ps1")
	if err != nil {
		t.Fatal(err)
	}
	ps := strings.ToLower(string(b))
	for _, never := range []string{"restart-computer", "shutdown.exe", "shutdown /r"} {
		if strings.Contains(ps, never) {
			t.Errorf("runner-windows.ps1 contains %q: nothing here restarts the machine", never)
		}
	}
	if !strings.Contains(ps, "restart=$owed") || !strings.Contains(ps, "\x27pagefile\x27 {") {
		t.Error("runner-windows.ps1 has the pagefile verb and reports restart=owed|no")
	}
	task, err := fs.ReadFile(tree, roleDir+"/tasks/wsl-pagefile.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(task), "(nova_windows_pagefile_mb | default(0) | int) > 0") {
		t.Error("wsl-pagefile.yml runs only when nova_windows_pagefile_mb is set")
	}
	play, err := fs.ReadFile(tree, "windows-runners.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(play), "tasks_from: wsl-pagefile.yml") {
		t.Error("the windows-runners play includes wsl-pagefile.yml")
	}
}

// TestWindowsAdminPasswordOnlyInTheEnvironment: an admin runner's password reaches powershell.exe only in its
// environment (nova-secrets exec adds the one --only name; WSLENV hands it through interop), both tasks that carry it
// are no_log, the script takes it out of its environment as it reads it and hands it only to calls inside its own
// process, and config.cmd never gets --windowslogonpassword.
func TestWindowsAdminPasswordOnlyInTheEnvironment(t *testing.T) {
	t.Parallel()
	carriers := 0
	for file, tasks := range roleTasks(t) {
		for _, task := range tasks {
			name, _ := task["name"].(string)
			cmd := text(t, task["ansible.builtin.shell"]) + text(t, task["ansible.builtin.command"])
			if strings.Contains(cmd, "windowslogonpassword") {
				t.Errorf("%s %q gives config.cmd the password as an argument", file, name)
			}
			if !strings.Contains(cmd, "-Action account") && !strings.Contains(cmd, "-Action logon") {
				continue
			}
			carriers++
			for _, want := range []string{"{{ github_runner_win_secrets_exec }} {{ item.secret | quote }} -- env WSLENV={{ item.secret | quote }}",
				"-SecretEnv {{ item.secret | quote }}", "</dev/null"} {
				if !strings.Contains(cmd, want) {
					t.Errorf("%s %q lacks %q", file, name, want)
				}
			}
			if task["no_log"] != true {
				t.Errorf("%s %q carries the password without no_log: true", file, name)
			}
		}
	}
	if carriers != 2 {
		t.Errorf("found %d tasks that carry an admin runner's password; want the account and logon steps", carriers)
	}
	d, err := fs.ReadFile(tree, roleDir+"/defaults/main.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(d), "github_runner_win_secrets_exec: >-\n  {{ [github_runner_nova_secrets, 'exec', '--store', nova_secrets_store, '--as', nova_seat,") ||
		!strings.Contains(string(d), "'--only'] | map('quote') | join(' ') }}") {
		t.Error("github_runner_win_secrets_exec must be the machine seat's nova-secrets exec, ending in --only")
	}
	b, err := fs.ReadFile(tree, roleDir+"/files/runner-windows.ps1")
	if err != nil {
		t.Fatal(err)
	}
	ps := string(b)
	for _, want := range []string{"$v = [Environment]::GetEnvironmentVariable($n, 'Process')", "[Environment]::SetEnvironmentVariable($n, $null, 'Process')"} {
		if !strings.Contains(ps, want) {
			t.Errorf("runner-windows.ps1 lacks %q", want)
		}
	}
	if strings.Contains(ps, "windowslogonpassword") {
		t.Error("runner-windows.ps1 gives config.cmd the password as an argument")
	}
	// a cmdlet's parameters are what module logging (event 4103) records: the value reaches only methods
	for _, never := range []string{"ConvertTo-SecureString", "StartPassword", "-AsPlainText"} {
		if strings.Contains(ps, never) {
			t.Errorf("runner-windows.ps1 hands the password to a cmdlet (%s), which module logging would record", never)
		}
	}
	allowed := []string{"$pw = Take-Secret $SecretEnv", "$pw = $null", "foreach ($c in $pw.ToCharArray()) { $sec.AppendChar($c) }",
		"[NovaAccount]::ServiceLogon($Account, $pw)", "$rc = [NovaAccount]::SetServiceLogon($s.Name, $want, $pw)",
		"$rc = [NovaAccount]::CreateService($svc, \"GitHub Actions Runner ($svc)\", \"`\"$Dir\\bin\\RunnerService.exe`\"\", $want, $pw)"}
	uses := 0
	for _, l := range strings.Split(ps, "\n") {
		if !strings.Contains(l, "$pw") {
			continue
		}
		uses++
		if !slices.ContainsFunc(allowed, func(a string) bool { return strings.Contains(l, a) }) {
			t.Errorf("runner-windows.ps1 uses the password outside the calls that take it in this process: %s", strings.TrimSpace(l))
		}
	}
	if uses < 6 {
		t.Errorf("runner-windows.ps1 names the password %d times; the account and logon actions read it", uses)
	}
}

// TestWindowsAdminRunnerOrder: the seat is checked for every admin runner's password before anything on the machine
// changes, the account is set before its runner registers, the service is switched to the account after registering
// and before ensure starts it; an admin runner carries only its entry's labels and is named by its account.
func TestWindowsAdminRunnerOrder(t *testing.T) {
	t.Parallel()
	b, err := fs.ReadFile(tree, roleDir+"/tasks/wsl-windows.yml")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	at := -1
	for _, step := range []string{
		"- name: no admin runner shares a label",
		"- name: the names in this machine's seat, for the admin runners' passwords",
		"- name: every admin runner's password is in this machine's seat",
		"- name: the scripts, staged on the Windows side where only Administrators can change them",
		"- name: each admin runner's local account",
		"- name: each wanted runner unpacked",
		"- name: register, one target at a time",
		"- name: each admin runner's service logs on as its account",
		"- name: each wanted runner's service starts at boot and is running",
		"- name: the CI rules on these runners",
	} {
		i := strings.Index(s, step)
		if i < 0 || i < at {
			t.Fatalf("wsl-windows.yml: %q is missing or out of order", step)
		}
		at = i
	}
	for _, want := range []string{
		"argv: [\"{{ github_runner_nova_secrets }}\", names, --store, \"{{ nova_secrets_store }}\", --as, \"{{ nova_seat }}\"]",
		"{ printf 'Nw1-'; openssl rand -hex 24; } | nova-secrets set {{ n }}",
		"nova-secrets grant {{ n }} --to {{ nova_seat }}",
		"nova-fleet secrets-refresh -- -e nova_seal_store=",
		"'labels': t.labels | unique | sort | join(','),",
		"'name': inventory_hostname ~ '-win-' ~ slug ~ '-' ~ t.account,",
		"reject('match', '^https://github[.]com/[^/]+/[^/]+$')",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("wsl-windows.yml lacks %q", want)
		}
	}
	want := s[strings.Index(s, "- name: the wanted Windows runners"):]
	admin := want[strings.Index(want, "{%- for t in nova_windows_admin_runners"):]
	admin = admin[:strings.Index(admin, "{%- endfor -%}")]
	if strings.Contains(admin, "github_runner_wsl_labels") {
		t.Error("an admin runner must not carry the role's labels (fleet, the machine's name)")
	}
}

// psAction is the body of one -Action of runner-windows.ps1.
func psAction(t *testing.T, ps, action string) string {
	t.Helper()
	i := strings.Index(ps, "  '"+action+"' {")
	if i < 0 {
		t.Fatalf("runner-windows.ps1 has no %s action", action)
	}
	body := ps[i:]
	return body[:strings.Index(body, "\n  }\n")]
}

// TestWindowsAdminRunnerGuards: the account action changes nothing on a user it did not make (no marker), checked
// before any right, group or password; the logon action proves the admin root and the whole runner tree sealed before
// it makes the service or sets its logon (on every call, never only after a password change), repairs nothing, and
// restarts only an idle runner; and the play fails a logon whose receipt does not say acl=protected.
func TestWindowsAdminRunnerGuards(t *testing.T) {
	t.Parallel()
	b, err := fs.ReadFile(tree, roleDir+"/files/runner-windows.ps1")
	if err != nil {
		t.Fatal(err)
	}
	ps := string(b)
	if !strings.Contains(ps, "$AccountMarker = 'nova-fleet: windows admin runner'") {
		t.Error("runner-windows.ps1 defines no account marker")
	}
	acct := psAction(t, ps, "account")
	guard := strings.Index(acct, `if ($u -and "$($u.Description)" -cne $AccountMarker) {`)
	for _, after := range []string{"Add-AccountApi", "New-LocalUser", "[NovaAccount]::Grant(", "Add-LocalGroupMember", "Set-LocalUser"} {
		if j := strings.Index(acct, after); guard < 0 || j < guard {
			t.Errorf("the account action reaches %s before refusing a user without the marker", after)
		}
	}
	if !strings.Contains(acct, "-Description $AccountMarker") || !strings.Contains(acct, "exit 9") {
		t.Error("the account action makes its user with the marker, and refuses another with exit 9")
	}
	logon := psAction(t, ps, "logon")
	proof := strings.Index(logon, "foreach ($why in [NovaTree]::Open($Disk, (Split-Path $Dir), $Sealers, $false), [NovaTree]::Open($Disk, $Dir, $Sealers + @($u.SID.Value), $true)) {")
	refuse := strings.Index(logon, `if ($why) { "ACL-OPEN ${why}: `)
	above := strings.Index(logon, "Assert-Above (Split-Path $Dir)")
	create := strings.Index(logon, "$rc = [NovaAccount]::CreateService(")
	set := strings.Index(logon, "\n        $rc = [NovaAccount]::SetServiceLogon($s.Name, $want, $pw)\n")
	if proof < 0 || refuse < proof || above < refuse || create < above || set < above {
		t.Errorf("the logon action must prove the tree sealed and the folders above it closed, refuse when they are not, and only then make the service or set its logon on every call (at %d, %d, %d, %d, %d)",
			proof, refuse, above, create, set)
	}
	if strings.Contains(logon, "icacls") || strings.Contains(logon, "Set-Acl") {
		t.Error("the logon action repairs nothing: a tree that is not sealed is refused")
	}
	if !strings.Contains(logon, "if (Test-Busy $Dir) { $restart = 'busy' }") ||
		!strings.Contains(ps, `(Get-CimInstance Win32_Process -Filter "ParentProcessId = $($l.ProcessId) AND Name = 'Runner.Worker.exe'")`) {
		t.Error("the logon action restarts the service only while no job runs (no Runner.Worker under its listener)")
	}
	if !strings.Contains(logon, " acl=protected ") {
		t.Error("the logon receipt says acl=protected")
	}
	y, err := fs.ReadFile(tree, roleDir+"/tasks/wsl-windows.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(y), "rejectattr('stdout', 'search', ' acl=protected ')") {
		t.Error("the play must fail a logon whose receipt does not say acl=protected")
	}
}

// inOrder fails unless each of steps is in s, each after the one before.
func inOrder(t *testing.T, what, s string, steps ...string) {
	t.Helper()
	at := -1
	for _, step := range steps {
		i := strings.Index(s, step)
		if i < 0 || i < at {
			t.Errorf("%s: %q is missing or out of order", what, step)
			return
		}
		at = i
	}
}

// TestWindowsAdminRunnerSealed: the wiring of an admin runner's checks (what each decides is run on fake ACLs by
// TestFunctionalWindowsSecurityDecisions). Its directories are created sealed in one call (owned by Administrators,
// nothing inherited) and a found one that is not sealed is refused; install proves the folders above the admin root
// closed before it seals, seals and proves before it unpacks, checks the PATH and the tool folders, records the one
// workflow the hook allows, hashes the copy it extracts and extracts that copy, and checks the files against it on
// every run; register gives NETWORK SERVICE nothing; the .env is the role's whole with CCACHE_DISABLE=1; the root is
// directly under C:\; and the job-started hook refuses an admin runner's job before anything else runs.
func TestWindowsAdminRunnerSealed(t *testing.T) {
	t.Parallel()
	b, err := fs.ReadFile(tree, roleDir+"/files/runner-windows.ps1")
	if err != nil {
		t.Fatal(err)
	}
	ps := string(b)
	seal := ps[strings.Index(ps, "function Seal-Dir("):]
	seal = seal[:strings.Index(seal, "\n}\n")]
	inOrder(t, "Seal-Dir", seal,
		"$ds.SetAccessRuleProtection($true, $false)",
		"$ds.SetOwner((New-Object Security.Principal.SecurityIdentifier 'S-1-5-32-544'))",
		"[void][IO.Directory]::CreateDirectory($p, $ds)",
		"$why = [NovaTree]::Open($Disk, $p, $allowed, $false)",
		`if ($why) { "PLANTED ${why}: `, "exit 11")
	if !strings.Contains(ps, "$Sealers = @('S-1-5-18', 'S-1-5-32-544')") {
		t.Error("only SYSTEM and Administrators seal an admin runner's directories")
	}
	if !strings.Contains(ps, "$Openers = @('S-1-5-20', 'S-1-5-6', 'S-1-2-0', 'S-1-5-11', 'S-1-5-32-545', 'S-1-1-0')") {
		t.Error("the accounts ordinary jobs act as are NETWORK SERVICE, SERVICE, LOCAL, Authenticated Users, Users and Everyone")
	}
	above := ps[strings.Index(ps, "function Assert-Above("):]
	above = above[:strings.Index(above, "\n}\n")]
	inOrder(t, "Assert-Above", above, "$why = [NovaTree]::Above($Disk, $root, $Openers)", `if ($why) { "ABOVE-OPEN ${why}: `, "exit 13")
	install := psAction(t, ps, "install")
	inOrder(t, "install, an admin runner", install,
		"if ($Account -and $Workflow -notmatch '^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+/\\.github/workflows/[A-Za-z0-9._-]+\\.ya?ml@refs/heads/main$') { 'BAD-ARGS -Workflow'; exit 2 }",
		"$u = Get-OurUser $Account",
		"Assert-Above (Split-Path $Dir)",
		"Seal-Dir (Split-Path $Dir) @()",
		"Seal-Dir $Dir @($u.SID.Value)",
		"$why = [NovaTree]::Open($Disk, $Dir, $Sealers + @($u.SID.Value), $true)",
		"if ($paths.Count -eq 0) { $paths = @([Environment]::GetEnvironmentVariable('Path', 'Machine')",
		"$paths += @($ToolDirs -split ';' | Where-Object { $_ })",
		"$open = [NovaTree]::OpenPath($Disk, [string[]]$paths, $Openers)",
		`if ($open) { "PATH-WRITABLE ${open}: `,
		"$wf = Join-Path $hd 'admin-workflow'",
		"[IO.File]::WriteAllText($wf, $Workflow)",
		"Copy-Item -Force -LiteralPath $zip -Destination $pinned",
		"$got = (Get-FileHash -LiteralPath $pinned -Algorithm SHA256).Hash.ToLower()",
		"if ($got -ne $Sha) { Remove-Item -Force -LiteralPath $pinned;",
		"[NovaTree]::Extract($pinned, $Dir)",
		"[NovaTree]::Verify($pinned, $Dir, @('bin', 'externals'))",
		`if ($why) { "TAMPERED ${why}: `,
		"$own['CCACHE_DISABLE'] = '1'", "$own['CCACHE_CONFIGPATH'] = \"$Dir\\.nova-ccache\\ccache.conf\"", "$own['CCACHE_CONFIGPATH2'] = \"$Dir\\.nova-ccache\\ccache-system.conf\"")
	if !strings.Contains(install, "$kept = @($lines | Where-Object { -not $Account -and (") {
		t.Error("an admin runner's .env keeps no line the role does not own")
	}
	register := psAction(t, ps, "register")
	inOrder(t, "register", register, "if ($Account) {", "$a += '--disableupdate'", "} else {", "& icacls.exe $Dir /reset /T /C /Q", "$a += '--runasservice'")

	h, err := fs.ReadFile(tree, roleDir+"/files/ci-rules/nova-ci-rules.ps1")
	if err != nil {
		t.Fatal(err)
	}
	hook := string(h)
	started := hook[strings.Index(hook, "function Invoke-Started {"):]
	inOrder(t, "the job-started hook", started,
		"$why = Get-AdminRefusal", "if ($why) {", `"::error::CI rule: $why"`, "$worker = Find-Ancestor $PID 'Runner.Worker' (Get-Rows 'name')", "if ($worker) { Stop-Process -Id $worker -Force", "exit 1", "Invoke-Disarm", "Invoke-Chain \"$env:NOVA_CI_CHAIN_JOB_STARTED\"")
	refusal := hook[strings.Index(hook, "function Get-AdminRefusal {"):]
	inOrder(t, "Get-AdminRefusal", refusal,
		"$wf = Join-Path $here 'admin-workflow'",
		"if (-not $want -and -not $me.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { return $null }",
		"$why = [NovaCiAdmin]::Refuse(\"$env:GITHUB_EVENT_NAME\", \"$env:GITHUB_REF\", \"$env:GITHUB_WORKFLOW_REF\", $want)",
		"if ($why) { return $why }",
		"for ($d = Split-Path $here; $d; $d = Split-Path $d) {",
		"-Directory -Force -ErrorAction Stop) }",
		"catch { return ",
		"$p = [NovaCiAdmin]::Planted($paths.ToArray(), $owners.ToArray(), $sh.ToArray())",
		"if ($p) { return ")

	y, err := fs.ReadFile(tree, roleDir+"/tasks/wsl-windows.yml")
	if err != nil {
		t.Fatal(err)
	}
	d, err := fs.ReadFile(tree, roleDir+"/defaults/main.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"'dir': github_runner_wsl_admin_dir ~ '\\\\' ~ slug ~ '-' ~ t.account,",
		"'workflow': t.workflow | default('')",
		"{{ ('-Account ' ~ (item.account | quote) ~ ' -Workflow ' ~ (item.workflow | quote) ~ ' -ToolDirs ' ~ (github_runner_win_admin_tools | join(';') | quote))",
		"github_runner_win_path: \"{{ github_runner_win_admin_path if item.account else github_runner_windows_path }}\"",
		"| first | default('')) is search('account=(updated|created)') else '' }} </dev/null",
	} {
		if !strings.Contains(string(y), want) {
			t.Errorf("wsl-windows.yml lacks %q", want)
		}
	}
	for _, want := range []string{
		`github_runner_wsl_admin_dir: 'C:\nova-admin-runners'`,
		"{{ github_runner_windows_path | reject('match', '(?i)' ~ (nova_runner_windows_ccache_dir",
		"github_runner_win_admin_tools: '{{ [nova_runner_windows_ccache_dir, nova_runner_windows_ccache_dir ~ ''\\msvc''] }}'",
	} {
		if !strings.Contains(string(d), want) {
			t.Errorf("defaults/main.yml lacks %q", want)
		}
	}
	c, err := fs.ReadFile(tree, "roles/runner-packages/tasks/main.yml")
	if err != nil {
		t.Fatal(err)
	}
	inOrder(t, "the ccache seal", string(c),
		"\"$ic\" \"$d\" /setowner '*S-1-5-32-544' /T /C /Q",
		"\"$ic\" \"$d\" /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-11:(OI)(CI)RX' '*S-1-5-32-545:(OI)(CI)RX' /Q",
		"\"$ic\" \"$d\" /remove:g '*S-1-5-20' '*S-1-5-6' '*S-1-2-0' '*S-1-1-0' '*S-1-3-0' /Q",
		"\"$ic\" \"$d\\\\*\" /reset /T /C /Q")
	w, err := fs.ReadFile(tree, "roles/runner-packages/tasks/windows-ccache.yml")
	if err != nil {
		t.Fatal(err)
	}
	// a file planted while the directory was open is never sealed in: not exact, the directory goes, and it must be
	// exact once sealed
	inOrder(t, "windows-ccache.yml", string(w),
		"checksum: \"sha256:{{ nova_runner_windows_ccache.sha256 }}\"",
		"- name: is the Windows ccache directory exactly the release",
		"ansible.builtin.file: { path: \"{{ rp_win_ccache_unix }}\", state: absent }",
		"when: rp_win_ccache_was.stdout == 'DIFFERS'",
		"- name: the Windows ccache directory sealed (before anything is copied in)",
		"cp \"$src/ccache.exe\" \"{{ rp_win_ccache_unix }}/msvc/cl.exe\"",
		"- name: the Windows ccache directory sealed (what was copied in inherits only the seal)",
		"- name: sealed, it is exactly the release",
		"failed_when: rp_win_ccache_is.rc != 0 or rp_win_ccache_is.stdout != 'EXACT'")
	inOrder(t, "the ccache exactness check", string(c),
		"if [ ! -e \"$d\" ] && [ ! -L \"$d\" ]; then echo ABSENT; exit 0; fi",
		"[ \"$have\" = 'd ./msvc f ./VERSION f ./ccache.exe f ./msvc/cl.exe ' ]",
		"cmp -s \"$src/ccache.exe\" \"$d/ccache.exe\" && cmp -s \"$src/ccache.exe\" \"$d/msvc/cl.exe\"")
}
