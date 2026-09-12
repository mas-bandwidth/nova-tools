package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// scratch is one job's shape: a write set with a data home inside it (rule 9), a read
// set, and a secret in NEITHER list.
func scratch(t *testing.T) (write, read, home, secret string) {
	t.Helper()
	base := t.TempDir()
	write = filepath.Join(base, "w")
	read = filepath.Join(base, "r")
	home = filepath.Join(write, "home")
	secretDir := filepath.Join(base, "secret")
	for _, d := range []string{write, read, home, secretDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	secret = filepath.Join(secretDir, "env")
	if err := os.WriteFile(secret, []byte("not-a-real-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Resolve, because /var on a Mac is a symlink to /private/var and every path this
	// package holds is resolved (rule 5).
	for _, p := range []*string{&write, &read, &home, &secret} {
		if got, err := filepath.EvalSymlinks(*p); err == nil {
			*p = got
		}
	}
	return write, read, home, secret
}

// anExecutable is a command this platform actually has, because rule 5 resolves the argv's
// first word before any policy exists and a test that hard-coded /bin/echo asserted
// "not_found" on windows while claiming to assert the thing it was written about.
func anExecutable(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return "/bin/echo"
	}
	for _, candidate := range []string{os.Getenv("COMSPEC"), filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")} {
		if candidate == "" {
			continue
		}
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate
		}
	}
	found, err := exec.LookPath("cmd.exe")
	if err != nil {
		t.Skip("skipped: this windows machine has no cmd.exe to resolve, and rule 5 resolves the command before the policy")
	}
	return found
}

// needUnixPaths skips a test whose subject is the TEXT of the darwin sandbox-exec profile.
// Its literals are absolute unix paths; a windows path is not one, and the AppContainer body
// writes no policy text at all. Darwin and linux both run these.
func needUnixPaths(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("skipped on windows: this asserts the darwin profile text, whose literals are unix paths")
	}
}

func in(t *testing.T, write, read, home string, argv ...string) Input {
	t.Helper()
	return Input{Reads: []string{read}, Writes: []string{write}, Home: home, Argv: argv}
}

// Rule 4: --write has no default, and zero of it is a refusal that names the flag.
func TestRefusesToGuessAWriteSet(t *testing.T) {
	_, bad := Build(Input{Argv: []string{anExecutable(t)}, Home: "/"})
	if len(bad) == 0 {
		t.Fatal("a run with no --write was built; rule 4 refuses to guess")
	}
	var found bool
	for _, r := range bad {
		if r.Reason == "bad_write" && strings.Contains(r.Text, "--write") && r.Code() == ExitRefused {
			found = true
		}
	}
	if !found {
		t.Fatalf("no bad_write refusal naming the flag at 125: %v", bad)
	}
}

// Rule 5: relative is refused WITH the absolute form; absent is refused and NOT created.
func TestPathsAreResolvedAbsoluteAndExisting(t *testing.T) {
	write, read, home, _ := scratch(t)

	_, bad := Build(in(t, "relative/dir", read, home, anExecutable(t)))
	if len(bad) == 0 || !strings.Contains(bad[0].Text, "is relative") {
		t.Fatalf("a relative --write was not refused: %v", bad)
	}
	abs, _ := filepath.Abs("relative/dir")
	if !strings.Contains(bad[0].Text, abs) {
		t.Fatalf("the refusal did not print the absolute form it wanted: %q", bad[0].Text)
	}

	missing := filepath.Join(write, "not-there")
	_, bad = Build(in(t, missing, read, home, anExecutable(t)))
	if len(bad) == 0 || !strings.Contains(bad[0].Text, "does not exist") {
		t.Fatalf("an absent --write was not refused: %v", bad)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("the absent path was created; rule 5 refuses, it does not create")
	}
}

// Rule 4: a path in both lists is a refusal naming both flags, never a silent merge.
func TestSamePathInBothListsIsARefusal(t *testing.T) {
	write, _, home, _ := scratch(t)
	_, bad := Build(Input{Reads: []string{write}, Writes: []string{write}, Home: home, Argv: []string{anExecutable(t)}})
	if len(bad) == 0 {
		t.Fatal("a path in both lists was merged")
	}
	if !strings.Contains(bad[0].Text, "--read") || !strings.Contains(bad[0].Text, "--write") {
		t.Fatalf("the refusal did not name both flags: %q", bad[0].Text)
	}
}

