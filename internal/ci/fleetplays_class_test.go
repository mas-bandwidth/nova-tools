package ci

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
)

// fleetplays_class_test.go is the fleet-plays rule (docs/SPEC-CI.md,
// `fleet-plays`): the plays that put a fleet in its configured state
// (fleet/tools.yml, fleet/redis.yml, fleet/loops.yml) read their values from
// the inventory `nova-config inventory` prints and from fleet/group_vars, and
// do their work through the Go tools, never a shell:
//
//   - no task uses ansible's shell, script or raw module;
//   - every play's hosts is a group the inventory prints, or localhost;
//   - every command task says how its change is read (changed_when);
//   - every template a task names exists;
//   - every nova_* a play or template reads is defined: by group_vars, by the
//     inventory (a host variable or all.vars of the fixtures' inventories), by
//     a set_fact, or named by an assert as the operator's -e;
//   - fleet/retired-tools.txt names no tool cmd/ ships;
//   - every loop_* a template reads is a variable of the play or the task that
//     renders it, every l.<field> a field of the inventory's loop record, and
//     every ansible_* one of the facts the plays gather.
//
// The inventory half renders both fixtures under fleet/testdata through the
// verb's own code (config.LoadFixture, config.BuildInventory) and asserts the
// groups and the typed loop records the plays read.

// fleetPlays are the plays the rule holds.
var fleetPlays = []string{"tools.yml", "redis.yml", "loops.yml"}

// fleetFacts are the gathered facts the plays and templates may read.
var fleetFacts = map[string]bool{"ansible_system": true, "ansible_architecture": true, "ansible_user_id": true, "ansible_user_uid": true, "ansible_env": true, "ansible_check_mode": true}

// fleetSource is the text the rule reads: each play's YAML and each
// template's text, by file name.
type fleetSource struct {
	plays     map[string][]byte
	templates map[string]string
	// defined are the nova_* names group_vars and the inventory define.
	defined map[string]bool
	// loopFields are the fields of an inventory loop record.
	loopFields map[string]bool
}

var (
	reNovaVar   = regexp.MustCompile(`\bnova_[a-z0-9_]+\b`)
	reLoopVar   = regexp.MustCompile(`\bloop_[a-z0-9_]+\b`)
	reFactVar   = regexp.MustCompile(`\bansible_[a-z0-9_]+\b`)
	reRecord    = regexp.MustCompile(`\bl\.([a-z_]+)\b`)
	reJinja     = regexp.MustCompile(`\{\{.*?\}\}|\{%.*?%\}`)
	reAssertVar = regexp.MustCompile(`(nova_[a-z0-9_]+) is defined`)
)

