package main

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// sdk_toolchain_play_test.go pins the sdk-toolchain play (cmd/nova-fleet/README.md, "sdk-toolchain"): every tool in
// its pinned defaults carries what its kind needs to be checked (a digest, a tag's commit, a module version, an npm
// integrity), the directories and command names do not collide, .NET 8 sits beside .NET 10, Rust comes through a
// pinned rustup, and the role installs only what its probe found missing, as the login, changing nothing under --check.

const sdkToolchainRole = "roles/sdk-toolchain"

type sdkPlatform struct {
	URL       string `yaml:"url"`
	SHA256    string `yaml:"sha256"`
	SHA512    string `yaml:"sha512"`
	Repo      string `yaml:"repo"`
	Ref       string `yaml:"ref"`
	Commit    string `yaml:"commit"`
	Package   string `yaml:"package"`
	Version   string `yaml:"version"`
	Integrity string `yaml:"integrity"`
	Linker    string `yaml:"linker"`
	Extras    []struct {
		URL    string `yaml:"url"`
		SHA256 string `yaml:"sha256"`
		Path   string `yaml:"path"`
	} `yaml:"extras"`
}

type sdkTool struct {
	Name    string       `yaml:"name"`
	Version string       `yaml:"version"`
	Dir     string       `yaml:"dir"`
	Probe   string       `yaml:"probe"`
	Kind    string       `yaml:"kind"`
	Build   string       `yaml:"build"`
	Links   []any        `yaml:"links"`
	Alias   string       `yaml:"alias"`
	Wrapper string       `yaml:"wrapper"`
	Linux   *sdkPlatform `yaml:"linux"`
	Darwin  *sdkPlatform `yaml:"darwin"`
}

type sdkDefaults struct {
	Dir   string    `yaml:"nova_sdk_dir"`
	Tools []sdkTool `yaml:"nova_sdk_tools"`
	More  []struct {
		Version string       `yaml:"version"`
		Runtime string       `yaml:"runtime"`
		Linux   *sdkPlatform `yaml:"linux"`
		Darwin  *sdkPlatform `yaml:"darwin"`
	} `yaml:"nova_sdk_dotnet_more"`
	Rustup struct {
		Version    string       `yaml:"version"`
		Linux      *sdkPlatform `yaml:"linux"`
		Toolchains []struct {
			Name       string   `yaml:"name"`
			Components []string `yaml:"components"`
			Targets    []string `yaml:"targets"`
		} `yaml:"toolchains"`
		Links []string `yaml:"links"`
	} `yaml:"nova_sdk_rustup"`
	CargoTools []struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	} `yaml:"nova_sdk_cargo_tools"`
}

func readSDKDefaults(t *testing.T) sdkDefaults {
	t.Helper()
	b, err := os.ReadFile(sdkToolchainRole + "/defaults/main.yml")
	require.NoError(t, err)
	var d sdkDefaults
	require.NoError(t, yaml.Unmarshal(b, &d))
	return d
}

func sdkTasks(t *testing.T) map[string][]map[string]any {
	t.Helper()
	files, err := filepath.Glob(sdkToolchainRole + "/tasks/*.yml")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	out := map[string][]map[string]any{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		var raw []any
		require.NoError(t, yaml.Unmarshal(b, &raw), f)
		out[filepath.Base(f)] = flattenTasks(t, mapsOf(t, raw))
	}
	return out
}

