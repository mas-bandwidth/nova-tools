// Command nova-fleet runs the fleet's Ansible plays. The plays, roles, templates
// and variables of this directory are embedded in the binary; a run extracts them
// to a private temporary directory, points ansible-playbook at an inventory
// (nova-config inventory by default) and exits with ansible-playbook's status.
package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
)

// tree is this directory as shipped: every file but the Go sources.
//
//go:embed *
var embedded embed.FS

// tree is what the verbs read; unit tests replace it.
var tree fs.FS = embedded

const banner = `nova-fleet: the fleet's Ansible plays, in one binary

  nova-fleet plays                  list the plays and what each does
  nova-fleet show <play|file>       print a play (or an embedded file, e.g. tailscale-acl)
  nova-fleet check [--runners URL]  what this control node lacks, and the line that fixes each
  nova-fleet up [--check] --tools TAG|DIR [--runners URL] <name>=<address>...
                                    from nothing to a fleet, in a fixed order
  nova-fleet <play> [<argument>] [--inventory FILE|SCRIPT] [--check] [--diff] [-- ansible-playbook args]
  nova-fleet help [plays|show|check|up|<play>]

A run extracts the plays to a private temporary directory, writes an inventory
script there that execs nova-config inventory (found on PATH; --inventory names
your own file or script instead) and runs ansible-playbook -i <inventory> <play>.yml
with the arguments after --. A play that takes an argument (join <machine>) names
the variable it sets in its help. Exit: 0 done; 1 the play failed (its ansible-playbook
exit printed); 2 could not run. fleet/README.md has each play's variables.
`

var verbHelp = map[string]string{
	"plays": `nova-fleet plays

List the embedded plays, one per line: name, then its purpose.
`,
	"show": `nova-fleet show <play|file>

Print the text of an embedded play (install prints install.yml) or of another
top-level embedded file by name (tailscale-acl prints tailscale-acl.json). Payload:
the file's bytes, on stdout.
`,
	"up":    upHelp,
	"check": checkHelp,
	"help": `nova-fleet help [plays|show|check|up|<play>]

Print the banner, or the help of a verb or a play.
`,
}

const playHelp = `nova-fleet %[1]s%[3]s [--inventory FILE|SCRIPT] [--check] [--diff] [-- ansible-playbook args]

%[2]s
%[4]s
  --inventory X  an inventory file or script; default a script that execs
                 nova-config inventory, found on PATH
  --check        passed to ansible-playbook (change nothing, report what would)
  --diff         passed to ansible-playbook
Everything after -- is passed to ansible-playbook as written (-e, -l, -v, ...).
nova-fleet show %[1]s prints the play.
`

// noController refuses a run on Windows, which ansible-core cannot control from.
func noController(verb string, stderr io.Writer) bool {
	if goos != "windows" {
		return false
	}
	fmt.Fprintf(stderr, "nova-fleet %s: ansible-core has no Windows controller, so plays run from a Linux, macOS or WSL control node; run: nova-fleet %s there\n", verb, verb)
	return true
}

// The exec seam: unit tests replace these.
var (
	goos     = runtime.GOOS
	goarch   = runtime.GOARCH
	lookPath = exec.LookPath
	runCmd   = func(bin string, args, env []string) (int, error) {
		cmd := exec.Command(bin, args...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr, cmd.Env = os.Stdin, os.Stdout, os.Stderr, env
		if err := cmd.Start(); err != nil {
			return 0, err
		}
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sig)
		go func() {
			for s := range sig {
				_ = cmd.Process.Signal(s)
			}
		}()
		err := cmd.Wait()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if code := ee.ExitCode(); code > 0 {
				return code, nil
			}
			return 1, nil
		}
		return 0, err
	}
	// outCmd runs a command to its end and returns its stdout, stderr and exit code
	// (127 when it could not start).
	outCmd = func(bin string, args, env []string) (string, string, int) {
		cmd := exec.Command(bin, args...)
		var o, e strings.Builder
		cmd.Stdout, cmd.Stderr, cmd.Env = &o, &e, env
		err := cmd.Run()
		var ee *exec.ExitError
		switch {
		case errors.As(err, &ee):
			return o.String(), e.String(), max(ee.ExitCode(), 1)
		case err != nil:
			return o.String(), err.Error(), 127
		}
		return o.String(), e.String(), 0
	}
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// plays returns the names of the embedded plays: the top-level .yml files but
// requirements.yml.
func plays(fsys fs.FS) []string {
	var out []string
	entries, _ := fs.ReadDir(fsys, ".")
	for _, e := range entries {
		if n := e.Name(); !e.IsDir() && strings.HasSuffix(n, ".yml") && n != "requirements.yml" {
			out = append(out, strings.TrimSuffix(n, ".yml"))
		}
	}
	return out
}

