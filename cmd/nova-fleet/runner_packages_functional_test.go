//go:build functional

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runner_packages_functional_test.go runs the runner-packages role's templates and expressions through ansible-core on
// localhost (runner_packages_play_test.go is the structural half): the ports source and the kernel hook rendered with
// group_vars/all.yml, the hook run on a temp image, and the shipped expressions that decide which apt sources to
// restrict, evaluated on the files Ubuntu ships.

// runPackagesPlay runs one play of tasks on localhost, with group_vars/all.yml as the play reads it and vars as extra
// vars (which win over it), and returns its output directory. HOME is a temp dir, so nothing reaches the real one.
func runPackagesPlay(t *testing.T, vars map[string]any, tasks []any) string {
	t.Helper()
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		t.Skip("ansible-core is not installed on this machine; runner_packages_play_test.go still runs")
	}
	fleet, err := filepath.Abs(".")
	require.NoError(t, err)
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	require.NoError(t, os.MkdirAll(home, 0o755))
	play := map[string]any{"hosts": "localhost", "connection": "local", "gather_facts": true, "gather_subset": []string{"min"},
		"vars_files": []string{fleet + "/group_vars/all.yml"}, "tasks": tasks}
	b, err := json.Marshal([]any{play})
	require.NoError(t, err)
	path := filepath.Join(dir, "play.yml")
	require.NoError(t, os.WriteFile(path, b, 0o644))
	extra := map[string]any{"fleet": fleet, "out": dir}
	for k, v := range vars {
		extra[k] = v
	}
	b, err = json.Marshal(extra)
	require.NoError(t, err)
	extraPath := filepath.Join(dir, "extra.json")
	require.NoError(t, os.WriteFile(extraPath, b, 0o644))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ansible-playbook", "-i", "localhost,", "-e", "@"+extraPath, path)
	cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "HOME="+home, "ANSIBLE_LOCAL_TEMP="+filepath.Join(dir, "tmp"))
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return dir
}

// renderRunnerPackagesTemplate renders one of the role's templates with vars.
func renderRunnerPackagesTemplate(t *testing.T, name string, vars map[string]any) string {
	t.Helper()
	dir := runPackagesPlay(t, vars, []any{map[string]any{"ansible.builtin.copy": map[string]any{
		"content": "{{ lookup('ansible.builtin.template', fleet ~ '/roles/runner-packages/templates/" + name + "') }}",
		"dest":    "{{ out }}/rendered",
	}}})
	b, err := os.ReadFile(filepath.Join(dir, "rendered"))
	require.NoError(t, err)
	return string(b)
}

func TestFunctionalRunnerPackagesPortsSource(t *testing.T) {
	t.Parallel()
	got := renderRunnerPackagesTemplate(t, "ports.sources.j2", map[string]any{"item": "s390x", "rp_codename": "resolute"})
	assert.Equal(t, `# nova-fleet runner-packages (nova_runner_foreign_architectures): s390x packages from Ubuntu's ports archive.
Types: deb
URIs: http://ports.ubuntu.com/ubuntu-ports/
Suites: resolute resolute-updates resolute-security
Components: main restricted universe multiverse
Architectures: s390x
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
`, got)
}

// The hook, as rendered, copies the image a kernel install hands it into the directory, readable by the login only;
// a second run leaves the same file, and a version with no image changes nothing.
func TestFunctionalRunnerPackagesVMKernelHook(t *testing.T) {
	t.Parallel()
	me, err := user.Current()
	require.NoError(t, err)
	root := t.TempDir()
	kernels := filepath.Join(root, "home", "vm-kernels")
	hook := renderRunnerPackagesTemplate(t, "vm-kernel.postinst.j2", map[string]any{"rp_vm_user": me.Username, "nova_runner_vm_kernel_dir": kernels})
	hookPath := filepath.Join(root, "zz-nova-vm-kernel")
	require.NoError(t, os.WriteFile(hookPath, []byte(hook), 0o755))
	image := filepath.Join(root, "vmlinuz-7.0.0-43-generic")
	require.NoError(t, os.WriteFile(image, []byte("kernel image"), 0o600))

	for range 2 {
		out, err := exec.Command("/bin/sh", hookPath, "7.0.0-43-generic", image).CombinedOutput()
		require.NoError(t, err, string(out))
		copied := filepath.Join(kernels, "vmlinuz-7.0.0-43-generic")
		b, err := os.ReadFile(copied)
		require.NoError(t, err)
		assert.Equal(t, "kernel image", string(b))
		st, err := os.Stat(copied)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	}
	out, err := exec.Command("/bin/sh", hookPath, "7.0.0-44-generic", filepath.Join(root, "absent")).CombinedOutput()
	require.NoError(t, err, string(out))
	assert.NoFileExists(t, filepath.Join(kernels, "vmlinuz-7.0.0-44-generic"))
}

