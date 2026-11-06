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

// fake replaces the exec seam: lookPath finds only the names in found, and
// runCmd records its call (and what the extracted directory held) and returns code.
type fake struct {
	found    map[string]string
	code     int
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
	oldLook, oldRun := lookPath, runCmd
	t.Cleanup(func() { lookPath, runCmd = oldLook, oldRun })
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
		"templates/nova-service.service.j2", "retired-tools.txt", "README.md"} {
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
		{[]string{"install", "localhost"}, "nova-fleet install: unexpected argument localhost (ansible-playbook arguments go after --); run: nova-fleet install -h\n"},
		{[]string{"install", "--inventory"}, "nova-fleet install: --inventory wants a file or script; run: nova-fleet install -h\n"},
		{[]string{"install", "--inventory", "/no/such/inv"}, "nova-fleet install: --inventory /no/such/inv: no such file; run: nova-fleet install --inventory <an existing file or script>\n"},
		{[]string{"nope"}, "nova-fleet: unknown verb or play nope; verbs: plays, show, check, up, help; plays: " + strings.Join(plays(tree), ", ") + "; run: nova-fleet help\n"},
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
	code, _, errs := runArgs("install")
	if code != 2 || errs != "nova-fleet install: nova-config not found on PATH; install nova-tools or run: nova-fleet install --inventory <file|script>\n" {
		t.Errorf("no nova-config: code %d stderr %q", code, errs)
	}
	f.found["nova-config"] = "/b/nova-config"
	code, _, errs = runArgs("install")
	if code != 2 || errs != "nova-fleet install: ansible-playbook not found on PATH; install ansible-core 2.15+ and run: nova-fleet show requirements > requirements.yml && ansible-galaxy collection install -r requirements.yml\n" {
		t.Fatalf("no ansible-playbook: code %d stderr %q", code, errs)
	}
	// the remedy's show works, and the extracted directory is gone (every exit removes it)
	if code, out, _ := runArgs("show", "requirements"); code != 0 || !strings.Contains(out, "collections") {
		t.Errorf("show requirements: code %d %q", code, out)
	}
	if left, _ := filepath.Glob(filepath.Join(os.Getenv("TMPDIR"), "nova-fleet-*")); len(left) != 0 {
		t.Errorf("extracted directories left behind: %v", left)
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
	if code, _, errs := runArgs(); code != 2 || errs != banner {
		t.Errorf("no arguments: code %d", code)
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
