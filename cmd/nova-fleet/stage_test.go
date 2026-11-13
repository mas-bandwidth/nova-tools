package main

import (
	"encoding/base64"
	"io/fs"
	"strings"
	"testing"
	"unicode/utf16"

	"gopkg.in/yaml.v3"
)

// stage_test.go: unit tests of the sealed stage directory (README "The stage directory"; tasks/wsl-stage.yml,
// files/stage-check.ps1). They read the role's YAML and scripts as data: nothing is run. The check's decisions are
// TestFunctionalStageCheck (testdata/stagecheck); the play's order is TestFunctionalStageCheckGuardsEveryElevatedCall.

// stageGuard is how every elevated call in the role starts: the stage proved, then powershell.exe on a staged file.
const stageGuard = `{{ github_runner_win_stage_check }} && "{{ github_runner_wsl_powershell }}" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File `

// shellText is the command a task runs with shell or command, or "".
func shellText(task map[string]any) string {
	for _, m := range commandModules {
		if s, ok := task[m].(string); ok {
			return strings.Join(strings.Fields(s), " ")
		}
	}
	return ""
}

// TestStageGuardsEveryElevatedCall: every task that runs powershell.exe on a file runs it on a staged script, straight
// after the stage check and joined by && to it, so a refusal runs nothing.
func TestStageGuardsEveryElevatedCall(t *testing.T) {
	t.Parallel()
	n := 0
	for file, tasks := range roleTasks(t) {
		for _, task := range tasks {
			cmd := shellText(task)
			if strings.Contains(cmd, "github_runner_wsl_powershell") && !strings.Contains(cmd, "-File") && !strings.Contains(cmd, "-EncodedCommand") {
				t.Errorf("%s %q: powershell.exe runs something other than a staged -File or an -EncodedCommand:\n%s", file, task["name"], cmd)
			}
			if !strings.Contains(cmd, "-File") {
				continue
			}
			n++
			name, _ := task["name"].(string)
			// an admin runner's account and logon run powershell.exe under nova-secrets exec, the password in its env
			cmd = strings.Replace(cmd, "{{ github_runner_win_stage_check }} && {{ github_runner_win_secrets_exec }} {{ item.secret | quote }} -- env WSLENV={{ item.secret | quote }} ",
				"{{ github_runner_win_stage_check }} && ", 1)
			if strings.Count(cmd, "-File") != 1 || !strings.Contains(cmd, stageGuard) {
				t.Errorf("%s %q: an elevated call not guarded by the stage check:\n%s", file, name, cmd)
				continue
			}
			rest := cmd[strings.Index(cmd, stageGuard)+len(stageGuard):]
			if !strings.HasPrefix(rest, "{{ github_runner_wsl_script | quote }}") && !strings.HasPrefix(rest, "{{ github_runner_wsl_ci_script | quote }}") {
				t.Errorf("%s %q: runs a file that is not staged: %s", file, name, rest)
			}
			args, _ := task["args"].(map[string]any)
			if _, token := args["stdin"]; !token && !strings.HasSuffix(cmd, "</dev/null") && !strings.HasSuffix(cmd, "</dev/null | tr -d '\\r'") {
				t.Errorf("%s %q: stdin is not closed: %s", file, name, cmd)
			}
		}
	}
	if n < 6 {
		t.Errorf("only %d elevated calls found; the role has install, ensure, register, apply (twice), keepalive and pagefile", n)
	}
}

// TestStageIncludedBeforeEveryCall: each task file that makes an elevated call stages and proves the scripts first.
func TestStageIncludedBeforeEveryCall(t *testing.T) {
	t.Parallel()
	all := roleTasks(t)
	for _, file := range []string{"wsl-windows.yml", "wsl-keepalive.yml", "wsl-pagefile.yml", "ci-rules.yml"} {
		staged := false
		for _, task := range all[file] {
			if inc, _ := task["ansible.builtin.include_tasks"].(string); inc == "wsl-stage.yml" {
				staged = true
			}
			if strings.Contains(shellText(task), "-File") && !staged {
				t.Errorf("%s: %q runs before wsl-stage.yml is included", file, task["name"])
			}
		}
		if !staged {
			t.Errorf("%s does not include wsl-stage.yml", file)
		}
	}
}