// header is the text of the "# <key>:" line in a play's leading comment: "# purpose:",
// and "# argument: <variable>" for a play that takes one positional, passed as -e.
func header(text, key string) string {
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "#") {
			break
		}
		if p, ok := strings.CutPrefix(line, "# "+key+":"); ok {
			return strings.TrimSpace(p)
		}
	}
	return ""
}

// showable resolves a name to an embedded top-level file: <name>.yml, else the
// one other file whose name without its extension is name.
func showable(fsys fs.FS, name string) (string, bool) {
	if slices.Contains(plays(fsys), name) {
		return name + ".yml", true
	}
	entries, _ := fs.ReadDir(fsys, ".")
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && !strings.HasSuffix(n, ".go") && strings.TrimSuffix(n, path.Ext(n)) == name {
			return n, true
		}
	}
	return "", false
}

// files lists the embedded files to extract: everything but Go sources.
func files(fsys fs.FS) []string {
	var out []string
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.HasSuffix(p, ".go") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// extract writes the tree into a new 0700 temporary directory.
func extract(fsys fs.FS) (string, error) {
	dir, err := os.MkdirTemp("", "nova-fleet-")
	if err != nil {
		return "", err
	}
	for _, p := range files(fsys) {
		b, err := fs.ReadFile(fsys, p)
		if err == nil {
			dst := filepath.Join(dir, filepath.FromSlash(p))
			if err = os.MkdirAll(filepath.Dir(dst), 0o700); err == nil {
				err = os.WriteFile(dst, b, 0o600)
			}
		}
		if err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
	}
	return dir, nil
}

// inventoryScript writes dir/inventory, a script that execs nova-config inventory.
func inventoryScript(dir, config string) (string, error) {
	script := filepath.Join(dir, "inventory")
	body := fmt.Sprintf("#!/bin/sh\nexec '%s' inventory \"$@\"\n", strings.ReplaceAll(config, "'", `'\''`))
	return script, os.WriteFile(script, []byte(body), 0o700)
}

func run(args []string, stdout, stderr io.Writer) int {
	refuse := func(who, format string, a ...any) int {
		fmt.Fprintf(stderr, "nova-fleet %s: %s; run: nova-fleet %s -h\n", who, fmt.Sprintf(format, a...), who)
		return 2
	}
	isHelp := func(a string) bool { return a == "-h" || a == "--help" }
	all := plays(tree)
	if len(args) == 0 {
		fmt.Fprint(stderr, banner)
		return 2
	}
	verb, rest := args[0], args[1:]
	if isHelp(verb) {
		verb, rest = "help", nil
	}
	if verb == "help" {
		if len(rest) > 0 && (isHelp(rest[0]) || rest[0] == "help") {
			fmt.Fprint(stdout, verbHelp["help"])
			return 0
		}
		if len(rest) == 0 {
			fmt.Fprint(stdout, banner)
			return 0
		}
		if strings.HasPrefix(rest[0], "-") {
			return refuse("help", "unknown flag %s", rest[0])
		}
		if len(rest) > 1 {
			return refuse("help", "want at most one verb or play, got %d", len(rest))
		}
		args = []string{rest[0], "-h"}
		verb, rest = rest[0], args[1:]
	}
	switch verb {
	case "up":
		return runUp(rest, stdout, stderr)
	case "check":
		return runCheck(rest, stdout, stderr)
	case "plays", "show":
		if len(rest) > 0 && isHelp(rest[0]) {
			fmt.Fprint(stdout, verbHelp[verb])
			return 0
		}
		for _, a := range rest {
			if strings.HasPrefix(a, "-") {
				return refuse(verb, "unknown flag %s", a)
			}
		}
		if verb == "plays" {
			if len(rest) > 0 {
				return refuse("plays", "takes no arguments, got %s", rest[0])
			}
			for _, p := range all {
				b, _ := fs.ReadFile(tree, p+".yml")
				fmt.Fprintf(stdout, "%-16s %s\n", p, header(string(b), "purpose"))
			}
			return 0
		}
		if len(rest) != 1 {
			return refuse("show", "want one play or file name")
		}
		f, ok := showable(tree, rest[0])
		if !ok {
			return refuse("show", "no play or file %s (plays: %s)", rest[0], strings.Join(all, ", "))
		}
		b, _ := fs.ReadFile(tree, f)
		_, _ = stdout.Write(b)
		return 0
	}
	if !slices.Contains(all, verb) {
		fmt.Fprintf(stderr, "nova-fleet: unknown verb or play %s; verbs: plays, show, check, up, help; plays: %s; run: nova-fleet help\n", verb, strings.Join(all, ", "))
		return 2
	}
	return runPlay(verb, rest, stdout, stderr, refuse)
}

func runPlay(play string, args []string, stdout, stderr io.Writer, refuse func(string, string, ...any) int) int {
	var inventory string
	var pass []string
	wrap := false
	text, _ := fs.ReadFile(tree, play+".yml")
	argVar, argSet := header(string(text), "argument"), false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			usage, note := "", ""
			if argVar != "" {
				usage, note = " <"+argVar+">", "\n  <"+argVar+">  passed to ansible-playbook as -e "+argVar+"=<value>\n"
			}
			fmt.Fprintf(stdout, playHelp, play, header(string(text), "purpose"), usage, note)
			return 0
		case a == "--":
			pass = append(pass, args[i+1:]...)
			i = len(args)
		case a == "--check" || a == "--diff":
			pass = append(pass, a)
		case a == "--inventory" || strings.HasPrefix(a, "--inventory="):
			v, ok := strings.CutPrefix(a, "--inventory=")
			if !ok {
				if i+1 >= len(args) {
					return refuse(play, "--inventory wants a file or script")
				}
				i++
				v = args[i]
			}
			inventory = v
		case strings.HasPrefix(a, "-"):
			return refuse(play, "unknown flag %s (ansible-playbook flags go after --)", a)
		case argVar != "" && !argSet:
			ev, _ := json.Marshal(map[string]string{argVar: a})
			pass, argSet = append(pass, "-e", string(ev)), true
		default:
			return refuse(play, "unexpected argument %s (ansible-playbook arguments go after --)", a)
		}
	}
	if noController(play, stderr) {
		return 2
	}
	if inventory != "" {
		abs, err := filepath.Abs(inventory)
		if _, serr := os.Stat(abs); err != nil || serr != nil {
			fmt.Fprintf(stderr, "nova-fleet %s: --inventory %s: no such file; run: nova-fleet %s --inventory <an existing file or script>\n", play, inventory, play)
			return 2
		}
		inventory = abs
	} else {
		cfg, err := lookPath("nova-config")
		if err != nil {
			fmt.Fprintf(stderr, "nova-fleet %s: nova-config not found on PATH; install nova-tools or run: nova-fleet %s --inventory <file|script>\n", play, play)
			return 2
		}
		if inventory, err = filepath.Abs(cfg); err != nil {
			inventory = cfg
		}
		wrap = true
	}
	dir, err := extract(tree)
	if err != nil {
		fmt.Fprintf(stderr, "nova-fleet %s: extract the plays: %v; run: nova-fleet %s again with TMPDIR set to a writable directory\n", play, err, play)
		return 2
	}
	if wrap {
		if inventory, err = inventoryScript(dir, inventory); err != nil {
			_ = os.RemoveAll(dir)
			fmt.Fprintf(stderr, "nova-fleet %s: write the inventory script: %v; run: nova-fleet %s --inventory <file|script>\n", play, err, play)
			return 2
		}
	}
	ansible, err := lookPath("ansible-playbook")
	if err != nil {
		fmt.Fprintf(stderr, "nova-fleet %s: ansible-playbook not found on PATH; install ansible-core 2.15+ and run: ansible-galaxy collection install -r %s\n", play, filepath.Join(dir, "requirements.yml"))
		return 2
	}
	defer os.RemoveAll(dir)
	fmt.Fprintf(stderr, "nova-fleet: ansible-playbook=%s inventory=%s play=%s\n", ansible, inventory, filepath.Join(dir, play+".yml"))
	argv := append([]string{"-i", inventory, filepath.Join(dir, play+".yml")}, pass...)
	env := append(os.Environ(), "ANSIBLE_INVENTORY_UNPARSED_FAILED=true")
	code, err := runCmd(ansible, argv, env)
	if err != nil {
		fmt.Fprintf(stderr, "nova-fleet %s: run %s: %v; run: %s --version\n", play, ansible, err, ansible)
		return 2
	}
	if code != 0 {
		fmt.Fprintf(stderr, "nova-fleet %s: FAILED ansible-playbook exit=%d; the play's own lines above say what, and the same command resumes\n", play, code)
		return 1
	}
	return 0
}
