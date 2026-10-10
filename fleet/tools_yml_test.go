package fleet

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestToolsYmlBuildsNovaSprintFromItsOwnRepo pins the rows of tools.yml that
// build nova-sprint from its own repository (docs/FLEET.md, "tools.yml", steps
// 2 and 3): nova-sprint is its own product (nova-tools PR 5309), so the play
// checks nova_sprint_repo out at nova_sprint_ref on the machine running it and
// builds ./cmd/nova-sprint there for every platform, stamped with the build's
// version, instead of taking it from the nova-tools checkout's cmd/. The build
// is staged in its OWN directory, never in the release stage whose bytes
// `release install` verifies against the release's SHA256SUMS: the two
// artifacts have different origins, and mixing them makes the install's
// checksum check refuse the stage instead of installing. The stage carries the
// sha256 the build play wrote beside the build, every machine verifies the
// bytes it staged against it before installation, the seat's candidate checks
// run the staged split build (the release stage's nova-sprint is never the
// candidate), and every release install in the play -- the install play's, and
// the seat window's on the coordinator, which installs instead of it -- is
// followed by a copy of that verified stage's nova-sprint into the bin
// directory by its name. Both of those copies set remote_src, so Ansible
// reads the stage on the managed machine and not on the controller.
// fleet/retired-tools.txt never names it.
func TestToolsYmlBuildsNovaSprintFromItsOwnRepo(t *testing.T) {
	t.Parallel()
	var vars map[string]any
	require.NoError(t, yaml.Unmarshal(readFleetFile(t, "group_vars", "all.yml"), &vars))
	var plays []map[string]any
	require.NoError(t, yaml.Unmarshal(readFleetFile(t, "tools.yml"), &plays))
	buildPlay := playNamed(t, plays, "the build, once per platform")
	installPlay := playNamed(t, plays, "the build in ~/.local/bin on every machine")
	seatPlay := playNamed(t, plays, "the seat adopts the build")
	// The candidate checks, the replacement detection and the window are nested
	// in blocks; flattening keeps them in document order with the top-level tasks.
	build := playTasks(t, buildPlay)
	install := flattenTasks(t, playTasks(t, installPlay))
	seat := flattenTasks(t, playTasks(t, seatPlay))
	installVars, _ := installPlay["vars"].(map[string]any)
	seatVars, _ := seatPlay["vars"].(map[string]any)
	sprintFile := str(installVars["tools_sprint_file"])
	sprintStage := str(installVars["tools_sprint_stage"])

	checkout := taskIndex(build, func(task map[string]any) bool {
		git, _ := task["ansible.builtin.git"].(map[string]any)
		return git["repo"] == "{{ nova_sprint_repo }}" && git["version"] == "{{ nova_sprint_ref }}" && git["dest"] == "{{ nova_sprint_src }}"
	})
	compile := taskIndex(build, func(task map[string]any) bool {
		cmd, _ := task["ansible.builtin.command"].(map[string]any)
		argv := strings.Join(stringList(cmd["argv"]), " ")
		env, _ := task["environment"].(map[string]any)
		return cmd["chdir"] == "{{ nova_sprint_src }}" && strings.Contains(argv, "nice -n 19 go build") &&
			strings.Contains(argv, "go build -trimpath -ldflags -s -w -X main.version={{ nova_version }} -o ") &&
			strings.HasSuffix(argv, "./cmd/nova-sprint") &&
			strings.Contains(argv, "-o {{ nova_sprint_out }}/{{ item }}/nova-sprint{{ '.exe' if item.startswith('windows-') else '' }} ./cmd/nova-sprint") && env["CGO_ENABLED"] == "0"
	})
	// The build play hashes each platform's build and writes the checksum
	// beside it, so every machine can verify the bytes it stages.
	hashed := taskIndex(build, func(task map[string]any) bool {
		st, _ := task["ansible.builtin.stat"].(map[string]any)
		return strings.Contains(str(st["path"]), "{{ nova_sprint_out }}/{{ item }}/nova-sprint") &&
			st["get_checksum"] == true && st["checksum_algorithm"] == "sha256"
	})
	hashedFile := taskIndex(build, func(task map[string]any) bool {
		cp, _ := task["ansible.builtin.copy"].(map[string]any)
		return str(cp["dest"]) == "{{ nova_sprint_out }}/{{ item.item }}/SHA256SUMS" &&
			strings.Contains(str(cp["content"]), "item.stat.checksum")
	})
	release := taskIndex(install, func(task map[string]any) bool {
		cmd, _ := task["ansible.builtin.command"].(map[string]any)
		return slices.Contains(stringList(cmd["argv"]), "install")
	})
	// The split build goes to its own stage, never into the release stage the
	// release's SHA256SUMS describes; the stage is written on every machine.
	stagedSplit := taskIndex(install, func(task map[string]any) bool {
		cp, _ := task["ansible.builtin.copy"].(map[string]any)
		return str(cp["src"]) == "{{ nova_sprint_out }}/{{ nova_platform }}/{{ tools_sprint_file }}" &&
			str(cp["dest"]) == "{{ tools_sprint_stage }}/{{ tools_sprint_file }}" && cp["mode"] == "0755"
	})
	stagedSums := taskIndex(install, func(task map[string]any) bool {
		cp, _ := task["ansible.builtin.copy"].(map[string]any)
		return str(cp["src"]) == "{{ nova_sprint_out }}/{{ nova_platform }}/SHA256SUMS" &&
			str(cp["dest"]) == "{{ tools_sprint_stage }}/SHA256SUMS"
	})
	// The split stage is a sibling of the release stage, so the release
	// directory task creates only tools_stage; without its own directory task
	// the first copy into the fresh stage fails with "Destination directory
	// ... does not exist" before the split build is verified or installed.
	sprintDir := taskIndex(install, func(task map[string]any) bool {
		f, _ := task["ansible.builtin.file"].(map[string]any)
		return str(f["path"]) == "{{ tools_sprint_stage }}" && f["state"] == "directory"
	})
	// The staged bytes are checked against the build's checksum before anything
	// installs them.
	verified := taskIndex(install, func(task map[string]any) bool {
		a, _ := task["ansible.builtin.assert"].(map[string]any)
		that := str(a["that"])
		return strings.Contains(that, "tools_sprint_bytes") && strings.Contains(that, "tools_sprint_recorded")
	})
	overlaid := taskIndex(install, func(task map[string]any) bool {
		cp, _ := task["ansible.builtin.copy"].(map[string]any)
		return str(cp["dest"]) == "{{ tools_stage }}/{{ tools_sprint_file }}"
	})
	// The candidate's nova-sprint is the split build, selected by its own
	// variable; its other tools (nova-friend) stay the release stage's.
	candidateSprint := taskIndex(install, func(task map[string]any) bool {
		sf, _ := task["ansible.builtin.set_fact"].(map[string]any)
		return strings.Contains(str(sf["seat_sprint"]), "tools_sprint_stage") &&
			strings.Contains(str(sf["seat_sprint"]), "tools_sprint_file")
	})
	candidateRelease := taskIndex(install, func(task map[string]any) bool {
		sf, _ := task["ansible.builtin.set_fact"].(map[string]any)
		return strings.Contains(str(sf["seat_cand"]), "tools_stage") && !strings.Contains(str(sf["seat_cand"]), "tools_sprint_file")
	})
	candidateLive := taskIndex(install, func(task map[string]any) bool {
		cmd, _ := task["ansible.builtin.command"].(map[string]any)
		argv := strings.Join(stringList(cmd["argv"]), " ")
		return strings.Contains(argv, "[seat_sprint, 'live'") && !strings.Contains(argv, "seat_cand ~ '/nova-sprint'")
	})
	// The copy reads the verified stage on the managed machine, never the
	// controller's build out: remote_src is part of the copy's identity here,
	// so a copy that lost it no longer matches and every row that names
	// `copied` reads red (docs/FLEET.md, "tools.yml", step 3).
	copied := taskIndex(install, func(task map[string]any) bool {
		cp, _ := task["ansible.builtin.copy"].(map[string]any)
		return str(cp["src"]) == "{{ tools_sprint_stage }}/{{ tools_sprint_file }}" &&
			str(cp["dest"]) == "{{ nova_bin_dir }}/{{ tools_sprint_file }}" && cp["mode"] == "0755" &&
			cp["remote_src"] == true
	})
	seatRelease := taskIndex(seat, func(task map[string]any) bool {
		cmd, _ := task["ansible.builtin.command"].(map[string]any)
		return slices.Contains(stringList(cmd["argv"]), "install")
	})
	seatCopied := taskIndex(seat, func(task map[string]any) bool {
		cp, _ := task["ansible.builtin.copy"].(map[string]any)
		return str(cp["src"]) == "{{ tools_sprint_stage }}/{{ tools_sprint_file }}" &&
			str(cp["dest"]) == "{{ nova_bin_dir }}/{{ tools_sprint_file }}" && cp["mode"] == "0755" &&
			cp["remote_src"] == true
	})
	// The window's replacement detection lists the release stage WITHOUT its
	// nova-sprint (that stale copy must not open the window on every run) and
	// the split build's own stage, whose bytes are the desired ones.
	stageFiles := taskIndex(install, func(task map[string]any) bool {
		f, _ := task["ansible.builtin.find"].(map[string]any)
		return str(f["paths"]) == "{{ seat_cand }}" && str(task["register"]) == "seat_stage_files" &&
			slices.Contains(stringList(f["excludes"]), "{{ tools_sprint_file }}")
	})
	sprintFiles := taskIndex(install, func(task map[string]any) bool {
		f, _ := task["ansible.builtin.find"].(map[string]any)
		return str(f["paths"]) == "{{ tools_sprint_stage }}" && str(task["register"]) == "seat_sprint_files"
	})
	binFiles := taskIndex(install, func(task map[string]any) bool {
		f, _ := task["ansible.builtin.find"].(map[string]any)
		return str(f["paths"]) == "{{ nova_bin_dir }}" && str(task["register"]) == "seat_bin_files"
	})
	replaces := taskIndex(install, func(task map[string]any) bool {
		sf, _ := task["ansible.builtin.set_fact"].(map[string]any)
		return strings.Contains(str(sf["seat_replaces"]), "seat_cand_files") &&
			strings.Contains(str(sf["seat_replaces"]), "seat_bin_files")
	})
	installReceipt := taskIndex(install, func(task map[string]any) bool {
		return strings.Contains(debugMsg(task), "SPRINT host=") && strings.Contains(debugMsg(task), "tools_sprint.changed")
	})
	seatReceipt := taskIndex(seat, func(task map[string]any) bool {
		return strings.Contains(debugMsg(task), "SPRINT host=") && strings.Contains(debugMsg(task), "seat_sprint.changed")
	})

	cases := []struct {
		name string
		ok   bool
	}{
		{"nova_sprint_repo is mas-bandwidth/nova-sprint", vars["nova_sprint_repo"] == "https://github.com/mas-bandwidth/nova-sprint.git"},
		{"nova_sprint_ref is main", vars["nova_sprint_ref"] == "main"},
		{"the installed file uses the platform executable name", sprintFile == "nova-sprint{{ '.exe' if nova_platform.startswith('windows-') else '' }}"},
		{"the checkout and the builds live under nova_release_out", strings.HasPrefix(str(vars["nova_sprint_src"]), "{{ nova_release_out }}/") &&
			strings.HasPrefix(str(vars["nova_sprint_out"]), "{{ nova_release_out }}/")},
		{"the split stage is its own directory under nova_release_dir, apart from the release stage", sprintStage == "{{ nova_release_dir }}/{{ nova_version }}/{{ nova_platform }}-nova-sprint"},
		{"the seat play names the same split stage", str(seatVars["tools_sprint_stage"]) == sprintStage},
		{"the build play checks out the nova-sprint repository", checkout >= 0},
		{"the build play builds ./cmd/nova-sprint in that checkout, after it", compile > checkout && checkout >= 0},
		{"the build play hashes each platform's split build", hashed >= 0 && hashedFile >= 0},
		{"the release stage never receives the split artifact", overlaid == -1},
		{"the split build is staged in its own directory on every machine",
			stagedSplit >= 0 && stagedSums > stagedSplit && !excludesCoordinators(install[stagedSplit]) &&
				strings.Contains(strings.Join(whenLines(install[stagedSplit]), " "), "not ansible_check_mode")},
		{"the split stage's directory is created before its first copy", sprintDir >= 0 && stagedSplit > sprintDir},
		{"the staged split build is verified against the build's checksum before installation",
			verified > stagedSums && stagedSums >= 0},
		{"the install play's copy follows the verification and the release's own install",
			verified >= 0 && copied > verified && copied > release && release >= 0},
		{"the install play's copy reads the verified stage on the managed machine, not the controller's build out", copied >= 0},
		{"the desired nova-sprint is the split build's stage, never the release stage's", candidateSprint >= 0},
		{"the candidate's directory for its other tools is the release stage", candidateRelease >= 0},
		{"the candidate checks run the selected split build", candidateLive >= 0},
		{"the window lists the release stage without nova-sprint and the split build's own stage",
			stageFiles >= 0 && sprintFiles >= 0 && binFiles >= 0 && replaces > stageFiles && replaces > sprintFiles && replaces > binFiles},
		{"the install play's release install sends coordinators to the seat window", release >= 0 && excludesCoordinators(install[release])},
		{"the install play's copy sends coordinators to the seat window", copied >= 0 && excludesCoordinators(install[copied])},
		{"the install play's copy still waits out check mode", copied >= 0 && strings.Contains(strings.Join(whenLines(install[copied]), " "), "not ansible_check_mode")},
		{"the seat play installs through the release's own install", seatRelease >= 0},
		{"the seat play places nova-sprint after that install", seatCopied > seatRelease && seatRelease >= 0},
		{"the seat window's copy is the one that installs on the coordinator", seatCopied >= 0 && !excludesCoordinators(seat[seatCopied])},
		{"both copies record a receipt", installReceipt >= 0 && seatReceipt >= 0},
		{"fleet/retired-tools.txt never names nova-sprint", !slices.Contains(strings.Fields(string(readFleetFile(t, "retired-tools.txt"))), "nova-sprint")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.True(t, tc.ok)
		})
	}
}