// fleetPlayProblems is every way the plays break the rule, one line each.
func fleetPlayProblems(src fleetSource, groups map[string]bool) []string {
	var out []string
	templateVars := map[string]map[string]bool{} // template -> the loop_* the rendering task gives it
	for _, name := range fleetKeys(src.plays) {
		text := uncommented(src.plays[name])
		var plays []map[string]any
		if err := yaml.Unmarshal(src.plays[name], &plays); err != nil {
			out = append(out, fmt.Sprintf("%s: not YAML: %v", name, err))
			continue
		}
		defined := map[string]bool{}
		for k := range src.defined {
			defined[k] = true
		}
		for _, m := range reAssertVar.FindAllStringSubmatch(text, -1) {
			defined[m[1]] = true
		}
		for _, p := range plays {
			for _, t := range fleetList(p["tasks"]) {
				if sf, ok := t["ansible.builtin.set_fact"].(map[string]any); ok {
					for k := range sf {
						defined[k] = true
					}
				}
			}
		}
		for _, v := range reNovaVar.FindAllString(text, -1) {
			if !defined[v] {
				out = append(out, fmt.Sprintf("%s reads %s, which neither group_vars nor the inventory defines", name, v))
			}
		}
		for _, p := range plays {
			hosts, _ := p["hosts"].(string)
			if !groups[hosts] && hosts != "localhost" {
				out = append(out, fmt.Sprintf("%s: play %q runs on %q, a group the inventory does not print", name, p["name"], hosts))
			}
			playVars := map[string]bool{}
			for k := range fleetMap(p["vars"]) {
				playVars[k] = true
			}
			for _, t := range fleetList(p["tasks"]) {
				for k := range fleetMap(t["ansible.builtin.set_fact"]) {
					playVars[k] = true
				}
			}
			for _, t := range fleetList(p["tasks"]) {
				task := fmt.Sprint(t["name"])
				for _, bad := range []string{"ansible.builtin.shell", "ansible.builtin.script", "ansible.builtin.raw", "shell", "script", "raw"} {
					if _, ok := t[bad]; ok {
						out = append(out, fmt.Sprintf("%s: task %q runs %s; do the work in a Go tool", name, task, bad))
					}
				}
				if _, ok := t["ansible.builtin.command"]; ok {
					if _, ok := t["changed_when"]; !ok {
						out = append(out, fmt.Sprintf("%s: command task %q has no changed_when", name, task))
					}
				}
				tmpl, ok := t["ansible.builtin.template"].(map[string]any)
				if !ok {
					continue
				}
				given := map[string]bool{}
				for k := range playVars {
					given[k] = true
				}
				for k := range fleetMap(t["vars"]) {
					given[k] = true
				}
				srcs := regexp.MustCompile(`templates/[a-z0-9.-]+\.j2`).FindAllString(fmt.Sprint(tmpl["src"]), -1)
				if len(srcs) == 0 {
					out = append(out, fmt.Sprintf("%s: template task %q names no templates/<file>.j2", name, task))
				}
				for _, s := range srcs {
					base := strings.TrimPrefix(s, "templates/")
					if _, ok := src.templates[base]; !ok {
						out = append(out, fmt.Sprintf("%s: task %q names %s, which does not exist", name, task, s))
						continue
					}
					templateVars[base] = given
				}
			}
		}
	}
	for _, name := range fleetKeys(src.templates) {
		given, rendered := templateVars[name]
		if !rendered {
			out = append(out, fmt.Sprintf("templates/%s is rendered by no play", name))
			continue
		}
		for _, block := range reJinja.FindAllString(src.templates[name], -1) {
			for _, v := range reLoopVar.FindAllString(block, -1) {
				if !given[v] {
					out = append(out, fmt.Sprintf("templates/%s reads %s, which its play and task do not set", name, v))
				}
			}
			for _, v := range reNovaVar.FindAllString(block, -1) {
				if !src.defined[v] {
					out = append(out, fmt.Sprintf("templates/%s reads %s, which neither group_vars nor the inventory defines", name, v))
				}
			}
			for _, v := range reFactVar.FindAllString(block, -1) {
				if !fleetFacts[v] {
					out = append(out, fmt.Sprintf("templates/%s reads %s, a fact the plays do not gather", name, v))
				}
			}
			for _, m := range reRecord.FindAllStringSubmatch(block, -1) {
				if !src.loopFields[m[1]] {
					out = append(out, fmt.Sprintf("templates/%s reads l.%s, which is no field of a loop record", name, m[1]))
				}
			}
		}
	}
	return out
}