// debianArchTask is the task of debian-arch.yml named name.
func debianArchTask(t *testing.T, name string) map[string]any {
	t.Helper()
	return taskNamed(t, "debian-arch.yml", name)
}

type sourcesVerdict struct {
	Stanzas     int `json:"stanzas"`
	Open        int `json:"open"`
	OnelineOpen int `json:"oneline_open"`
}

// evalSources runs the role's own set_fact and replace tasks over a deb822 ubuntu.sources and a one-line sources.list
// (empty: the file is absent), returning the verdict and ubuntu.sources after the replace.
func evalSources(t *testing.T, deb822, oneline string) (sourcesVerdict, string) {
	t.Helper()
	which := debianArchTask(t, "which native sources name no architecture")["ansible.builtin.set_fact"]
	open := debianArchTask(t, "the deb822 sources with no Architectures line")["ansible.builtin.set_fact"]
	restrict := debianArchTask(t, "the native sources are restricted to the native architecture")
	args, _ := restrict["ansible.builtin.replace"].(map[string]any)
	require.NotNil(t, args)
	file := filepath.Join(t.TempDir(), "ubuntu.sources")
	require.NoError(t, os.WriteFile(file, []byte(deb822), 0o644))
	vars := map[string]any{"rp_native_arch": map[string]any{"stdout": "amd64"}, "rp_deb822": map[string]any{}, "rp_oneline": map[string]any{}}
	if deb822 != "" {
		vars["rp_deb822"] = map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(deb822))}
	}
	if oneline != "" {
		vars["rp_oneline"] = map[string]any{"content": base64.StdEncoding.EncodeToString([]byte(oneline))}
	}
	dir := runPackagesPlay(t, vars, []any{
		map[string]any{"ansible.builtin.set_fact": which},
		map[string]any{"ansible.builtin.set_fact": open},
		map[string]any{"ansible.builtin.copy": map[string]any{
			"content": "{{ {'stanzas': rp_stanzas | length, 'open': rp_stanzas_open | length, 'oneline_open': rp_oneline_open | length} | to_json }}",
			"dest":    "{{ out }}/verdict.json",
		}},
		map[string]any{
			"ansible.builtin.replace": map[string]any{"path": file, "regexp": args["regexp"], "replace": args["replace"]},
			"when":                    restrict["when"],
		},
	})
	b, err := os.ReadFile(filepath.Join(dir, "verdict.json"))
	require.NoError(t, err)
	var v sourcesVerdict
	require.NoError(t, json.Unmarshal(b, &v), string(b))
	after, err := os.ReadFile(file)
	require.NoError(t, err)
	return v, string(after)
}

// Ubuntu 24.04's ubuntu.sources as installed (no Architectures line); its sources.list holds only a comment.
const ubuntuSources2404 = `## Ubuntu distribution repository
## See sources.list(5) for more details.

Types: deb
URIs: http://archive.ubuntu.com/ubuntu/
Suites: noble noble-updates noble-backports
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg

## Ubuntu security updates. Aside from URIs and Suites,
## this should mirror your choices in the previous section.
Types: deb
URIs: http://security.ubuntu.com/ubuntu/
Suites: noble-security
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
`

const sourcesListComment = "# Ubuntu sources have moved to /etc/apt/sources.list.d/ubuntu.sources\n"