// Rule 9: a HOME outside every --write is refused BEFORE the command runs.
func TestHomeOutsideTheWriteSetIsRefused(t *testing.T) {
	write, read, _, _ := scratch(t)
	_, bad := Build(in(t, write, read, os.TempDir(), anExecutable(t)))
	var found bool
	for _, r := range bad {
		if r.Reason == "home_outside" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a HOME outside every --write was accepted: %v", bad)
	}
	if p, bad := Build(in(t, write, read, filepath.Join(write, "home"), anExecutable(t))); len(bad) > 0 || p.Home == "" {
		t.Fatalf("a HOME inside the write set was refused: %v", bad)
	}
}

// Rules 13 and 8: the cwd and the temp directory default to the first --write, and an
// explicit one outside the write set is refused.
func TestCwdAndTmpAreInsideTheWall(t *testing.T) {
	write, read, home, _ := scratch(t)
	p, bad := Build(in(t, write, read, home, anExecutable(t)))
	if len(bad) > 0 {
		t.Fatalf("refused: %v", bad)
	}
	if p.Cwd != write {
		t.Fatalf("cwd = %q, want the first --write %q", p.Cwd, write)
	}
	if want := filepath.Join(write, tmpDirName); !Inside(p.Tmp, write) || filepath.Base(p.Tmp) != filepath.Base(want) {
		t.Fatalf("tmp = %q, want %q", p.Tmp, want)
	}
	if fi, err := os.Stat(p.Tmp); err != nil || !fi.IsDir() {
		t.Fatalf("the one directory the tool creates was not created: %v", err)
	}
	iv := in(t, write, read, home, anExecutable(t))
	iv.Cwd = read
	if _, bad = Build(iv); len(bad) == 0 || bad[0].Reason != "bad_cwd" {
		t.Fatalf("a --cwd outside the write set was accepted: %v", bad)
	}
}

// The exit-codes section: the pre-flight stats the resolved command OUTSIDE the wall.
func TestCommandPreflight(t *testing.T) {
	write, read, home, _ := scratch(t)
	notExec := filepath.Join(write, "data.txt")
	if err := os.WriteFile(notExec, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// Windows has no executable bit, and resolveCommand says so in one place: the
		// preflight there is the stat and the directory check, not the mode. Asserting a
		// refusal that cannot fire would be asserting the assertion.
		t.Log("the executable-bit half of the preflight is skipped on windows: there is no such bit")
	} else if _, bad := Build(in(t, write, read, home, notExec)); len(bad) == 0 || bad[0].Reason != "not_executable" || bad[0].Code() != ExitRefused {
		t.Fatalf("a command with no executable bit was accepted: %v", bad)
	}
	iv := in(t, write, read, home, "definitely-not-a-command-here")
	iv.LookAt = write
	_, bad := Build(iv)
	if len(bad) == 0 || bad[0].Reason != "not_found" || bad[0].Code() != ExitNotFound {
		t.Fatalf("a command on no PATH entry was not 127 not_found: %v", bad)
	}
}

// The build's own decision, from "to verify at build" item 2: a path carrying an SBPL
// metacharacter is refused, because the ancestor literals put a path INTO the profile.
func TestPathWithSbplMetacharacterIsRefused(t *testing.T) {
	needUnixPaths(t) // `C:\Program Files (x86)` is an ordinary windows directory
	write, read, home, _ := scratch(t)
	odd := filepath.Join(write, `a (paren)`)
	if err := os.MkdirAll(odd, 0o755); err != nil {
		t.Fatal(err)
	}
	iv := in(t, odd, read, home, anExecutable(t))
	if _, bad := Build(iv); len(bad) == 0 {
		t.Fatal("a path holding a paren was accepted into the generated policy")
	}
}