// uncommented is a play's text without its comment lines: prose about a
// variable is not a read of it.
func uncommented(b []byte) string {
	var keep []string
	for _, l := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

func fleetList(v any) []map[string]any {
	var out []map[string]any
	if l, ok := v.([]any); ok {
		for _, e := range l {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

func fleetMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func fleetKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// fleetInventories renders the two fixtures' inventories through the
// verb's code, the machine of the check fixture marked local.
func fleetInventories(t *testing.T, root string) map[string]*config.AnsibleInventory {
	t.Helper()
	out := map[string]*config.AnsibleInventory{}
	for fixture, local := range map[string]string{"inventory-fixture.yml": "", "check-fixture.yml": "localhost"} {
		snap, err := config.LoadFixture(filepath.Join(root, "fleet", "testdata", fixture))
		require.NoError(t, err, fixture)
		inv, err := config.BuildInventory(snap, local)
		require.NoError(t, err, fixture)
		out[fixture] = inv
	}
	return out
}

// readFleetSource reads the plays, the templates, group_vars and the
// inventories' variable names.
func readFleetSource(t *testing.T, root string, invs map[string]*config.AnsibleInventory) (fleetSource, map[string]bool) {
	t.Helper()
	src := fleetSource{plays: map[string][]byte{}, templates: map[string]string{}, defined: map[string]bool{}, loopFields: map[string]bool{}}
	for _, p := range fleetPlays {
		b, err := os.ReadFile(filepath.Join(root, "fleet", p))
		require.NoError(t, err)
		src.plays[p] = b
	}
	tmpls, err := filepath.Glob(filepath.Join(root, "fleet", "templates", "nova-loop.*.j2"))
	require.NoError(t, err)
	require.Len(t, tmpls, 3, "launchd's plist, systemd's service and its timer")
	for _, f := range tmpls {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		src.templates[filepath.Base(f)] = string(b)
	}
	var vars map[string]any
	b, err := os.ReadFile(filepath.Join(root, "fleet", "group_vars", "all.yml"))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(b, &vars))
	for k := range vars {
		src.defined[k] = true
	}
	groups := map[string]bool{}
	for _, inv := range invs {
		raw, err := inv.JSON()
		require.NoError(t, err)
		var top map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &top))
		for g := range top {
			groups[g] = true
		}
		for k := range inv.All.Vars {
			src.defined[k] = true
		}
		for _, hv := range inv.Meta.Hostvars {
			for k := range hv {
				src.defined[k] = true
			}
		}
	}
	for _, f := range jsonFields(config.InventoryLoop{}) {
		src.loopFields[f] = true
	}
	return src, groups
}

func jsonFields(v any) []string {
	b, _ := json.Marshal(v) // ignored: a struct of strings, ints, bools and slices always marshals
	var m map[string]any
	_ = json.Unmarshal(b, &m) // ignored: it is the JSON just written
	return fleetKeys(m)
}

// TestFleetPlaysReadOnlyTheInventory holds the three plays and their
// templates to the rule, and the fixtures' inventories to what the plays
// read.
func TestFleetPlaysReadOnlyTheInventory(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	invs := fleetInventories(t, root)
	src, groups := readFleetSource(t, root, invs)
	for _, p := range fleetPlayProblems(src, groups) {
		assert.Fail(t, p)
	}

	full := invs["inventory-fixture.yml"]
	assert.Equal(t, []string{"bench-a", "bench-b"}, full.All.Hosts)
	assert.Equal(t, []string{"bench-a"}, full.StoreDeployer.Hosts)
	assert.Equal(t, "bench-b", full.All.Vars["nova_store"])
	for _, host := range full.All.Hosts {
		assert.Equal(t, "bench-b:6380", full.Meta.Hostvars[host]["nova_redis_addr"], host)
		assert.Equal(t, 6380, full.Meta.Hostvars[host]["nova_redis_port"], host)
		assert.Equal(t, "postgres://nova_config@localhost:5432/nova", full.Meta.Hostvars[host]["nova_pg_dsn"], host)
	}
	member := full.Meta.Hostvars["bench-b"]["nova_loops"].([]config.InventoryLoop)
	require.Len(t, member, 1)
	assert.Equal(t, config.InventoryLoop{
		Name: "member-bench-b", Argv: []string{"/usr/bin/env", "NOVA_SPRINT_REDIS_USER=bench", "NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_BENCH_PASSWORD", "~/.local/bin/nova-swarm", "member", "--as", "bench-b"}, Seat: "bench-b",
		Keys: []string{"API_KEY", "NOVA_REDIS_BENCH_PASSWORD"}, Keepalive: true, Enabled: true, Log: "~/nova-bench/loops/member-bench-b.log",
	}, member[0])
	// The retired tools are names nova-tools no longer ships: none is a
	// living cmd/ directory, so tools.yml never removes a tool it installs.
	retired, err := os.ReadFile(filepath.Join(root, "fleet", "retired-tools.txt"))
	require.NoError(t, err)
	n := 0
	for _, l := range strings.Split(string(retired), "\n") {
		if l = strings.TrimSpace(l); l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		n++
		assert.Regexp(t, `^nova-[a-z0-9-]+$`, l)
		_, err := os.Stat(filepath.Join(root, "cmd", l))
		assert.True(t, os.IsNotExist(err), "fleet/retired-tools.txt names %s, which cmd/ still ships", l)
	}
	assert.NotZero(t, n)
	assert.Equal(t, []string{"bench-b"}, full.TLA.Hosts, "the tla group is the rows with tla: true")
	assert.Equal(t, true, full.Meta.Hostvars["bench-b"]["nova_tla"])
	assert.Equal(t, false, full.Meta.Hostvars["bench-a"]["nova_tla"])
	check := invs["check-fixture.yml"]
	assert.Equal(t, "local", check.Meta.Hostvars["localhost"]["ansible_connection"])
	assert.Empty(t, check.StoreDeployer.Hosts, "the check fixture dials no store")
}

// TestFleetPlaysRuleReadsTheShapes plants each offence in a copy of the
// real source and sees the rule name it; the real source is clean.
func TestFleetPlaysRuleReadsTheShapes(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	invs := fleetInventories(t, root)
	base, groups := readFleetSource(t, root, invs)
	require.Empty(t, fleetPlayProblems(base, groups))
	plant := func(edit func(*fleetSource)) []string {
		s := fleetSource{plays: map[string][]byte{}, templates: map[string]string{}, defined: base.defined, loopFields: base.loopFields}
		for k, v := range base.plays {
			s.plays[k] = append([]byte{}, v...)
		}
		for k, v := range base.templates {
			s.templates[k] = v
		}
		edit(&s)
		return fleetPlayProblems(s, groups)
	}
	appendTask := func(play, task string) func(*fleetSource) {
		return func(s *fleetSource) {
			s.plays[play] = append(s.plays[play], []byte(task)...)
		}
	}
	cases := []struct {
		name string
		edit func(*fleetSource)
		want string
	}{
		{"shell", appendTask("redis.yml", "    - name: by hand\n      ansible.builtin.shell: redis-cli acl list\n"), "runs ansible.builtin.shell"},
		{"command without changed_when", appendTask("redis.yml", "    - name: bare\n      ansible.builtin.command: nova-redis acl render\n"), `command task "bare" has no changed_when`},
		{"undefined nova var", appendTask("redis.yml", "    - name: x\n      ansible.builtin.debug: { msg: \"{{ nova_typo }}\" }\n"), "reads nova_typo"},
		{"template var the task does not set", func(s *fleetSource) {
			s.templates["nova-loop.service.j2"] += "{{ loop_become }}\n"
		}, "reads loop_become"},
		{"record field", func(s *fleetSource) { s.templates["nova-loop.plist.j2"] += "{{ l.interval }}\n" }, "reads l.interval"},
		{"fact not gathered", func(s *fleetSource) { s.templates["nova-loop.plist.j2"] += "{{ ansible_hostname }}\n" }, "reads ansible_hostname"},
		{"unknown group", func(s *fleetSource) {
			s.plays["redis.yml"] = []byte(strings.Replace(string(s.plays["redis.yml"]), "hosts: store_deployer", "hosts: deployers", 1))
		}, `runs on "deployers"`},
		{"missing template", func(s *fleetSource) { delete(s.templates, "nova-loop.plist.j2") }, "templates/nova-loop.plist.j2, which does not exist"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := strings.Join(plant(tc.edit), "\n")
			assert.Contains(t, got, tc.want)
		})
	}
}
