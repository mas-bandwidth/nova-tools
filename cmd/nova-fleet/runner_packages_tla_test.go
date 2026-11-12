package main

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// runner_packages_tla_test.go pins TLC's jar in the runner-packages role (roles/runner-packages/tasks/tla.yml): the pin
// is the jar the repository's own model checks pin, a jar that differs is refused before anything is written and never
// replaced, and the copy lands root's and readable by every login, only where the machine had no jar.

func TestRunnerPackagesTLAJarPinIsTheRepositorysJar(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("group_vars/all.yml")
	require.NoError(t, err)
	var vars struct {
		Jar    string `yaml:"nova_runner_tla_jar"`
		Sha    string `yaml:"nova_runner_tla_jar_sha256"`
		Source string `yaml:"nova_runner_tla_jar_source"`
	}
	require.NoError(t, yaml.Unmarshal(b, &vars))
	pinned, err := os.ReadFile("../../tests/tla/tla2tools.sha256")
	require.NoError(t, err)
	want, _, _ := strings.Cut(string(pinned), " ")
	assert.Equal(t, want, vars.Sha, "the fleet's jar is the one tests/tla/check.sh pins")
	assert.Equal(t, "/opt/tla/tla2tools.jar", vars.Jar)
	assert.Empty(t, vars.Source, "no machine is named by default: the source is the deployment's")

	main := taskNamed(t, "main.yml", "TLC's jar, for the Java runtime above (Debian, Ubuntu)")
	assert.Equal(t, "tla.yml", asText(main["ansible.builtin.include_tasks"]))
	assert.Equal(t, "rp_os == 'debian' and nova_runner_tla_jar_sha256 | length > 0", asText(main["when"]))
}

// Every task of tla.yml that writes to the machine comes after both refusals, sits in the block that runs only outside
// --check and only where the machine had no jar, and comes after the check of the fetched copy.
func TestRunnerPackagesTLAJarMismatchIsRefusedBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(runnerPackagesRole + "/tasks/tla.yml")
	require.NoError(t, err)
	var raw []any
	require.NoError(t, yaml.Unmarshal(b, &raw))
	top := mapsOf(t, raw)

	names := []string{}
	for _, task := range top {
		names = append(names, asText(task["name"]))
	}
	machine := slices.Index(names, "a jar that differs from the pin is refused, never replaced")
	source := slices.Index(names, "a missing jar has a machine to come from")
	block := slices.Index(names, "the jar, from the source machine through the control node")
	require.GreaterOrEqual(t, machine, 0)
	require.GreaterOrEqual(t, source, 0)
	require.Greater(t, block, machine, "the machine's jar is checked before anything is fetched or written")
	require.Greater(t, block, source)

	refuse := top[machine]
	msg := asText(refuse["ansible.builtin.fail"].(map[string]any)["msg"])
	for _, field := range []string{"TLA-JAR-MISMATCH host={{ inventory_hostname }}", "want={{ nova_runner_tla_jar_sha256 }}", "got=", "run: nova-fleet runner-packages"} {
		assert.Contains(t, msg, field)
	}
	assert.Contains(t, asText(refuse["when"]), "rp_tla.stat.checksum")
	assert.Contains(t, asText(refuse["when"]), "!= nova_runner_tla_jar_sha256")

	for i, task := range top {
		if i == block {
			continue
		}
		for _, m := range []string{"ansible.builtin.copy", "ansible.builtin.file", "ansible.builtin.fetch", "ansible.builtin.get_url"} {
			assert.NotContains(t, task, m, "task %q writes outside the guarded block", names[i])
		}
	}
	guard := top[block]
	assert.Equal(t, "not ansible_check_mode and not rp_tla.stat.exists", asText(guard["when"]), "an existing jar is never replaced; --check writes nothing")

	inner := mapsOf(t, guard["block"])
	innerNames := []string{}
	for _, task := range inner {
		innerNames = append(innerNames, asText(task["name"]))
	}
	check := slices.Index(innerNames, "a fetched jar that differs from the pin is refused before anything is written")
	dir := slices.Index(innerNames, "the jar's directory (root's, readable by every login)")
	jar := slices.Index(innerNames, "the jar (root's, readable by every login)")
	require.GreaterOrEqual(t, check, 0)
	require.Greater(t, dir, check)
	require.Greater(t, jar, check)
	fetched := asText(inner[check]["ansible.builtin.fail"].(map[string]any)["msg"])
	assert.Contains(t, fetched, "TLA-JAR-MISMATCH host={{ nova_runner_tla_jar_source }}")
	assert.Contains(t, asText(inner[check]["when"]), "!= nova_runner_tla_jar_sha256")

	d := inner[dir]["ansible.builtin.file"].(map[string]any)
	assert.Equal(t, "root", d["owner"])
	assert.Equal(t, "0755", d["mode"])
	assert.Equal(t, true, inner[dir]["become"])
	c := inner[jar]["ansible.builtin.copy"].(map[string]any)
	assert.Equal(t, "{{ nova_runner_tla_jar }}", c["dest"])
	assert.Equal(t, "root", c["owner"])
	assert.Equal(t, "0644", c["mode"])
	assert.Equal(t, true, inner[jar]["become"])
	assert.Equal(t, false, c["force"], "the copy only ever lands where there is no jar, even one that appeared after the check")
	stat := top[0]["ansible.builtin.stat"].(map[string]any)
	assert.Equal(t, true, stat["follow"], "a link to the pinned jar is checked by what it points to")
	assert.Contains(t, asText(top[source]["when"]), "nova_runner_tla_jar_source == inventory_hostname", "a machine is never its own source")
}