// Rule 9 and this build's agent fix: the environment passes through, the three temp
// variables are the tool's, and the agent variables are dropped.
func TestChildEnv(t *testing.T) {
	env := []string{"HOME=/w/home", "ANTHROPIC_API_KEY=sk-not-real", "TMPDIR=/outside", "SSH_AUTH_SOCK=/private/tmp/agent.sock", "SSH_AGENT_PID=9", "PATH=/bin"}
	got := strings.Join(ChildEnv(env, "/w/.nova-sandbox-tmp"), "\n")
	for _, want := range []string{"ANTHROPIC_API_KEY=sk-not-real", "PATH=/bin", "TMPDIR=/w/.nova-sandbox-tmp", "TMP=/w/.nova-sandbox-tmp", "TEMP=/w/.nova-sandbox-tmp"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the child's environment is missing %q:\n%s", want, got)
		}
	}
	for _, gone := range []string{"SSH_AUTH_SOCK", "SSH_AGENT_PID", "TMPDIR=/outside"} {
		if strings.Contains(got, gone) {
			t.Fatalf("%q survived into the child's environment:\n%s", gone, got)
		}
	}
	if d := DroppedEnv(env); len(d) != 2 {
		t.Fatalf("DroppedEnv = %v, want the two agent variables", d)
	}
}

// The ancestor literals of the darwin profile: every proper ancestor, "/" excluded.
func TestAncestors(t *testing.T) {
	// Only the directories ABOVE each path are ancestors: c and d are the paths
	// themselves, and they are granted by their own subpath rule.
	got := Ancestors(filepath.FromSlash("/a/b/c"), filepath.FromSlash("/a/b/d"))
	want := []string{filepath.FromSlash("/a"), filepath.FromSlash("/a/b")}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("Ancestors = %v, want %v", got, want)
	}
	// The top of the tree is excluded, and it is asked for rather than spelled: "/" on unix
	// and `C:\` on windows are the same fact about the same loop.
	for _, d := range got {
		if filepath.Dir(d) == d {
			t.Fatalf("%q is the top of the tree and is in the ancestor list; it is granted file-read* above", d)
		}
	}
}

// Rule 15 and this build's network fix: the generated profile fills every marker, names
// the caller's paths only as parameters, and grants IP plus unix sockets under the write
// set — never (allow network*), which reaches the SSH agent socket.
func TestDarwinProfileIsGenerated(t *testing.T) {
	needUnixPaths(t)
	write, read, home, _ := scratch(t)
	p, bad := Build(in(t, write, read, home, anExecutable(t)))
	if len(bad) > 0 {
		t.Fatalf("refused: %v", bad)
	}
	text, params, err := DarwinProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	// The template's own header documents each marker and names the form this build
	// rejected, so both assertions are about the GRANTS — the lines that are not comments.
	grants := grantLines(text)
	if strings.Contains(grants, "@@") {
		t.Fatalf("a marker survived into the filled profile:\n%s", text)
	}
	if strings.Contains(grants, "(allow network*)") {
		t.Fatal("(allow network*) grants every unix-domain socket, including the SSH agent's")
	}
	for _, want := range []string{
		`(allow network-outbound (remote ip) (literal "/private/var/run/mDNSResponder"))`,
		`(allow network-outbound (subpath (param "WRITE0")))`,
		`(allow file-read* (subpath (param "READ0")))`,
		`(allow file-read* file-write* (subpath (param "WRITE0")))`,
		`(allow file-read-metadata (literal "` + filepath.Dir(write) + `"))`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the filled profile is missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, write+`"`) && !strings.Contains(text, `(literal "`+filepath.Dir(write)) {
		t.Fatal("a caller path entered the profile text as data")
	}
	// Every param the text names must be passed, or sandbox-exec is exit 65.
	for _, name := range []string{"READ0", "WRITE0", "HOME"} {
		if !strings.Contains(strings.Join(params, " "), name+"=") {
			t.Fatalf("param %s is named by the profile and not passed: %v", name, params)
		}
	}
	// Byte-identical twice: the same lists produce the same policy.
	again, _, err := DarwinProfile(p)
	if err != nil || again != text {
		t.Fatal("the generated policy is not deterministic")
	}
	// Rule 7: under --net-deny every network grant is withheld.
	p.NetDeny = true
	denied, _, err := DarwinProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	// Under --net-deny the IP grants are withheld. The per-write
	// (allow network-outbound (subpath (param "WRITEn"))) line stays, because the
	// template emits it with the write grant: it reaches a unix socket that is the job's
	// own file inside its own write set, which --net-deny is not about.
	for _, gone := range []string{"(remote ip)", "network-inbound"} {
		if strings.Contains(grantLines(denied), gone) {
			t.Fatalf("--net-deny left %s in the profile:\n%s", gone, denied)
		}
	}
	if p.Net() != "denied" {
		t.Fatalf("net = %q, want denied", p.Net())
	}
}

