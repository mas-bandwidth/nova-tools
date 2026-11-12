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
	Build     string `yaml:"build"`
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
	Run     string       `yaml:"run"`
	Linux   *sdkPlatform `yaml:"linux"`
	Darwin  *sdkPlatform `yaml:"darwin"`
}

// platforms is each platform the tool has an entry for, by name.
func (tool sdkTool) platforms() map[string]*sdkPlatform {
	out := map[string]*sdkPlatform{}
	if tool.Linux != nil {
		out["linux"] = tool.Linux
	}
	if tool.Darwin != nil {
		out["darwin"] = tool.Darwin
	}
	return out
}

// build is the build the role runs on a platform: the platform's own, else the tool's.
func (tool sdkTool) build(p *sdkPlatform) string {
	if p.Build != "" {
		return p.Build
	}
	return tool.Build
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

// Every tool is pinned for each platform it names, its directory and commands are its own on each platform, and the
// probe is in the directory, so an installed tool is found and a second run changes nothing.
func TestSDKToolchainEveryToolIsPinned(t *testing.T) {
	t.Parallel()
	d := readSDKDefaults(t)
	assert.Equal(t, "{{ ansible_facts['user_dir'] }}/sdk", d.Dir)
	require.Greater(t, len(d.Tools), 20, "the defaults are the toolchain the benches run, not a stub")
	dirs := map[string]string{}
	names := map[string]map[string]string{"linux": {}, "darwin": {}}
	claim := func(platform, name, owner string) {
		if prev, ok := names[platform][name]; ok {
			t.Errorf("<sdk>/bin/%s is claimed by %s and %s on %s", name, prev, owner, platform)
		}
		names[platform][name] = owner
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
		require.NotEmpty(t, tool.platforms(), "%s: a tool is on Linux, macOS or both", tool.Name)
		for platform, p := range tool.platforms() {
			if why := pinned(tool.Kind, p, tool.build(p)); why != "" {
				t.Errorf("%s (%s) needs %s", tool.Name, platform, why)
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
				claim(platform, name, tool.Name)
			}
			if tool.Wrapper != "" {
				claim(platform, tool.Name, tool.Name+" (wrapper)")
				assert.True(t, strings.HasPrefix(tool.Wrapper, "#!/bin/sh\n"), "%s: the wrapper is a shell script", tool.Name)
			}
		}
	}
	require.NotNil(t, d.Rustup.Linux, "rustup is a Linux pin")
	for _, n := range d.Rustup.Links {
		claim("linux", n, "rustup")
	}
	claim("linux", "rustup", "rustup (wrapper)")
	claim("linux", "rustc", "rustc (wrapper)")
	for _, c := range d.CargoTools {
		claim("linux", c.Name, "cargo tool")
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
		var lock []string
		for _, m := range locked {
			lock = append(lock, m[1])
		}
		require.NotNil(t, tool.Darwin, "%s: a Mac bench builds it too", name)
		for platform, p := range tool.platforms() {
			assert.Empty(t, p.Build, "%s (%s): one build on every platform", name, platform)
			var wheels []string
			for _, e := range p.Extras {
				assert.True(t, strings.HasSuffix(e.Path, "-py3-none-any.whl"), "%s (%s): %s is a pure-Python wheel, the same on every platform", name, platform, e.Path)
				wheels = append(wheels, e.SHA256)
			}
			require.NotEmpty(t, wheels, "%s (%s): mypy's wheels are fetched as extras", name, platform)
			assert.ElementsMatch(t, wheels, lock, "%s (%s): the hash lock names exactly the wheels fetched", name, platform)
		}
	}
}

// A Mac bench holds every pin a schema leg stages from the SDK, and Rust and Erlang/OTP, each held to a digest or a
// tag's commit. Rust there is the release's standalone installer, giving the commands Linux links from rustup.
func TestSDKToolchainMacPins(t *testing.T) {
	t.Parallel()
	d := readSDKDefaults(t)
	byName := map[string]sdkTool{}
	for _, tool := range d.Tools {
		byName[tool.Name] = tool
	}
	for _, name := range []string{"python3.11", "python3.15", "squirrel-double", "lua", "luau", "stylua", "goose", "lobster", "moo", "rust", "otp"} {
		tool, ok := byName[name]
		require.True(t, ok, "%s is in the defaults", name)
		p := tool.Darwin
		require.NotNil(t, p, "%s has a darwin pin", name)
		if tool.Kind == "git" {
			assert.Regexp(t, hex40, p.Commit, "%s (darwin): the tag's commit", name)
		} else {
			assert.True(t, hex64.MatchString(p.SHA256) || hex128.MatchString(p.SHA512), "%s (darwin): the archive's digest", name)
		}
		assert.NotEmpty(t, tool.build(p)+tool.Probe, "%s (darwin): a probe and, for a source pin, a build", name)
	}
	rust := byName["rust"]
	assert.Nil(t, rust.Linux, "Linux gets Rust through rustup")
	assert.Equal(t, "https://static.rust-lang.org/dist/rust-"+rust.Version+"-aarch64-apple-darwin.tar.xz", rust.Darwin.URL)
	assert.Equal(t, d.Rustup.Toolchains[0].Name, rust.Version, "the Mac's Rust is the toolchain Linux has on PATH")
	var links []string
	for _, l := range rust.Links {
		s, _ := l.(string)
		links = append(links, filepath.Base(s))
	}
	assert.ElementsMatch(t, append([]string{"rustc"}, d.Rustup.Links...), links, "the same commands in <sdk>/bin as Linux (where rustc is the wrapper)")
	assert.Contains(t, rust.Build, "--components=rustc,rust-std-aarch64-apple-darwin,cargo,rustfmt-preview,clippy-preview")
	otp := byName["otp"]
	assert.NotEmpty(t, otp.Darwin.Build, "otp: macOS builds the source, Linux unpacks hex.pm's build")
	assert.Contains(t, otp.Darwin.Build, "--disable-dynamic-ssl-lib", "otp: crypto reaches no library outside the SDK")
}

// The role follows every symlink in nova_sdk_dir before it uses it, so env.sh and every link it writes name the real
// directory; every task, the template and the defaults use the resolved sdk_dir, never nova_sdk_dir itself.
func TestSDKToolchainResolvesTheSDKDirectory(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(sdkToolchainRole + "/tasks/main.yml")
	require.NoError(t, err)
	var raw []any
	require.NoError(t, yaml.Unmarshal(b, &raw))
	tasks := mapsOf(t, raw)
	require.GreaterOrEqual(t, len(tasks), 3)
	// first the shared SDK's checks, when asked for (tasks/shared.yml); the resolution then names the shared directory
	assert.Equal(t, "shared.yml", asText(tasks[0]["ansible.builtin.include_tasks"]))
	assert.Equal(t, "nova_sdk_shared | bool", asText(tasks[0]["when"]))
	resolve := taskText(t, tasks[1])
	assert.Contains(t, resolve, "os.path.realpath")
	assert.Contains(t, resolve, "if nova_sdk_shared | bool else nova_sdk_dir }}")
	assert.Equal(t, false, tasks[1]["check_mode"], "resolved under --check too")
	fact, _ := tasks[2]["ansible.builtin.set_fact"].(map[string]any)
	assert.Equal(t, "{{ sdk_dir_real.stdout }}", fact["sdk_dir"], "sdk_dir is the resolved path")
	files, err := filepath.Glob(sdkToolchainRole + "/*/*")
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		if strings.HasSuffix(f, "tasks/link.yml") {
			continue // the link nova_sdk_dir becomes, made once the shared SDK is whole
		}
		assert.Equal(t, 0, strings.Count(string(b), "{{ nova_sdk_dir }}"), "%s uses the resolved sdk_dir", f)
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

// A CI job's rustup-init (CARGO_HOME under the runner's _work/_temp) wrote `. "<temp>/cargo/env"` into the login's
// shell files; the role takes that line out, and only that line.
func TestSDKToolchainRemovesACIJobsCargoEnvLine(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(sdkToolchainRole + "/tasks/rustup.yml")
	require.NoError(t, err)
	var raw []any
	require.NoError(t, yaml.Unmarshal(b, &raw))
	var found map[string]any
	for _, task := range mapsOf(t, raw) {
		if task["name"] == "no shell file sources a CI job's cargo env" {
			found = task
		}
	}
	require.NotNil(t, found, "the role removes a CI job's cargo env line")
	li, ok := found["ansible.builtin.lineinfile"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "absent", li["state"])
	re := regexp.MustCompile(asText(li["regexp"]))
	assert.True(t, re.MatchString(`. "/home/nova/actions-runner/mas-bandwidth-2/_work/_temp/cargo/env"`))
	assert.False(t, re.MatchString(`. "$HOME/.cargo/env"`), "a login's own cargo env stays")
	assert.Subset(t, found["loop"], []any{".profile", ".bashrc"}, "the files rustup-init writes on these benches")
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
	for _, dir := range []string{"{{ sdk_dir }}/dotnet-home", "{{ sdk_dir }}/dotnet-home/.dotnet"} {
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

// A present tool whose run probe fails (run under --check too, never failing the play) joins the missing tools, and
// the install moves its old directory aside, dated, never deleted. otp's probe holds bin/erl's fallback root inside its
// own real directory and starts the runtime.
func TestSDKToolchainRebuildsABrokenInstall(t *testing.T) {
	t.Parallel()
	all := ""
	for _, task := range sdkTasks(t)["main.yml"] {
		if task["register"] == "sdk_run" {
			assert.Equal(t, false, task["check_mode"])
			assert.Equal(t, false, task["failed_when"])
		}
		all += taskText(t, task)
	}
	assert.Contains(t, all, "sdk_run.results | rejectattr('rc', 'equalto', 0) | map(attribute='item')", "a broken tool is missing")
	assert.Contains(t, all, `mv "$dest" "$dest.broken-$(date -u +%Y%m%dT%H%M%SZ)"`)
	for _, tool := range readSDKDefaults(t).Tools {
		if tool.Name == "otp" {
			assert.Equal(t, `grep -qF "\"$dest" bin/erl && bin/erl -noshell -eval 'halt().'`, tool.Run)
		}
	}
}

// env.sh puts the SDK's bin first on PATH and is the file ~/.bashrc sources.
func TestSDKToolchainEnvPutsTheSDKFirst(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(sdkToolchainRole + "/templates/env.sh.j2")
	require.NoError(t, err)
	env := string(b)
	assert.Contains(t, env, `PATH="{{ sdk_dir }}/bin:`)
	assert.Contains(t, env, `export DOTNET_CLI_HOME="{{ sdk_dir }}/dotnet-home"`)
	assert.NotContains(t, env, "RUSTUP_HOME", "a CI job's own rustup keeps its own home; <sdk>/bin/rustup names the SDK's")
	rc := ""
	for _, task := range sdkTasks(t)["main.yml"] {
		if li, ok := task["ansible.builtin.lineinfile"].(map[string]any); ok {
			rc = asText(li["line"])
			assert.Equal(t, "BOF", li["insertbefore"], "first, before a non-interactive shell's early return")
		}
	}
	assert.Contains(t, rc, `. "{{ sdk_dir }}/env.sh"`)
}

// A Makefile that stages a pin from the SDK (a sandboxed job has no network) finds it by directory: NOVA_SDK names the
// SDK itself, each interpreter is in <name>-<version> with the command at the path the Makefile looks for, and moo,
// built from a tag, records the commit it was built from in PIN, written last so it is also the probe.
func TestSDKToolchainPinsAreWhereAMakefileStagesThem(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(sdkToolchainRole + "/defaults/main.yml")
	require.NoError(t, err)
	var files struct {
		Env map[string]string `yaml:"nova_sdk_env_files"`
	}
	require.NoError(t, yaml.Unmarshal(b, &files))
	assert.Equal(t, ".", files.Env["NOVA_SDK"], "NOVA_SDK names the SDK directory, exported where it is")
	d := readSDKDefaults(t)
	tool := func(name string) *sdkTool {
		for i := range d.Tools {
			if d.Tools[i].Name == name {
				return &d.Tools[i]
			}
		}
		t.Fatalf("%s is not in the defaults", name)
		return nil
	}
	for name, probe := range map[string]string{"lua": "bin/lua", "luau": "bin/luau", "goose": "goose", "lobster": "bin/lobster",
		"stylua": "stylua", "moo": "PIN"} {
		tl := tool(name)
		dir := strings.TrimSuffix(tl.Dir, "-"+tl.Version)
		if dir == tl.Dir {
			dir = strings.TrimSuffix(tl.Dir, "-v"+tl.Version)
		}
		assert.Equal(t, name, dir, "%s: its directory is <name>-<version> or <name>-v<version>", name)
		assert.Equal(t, probe, tl.Probe, "%s: the probe is the file the Makefile stages", name)
	}
	moo := tool("moo")
	assert.Equal(t, "git", moo.Kind)
	assert.Contains(t, moo.Build, `git rev-parse HEAD >"$dest/PIN"`)
	assert.Equal(t, []any{"bin/moo", "bin/moo-analyze"}, moo.Links, "moo: the CLI and the analyzer, both built")
	for _, name := range []string{"goose", "lobster", "stylua", "lua"} {
		assert.NotNil(t, tool(name).Darwin, "%s: a Mac bench stages it too", name)
	}
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
	assert.Contains(t, string(env), `if [ -x "{{ sdk_dir }}/{{ v }}" ]; then export {{ k }}=`)
}

// Each make variable env.sh exports for a file under the SDK names a file the role installs there: a tool's probe or
// one of its links, under that tool's directory, so it is executable exactly where the tool is installed.
func TestSDKToolchainEnvFilesNameInstalledFiles(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile(sdkToolchainRole + "/defaults/main.yml")
	require.NoError(t, err)
	var files struct {
		Env map[string]string `yaml:"nova_sdk_env_files"`
	}
	require.NoError(t, yaml.Unmarshal(b, &files))
	for _, k := range []string{"SQ", "LUA", "LUAU", "LUAU_ANALYZE", "MOO", "MOO_ANALYZE", "GOOSE", "LOBSTER"} {
		assert.Contains(t, files.Env, k, "env.sh exports %s where its file is", k)
	}
	d := readSDKDefaults(t)
	for k, v := range files.Env {
		if k == "NOVA_SDK" {
			continue
		}
		dir, rest, _ := strings.Cut(v, "/")
		var tool *sdkTool
		for i := range d.Tools {
			if d.Tools[i].Dir == dir {
				tool = &d.Tools[i]
			}
		}
		require.NotNil(t, tool, "%s=%s names a tool's directory", k, v)
		installed := rest == tool.Probe
		for _, l := range tool.Links {
			switch p := l.(type) {
			case string:
				installed = installed || rest == p
			case map[string]any:
				installed = installed || rest == p["path"]
			}
		}
		assert.True(t, installed, "%s=%s is %s's probe or one of its links", k, v, tool.Name)
	}
}

// matchRe is the regexp inside a `<x> is match('<re>')` condition (Python's re; these patterns read the same in RE2).
func matchRe(t *testing.T, cond string) *regexp.Regexp {
	t.Helper()
	m := regexp.MustCompile(`is match\('([^']*)'\)`).FindStringSubmatch(cond)
	require.Len(t, m, 2, "no match('...') in %q", cond)
	return regexp.MustCompile(m[1])
}

// rowan-tools' fleet/bench.yml put a Go first by hand: a marked "fleet Go" block in ~/.bashrc and ~/go/bin/go and
// gofmt linked to one SDK Go (go1.26.6 on amd-3, hulk and intel). The role takes out that block and those links, and
// only them; a link ~/.local/bin/go resolves through stays, since a runner unit's PATH may reach go only that way.
func TestSDKToolchainRemovesTheHandGoBlockAndLinks(t *testing.T) {
	t.Parallel()
	tasks := sdkTasks(t)["main.yml"]

	i := taskIndex(tasks, `~/.bashrc has no hand "fleet Go" block`)
	require.GreaterOrEqual(t, i, 0)
	bi, ok := tasks[i]["ansible.builtin.blockinfile"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "absent", bi["state"])
	assert.Equal(t, "# {mark} fleet Go (rowan-tools fleet/bench.yml)", bi["marker"], "the markers bench.yml wrote, exactly")
	assert.Equal(t, "{{ ansible_facts['user_dir'] }}/.bashrc", bi["path"])
	assert.Equal(t, "sdk_platform == 'linux'", asText(tasks[i]["when"]))
	assert.Greater(t, i, taskIndex(tasks, "~/.bashrc sources env.sh first"), "env.sh's line is in place before the block goes")

	s := taskIndex(tasks, "the go links in ~/go/bin")
	require.GreaterOrEqual(t, s, 0)
	assert.Equal(t, []any{"go/bin/go", "go/bin/gofmt", ".local/bin/go"}, tasks[s]["loop"])

	r := taskIndex(tasks, "~/go/bin has no go or gofmt linked to an SDK Go")
	require.GreaterOrEqual(t, r, 0)
	assert.Greater(t, r, s)
	f, ok := tasks[r]["ansible.builtin.file"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "absent", f["state"])
	assert.Equal(t, "{{ sdk_go_links.results[:2] if sdk_platform == 'linux' else [] }}", asText(tasks[r]["loop"]),
		"only the two ~/go/bin links are candidates; ~/.local/bin/go is never removed here")
	when := mapsOrText(tasks[r]["when"])
	require.Len(t, when, 3)
	assert.Equal(t, "item.stat.islnk | default(false)", when[0], "a real binary in ~/go/bin is never removed")
	re := matchRe(t, when[1])
	for _, target := range []string{
		"/home/nova/sdk/go1.26.6/bin/go", "/home/nova/sdk/go1.26.10/bin/gofmt",
		"/home/glenn/nova-bench/sdk/go1.26.10/bin/gofmt", "/home/glenn/sdk/go1.26.10/bin/go",
	} {
		assert.True(t, re.MatchString(target), target)
	}
	for _, target := range []string{
		"/home/nova/go/bin/go1.26.6", "/usr/lib/go-1.26/bin/go", "/home/nova/sdk/go1.26.10/bin/go.old",
		"/home/nova/sdk/golangci-lint-2.12.2/golangci-lint", "go1.26.6/bin/go",
	} {
		assert.False(t, re.MatchString(target), target)
	}
	assert.Equal(t, "(sdk_go_links.results[2].stat.lnk_target | default('')) != item.stat.path", when[2],
		"a ~/go/bin link that ~/.local/bin/go points at stays")
}

// An older SDK install linked gh 2.102.0 into <sdk>/bin on captain and hulk; no tool of the role installs gh. The role
// removes that link when it points exactly there, and nothing else named gh.
func TestSDKToolchainRemovesTheHandGhLink(t *testing.T) {
	t.Parallel()
	tasks := sdkTasks(t)["main.yml"]
	i := taskIndex(tasks, "<sdk>/bin has no hand gh link")
	require.GreaterOrEqual(t, i, 0)
	f, ok := tasks[i]["ansible.builtin.file"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "{{ sdk_dir }}/bin/gh", f["path"])
	assert.Equal(t, "absent", f["state"])
	when := mapsOrText(tasks[i]["when"])
	assert.Equal(t, []string{
		"sdk_gh_link.stat.islnk | default(false)",
		"sdk_gh_link.stat.lnk_target == sdk_dir ~ '/gh-2.102.0/gh_2.102.0_linux_amd64/bin/gh'",
	}, when)
	for _, tool := range readSDKDefaults(t).Tools {
		assert.NotEqual(t, "gh", tool.Name, "a gh tool would put the link back each run; this removal would then go")
	}
}

// mapsOrText is a task's when as a list of strings (a single condition is a list of one).
func mapsOrText(v any) []string {
	if l, ok := v.([]any); ok {
		out := make([]string, 0, len(l))
		for _, c := range l {
			out = append(out, asText(c))
		}
		return out
	}
	return []string{asText(v)}
}
