package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// fake replaces the exec seam: lookPath finds only the names in found, runCmd
// records its call (and what the extracted directory held) and returns code, and
// outCmd answers ansible-playbook --list-hosts with list and ansible-galaxy with
// ansible.posix.
type fake struct {
	found    map[string]string
	code     int
	list     string
	noGalaxy bool   // ansible-galaxy cannot run (exit 127)
	invFail  string // nova-config inventory --list fails with this stderr (exit 2)
	bin      string
	argv     []string
	env      []string
	dirMode  fs.FileMode
	script   string
	playText string
}

func (f *fake) install(t *testing.T) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	oldLook, oldRun, oldOut := lookPath, runCmd, outCmd
	t.Cleanup(func() { lookPath, runCmd, outCmd = oldLook, oldRun, oldOut })
	outCmd = func(bin string, args, env []string) (string, string, int) {
		switch {
		case slices.Contains(args, "--list-hosts"):
			return f.list, "", 0
		case filepath.Base(bin) == "nova-config" && f.invFail != "":
			return "", f.invFail, 2
		case filepath.Base(bin) == "nova-config":
			return "{}", "", 0
		case filepath.Base(bin) == "ansible-galaxy" && f.noGalaxy:
			return "", "exec: not found", 127
		case filepath.Base(bin) == "ansible-galaxy":
			return "ansible.posix 1.5.4\n", "", 0
		}
		return "", bin + ": not faked", 127
	}
	lookPath = func(name string) (string, error) {
		if p, ok := f.found[name]; ok {
			return p, nil
		}
		return "", errors.New("not found")
	}
	runCmd = func(bin string, argv, env []string) (int, error) {
		f.bin, f.argv, f.env = bin, argv, env
		play := argv[2]
		if fi, err := os.Stat(filepath.Dir(play)); err == nil {
			f.dirMode = fi.Mode().Perm()
		}
		b, _ := os.ReadFile(play)
		f.playText = string(b)
		s, _ := os.ReadFile(argv[1])
		f.script = string(s)
		return f.code, nil
	}
}

func runArgs(args ...string) (int, string, string) {
	var o, e bytes.Buffer
	code := run(args, &o, &e)
	return code, o.String(), e.String()
}

func TestEmbeddedTree(t *testing.T) {
	got := files(tree)
	for _, want := range []string{"install.yml", "services.yml", "requirements.yml", "group_vars/all.yml",
		"roles/nova-tools-install/tasks/main.yml", "roles/nova-tools-install/defaults/main.yml",
		"templates/nova-service.service.j2", "README.md"} {
		if !slices.Contains(got, want) {
			t.Errorf("embedded tree has no %s; got %v", want, got)
		}
	}
	for _, p := range got {
		if strings.HasSuffix(p, ".go") {
			t.Errorf("embedded tree extracts Go source %s", p)
		}
	}
	if ps := plays(tree); !slices.Contains(ps, "install") || !slices.Contains(ps, "services") || slices.Contains(ps, "requirements") {
		t.Errorf("plays = %v, want install and services and never requirements", ps)
	}
}

// Every play says what it is for: the purpose line is what nova-fleet plays prints.
func TestEveryPlayHasAPurpose(t *testing.T) {
	for _, p := range plays(tree) {
		b, err := fs.ReadFile(tree, p+".yml")
		if err != nil || header(string(b), "purpose") == "" {
			t.Errorf("%s.yml has no '# purpose:' line in its leading comment", p)
		}
	}
}