var (
	hex64  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	hex128 = regexp.MustCompile(`^[0-9a-f]{128}$`)
	hex40  = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// pinned says what is missing from a tool's platform entry for its kind to be checked, or "".
func pinned(kind string, p *sdkPlatform, build string) string {
	switch kind {
	case "", "archive", "file":
		if !strings.HasPrefix(p.URL, "https://") {
			return "an https url"
		}
		if !hex64.MatchString(p.SHA256) && !hex128.MatchString(p.SHA512) {
			return "a sha256 or sha512"
		}
	case "git":
		if !strings.HasPrefix(p.Repo, "https://") || p.Ref == "" || !hex40.MatchString(p.Commit) || build == "" {
			return "a repo, a ref, the ref's 40-hex commit and a build"
		}
	case "go":
		if p.Package == "" || !strings.HasPrefix(p.Version, "v") {
			return "a package and a v-version"
		}
	case "npm":
		if p.Package == "" || p.Version == "" || !strings.HasPrefix(p.Integrity, "sha512-") {
			return "a package, a version and the registry's sha512 integrity"
		}
	case "script":
		if build == "" {
			return "a build"
		}
	default:
		return "a known kind"
	}
	for _, e := range p.Extras {
		if !strings.HasPrefix(e.URL, "https://") || !hex64.MatchString(e.SHA256) || e.Path == "" {
			return "an https url, sha256 and path for each extra"
		}
	}
	return ""
}

func TestSDKToolchainPlayTargetsTheBenchAndRunnerGroups(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("sdk-toolchain.yml")
	require.NoError(t, err)
	assert.NotEmpty(t, header(string(b), "purpose"), "the play's purpose line is what nova-fleet plays prints")
	var docs []map[string]any
	require.NoError(t, yaml.Unmarshal(b, &docs))
	require.Len(t, docs, 1)
	assert.Equal(t, "bench:runner", docs[0]["hosts"], "a bench runs the same gates as a runner, so it has the same toolchains")
	assert.NotContains(t, docs[0], "become", "the SDK is the login's: nothing in the play runs as root")
	assert.Equal(t, []any{"sdk-toolchain"}, docs[0]["roles"])
	assert.True(t, slices.Contains(plays(tree), "sdk-toolchain"), "nova-fleet lists the play")
}

// Every tool is pinned for each platform it names, its directory and commands are its own, and the probe is in the
// directory, so an installed tool is found and a second run changes nothing.
func TestSDKToolchainEveryToolIsPinned(t *testing.T) {
	t.Parallel()
	d := readSDKDefaults(t)
	assert.Equal(t, "{{ ansible_facts['user_dir'] }}/sdk", d.Dir)
	require.Greater(t, len(d.Tools), 20, "the defaults are the toolchain the benches run, not a stub")
	dirs, names := map[string]string{}, map[string]string{}
	claim := func(name, owner string) {
		if prev, ok := names[name]; ok {
			t.Errorf("%s/bin/%s is claimed by %s and %s", "<sdk>", name, prev, owner)
		}
		names[name] = owner
	}
	for _, tool := range d.Tools {
		require.NotEmpty(t, tool.Name)
		assert.NotEmpty(t, tool.Version, tool.Name)
		assert.NotEmpty(t, tool.Probe, "%s has no probe: a run could not tell it is installed", tool.Name)
		assert.NotContains(t, tool.Dir, "/", "%s: dir is one directory under the SDK", tool.Name)
		assert.NotContains(t, tool.Probe, "..", "%s: the probe is inside the tool's directory", tool.Name)
		if prev, ok := dirs[tool.Dir]; ok {
			t.Errorf("%s and %s share the directory %s: installing one would replace the other", prev, tool.Name, tool.Dir)
		}
		dirs[tool.Dir] = tool.Name
		if tool.Kind != "script" {
			assert.Contains(t, tool.Dir, tool.Version, "%s: a version's directory names its version, so a bump installs beside it", tool.Name)
		}
		require.NotNil(t, tool.Linux, "%s: every tool is on Linux", tool.Name)
		if why := pinned(tool.Kind, tool.Linux, tool.Build); why != "" {
			t.Errorf("%s (linux) needs %s", tool.Name, why)
		}
		if tool.Darwin != nil {
			if why := pinned(tool.Kind, tool.Darwin, tool.Build); why != "" {
				t.Errorf("%s (darwin) needs %s", tool.Name, why)
			}
		}
		for _, l := range tool.Links {
			name := ""
			switch v := l.(type) {
			case string:
				name = filepath.Base(v)
			case map[string]any:
				name, _ = v["name"].(string)
				assert.NotEmpty(t, v["path"], "%s: a named link has a path", tool.Name)
			}
			require.NotEmpty(t, name, "%s: a link with no name", tool.Name)
			claim(name, tool.Name)
		}
		if tool.Wrapper != "" {
			claim(tool.Name, tool.Name+" (wrapper)")
			assert.True(t, strings.HasPrefix(tool.Wrapper, "#!/bin/sh\n"), "%s: the wrapper is a shell script", tool.Name)
		}
	}
	for _, n := range d.Rustup.Links {
		claim(n, "rustup")
	}
	claim("rustup", "rustup (wrapper)")
	claim("rustc", "rustc (wrapper)")
	for _, c := range d.CargoTools {
		claim(c.Name, "cargo tool")
		assert.NotEmpty(t, c.Version, "%s is pinned", c.Name)
	}
	for _, want := range []string{"go", "node", "dart", "otp", "elixir", "jdk", "dotnet", "golangci-lint", "staticcheck", "esbmc",
		"spotbugs", "deno", "sbcl", "lua", "luau", "afl", "frama-c", "python3.11", "python3.15"} {
		assert.Contains(t, dirs, dirFor(d, want), "%s is in the defaults", want)
	}
}

func dirFor(d sdkDefaults, name string) string {
	for _, tool := range d.Tools {
		if tool.Name == name {
			return tool.Dir
		}
	}
	return "(no " + name + ")"
}

// Each CPython is built from source into python-<version> and gets mypy offline: pip takes only the wheels fetched as
// extras, and the hash lock the build writes names exactly those wheels' sha256s, so pip refuses any other.
func TestSDKToolchainPythonMypyIsHashLocked(t *testing.T) {
	t.Parallel()
	d := readSDKDefaults(t)
	for _, name := range []string{"python3.11", "python3.15"} {
		var tool *sdkTool
		for i := range d.Tools {
			if d.Tools[i].Name == name {
				tool = &d.Tools[i]
			}
		}
		require.NotNil(t, tool, name)
		assert.Equal(t, "python-"+tool.Version, tool.Dir, "%s: the directory a Makefile looks for", name)
		assert.Equal(t, []any{"bin/" + name}, tool.Links, "%s: linked by its version, never as python3", name)
		assert.Equal(t, "bin/mypy", tool.Probe, "%s: mypy is installed last, so a build that stopped part way is redone", name)
		assert.Contains(t, tool.Build, "--disable-test-modules --with-ensurepip=install")
		assert.Contains(t, tool.Build, "--no-index")
		assert.Contains(t, tool.Build, "--require-hashes")
		locked := regexp.MustCompile(`--hash=sha256:([0-9a-f]{64})`).FindAllStringSubmatch(tool.Build, -1)
		var lock, wheels []string
		for _, m := range locked {
			lock = append(lock, m[1])
		}
		for _, e := range tool.Linux.Extras {
			assert.True(t, strings.HasSuffix(e.Path, ".whl"), "%s: %s is a wheel", name, e.Path)
			wheels = append(wheels, e.SHA256)
		}
		require.NotEmpty(t, wheels, "%s: mypy's wheels are fetched as extras", name)
		assert.ElementsMatch(t, wheels, lock, "%s: the hash lock names exactly the wheels fetched", name)
	}
}

// .NET 8 goes into the .NET 10 directory from Microsoft's archives, each held to its SHA-512, on every platform the
// dotnet tool is on; Rust comes from a pinned rustup-init and its first toolchain carries the components CI runs.
func TestSDKToolchainDotnet8AndRustup(t *testing.T) {
	t.Parallel()
	d := readSDKDefaults(t)
	var dotnet *sdkTool
	for i := range d.Tools {
		if d.Tools[i].Name == "dotnet" {
			dotnet = &d.Tools[i]
		}
	}
	require.NotNil(t, dotnet)
	assert.True(t, strings.HasPrefix(dotnet.Version, "10.0."), "the dotnet tool is the 10.0 SDK")
	require.Len(t, d.More, 1)
	assert.True(t, strings.HasPrefix(d.More[0].Version, "8.0."), "the extra SDK is 8.0")
	assert.True(t, strings.HasPrefix(d.More[0].Runtime, "8.0."))
	for name, p := range map[string]*sdkPlatform{"linux": d.More[0].Linux, "darwin": d.More[0].Darwin} {
		require.NotNil(t, p, "%s: .NET 8 wherever .NET 10 is", name)
		assert.True(t, strings.HasPrefix(p.URL, "https://builds.dotnet.microsoft.com/"), "%s: Microsoft's archive", name)
		assert.Contains(t, p.URL, d.More[0].Version)
		assert.Regexp(t, hex128, p.SHA512, "%s: Microsoft's SHA-512", name)
	}

	require.NotNil(t, d.Rustup.Linux)
	assert.Equal(t, "https://static.rust-lang.org/rustup/archive/"+d.Rustup.Version+"/x86_64-unknown-linux-gnu/rustup-init", d.Rustup.Linux.URL)
	assert.Regexp(t, hex64, d.Rustup.Linux.SHA256)
	require.NotEmpty(t, d.Rustup.Toolchains)
	first := d.Rustup.Toolchains[0]
	assert.Regexp(t, `^[0-9]+\.[0-9]+\.[0-9]+$`, first.Name, "the toolchain on PATH is a release, never a moving channel")
	assert.ElementsMatch(t, []string{"clippy", "rustfmt"}, first.Components)
	for _, tc := range d.Rustup.Toolchains {
		assert.NotContains(t, []string{"stable", "beta", "nightly"}, tc.Name, "%s moves: pin a version or a dated nightly", tc.Name)
	}
}

// A bare rustc runs a bare `cc` through a PATH it changed, which Rust's std starts by fork with a SEQPACKET socketpair
// that a seccomp sandbox refuses; a linker named by path is started by posix_spawn. So <sdk>/bin/rustc is a wrapper
// naming an absolute linker, never the toolchain's rustc linked in, and an earlier link is removed before it is written.
func TestSDKToolchainRustcLinksWithAnAbsoluteLinker(t *testing.T) {
	t.Parallel()
	d := readSDKDefaults(t)
	require.NotNil(t, d.Rustup.Linux)
	assert.True(t, filepath.IsAbs(d.Rustup.Linux.Linker), "the linker is a path, not a name looked up on PATH: %q", d.Rustup.Linux.Linker)
	assert.NotContains(t, d.Rustup.Links, "rustc", "rustc is the wrapper, never a link")
	b, err := os.ReadFile(sdkToolchainRole + "/tasks/rustup.yml")
	require.NoError(t, err)
	text := string(b)
	assert.Contains(t, text, `exec "{{ sdk_rust_bin }}/rustc" -C linker={{ nova_sdk_rustup[sdk_platform].linker | quote }} "$@"`)
	assert.Less(t, strings.Index(text, "an earlier rustc link goes"), strings.Index(text, "rustc in the SDK's bin, with an absolute linker"),
		"the old link is removed before the wrapper is written, so the copy never writes through it")
}

// The .NET 10 SDK names the exact targeting and apphost packs it builds an older target with (its
// Microsoft.NETCoreSdk.BundledVersions.props), and restore downloads any that are not in packs/, which a gate with no
// network cannot. So the role pairs the 10 SDK with the 8 SDK of the same release day and checks after installing that
// every pack the 10 SDK names for each extra SDK's target is there, failing the run when it is not.
func TestSDKToolchainDotnetPacksMatchThe10SDK(t *testing.T) {
	t.Parallel()
	var guard map[string]any
	for _, task := range sdkTasks(t)["dotnet.yml"] {
		if strings.Contains(taskText(t, task), "BundledVersions.props") {
			guard = task
		}
	}
	require.NotNil(t, guard, "dotnet.yml checks the packs the 10 SDK's BundledVersions.props names")
	text := taskText(t, guard)
	for _, want := range []string{"KnownFrameworkReference", "KnownAppHostPack", "TargetingPackVersion", "AppHostPackVersion", "/packs/", "nova_sdk_dotnet_more", "exit 1"} {
		assert.Contains(t, text, want)
	}
	assert.Contains(t, asText(guard["when"]), "not ansible_check_mode", "the packs are there only after a real run")
}

// The CLI writes a sentinel per SDK version into <DOTNET_CLI_HOME>/.dotnet on its first run, and every dotnet command of
// an SDK whose sentinels are missing fails where it cannot ("Access to the path ... toolpath.sentinel is denied"). So
// the role holds the CLI home and its .dotnet writable by the login, whatever mode it finds them in.
func TestSDKToolchainDotnetCLIHomeIsWritable(t *testing.T) {
	t.Parallel()
	modes := map[string]string{}
	for _, task := range sdkTasks(t)["dotnet.yml"] {
		f, ok := task["ansible.builtin.file"].(map[string]any)
		if !ok || f["state"] != "directory" {
			continue
		}
		paths := []any{f["path"]}
		if f["path"] == "{{ item }}" {
			paths, _ = task["loop"].([]any)
		}
		for _, p := range paths {
			modes[asText(p)] = asText(f["mode"])
		}
	}
	for _, dir := range []string{"{{ nova_sdk_dir }}/dotnet-home", "{{ nova_sdk_dir }}/dotnet-home/.dotnet"} {
		assert.Equal(t, "0755", modes[dir], "%s is a directory the login can write", dir)
	}
}

// The role installs only what the probe found missing, changes nothing under --check, and never becomes root.
func TestSDKToolchainInstallsOnlyWhatIsMissing(t *testing.T) {
	t.Parallel()
	installs := 0
	for file, tasks := range sdkTasks(t) {
		for _, task := range tasks {
			name := asText(task["name"])
			assert.NotContains(t, task, "become", "%s: task %q: the SDK is the login's", file, name)
			text := taskText(t, task)
			if task["ansible.builtin.get_url"] != nil || strings.Contains(text, "SDK-TOOLCHAIN-INSTALLED") {
				installs++
				if task["check_mode"] == nil {
					assert.Contains(t, asText(task["when"]), "not ansible_check_mode", "%s: task %q changes nothing under --check", file, name)
				} else {
					assert.Contains(t, text, "ansible_check_mode", "%s: task %q runs under --check, so it reports instead of installing there", file, name)
				}
				assert.Regexp(t, `missing|sdk_rustup_stat|nova_sdk_rustup.toolchains`, text, "%s: task %q works only on what is missing", file, name)
			}
		}
	}
	assert.GreaterOrEqual(t, installs, 6, "downloads and installs for the tools, the extra SDKs and rustup")
}

// env.sh puts the SDK's bin first on PATH and is the file ~/.bashrc sources.
func TestSDKToolchainEnvPutsTheSDKFirst(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(sdkToolchainRole + "/templates/env.sh.j2")
	require.NoError(t, err)
	env := string(b)
	assert.Contains(t, env, `PATH="{{ nova_sdk_dir }}/bin:`)
	assert.Contains(t, env, `export DOTNET_CLI_HOME="{{ nova_sdk_dir }}/dotnet-home"`)
	assert.NotContains(t, env, "RUSTUP_HOME", "a CI job's own rustup keeps its own home; <sdk>/bin/rustup names the SDK's")
	rc := ""
	for _, task := range sdkTasks(t)["main.yml"] {
		if li, ok := task["ansible.builtin.lineinfile"].(map[string]any); ok {
			rc = asText(li["line"])
			assert.Equal(t, "BOF", li["insertbefore"], "first, before a non-interactive shell's early return")
		}
	}
	assert.Contains(t, rc, `. "{{ nova_sdk_dir }}/env.sh"`)
}

// SQ names a Squirrel built with 64-bit integers and floats (sq64 with SQUSEDOUBLE), the interpreter a float64 survives,
// and env.sh exports it only where that binary is.
func TestSDKToolchainSQIsTheDoubleSquirrel(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(sdkToolchainRole + "/defaults/main.yml")
	require.NoError(t, err)
	var files struct {
		Env map[string]string `yaml:"nova_sdk_env_files"`
	}
	require.NoError(t, yaml.Unmarshal(b, &files))
	sq := files.Env["SQ"]
	dir, _, _ := strings.Cut(sq, "/")
	var tool *sdkTool
	d := readSDKDefaults(t)
	for i := range d.Tools {
		if d.Tools[i].Dir == dir {
			tool = &d.Tools[i]
		}
	}
	require.NotNil(t, tool, "SQ=%s names a tool's directory", sq)
	assert.Equal(t, dir+"/"+tool.Probe, sq, "SQ is the tool's probe, so it exists once the tool is installed")
	assert.Contains(t, tool.Build, "sq64")
	assert.Contains(t, tool.Build, "-DSQUSEDOUBLE")
	env, err := os.ReadFile(sdkToolchainRole + "/templates/env.sh.j2")
	require.NoError(t, err)
	assert.Contains(t, string(env), `if [ -x "{{ nova_sdk_dir }}/{{ v }}" ]; then export {{ k }}=`)
}
