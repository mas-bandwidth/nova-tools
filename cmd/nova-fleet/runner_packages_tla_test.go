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

// runner_packages_tla_test.go pins TLC's jar and the MemorySanitizer libc++ in the runner-packages role
// (roles/runner-packages/tasks/tla.yml, msan-libcxx.yml): each is pinned by the sha256 of what is downloaded, a jar that
// differs is refused before anything is written and never replaced, and what lands is root's and readable by every login,
// only where the machine had none.

// rpTaskFile is the top-level task list of one of the role's task files.
func rpTaskFile(t *testing.T, file string) ([]map[string]any, []string) {
	t.Helper()
	b, err := os.ReadFile(runnerPackagesRole + "/tasks/" + file)
	require.NoError(t, err)
	var raw []any
	require.NoError(t, yaml.Unmarshal(b, &raw))
	top := mapsOf(t, raw)
	names := []string{}
	for _, task := range top {
		names = append(names, asText(task["name"]))
	}
	return top, names
}

func TestRunnerPackagesTLAJarPinIsTheRepositorysJar(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("group_vars/all.yml")
	require.NoError(t, err)
	var vars struct {
		Jar  string            `yaml:"nova_runner_tla_jar"`
		Sha  string            `yaml:"nova_runner_tla_jar_sha256"`
		URL  string            `yaml:"nova_runner_tla_jar_url"`
		MSan map[string]string `yaml:"nova_runner_msan_libcxx_sha256"`
	}
	require.NoError(t, yaml.Unmarshal(b, &vars))
	pinned, err := os.ReadFile("../../tests/tla/tla2tools.sha256")
	require.NoError(t, err)
	want, _, _ := strings.Cut(string(pinned), " ")
	assert.Equal(t, want, vars.Sha, "the fleet's jar is the one tests/tla/check.sh pins")
	assert.Equal(t, "/opt/tla/tla2tools.jar", vars.Jar)
	assert.Equal(t, "https://github.com/tlaplus/tlaplus/releases/download/v1.7.4/tla2tools.jar", vars.URL, "the TLA+ release the pin is of")
	for v, sum := range vars.MSan {
		assert.Regexp(t, `^[0-9]+\.[0-9]+\.[0-9]+$`, v)
		assert.Regexp(t, `^[0-9a-f]{64}$`, sum, "llvm-project %s", v)
	}
	assert.Contains(t, vars.MSan, "21.1.8", "the fleet's clang")

	main := taskNamed(t, "main.yml", "TLC's jar, for the Java runtime above (Debian, Ubuntu)")
	assert.Equal(t, "tla.yml", asText(main["ansible.builtin.include_tasks"]))
	assert.Equal(t, "rp_os == 'debian' and nova_runner_tla_jar_sha256 | length > 0", asText(main["when"]))
	msan := taskNamed(t, "main.yml", "the MemorySanitizer libc++ for the clang above (Debian, Ubuntu; x86_64)")
	assert.Equal(t, "msan-libcxx.yml", asText(msan["ansible.builtin.include_tasks"]))
}