func readFleetFile(t *testing.T, path ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(path...))
	require.NoError(t, err)
	return b
}

func playNamed(t *testing.T, plays []map[string]any, name string) map[string]any {
	t.Helper()
	for _, p := range plays {
		if p["name"] == name {
			return p
		}
	}
	require.Failf(t, "no play", "tools.yml has no play named %q", name)
	return nil
}

func playTasks(t *testing.T, play map[string]any) []map[string]any {
	t.Helper()
	var tasks []map[string]any
	entries, ok := play["tasks"].([]any)
	require.True(t, ok, "play has no task list")
	for _, e := range entries {
		task, ok := e.(map[string]any)
		require.True(t, ok, "play task is not a mapping")
		tasks = append(tasks, task)
	}
	return tasks
}

// taskIndex is the index of the first task match accepts, or -1.
func taskIndex(tasks []map[string]any, match func(map[string]any) bool) int {
	return slices.IndexFunc(tasks, match)
}

// whenLines is a task's when as written: the scalar form as one line, the list
// form as its lines, and no condition as none.
func whenLines(task map[string]any) []string {
	switch w := task["when"].(type) {
	case string:
		return []string{w}
	case []any:
		var out []string
		for _, e := range w {
			out = append(out, str(e))
		}
		return out
	case nil:
		return nil
	default:
		return []string{str(w)}
	}
}

