package ci

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// fleetbootstrap_test.go holds fleet/bootstrap.yml, the first of the fleet's two
// phases (docs/FLEET.md, "From a fresh host"), to its setup contract.
//
// The contract half reads the play, its role (fleet/roles/bootstrap) and the
// docs as text, with no ansible and no host:
//
//   - each part of the setup contract (the base, the harness dependencies, the
//     tool versions, Tailscale, the seat, PostgreSQL, Redis persistence, the
//     supervised services, the backups) is a tag of the play, a row of
//     FLEET.md's contract table, and a task file that does it;
//   - no task uses ansible's shell, script or raw module, and every command task
//     says how its change is read (changed_when), as the steady-state plays;
//   - every task that reads a secret from the environment is no_log;
//   - every template a task names exists, and every template is rendered;
//   - every nova_* the phase reads is a default of fleet/group_vars/all.yml, a
//     fact the phase sets, or an owner's input the first play names; an
//     owner's input has no default, and FLEET.md's inputs table names each one;
//   - the store's Redis is `nova-redis serve` on its own directory (the
//     persistence is serve's), with the distro's redis-server masked.
//
// The host half is the acceptance of both phases on a disposable host the
// environment names (docs/FLEET.md, "The acceptance on a disposable host"):
// NOVA_FLEET_DISPOSABLE_INVENTORY, a bootstrap inventory of one host that is
// fleet_store and a member; NOVA_FLEET_DISPOSABLE_STORE, its store as
// <host>:<port>; the play's secrets and NOVA_REDIS_PASSWORD in the
// environment; the host's seat holding the friends' NOVA_BUS_ADA_PASSWORD and
// NOVA_BUS_BOB_PASSWORD besides the stores'. Provision, a second run with
// changed=0, the hand-off and the steady state from nova-config's inventory,
// a test message delivered and receipted, a bounded worker job, every play
// again with changed=0, then a managed setting drifted by hand, --check
// --diff showing it, the run converging it, and --check clean
// (bootstrapOnADisposableHost). Without that inventory and ansible-playbook
// it is skipped, by name.
func TestFleetPlaysConvergeAFreshHostIdempotently(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	t.Run("contract", func(t *testing.T) {
		t.Parallel()
		src := readBootstrapSource(t, root)
		for _, p := range bootstrapProblems(src) {
			assert.Fail(t, p)
		}
	})
	t.Run("rule reads the shapes", func(t *testing.T) {
		t.Parallel()
		base := readBootstrapSource(t, root)
		require.Empty(t, bootstrapProblems(base))
		for _, tc := range []struct {
			name string
			edit func(*bootstrapSource)
			want string
		}{
			{"shell", func(s *bootstrapSource) {
				s.tasks["redis.yml"] += "- name: by hand\n  ansible.builtin.shell: redis-cli ping\n"
			}, "runs ansible.builtin.shell"},
			{"command without changed_when", func(s *bootstrapSource) {
				s.tasks["redis.yml"] += "- name: bare\n  ansible.builtin.command: { argv: [true] }\n"
			}, `command task "bare" has no changed_when`},
			{"secret logged", func(s *bootstrapSource) {
				s.tasks["postgres.yml"] = strings.Replace(s.tasks["postgres.yml"], "  no_log: true\n", "", 1)
			}, "reads the environment and is not no_log"},
			{"undefined var", func(s *bootstrapSource) {
				s.tasks["backup.yml"] += "- name: x\n  ansible.builtin.debug: { msg: \"{{ nova_bootstrap_typo }}\" }\n"
			}, "reads nova_bootstrap_typo"},
			{"missing template", func(s *bootstrapSource) { delete(s.templates, "nova-store-redis.service.j2") }, "nova-store-redis.service.j2, which does not exist"},
			{"contract part dropped", func(s *bootstrapSource) {
				s.play = strings.ReplaceAll(s.play, "backup]", "nobackup]")
			}, `the contract's "backup" is no tag of bootstrap.yml`},
			{"a part's play gathers no facts", func(s *bootstrapSource) {
				s.play = strings.Replace(s.play, "  gather_facts: true\n  gather_subset: [min]\n  tags: [seat]\n", "  gather_facts: false\n  tags: [seat]\n", 1)
			}, `play "the seat on every host" runs the role and gathers no facts`},
			{"the unit's address carried from the tailscale part", func(s *bootstrapSource) {
				s.tasks["redis.yml"] = strings.Replace(s.tasks["redis.yml"], "import_tasks: tailnet.yml", "debug: { msg: tailnet }", 1)
			}, "redis.yml does not read the tailnet address itself"},
			{"input given a default", func(s *bootstrapSource) { s.defaults["nova_bootstrap_backup_dir"] = true }, "nova_bootstrap_backup_dir is the owner's input and has a default"},
			{"input not documented", func(s *bootstrapSource) {
				s.docs = strings.ReplaceAll(s.docs, "`nova_bootstrap_seat_keys`", "the keys")
			}, "FLEET.md's inputs do not name nova_bootstrap_seat_keys"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				s := base.clone()
				tc.edit(&s)
				assert.Contains(t, strings.Join(bootstrapProblems(s), "\n"), tc.want)
			})
		}
	})
	t.Run("on a disposable host", func(t *testing.T) {
		t.Parallel()
		if os.Getenv("NOVA_FLEET_DISPOSABLE_AUTHORIZED") != "yes-disposable" {
			t.Skip("NOVA_FLEET_DISPOSABLE_AUTHORIZED=yes-disposable is required for owner-authorized scratch acceptance")
		}
		inv := os.Getenv("NOVA_FLEET_DISPOSABLE_INVENTORY")
		if inv == "" {
			t.Skip("NOVA_FLEET_DISPOSABLE_INVENTORY names no bootstrap inventory of a disposable host (docs/FLEET.md, \"From a fresh host\")")
		}
		playbook, err := exec.LookPath("ansible-playbook")
		if err != nil {
			t.Skip("ansible-playbook is not installed on this machine")
		}
		bootstrapOnADisposableHost(t, root, inv, playbook)
	})
}