// TestStageTasks: wsl-stage.yml makes the directory sealed, stages the very files it hashed, then proves them; the
// check closes stdin (the register token is on it) and the make and check differ only by $Make; --check runs none.
func TestStageTasks(t *testing.T) {
	t.Parallel()
	tasks := roleTasks(t)["wsl-stage.yml"]
	var order []string
	var hashed, staged string
	for _, task := range tasks {
		cmd := shellText(task)
		switch {
		case task["ansible.builtin.stat"] != nil:
			hashed = text(t, task["loop"])
			if task["delegate_to"] != "localhost" {
				t.Error("the sha256 must come from the role's own files on the control node")
			}
		case cmd == "{{ github_runner_win_stage_make }}":
			order = append(order, "make")
		case cmd == "{{ github_runner_win_stage_check }}":
			order = append(order, "check")
		case task["ansible.builtin.copy"] != nil:
			order = append(order, "copy")
			staged = text(t, task["loop"])
			if !strings.Contains(text(t, task["ansible.builtin.copy"]), "{{ github_runner_wsl_stage_unix }}/") {
				t.Error("the scripts are not copied into the stage directory")
			}
		case task["ansible.builtin.set_fact"] != nil:
			facts, _ := task["ansible.builtin.set_fact"].(map[string]any)
			for k, v := range facts {
				str, _ := v.(string)
				s := strings.Join(strings.Fields(str), " ")
				if k == "github_runner_win_stage_make" || k == "github_runner_win_stage_check" {
					if !strings.Contains(s, "-NoLogo -EncodedCommand") || !strings.HasSuffix(s, "</dev/null") {
						t.Errorf("%s is not an encoded command with stdin closed: %s", k, s)
					}
				}
				if k == "github_runner_win_stage_make" && !strings.Contains(s, "$Make = $true") {
					t.Error("the make call does not set $Make")
				}
				if k == "github_runner_win_stage_check" && (!strings.Contains(s, "$Make = $false") || !strings.Contains(s, "github_runner_win_stage_shas")) {
					t.Error("the check call makes, or does not check the hashes")
				}
			}
		}
		if (cmd != "" || task["ansible.builtin.copy"] != nil) && task["when"] != "not ansible_check_mode" {
			t.Errorf("%q runs under --check", task["name"])
		}
	}
	if strings.Join(order, ",") != "make,copy,check" {
		t.Errorf("wsl-stage.yml runs %v, want make, copy, check", order)
	}
	if hashed == "" || hashed != staged {
		t.Errorf("the files staged are not the files hashed:\n%s\n%s", hashed, staged)
	}
}