// debugMsg is a debug task's message, or the empty string for another task.
func debugMsg(task map[string]any) string {
	dbg, _ := task["ansible.builtin.debug"].(map[string]any)
	return str(dbg["msg"])
}

// excludesCoordinators is true when a task's when keeps the coordinator out of
// it: the coordinator installs inside the seat play's window, not the install
// play (docs/FLEET.md, "tools.yml", step 3), so the install play's copy leaves
// the coordinator's nova-sprint to the guarded copy in that window.
func excludesCoordinators(task map[string]any) bool {
	return slices.ContainsFunc(whenLines(task), func(line string) bool {
		return strings.Contains(line, "inventory_hostname not in") && strings.Contains(line, "coordinator")
	})
}

// flattenTasks splices every task's nested block in at the task's place, so
// document order survives for tasks a play nests: the install play's candidate
// checks and the seat play's release install and nova-sprint copy are all
// inside blocks.
func flattenTasks(t *testing.T, tasks []map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, task := range tasks {
		out = append(out, task)
		entries, ok := task["block"].([]any)
		if !ok {
			continue
		}
		var nested []map[string]any
		for _, e := range entries {
			m, ok := e.(map[string]any)
			require.True(t, ok, "a nested block task is not a mapping")
			nested = append(nested, m)
		}
		out = append(out, flattenTasks(t, nested)...)
	}
	return out
}

func stringList(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			out = append(out, str(e))
		}
		return out
	default:
		return nil
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