// bootstrapContract is the setup contract: each part's tag, and the task file
// of fleet/roles/bootstrap that does it.
var bootstrapContract = map[string]string{
	"base": "base.yml", "harness": "base.yml", "tools": "", "tailscale": "tailscale.yml", "seat": "seat.yml",
	"postgres": "postgres.yml", "redis": "redis.yml", "services": "redis.yml", "backup": "backup.yml",
}

// bootstrapSets are the facts the phase sets that a later task reads.
var bootstrapSets = map[string]bool{"nova_bootstrap_tailnet_ip": true}

type bootstrapSource struct {
	play      string
	tasks     map[string]string // role task file -> text
	templates map[string]string
	defaults  map[string]bool // the nova_* group_vars/all.yml defines
	docs      string          // docs/FLEET.md
}

func (s bootstrapSource) clone() bootstrapSource {
	c := bootstrapSource{play: s.play, docs: s.docs, tasks: map[string]string{}, templates: map[string]string{}, defaults: map[string]bool{}}
	for k, v := range s.tasks {
		c.tasks[k] = v
	}
	for k, v := range s.templates {
		c.templates[k] = v
	}
	for k, v := range s.defaults {
		c.defaults[k] = v
	}
	return c
}

func readBootstrapSource(t *testing.T, root string) bootstrapSource {
	t.Helper()
	read := func(p string) string {
		b, err := os.ReadFile(filepath.Join(root, p))
		require.NoError(t, err)
		return string(b)
	}
	s := bootstrapSource{play: read("fleet/bootstrap.yml"), docs: read("docs/FLEET.md"), tasks: map[string]string{}, templates: map[string]string{}, defaults: map[string]bool{}}
	for dir, into := range map[string]map[string]string{"tasks": s.tasks, "templates": s.templates} {
		files, err := filepath.Glob(filepath.Join(root, "fleet", "roles", "bootstrap", dir, "*"))
		require.NoError(t, err)
		require.NotEmpty(t, files, dir)
		for _, f := range files {
			into[filepath.Base(f)] = read(filepath.Join("fleet", "roles", "bootstrap", dir, filepath.Base(f)))
		}
	}
	var vars map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(read("fleet/group_vars/all.yml")), &vars))
	for k := range vars {
		s.defaults[k] = true
	}
	return s
}