// TestStageDirectory: the stage directory is directly under C:\ and the WSL path is the same place; no task, default or
// script stages into C:\ProgramData again (the old folders there are never read: README "The stage directory").
func TestStageDirectory(t *testing.T) {
	t.Parallel()
	b, err := fs.ReadFile(tree, roleDir+"/defaults/main.yml")
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := yaml.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	if d["github_runner_wsl_stage_dir"] != `C:\nova-stage` || d["github_runner_wsl_stage_unix"] != "/mnt/c/nova-stage" {
		t.Errorf("stage dir %v, WSL path %v: want C:\\nova-stage, /mnt/c/nova-stage", d["github_runner_wsl_stage_dir"], d["github_runner_wsl_stage_unix"])
	}
	for _, k := range []string{"github_runner_wsl_script", "github_runner_wsl_ci_script"} {
		if s, _ := d[k].(string); !strings.HasPrefix(s, `{{ github_runner_wsl_stage_dir }}\`) {
			t.Errorf("%s = %q is not in the stage directory", k, s)
		}
	}
	err = fs.WalkDir(tree, roleDir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		f, err := fs.ReadFile(tree, p)
		if err != nil {
			return err
		}
		for _, l := range strings.Split(string(f), "\n") {
			c := strings.TrimSpace(l)
			if strings.HasPrefix(c, "#") {
				continue
			}
			if strings.Contains(strings.ToLower(c), `programdata\nova-`) {
				t.Errorf("%s stages in C:\\ProgramData: %s", p, c)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestStageCheckScript: the check refuses with exit 14, never repairs (no ACL is set but on the directory it creates,
// sealed in the one call), and its encoded command line fits Windows' 32767-character limit with the role's four
// scripts named.
func TestStageCheckScript(t *testing.T) {
	t.Parallel()
	b, err := fs.ReadFile(tree, roleDir+"/files/stage-check.ps1")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"\n  exit 14\n}\n", "[IO.Directory]::CreateDirectory($Stage, $ds)", "$ds.SetAccessRuleProtection($true, $false)",
		"[NovaStage]::Check($disk, $Stage, $owners, [string[]]$Names, [string[]]$Shas)", "if ($why) {"} {
		if !strings.Contains(s, want) {
			t.Errorf("stage-check.ps1 lacks %q", want)
		}
	}
	for _, never := range []string{"Set-Acl", "SetAccessControl", "icacls", "takeown", "Remove-Item -LiteralPath $"} {
		if strings.Contains(s, never) {
			t.Errorf("stage-check.ps1 contains %q: it never changes what it finds", never)
		}
	}
	sha := strings.Repeat("0", 64)
	head := "$Make = $false; $Stage = 'C:\\nova-stage'; $Names = @('runner-windows.ps1', 'nova-ci-rules.ps1', 'job-started.ps1', " +
		"'job-completed.ps1'); $Shas = @('" + strings.Join([]string{sha, sha, sha, sha}, "', '") + "')\n"
	u := utf16.Encode([]rune(head + s))
	raw := make([]byte, 2*len(u))
	for i, c := range u {
		raw[2*i], raw[2*i+1] = byte(c), byte(c>>8)
	}
	line := `"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -NoLogo -EncodedCommand ` +
		base64.StdEncoding.EncodeToString(raw)
	if len(line) > 32000 {
		t.Errorf("the stage check's command line is %d characters; Windows takes 32767: shorten stage-check.ps1", len(line))
	}
}

// TestNativeStageGuardsEveryElevatedCall: on a plain Windows host (tasks/windows-stage.yml) win_command cannot join two
// programs with &&, so every win_command that runs powershell.exe on a staged -File is the task straight after the
// stage check, under the same condition; and windows-stage.yml stages the very files it hashed.
func TestNativeStageGuardsEveryElevatedCall(t *testing.T) {
	t.Parallel()
	const check = "{{ github_runner_win_stage_check_argv }}"
	n := 0
	for file, tasks := range roleTasks(t) {
		for i, task := range tasks {
			wc, ok := task["ansible.windows.win_command"].(map[string]any)
			if !ok {
				continue
			}
			argv := text(t, wc["argv"])
			if !strings.Contains(argv, "-File") {
				continue
			}
			n++
			if !strings.Contains(argv, "github_runner_wsl_script") && !strings.Contains(argv, "github_runner_wsl_ci_script") {
				t.Errorf("%s %q: runs a file that is not staged: %s", file, task["name"], argv)
			}
			prev := map[string]any{}
			if i > 0 {
				prev = tasks[i-1]
			}
			pc, _ := prev["ansible.windows.win_command"].(map[string]any)
			if pc == nil || pc["argv"] != check {
				t.Errorf("%s %q: not straight after the stage check", file, task["name"])
			}
			if prev["when"] != nil && prev["when"] != task["when"] {
				t.Errorf("%s %q: the check before it runs under %v, the call under %v", file, task["name"], prev["when"], task["when"])
			}
		}
	}
	if n != 4 {
		t.Errorf("found %d elevated win_command calls; the plain Windows path has install, register, ensure and apply", n)
	}
	var hashed, staged string
	for _, task := range roleTasks(t)["windows-stage.yml"] {
		if task["ansible.builtin.stat"] != nil {
			hashed = text(t, task["loop"])
		}
		if task["ansible.windows.win_copy"] != nil {
			staged = text(t, task["loop"])
		}
	}
	if hashed == "" || hashed != staged {
		t.Errorf("windows-stage.yml: the files staged are not the files hashed:\n%s\n%s", hashed, staged)
	}
}