func TestPurposeParsing(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"# purpose: do a thing\n- hosts: all\n", "do a thing"},
		{"# a.yml\n#\n# purpose:   spaced out  \n", "spaced out"},
		{"- hosts: all\n# purpose: after the comment block\n", ""},
		{"# no purpose here\n", ""},
		{"", ""},
	} {
		if got := header(tc.text, "purpose"); got != tc.want {
			t.Errorf("header(%q, purpose) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestShowable(t *testing.T) {
	m := fstest.MapFS{
		"join.yml":            {Data: []byte("# purpose: join\n")},
		"requirements.yml":    {Data: []byte("collections: []\n")},
		"tailscale-acl.json":  {Data: []byte("{}\n")},
		"main.go":             {Data: []byte("package main\n")},
		"roles/x/tasks/a.yml": {Data: []byte("[]\n")},
	}
	for name, want := range map[string]string{"join": "join.yml", "tailscale-acl": "tailscale-acl.json", "requirements": "requirements.yml"} {
		if got, ok := showable(m, name); !ok || got != want {
			t.Errorf("showable(%s) = %q %v, want %q", name, got, ok, want)
		}
	}
	for _, name := range []string{"main", "a", "nope"} {
		if got, ok := showable(m, name); ok {
			t.Errorf("showable(%s) = %q, want refused", name, got)
		}
	}
	if got := plays(m); !slices.Equal(got, []string{"join"}) {
		t.Errorf("plays = %v, want [join]", got)
	}
}

func TestPlaysAndShow(t *testing.T) {
	code, out, errs := runArgs("plays")
	if code != 0 || errs != "" || !strings.Contains(out, "install ") || !strings.Contains(out, "install one nova-tools release") {
		t.Errorf("plays: code %d stdout %q stderr %q", code, out, errs)
	}
	want, _ := fs.ReadFile(tree, "install.yml")
	if code, out, _ := runArgs("show", "install"); code != 0 || out != string(want) {
		t.Errorf("show install: code %d, stdout is not install.yml", code)
	}
}

func TestPassThrough(t *testing.T) {
	f := &fake{found: map[string]string{"ansible-playbook": "/opt/ansible/bin/ansible-playbook"}, code: 4}
	f.install(t)
	inv := filepath.Join(t.TempDir(), "hosts.ini")
	if err := os.WriteFile(inv, []byte("localhost\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errs := runArgs("install", "--check", "--inventory", inv, "--diff", "--", "-e", "nova_tools_version=v0", "-l", "localhost", "--check")
	if code != 1 || !strings.HasSuffix(errs, "nova-fleet install: FAILED ansible-playbook exit=4; the play's own lines above say what, and the same command resumes\n") {
		t.Fatalf("exit %d, want 1 with ansible-playbook's 4 named (stderr %q)", code, errs)
	}
	if f.bin != "/opt/ansible/bin/ansible-playbook" {
		t.Errorf("ran %s", f.bin)
	}
	play := f.argv[2]
	want := []string{"-i", inv, play, "--check", "--diff", "-e", "nova_tools_version=v0", "-l", "localhost", "--check"}
	if !slices.Equal(f.argv, want) || filepath.Base(play) != "install.yml" {
		t.Errorf("argv = %q, want %q", f.argv, want)
	}
	if f.dirMode != 0o700 {
		t.Errorf("extracted dir mode %v, want 0700", f.dirMode)
	}
	if b, _ := fs.ReadFile(tree, "install.yml"); f.playText != string(b) {
		t.Error("the extracted play is not the embedded one")
	}
	if !slices.Contains(f.env, "ANSIBLE_INVENTORY_UNPARSED_FAILED=true") {
		t.Error("env lacks ANSIBLE_INVENTORY_UNPARSED_FAILED=true")
	}
	if _, err := os.Stat(filepath.Dir(play)); !os.IsNotExist(err) {
		t.Errorf("extracted dir %s left behind after the run", filepath.Dir(play))
	}
}

// listHosts is ansible-playbook --list-hosts' output for postgres.yml when no machine is
// in the services group: its one machine play matched nothing; the localhost play is not counted.
const listHosts = `
playbook: postgres.yml

  play #1 (services): nova-config's PostgreSQL	TAGS: []
    pattern: ['services']
    hosts (0):

  play #2 (localhost): the port answers from the control node	TAGS: []
    pattern: ['localhost']
    hosts (1):
      localhost
`

// A play whose host group is empty (no machine has the role, or -l names none in it) would exit 0
// having done nothing: it is refused before it runs, naming the group and how to fill it.
func TestEmptyHostGroupIsRefused(t *testing.T) {
	f := &fake{found: map[string]string{"ansible-playbook": "/a/ansible-playbook", "nova-config": "/b/nova-config"}, list: listHosts}
	f.install(t)
	code, out, errs := runArgs("postgres", "--check")
	want := "nova-fleet postgres: no host is in services (no machine row has that role, or -l names none in it), so the play would do nothing; " +
		"run: nova-config machine set <machine> --roles <its roles>,services --actor <you>, then nova-fleet postgres again\n"
	if code != 1 || out != "" || errs != want || f.argv != nil {
		t.Errorf("empty group: code %d stdout %q stderr %q ran %q; want exit 1, nothing run and %q", code, out, errs, f.argv, want)
	}
	// a pattern of several groups names each; an empty inventory (all) asks for a machine row
	f.list = "  play #1 (bench:runner:services): x\tTAGS: []\n    pattern: ['bench:runner:services']\n    hosts (0):\n"
	if code, _, errs := runArgs("health"); code != 1 || !strings.HasPrefix(errs, "nova-fleet health: no host is in bench, runner, services (") {
		t.Errorf("several groups: code %d stderr %q", code, errs)
	}
	f.list = "  play #1 (all): x\tTAGS: []\n    pattern: ['all']\n    hosts (0):\n"
	if code, _, errs := runArgs("secrets-refresh"); code != 1 || !strings.Contains(errs, "run: nova-config machine add <machine> --user <login> --roles bench --actor <you>") {
		t.Errorf("empty inventory: code %d stderr %q", code, errs)
	}
	// one machine play with a host runs (install's opencode play may match nothing); so does a
	// play whose only empty plays are localhost or negated ("!all": no machine named)
	for _, list := range []string{
		"  play #1 (bench:runner:services): a\n    hosts (1):\n      m1\n  play #2 (bench): b\n    hosts (0):\n",
		"  play #1 (localhost): a\n    hosts (1):\n      localhost\n  play #2 (!all): b\n    hosts (0):\n",
		"",
	} {
		f.list, f.argv = list, nil
		if code, _, errs := runArgs("install"); code != 0 || f.argv == nil {
			t.Errorf("list %q: code %d stderr %q ran %q, want the play run", list, code, errs, f.argv)
		}
	}
	// --syntax-check needs no host: the empty group is not asked about
	f.list, f.argv = listHosts, nil
	if code, _, errs := runArgs("postgres", "--", "--syntax-check"); code != 0 || f.argv == nil {
		t.Errorf("--syntax-check: code %d stderr %q", code, errs)
	}
}

// install's own flags reach ansible-playbook as one -e and one -l, before what follows --.
func TestInstallRepoTagHosts(t *testing.T) {
	f := &fake{found: map[string]string{"ansible-playbook": "/a/ansible-playbook"}}
	f.install(t)
	inv := filepath.Join(t.TempDir(), "hosts.ini")
	if err := os.WriteFile(inv, []byte("hetzner2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errs := runArgs("install", "--check", "--repo", "mas-bandwidth/nova-sprint", "--inventory", inv, "--tag=v2.0.0", "hetzner2", "bench", "--", "-v")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	want := []string{"-i", inv, f.argv[2], "--check", "-e",
		`{"nova_tools_names":[],"nova_tools_repo":"mas-bandwidth/nova-sprint","nova_tools_version":"v2.0.0"}`, "-l", "hetzner2,bench", "-v"}
	if !slices.Equal(f.argv, want) {
		t.Errorf("argv = %q, want %q", f.argv, want)
	}
}

func TestInstallArgs(t *testing.T) {
	for _, tc := range []struct{ in, want []string }{
		{[]string{"--tag", "v2.0.0", "bench"}, []string{"--", "-e", `{"nova_tools_version":"v2.0.0"}`, "-l", "bench"}},
		{[]string{"--repo", "o/n", "--names", "nova-sprint,nova-sprint-dash", "--tag", "v2.0.0"},
			[]string{"--", "-e", `{"nova_tools_names":["nova-sprint","nova-sprint-dash"],"nova_tools_repo":"o/n","nova_tools_version":"v2.0.0"}`}},
		{[]string{"--check", "--", "-e", "nova_tools_version=v1.0.0", "-l", "x"}, []string{"--check", "--", "-e", "nova_tools_version=v1.0.0", "-l", "x"}},
		{[]string{"-h"}, []string{"-h"}},
	} {
		if got, err := installArgs(tc.in); err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("installArgs(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestDefaultInventoryExecsNovaConfig(t *testing.T) {
	f := &fake{found: map[string]string{"ansible-playbook": "/a/ansible-playbook", "nova-config": "/b/nova-config"}}
	f.install(t)
	if code, _, errs := runArgs("services", "--", "-l", "bench"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if filepath.Dir(f.argv[1]) != filepath.Dir(f.argv[2]) || filepath.Base(f.argv[1]) != "inventory" {
		t.Errorf("inventory %s is not the script beside the play %s", f.argv[1], f.argv[2])
	}
	if f.script != "#!/bin/sh\nexec '/b/nova-config' inventory \"$@\"\n" {
		t.Errorf("inventory script = %q", f.script)
	}
	if !slices.Equal(f.argv[3:], []string{"-l", "bench"}) {
		t.Errorf("pass-through = %q", f.argv[3:])
	}
}

// A store nova-config cannot reach stops the play before ansible-playbook: one line, the cause first, then the fix,
// and ansible-playbook never runs (it would bury the cause under pages of warnings and "No inventory was parsed").
func TestInventoryFailureSaysItsCauseFirst(t *testing.T) {
	fix := "; nova-config inventory --list shows the whole error; run: NOVA_PG_DSN=" + `'postgres://nova_config@<the store machine>:5432/nova?sslmode=disable'` + " nova-secrets exec --only NOVA_PG_PASSWORD -- nova-fleet windows-runners ...\n"
	for _, tc := range []struct{ stderr, cause string }{
		{"nova-config inventory: no store: set NOVA_PG_DSN or --pg (postgres://USER@HOST:PORT/DB?sslmode=disable)\n",
			"nova-config inventory: no store: set NOVA_PG_DSN or --pg (postgres://USER@HOST:PORT/DB?sslmode=disable)"},
		{"some warning\nnova-config inventory: dial tcp 127.0.0.1:5432: connect: connection refused\n\n",
			"nova-config inventory: dial tcp 127.0.0.1:5432: connect: connection refused"},
	} {
		f := &fake{found: map[string]string{"ansible-playbook": "/a/ansible-playbook", "nova-config": "/b/nova-config"}, invFail: tc.stderr}
		f.install(t)
		code, out, errs := runArgs("windows-runners", "--", "-l", "captain")
		want := "nova-fleet windows-runners: the inventory (nova-config inventory --list) failed: " + tc.cause + fix
		if code != 2 || out != "" || errs != want {
			t.Errorf("stderr %q: exit %d, stdout %q, stderr %q; want exit 2 and %q", tc.stderr, code, out, errs, want)
		}
		if f.bin != "" {
			t.Errorf("stderr %q: ansible-playbook ran (%s) after the inventory failed", tc.stderr, f.bin)
		}
	}
}

func TestRefusals(t *testing.T) {
	f := &fake{found: map[string]string{"nova-config": "/b/nova-config"}}
	f.install(t)
	if ps := plays(tree); len(ps) < 2 {
		t.Fatalf("plays = %v", ps)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"install", "--bogus"}, "nova-fleet install: unknown flag --bogus (ansible-playbook flags go after --); run: nova-fleet install -h\n"},
		{[]string{"install", "-l", "x"}, "nova-fleet install: unknown flag -l (ansible-playbook flags go after --); run: nova-fleet install -h\n"},
		{[]string{"install", "localhost", "--tag"}, "nova-fleet install: --tag wants a value; run: nova-fleet install -h\n"},
		{[]string{"services", "localhost"}, "nova-fleet services: unexpected argument localhost (ansible-playbook arguments go after --); run: nova-fleet services -h\n"},
		{[]string{"install", "--inventory"}, "nova-fleet install: --inventory wants a file or script; run: nova-fleet install -h\n"},
		{[]string{"install", "--inventory", "/no/such/inv"}, "nova-fleet install: --inventory /no/such/inv: no such file; run: nova-fleet install --inventory <an existing file or script>\n"},
		{[]string{"nope"}, "nova-fleet: unknown verb or play nope; verbs: plays, show, check, up, rented, help; plays: " + strings.Join(plays(tree), ", ") + "; run: nova-fleet help\n"},
		{[]string{"show"}, "nova-fleet show: want one play or file name; run: nova-fleet show -h\n"},
		{[]string{"show", "nope"}, "nova-fleet show: no play or file nope (plays: " + strings.Join(plays(tree), ", ") + "); run: nova-fleet show -h\n"},
		{[]string{"plays", "x"}, "nova-fleet plays: takes no arguments, got x; run: nova-fleet plays -h\n"},
		{[]string{"help", "--x"}, "nova-fleet help: unknown flag --x; run: nova-fleet help -h\n"},
	} {
		code, out, errs := runArgs(tc.args...)
		if code != 2 || out != "" || !strings.HasPrefix(errs, tc.want) {
			t.Errorf("%q: code %d stdout %q stderr %q, want exit 2 and %q", tc.args, code, out, errs, tc.want)
		}
	}
	if f.argv != nil {
		t.Errorf("a refusal ran ansible-playbook: %q", f.argv)
	}
}

func TestMissingHelpers(t *testing.T) {
	f := &fake{found: map[string]string{}}
	f.install(t)
	code, out, errs := runArgs("install")
	if code != 2 || !strings.HasPrefix(out, "CHECK MISSING ansible-playbook: ") || !strings.Contains(out, "\nCHECK MISSING nova-config: mkdir -p ~/.local/bin && ") ||
		errs != "nova-fleet install: this control node lacks 2 of what the play needs; the CHECK MISSING lines above paste as they stand (nova-config: or run: nova-fleet install --inventory <file|script>); run: nova-fleet check\n" {
		t.Errorf("nothing on PATH: code %d stdout %q stderr %q", code, out, errs)
	}
	f.found["nova-config"] = "/b/nova-config"
	code, out, errs = runArgs("install")
	if code != 2 || strings.Contains(out, "nova-config") || !strings.HasPrefix(out, "CHECK MISSING ansible-playbook: ") || !strings.Contains(errs, "lacks 1 of what the play needs") {
		t.Fatalf("no ansible-playbook: code %d stdout %q stderr %q", code, out, errs)
	}
	// the remedy's show works, and the extracted directory is gone (every exit removes it)
	if code, out, _ := runArgs("show", "requirements"); code != 0 || !strings.Contains(out, "collections") {
		t.Errorf("show requirements: code %d %q", code, out)
	}
	if left, _ := filepath.Glob(filepath.Join(os.Getenv("TMPDIR"), "nova-fleet-*")); len(left) != 0 {
		t.Errorf("extracted directories left behind: %v", left)
	}
}

// A play's gate with an ansible-galaxy that cannot run counts ansible.posix missing, with the reason.
func TestGateGalaxyThatCannotRun(t *testing.T) {
	f := &fake{found: map[string]string{"ansible-playbook": "/a/ansible-playbook", "nova-config": "/b/nova-config"}, noGalaxy: true}
	f.install(t)
	code, out, errs := runArgs("services")
	if code != 2 || f.argv != nil || out != "CHECK MISSING ansible.posix: nova-fleet show requirements > requirements.yml && /a/ansible-galaxy collection install -r requirements.yml  # /a/ansible-galaxy could not run, so the collections are unchecked: exec: not found\n" ||
		!strings.Contains(errs, "lacks 1 of what the play needs") {
		t.Errorf("code %d ran %q stdout %q stderr %q", code, f.argv, out, errs)
	}
}

func TestHelp(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		if code, out, errs := runArgs(args...); code != 0 || out != banner || errs != "" {
			t.Errorf("%q: code %d stderr %q", args, code, errs)
		}
	}
	for _, args := range [][]string{{"plays", "-h"}, {"help", "show"}, {"show", "--help"}, {"help", "help"}, {"help", "-h"}, {"install", "-h"}, {"help", "install"}} {
		code, out, errs := runArgs(args...)
		if code != 0 || errs != "" || !strings.HasPrefix(out, "nova-fleet "+strings.TrimPrefix(args[len(args)-1], "-")) && !strings.HasPrefix(out, "nova-fleet "+args[0]) {
			t.Errorf("%q: code %d stdout %q stderr %q", args, code, out, errs)
		}
	}
	if code, out, errs := runArgs(); code != 2 || out != "" || errs != "nova-fleet: want a verb or a play; run: nova-fleet help\n" {
		t.Errorf("no arguments: code %d stdout %q stderr %q", code, out, errs)
	}
}

// A play with "# argument: <variable>" takes one positional, passed as -e {"<variable>":"<value>"}.
func TestPlayArgument(t *testing.T) {
	f := &fake{found: map[string]string{"ansible-playbook": "/a/ansible-playbook", "nova-config": "/b/nova-config"}}
	f.install(t)
	old := tree
	t.Cleanup(func() { tree = old })
	tree = fstest.MapFS{
		"join.yml":         {Data: []byte("# purpose: a machine joins\n# argument: nova_join_host\n- hosts: localhost\n")},
		"requirements.yml": {Data: []byte("collections: []\n")},
	}
	if code, _, errs := runArgs("join", "new-box", "--check", "--", "-v"); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if want := []string{"-e", `{"nova_join_host":"new-box"}`, "--check", "-v"}; !slices.Equal(f.argv[3:], want) {
		t.Errorf("pass-through = %q, want %q", f.argv[3:], want)
	}
	code, _, errs := runArgs("join", "a", "b")
	if code != 2 || !strings.HasPrefix(errs, "nova-fleet join: unexpected argument b") {
		t.Errorf("second positional: code %d stderr %q", code, errs)
	}
	code, out, _ := runArgs("join", "-h")
	if code != 0 || !strings.HasPrefix(out, "nova-fleet join <nova_join_host> ") || !strings.Contains(out, "-e nova_join_host=<value>") {
		t.Errorf("join -h: code %d stdout %q", code, out)
	}
}

// On Windows a play or up is refused with the reason; help still works.
func TestNoWindowsController(t *testing.T) {
	f := &fake{found: map[string]string{"ansible-playbook": "/a/ansible-playbook", "nova-config": "/b/nova-config"}}
	f.install(t)
	old := goos
	t.Cleanup(func() { goos = old })
	goos = "windows"
	for _, args := range [][]string{{"install"}, {"up", "--tools", "v1.0.0", "a=1.2.3.4"}} {
		code, _, errs := runArgs(args...)
		if code != 2 || !strings.HasPrefix(errs, "nova-fleet "+args[0]+": ansible-core has no Windows controller") {
			t.Errorf("%q: code %d stderr %q", args, code, errs)
		}
	}
	if f.argv != nil {
		t.Errorf("ran ansible-playbook on windows: %q", f.argv)
	}
	if code, _, _ := runArgs("install", "-h"); code != 0 {
		t.Errorf("install -h on windows: code %d", code)
	}
	if code, _, errs := runArgs("install", "--bogus"); code != 2 || !strings.Contains(errs, "unknown flag --bogus") {
		t.Errorf("a malformed run on windows is refused as malformed first: %q", errs)
	}
}