var (
	reBootstrapInput = regexp.MustCompile(`\b(nova_[a-z0-9_]+) is not defined`)
	reTemplateSrc    = regexp.MustCompile(`\b([a-z0-9.-]+\.j2)\b`)
)

// bootstrapInputs are the owner's inputs the first play names: each variable
// it refuses the run without. The hosts' own (nova_seat, ansible_user) are
// named by the inventory's example and FLEET.md.
func bootstrapInputs(play string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range reBootstrapInput.FindAllStringSubmatch(play, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// bootstrapTasks flattens a task list, block, rescue and always included.
func bootstrapTasks(v any) []map[string]any {
	var out []map[string]any
	for _, t := range fleetList(v) {
		out = append(out, t)
		for _, k := range []string{"block", "rescue", "always"} {
			out = append(out, bootstrapTasks(t[k])...)
		}
	}
	return out
}

// bootstrapProblems is every way the phase breaks its contract, one line each.
func bootstrapProblems(s bootstrapSource) []string {
	var out []string
	var plays []map[string]any
	if err := yaml.Unmarshal([]byte(s.play), &plays); err != nil {
		return []string{fmt.Sprintf("bootstrap.yml: not YAML: %v", err)}
	}
	tags := map[string]bool{}
	for _, p := range plays {
		for _, tag := range fleetListAny(p["tags"]) {
			tags[fmt.Sprint(tag)] = true
		}
		for _, t := range bootstrapTasks(p["tasks"]) {
			for _, tag := range fleetListAny(t["tags"]) {
				tags[fmt.Sprint(tag)] = true
			}
		}
		// A play that runs a part of the role gathers its own facts: under
		// --tags <part> no earlier play has gathered them (nova_home reads
		// ansible_env), and the part fails on a run limited to it.
		body, _ := yaml.Marshal(p["tasks"])
		if strings.Contains(string(body), "name: bootstrap") && p["gather_facts"] != true {
			out = append(out, fmt.Sprintf("bootstrap.yml: play %q runs the role and gathers no facts, so --tags on it alone fails", fmt.Sprint(p["name"])))
		}
	}
	for _, part := range fleetKeys(bootstrapContract) {
		if !tags[part] {
			out = append(out, fmt.Sprintf("the contract's %q is no tag of bootstrap.yml", part))
		}
		if !strings.Contains(s.docs, "| `"+part+"`") && !strings.Contains(s.docs, ", `"+part+"` |") {
			out = append(out, fmt.Sprintf("FLEET.md's contract table names no part tagged %q", part))
		}
		if f := bootstrapContract[part]; f != "" && s.tasks[f] == "" {
			out = append(out, fmt.Sprintf("the contract's %q is done by roles/bootstrap/tasks/%s, which does not exist", part, f))
		}
	}
	if !strings.Contains(s.docs, "## From a fresh host") || !strings.Contains(s.docs, "fleet/bootstrap.yml") {
		out = append(out, "FLEET.md has no section \"From a fresh host\" naming fleet/bootstrap.yml")
	}

	inputs := bootstrapInputs(s.play)
	known := map[string]bool{"nova_seat": true}
	for k := range s.defaults {
		known[k] = true
	}
	for k := range bootstrapSets {
		known[k] = true
	}
	for _, in := range inputs {
		known[in] = true
		if s.defaults[in] {
			out = append(out, in+" is the owner's input and has a default in group_vars/all.yml")
		}
		if !strings.Contains(s.docs, "`"+in+"`") {
			out = append(out, "FLEET.md's inputs do not name "+in)
		}
	}
	for _, env := range []string{"nova_bootstrap_tailscale_authkey_env", "nova_pg_password_key"} {
		if !strings.Contains(s.play, "lookup('env', "+env+")") {
			out = append(out, "bootstrap.yml's first play does not refuse a run whose environment lacks "+env)
		}
	}

	rendered := map[string]bool{}
	texts := map[string]string{"bootstrap.yml": s.play}
	for name, text := range s.tasks {
		texts["roles/bootstrap/tasks/"+name] = text
		var tasks []any
		if err := yaml.Unmarshal([]byte(text), &tasks); err != nil {
			out = append(out, fmt.Sprintf("roles/bootstrap/tasks/%s: not YAML: %v", name, err))
			continue
		}
		for _, t := range bootstrapTasks(tasks) {
			task := fmt.Sprint(t["name"])
			for _, bad := range []string{"ansible.builtin.shell", "ansible.builtin.script", "ansible.builtin.raw", "shell", "script", "raw"} {
				if _, ok := t[bad]; ok {
					out = append(out, fmt.Sprintf("%s: task %q runs %s", name, task, bad))
				}
			}
			if _, ok := t["ansible.builtin.command"]; ok {
				if _, ok := t["changed_when"]; !ok {
					out = append(out, fmt.Sprintf("%s: command task %q has no changed_when", name, task))
				}
			}
			if _, isBlock := t["block"]; !isBlock {
				body, _ := yaml.Marshal(t)
				if strings.Contains(string(body), "lookup('env'") && t["no_log"] != true {
					out = append(out, fmt.Sprintf("%s: task %q reads the environment and is not no_log", name, task))
				}
			}
			if tm, ok := t["ansible.builtin.template"]; ok {
				body, _ := yaml.Marshal(tm)
				srcs := reTemplateSrc.FindAllString(string(body), -1)
				if lp, ok := t["loop"]; ok {
					lb, _ := yaml.Marshal(lp)
					srcs = append(srcs, reTemplateSrc.FindAllString(string(lb), -1)...)
				}
				for _, src := range srcs {
					if _, ok := s.templates[src]; !ok {
						out = append(out, fmt.Sprintf("%s: task %q names %s, which does not exist", name, task, src))
					}
					rendered[src] = true
				}
			}
		}
	}
	for name, text := range s.templates {
		texts["roles/bootstrap/templates/"+name] = text
		if !rendered[name] {
			out = append(out, fmt.Sprintf("roles/bootstrap/templates/%s is rendered by no task", name))
		}
	}
	for _, name := range fleetKeys(texts) {
		for _, v := range reNovaVar.FindAllString(uncommented([]byte(texts[name])), -1) {
			if !known[v] {
				out = append(out, fmt.Sprintf("%s reads %s, which neither group_vars, a set_fact nor the owner's inputs define", name, v))
				known[v] = true // once
			}
		}
	}

	unit := s.templates["nova-store-redis.service.j2"]
	for _, w := range []string{`"nova-redis" "serve"`, `"--dir" "{{ nova_bootstrap_redis_dir }}"`, `"--only" "NOVA_REDIS_PASSWORD"`, "Restart=always"} {
		if unit != "" && !strings.Contains(strings.ReplaceAll(unit, `{{ nova_bin_dir }}/nova-redis`, "nova-redis"), w) {
			out = append(out, "nova-store-redis.service.j2 does not say "+w)
		}
	}
	if !strings.Contains(s.tasks["redis.yml"], "masked: true") {
		out = append(out, "redis.yml does not mask the distro's redis-server")
	}
	// The unit binds the tailnet address, read where it is used: a run that
	// skips the tailscale part renders the unit a whole run does.
	for _, f := range []string{"tailscale.yml", "redis.yml", "receipt.yml"} {
		if !strings.Contains(s.tasks[f], "import_tasks: tailnet.yml") {
			out = append(out, f+" does not read the tailnet address itself (import_tasks: tailnet.yml)")
		}
	}
	return out
}

func fleetListAny(v any) []any {
	l, _ := v.([]any)
	return l
}

// bootstrapOnADisposableHost is the acceptance on a real host
// (fleet/testdata/acceptance.yml says each step): provision; rerun with zero
// changes; the hand-off's rows; the steady state from the inventory
// nova-config prints; a test message delivered and receipted; a bounded
// worker job; every play run again with zero changes; a managed setting
// drifted by hand, seen by --check --diff, converged, and --check clean.
func bootstrapOnADisposableHost(t *testing.T, root, inv, playbook string) {
	dir := t.TempDir()
	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, playbook, args...)
		cmd.WaitDelay = 10 * time.Second
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "ANSIBLE_INVENTORY_UNPARSED_FAILED=true",
			"ANSIBLE_HOME="+filepath.Join(dir, "ansible"), "ANSIBLE_LOCAL_TEMP="+filepath.Join(dir, "ansible", "tmp"))
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	build := []string{"-e", "nova_version=" + fleetEnvOr("NOVA_FLEET_DISPOSABLE_VERSION", "v0.0.0-bootstrap"), "-e", "nova_source=" + root,
		"-e", "nova_release_out=" + filepath.Join(dir, "release"),
		"-e", `{"nova_release_gate_args": ["--no-dogfood-gate", "--reason", "the disposable-host acceptance of bootstrap.yml"]}`}
	play := func(inventory, name string, extra ...string) string {
		t.Helper()
		out, err := run(append([]string{"-i", inventory, filepath.Join(root, "fleet", name)}, extra...)...)
		require.NoError(t, err, out)
		return out
	}
	bootstrap := func(extra ...string) string {
		t.Helper()
		return play(inv, "bootstrap.yml", append(append([]string{}, build...), extra...)...)
	}
	clean := regexp.MustCompile(`(?m)^\S+\s+: ok=\d+\s+changed=0 `)
	changed := regexp.MustCompile(`(?m)^\S+\s+: ok=\d+\s+changed=[1-9]`)

	first := bootstrap()
	assert.Regexp(t, `BOOTSTRAP host=\S+ store=yes tailnet=100\.`, first)
	assert.NotContains(t, first, "sops=none")
	assert.NotContains(t, first, "nova=none")
	second := bootstrap()
	assert.Regexp(t, clean, second, "a second run changed something")
	assert.NotRegexp(t, changed, second)

	// The hand-off: the rows, then the steady state from the inventory
	// nova-config prints, with the group_vars FLEET.md has the owner write.
	acceptance := func(tag string, extra ...string) string {
		t.Helper()
		return play(inv, filepath.Join("testdata", "acceptance.yml"), append([]string{"--tags", tag}, extra...)...)
	}
	acceptance("rows")
	steady := bootstrapSteadyInventory(t, root, dir, inv)
	steadyPlays := func() []string {
		t.Helper()
		return []string{play(steady, "redis.yml"), play(steady, "tools.yml", build...), play(steady, "loops.yml")}
	}
	steadyPlays()
	assert.Regexp(t, `DELIVERED id=\S+ from=ada to=bob receipted=yes owed=0`, acceptance("deliver"))
	harness := bootstrapLinuxBinary(t, root, dir, "./cmd/nova-swarm/testdata/fakeharness")
	assert.Contains(t, acceptance("work", "-e", "acceptance_harness="+harness), "WORKED NATIVE OK label=finishes NATIVE INCOMPLETE label=outlives")

	// Every play again: nothing changes.
	assert.Regexp(t, clean, bootstrap(), "bootstrap.yml changed something on a converged fleet")
	assert.Regexp(t, clean, acceptance("rows"), "the hand-off's rows changed on a second run")
	for _, out := range steadyPlays() {
		assert.Regexp(t, clean, out, "a steady-state play changed something on a converged fleet")
		assert.NotRegexp(t, changed, out)
	}

	// The drift is made by hand, outside bootstrap.yml: a play of the test's own
	// adds a line to the store's Redis unit.
	const drift = "Environment=NOVA_DRIFT=by-hand"
	hand := filepath.Join(dir, "drift.yml")
	require.NoError(t, os.WriteFile(hand, []byte("- hosts: fleet_store\n  gather_facts: false\n  tasks:\n"+
		"    - ansible.builtin.lineinfile: { path: /etc/systemd/system/nova-store-redis.service, insertafter: '^Type=', line: "+drift+" }\n"+
		"      become: true\n"), 0o644))
	out, err := run("-i", inv, hand)
	require.NoError(t, err, out)

	check := bootstrap("--check", "--diff")
	assert.Regexp(t, changed, check, "--check does not see the drift")
	assert.Contains(t, check, "-"+drift, "--diff does not show the drift going")

	converge := bootstrap()
	assert.Regexp(t, changed, converge)
	after := bootstrap("--check", "--diff")
	assert.Regexp(t, clean, after, "the run did not converge the drift")
	assert.NotContains(t, after, drift)
}