// Every task of tla.yml that writes comes after the refusal, sits in the block that runs only outside --check and only
// where the machine had no jar, and the download is checked against the pin before it lands.
func TestRunnerPackagesTLAJarMismatchIsRefusedBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	top, names := rpTaskFile(t, "tla.yml")
	machine := slices.Index(names, "a jar that differs from the pin is refused, never replaced")
	block := slices.Index(names, "the jar, from the TLA+ release")
	require.GreaterOrEqual(t, machine, 0)
	require.Greater(t, block, machine, "the machine's jar is checked before anything is downloaded or written")

	stat := top[0]["ansible.builtin.stat"].(map[string]any)
	assert.Equal(t, true, stat["follow"], "a link to the pinned jar is checked by what it points to")
	refuse := top[machine]
	msg := asText(refuse["ansible.builtin.fail"].(map[string]any)["msg"])
	for _, field := range []string{"TLA-JAR-MISMATCH host={{ inventory_hostname }}", "want={{ nova_runner_tla_jar_sha256 }}", "got=", "run: nova-fleet runner-packages"} {
		assert.Contains(t, msg, field)
	}
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
	require.Len(t, inner, 3)
	d := inner[0]["ansible.builtin.file"].(map[string]any)
	assert.Equal(t, "root", d["owner"])
	assert.Equal(t, "0755", d["mode"])
	assert.Equal(t, true, inner[0]["become"])
	g := inner[1]["ansible.builtin.get_url"].(map[string]any)
	assert.Equal(t, "{{ nova_runner_tla_jar_url }}", g["url"])
	assert.Equal(t, "{{ nova_runner_tla_jar }}.download", g["dest"], "downloaded beside its place, never onto it")
	assert.Equal(t, "sha256:{{ nova_runner_tla_jar_sha256 }}", g["checksum"], "get_url refuses a download that is not the pin")
	c := inner[2]["ansible.builtin.copy"].(map[string]any)
	assert.Equal(t, "{{ nova_runner_tla_jar }}", c["dest"])
	assert.Equal(t, false, c["force"], "a file that appeared since the check is never replaced")
	assert.Equal(t, "root", c["owner"])
	assert.Equal(t, "0644", c["mode"])
	assert.Equal(t, true, inner[2]["become"])
	assert.Equal(t, "root", g["owner"])
	assert.Equal(t, "0644", g["mode"])
	assert.Equal(t, true, inner[1]["become"])
}

// The instrumented libc++ is built only where it is missing, from the llvm-project source of the machine's own clang
// checked against its pin, under nova-reserve as the login; only the install is root's, and --check builds nothing.
func TestRunnerPackagesMSanLibcxxIsPinnedAndBuiltUnderReserve(t *testing.T) {
	t.Parallel()
	top, names := rpTaskFile(t, "msan-libcxx-build.yml")
	nopin := slices.Index(names, "a clang with no pinned llvm-project source is refused")
	block := slices.Index(names, "the instrumented libc++, built here")
	require.GreaterOrEqual(t, nopin, 0)
	require.Greater(t, block, nopin)
	assert.Contains(t, asText(top[nopin]["ansible.builtin.fail"].(map[string]any)["msg"]), "MSAN-LIBCXX-NO-PIN host={{ inventory_hostname }}")
	assert.Equal(t, "not ansible_check_mode and not rp_msan_present", asText(top[block]["when"]))
	for i, task := range top {
		if i != block {
			assert.NotContains(t, task, "become", "task %q: only the install is root's", names[i])
		}
	}
	text := taskText(t, top[block])
	assert.Contains(t, text, `checksum: sha256:{{ nova_runner_msan_libcxx_sha256[rp_msan_version] }}`)
	assert.Contains(t, text, "nova-reserve run --why msan-libcxx --")
	assert.Contains(t, text, "-DLLVM_USE_SANITIZER=MemoryWithOrigins")
	for _, task := range mapsOf(t, top[block]["block"]) {
		if task["become"] == true {
			name := asText(task["name"])
			assert.Contains(t, []string{"the build installed (root's, readable by every login)", "the prefix readable by every login"}, name)
		}
	}
}

// Under --check the packages are not installed, so a machine with no clang yet is told what would be built, not
// failed; outside --check no clang is refused by name, and the build runs only for a clang that answered.
func TestRunnerPackagesMSanLibcxxWithoutClang(t *testing.T) {
	t.Parallel()
	top, names := rpTaskFile(t, "msan-libcxx.yml")
	would := top[slices.Index(names, "would build the instrumented libc++ for the clang the packages bring")]
	assert.Equal(t, "ansible_check_mode and not rp_msan_has_clang", asText(would["when"]))
	refuse := top[slices.Index(names, "a clang to build for")]
	assert.Equal(t, "not ansible_check_mode and not rp_msan_has_clang", asText(refuse["when"]))
	assert.Contains(t, asText(refuse["ansible.builtin.fail"].(map[string]any)["msg"]), "MSAN-LIBCXX-NO-CLANG host={{ inventory_hostname }}")
	build := top[slices.Index(names, "the instrumented libc++ for that clang")]
	assert.Equal(t, "msan-libcxx-build.yml", asText(build["ansible.builtin.include_tasks"]))
	assert.Equal(t, "rp_msan_has_clang", asText(build["when"]))
}