func TestFunctionalRunnerPackagesRestrictsDeb822Sources(t *testing.T) {
	t.Parallel()
	v, after := evalSources(t, ubuntuSources2404, sourcesListComment)
	assert.Equal(t, sourcesVerdict{Stanzas: 2, Open: 2, OnelineOpen: 0}, v)
	assert.Equal(t, 2, strings.Count(after, "Types: deb\nArchitectures: amd64\nURIs:"), after)

	again, still := evalSources(t, after, sourcesListComment)
	assert.Equal(t, sourcesVerdict{Stanzas: 2, Open: 0, OnelineOpen: 0}, again, "a second run finds nothing to restrict")
	assert.Equal(t, after, still, "a second run changes nothing")
}

func TestFunctionalRunnerPackagesRefusesMixedDeb822(t *testing.T) {
	t.Parallel()
	mixed := strings.Replace(ubuntuSources2404, "Suites: noble-security\n", "Suites: noble-security\nArchitectures: amd64\n", 1)
	v, _ := evalSources(t, mixed, "")
	assert.Equal(t, sourcesVerdict{Stanzas: 2, Open: 1, OnelineOpen: 0}, v, "1 open of 2: the play's assert refuses it before the replace")
}

// Ubuntu 22.04's one-line sources.list: a deb line with no [arch=...] is counted (the play refuses it), a restricted one
// and a commented one are not, and the file is never edited.
func TestFunctionalRunnerPackagesCountsOpenOneLineSources(t *testing.T) {
	t.Parallel()
	jammy := `# See http://help.ubuntu.com/community/UpgradeNotes
deb http://archive.ubuntu.com/ubuntu/ jammy main restricted
deb [arch=amd64 signed-by=/usr/share/keyrings/ubuntu-archive-keyring.gpg] http://archive.ubuntu.com/ubuntu/ jammy-updates main
	deb http://security.ubuntu.com/ubuntu jammy-security main
# deb-src http://archive.ubuntu.com/ubuntu/ jammy main restricted
deb-src http://archive.ubuntu.com/ubuntu/ jammy universe
`
	v, after := evalSources(t, "", jammy)
	assert.Equal(t, sourcesVerdict{Stanzas: 0, Open: 0, OnelineOpen: 2}, v)
	assert.Equal(t, "", after)
}

// The presence check keys on package_facts' names, and with the apt manager (python-apt's cache keys) a foreign
// architecture's package is named name:arch (libsodium-dev:s390x, as dpkg on an s390x-enabled host reports it), so a
// wanted name:arch is present only when that architecture's package is installed.
func TestFunctionalRunnerPackagesPresenceKnowsForeignArch(t *testing.T) {
	t.Parallel()
	task := taskNamed(t, "debian.yml", "what is present and what is missing")
	facts, _ := task["ansible.builtin.set_fact"].(map[string]any)
	require.NotNil(t, facts)
	exprs := map[string]any{}
	for k, v := range facts {
		exprs[k] = strings.ReplaceAll(asText(v), "ansible_facts.packages", "fake_packages")
	}
	dir := runPackagesPlay(t, map[string]any{
		"rp_wanted": []string{"libsodium-dev:s390x", "libsodium-dev", "libbpf-dev:s390x", "cmake"},
		"fake_packages": map[string]any{
			"libsodium-dev":       []any{map[string]any{"name": "libsodium-dev", "arch": "amd64"}},
			"libsodium-dev:s390x": []any{map[string]any{"name": "libsodium-dev:s390x", "arch": "s390x"}},
			"libbpf-dev":          []any{map[string]any{"name": "libbpf-dev", "arch": "amd64"}},
			"cmake":               []any{map[string]any{"name": "cmake", "arch": "amd64"}},
		},
	}, []any{
		map[string]any{"ansible.builtin.set_fact": exprs},
		map[string]any{"ansible.builtin.copy": map[string]any{"content": "{{ {'present': rp_present, 'missing': rp_missing} | to_json }}", "dest": "{{ out }}/p.json"}},
	})
	b, err := os.ReadFile(filepath.Join(dir, "p.json"))
	require.NoError(t, err)
	var got struct{ Present, Missing []string }
	require.NoError(t, json.Unmarshal(b, &got), string(b))
	assert.Equal(t, []string{"libsodium-dev:s390x", "libsodium-dev", "cmake"}, got.Present)
	assert.Equal(t, []string{"libbpf-dev:s390x"}, got.Missing)
}