// bootstrapSteadyInventory is the steady state's inventory for the disposable
// fleet: a wrapper running nova-config inventory (built from this checkout)
// against the bootstrapped store as its default user, whose password is
// NOVA_REDIS_PASSWORD in the test's environment, and beside it the
// group_vars/store_deployer.yml the hand-off has the owner write
// (docs/FLEET.md, "The hand-off to steady state").
func bootstrapSteadyInventory(t *testing.T, root, dir, inv string) string {
	t.Helper()
	store := os.Getenv("NOVA_FLEET_DISPOSABLE_STORE")
	require.NotEmpty(t, store, "NOVA_FLEET_DISPOSABLE_STORE names the store <host>:<port> the steady state reads (the fleet_store host of %s)", inv)
	require.NotEmpty(t, os.Getenv("NOVA_REDIS_PASSWORD"), "the steady state's inventory logs in as the store's default user: NOVA_REDIS_PASSWORD")
	config := filepath.Join(dir, "nova-config")
	cmd := exec.Command("go", "build", "-o", config, "./cmd/nova-config")
	cmd.Dir, cmd.Env = root, goenv.Clean(os.Environ())
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	steady := filepath.Join(dir, "steady")
	require.NoError(t, os.MkdirAll(filepath.Join(steady, "group_vars"), 0o755))
	wrapper := filepath.Join(steady, "nova-inventory")
	require.NoError(t, os.WriteFile(wrapper, []byte("#!/bin/sh\nexec env NOVA_SPRINT_REDIS="+store+
		" NOVA_SPRINT_REDIS_USER=default NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_PASSWORD "+config+" inventory \"$@\"\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(steady, "group_vars", "store_deployer.yml"), []byte(
		"nova_redis_admin_user: default\nnova_redis_admin_password_key: NOVA_REDIS_PASSWORD\n"+
			"nova_redis_user_password_keys: \"{{ nova_bootstrap_redis_user_password_keys }}\"\n"), 0o644))
	return wrapper
}

// bootstrapLinuxBinary builds a package for the disposable host: linux, on
// NOVA_FLEET_DISPOSABLE_GOARCH (else this machine's architecture).
func bootstrapLinuxBinary(t *testing.T, root, dir, pkg string) string {
	t.Helper()
	bin := filepath.Join(dir, filepath.Base(pkg))
	cmd := exec.Command("go", "build", "-o", bin, pkg)
	cmd.Dir = root
	cmd.Env = append(goenv.Clean(os.Environ()), "GOOS=linux", "GOARCH="+fleetEnvOr("NOVA_FLEET_DISPOSABLE_GOARCH", runtime.GOARCH), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return bin
}

func fleetEnvOr(name, or string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return or
}