// grantLines is the filled profile with its comments removed: the template's header
// documents the markers and the form this build rejected, and a test that searched the
// whole text would be reading the documentation rather than the policy.
func grantLines(profile string) string {
	var out []string
	for _, line := range strings.Split(profile, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), ";;") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// Rule 7 of revision 6: inbound is granted only under --net-listen, and --net-deny with
// --net-listen is a refusal rather than a tool picking which the caller meant.
func TestInboundIsOnlyGrantedWhenAsked(t *testing.T) {
	needUnixPaths(t)
	write, read, home, _ := scratch(t)
	p, bad := Build(in(t, write, read, home, anExecutable(t)))
	if len(bad) > 0 {
		t.Fatalf("refused: %v", bad)
	}
	text, _, err := DarwinProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(grantLines(text), "network-inbound") {
		t.Fatal("inbound was granted to a job that did not ask to listen")
	}
	p.NetListen = true
	listening, _, err := DarwinProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(grantLines(listening), "(allow network-inbound (local ip))") {
		t.Fatalf("--net-listen granted no inbound:\n%s", listening)
	}
	iv := in(t, write, read, home, anExecutable(t))
	iv.NetDeny, iv.NetListen = true, true
	if _, bad := Build(iv); len(bad) == 0 || bad[0].Reason != "bad_net" {
		t.Fatalf("--net-deny with --net-listen was accepted: %v", bad)
	}
}

// Rule 9, revision 7: the scrub set is exactly what the spec names. AI_AGENT and
// CLAUDE_AGENT_SDK_VERSION are names that say what is RUNNING the job; they address
// nothing and must arrive, or the SANDBOX NOTE line is a false statement.
func TestScrubSetIsExactlyTheSpecs(t *testing.T) {
	caller := []string{
		"SSH_AUTH_SOCK=/private/tmp/agent.sock",
		"SSH_AGENT_PID=4242",
		"GPG_AGENT_INFO=/private/tmp/gpg:1:1",
		"PODMAN_AGENT_SOCK=/private/tmp/p.sock",
		"AI_AGENT=rowan",
		"CLAUDE_AGENT_SDK_VERSION=1.2.3",
		"FOO_TOKEN=secret-that-must-arrive",
	}
	got := map[string]bool{}
	for _, kv := range ChildEnv(caller, "/w/.nova-sandbox-tmp") {
		name, _, _ := strings.Cut(kv, "=")
		got[name] = true
	}
	for _, gone := range []string{"SSH_AUTH_SOCK", "SSH_AGENT_PID", "GPG_AGENT_INFO", "PODMAN_AGENT_SOCK"} {
		if got[gone] {
			t.Errorf("%s survived the scrub", gone)
		}
	}
	for _, kept := range []string{"AI_AGENT", "CLAUDE_AGENT_SDK_VERSION", "FOO_TOKEN"} {
		if !got[kept] {
			t.Errorf("%s was dropped; it names what runs the job, not an address", kept)
		}
	}
	dropped := strings.Join(DroppedEnv(caller), " ")
	if strings.Contains(dropped, "AI_AGENT") || strings.Contains(dropped, "CLAUDE_AGENT_SDK_VERSION") {
		t.Errorf("the NOTE line claims to have dropped a variable it did not: %q", dropped)
	}
}

// Rule 7, revision 7: mach-lookup is narrowed and the unqualified form is gone.
func TestMachLookupIsNarrowed(t *testing.T) {
	needUnixPaths(t)
	write, read, home, _ := scratch(t)
	pol, bad := Build(in(t, write, read, home, anExecutable(t)))
	if len(bad) > 0 {
		t.Fatalf("refused: %v", bad)
	}
	text, _, err := DarwinProfile(pol)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "(allow mach-lookup)" {
			t.Fatal("the template still carries the unqualified (allow mach-lookup): pbpaste reads the clipboard under it")
		}
	}
	for _, name := range []string{
		"com.apple.system.opendirectoryd.libinfo",
		"com.apple.SecurityServer",
		"com.apple.system.logger",
	} {
		if !strings.Contains(text, name) {
			t.Errorf("the measured mach-lookup set is missing %s", name)
		}
	}
}
