package release

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
)

// machineName is what may be handed to ssh as a destination. It is deliberately
// narrower than what ssh accepts: the thing this verb replaces built its remote
// commands by pasting a bench name into a shell line, and a name that cannot
// carry a space, a quote, a semicolon or a `$` cannot be the half of that which
// went wrong.
var machineName = regexp.MustCompile(`^[A-Za-z0-9_.@-]+$`)

// installedLine reads a remote install's receipt back out of its output. The
// receipt is read from what the remote SAID, never from its exit code: a shell
// that could not find the binary exits non-zero for the same reason a disk that
// filled up does, and only one of those is worth the same remedy.
var installedLine = regexp.MustCompile(`RELEASE INSTALLED version=(\S+) tools=(\d+) skipped=(\d+)`)

// Machines reads the machine list: one name per line, blanks and `#` comments
// skipped, every name checked before ssh is reached. The file is a flag because
// the fleet is not a constant -- it was four benches, then five, and the day the
// iMac Pro joined nothing in a tool should have needed editing.
func Machines(r io.Reader) ([]string, error) {
	var machines []string
	s := bufio.NewScanner(r)
	for line := 1; s.Scan(); line++ {
		name := strings.TrimSpace(s.Text())
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		if !machineName.MatchString(name) {
			return nil, refuse("one machine name per line, letters, digits, dot, dash, underscore or @",
				"line %d is not a machine name: %q", line, name)
		}
		machines = append(machines, name)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(machines) == 0 {
		return nil, refuse("put one machine name per line in the file",
			"the machine list names no machine")
	}
	return machines, nil
}

func adopt(ctx context.Context, o options, deps Deps, out, errs io.Writer) int {
	goos, goarch, err := Platform(o.platform)
	if err != nil {
		return refusal(errs, "ADOPT", err)
	}
	f, err := os.Open(o.machines)
	if err != nil {
		return refusal(errs, "ADOPT", fmt.Errorf("cannot open %s: %w (name a readable --machines file, one machine per line)", o.machines, err))
	}
	machines, err := Machines(f)
	f.Close()
	if err != nil {
		return refusal(errs, "ADOPT", err)
	}
	local := ArtifactDir(o.from, o.version, goos, goarch)
	arts, err := ReadSums(local)
	if err != nil {
		if os.IsNotExist(err) {
			return refusal(errs, "ADOPT", refuse(
				fmt.Sprintf("build it first: nova-update release build --version %s --out %s --source <checkout> --platform %s-%s", o.version, o.from, goos, goarch),
				"there is nothing to adopt: no %s for %s at %s", o.version, goos+"-"+goarch, local))
		}
		return refusal(errs, "ADOPT", err)
	}
	// THE RELEASE INSTALLS ITSELF. The nova-update that runs the remote
	// install is the one this verb just copied there, so a machine with no
	// nova-tools at all -- a bench provisioned this morning -- adopts with the
	// same command as one that is a version behind.
	// The file is named for the TARGET platform, never this host: adopting a
	// windows bench from the Studio must look for, send and run
	// `nova-update.exe`. A bare `nova-update` there is a path that exists
	// nowhere in the release, and the machine would refuse with `command not
	// found` for a mistake made on this side.
	updateFile := ToolFile("nova-update", goos)
	var carriesUpdate bool
	for _, a := range arts {
		if a.Name == updateFile {
			carriesUpdate = true
		}
	}
	if !carriesUpdate {
		return refusal(errs, "ADOPT", refuse("build from a checkout that has cmd/nova-update",
			"this release carries no %s, so no machine could run the install", updateFile))
	}
	ssh := deps.SSH
	if ssh == nil {
		ssh = ExecSSH{Path: o.ssh}
	}
	// Remote paths are slash paths whatever this host is: a release cut on the
	// Studio installs onto Linux benches, and filepath.Join on darwin would be
	// right by accident and on windows wrong on purpose.
	remoteDir := path.Join(o.dest, o.version, goos+"-"+goarch)
	adopted, refused := 0, 0
	for _, machine := range machines {
		progress(errs, "sending %s to %s:%s", o.version, machine, remoteDir)
		if output, err := ssh.Send(ctx, machine, local, path.Dir(remoteDir)); err != nil {
			refused++
			fmt.Fprintf(errs, "RELEASE REFUSED machine=%s version=%s: %s (check `ssh %s` reaches it and that %s is writable there)\n",
				field(machine), field(o.version), oneLine(output, err), machine, o.dest)
			continue
		}
		argv := []string{
			path.Join(remoteDir, updateFile), "release", "install",
			"--from", o.dest, "--version", o.version, "--bin", o.bin,
			"--platform", goos + "-" + goarch,
		}
		progress(errs, "installing on %s", machine)
		output, err := ssh.Run(ctx, machine, argv)
		if err != nil {
			refused++
			fmt.Fprintf(errs, "RELEASE REFUSED machine=%s version=%s: %s (run `ssh %s %s` by hand to see the whole message)\n",
				field(machine), field(o.version), oneLine(output, err), machine, argv[0])
			continue
		}
		m := installedLine.FindStringSubmatch(output)
		if m == nil {
			refused++
			fmt.Fprintf(errs, "RELEASE REFUSED machine=%s version=%s: the install printed no RELEASE INSTALLED line: %s (run `ssh %s %s` by hand)\n",
				field(machine), field(o.version), oneLine(output, errNoReceipt), machine, argv[0])
			continue
		}
		if m[1] != o.version {
			refused++
			fmt.Fprintf(errs, "RELEASE REFUSED machine=%s version=%s: it installed %s instead (check --dest and --version name the same release)\n",
				field(machine), field(o.version), field(m[1]))
			continue
		}
		adopted++
		fmt.Fprintf(out, "RELEASE ADOPTED machine=%s version=%s tools=%s skipped=%s bin=%s\n",
			field(machine), field(o.version), field(m[2]), field(m[3]), field(o.bin))
	}
	w, result, code := out, "OK", 0
	if refused > 0 {
		w, result, code = errs, "FAIL", 1
	}
	fmt.Fprintf(w, "RELEASE ADOPT %s machines=%d adopted=%d refused=%d version=%s\n",
		result, len(machines), adopted, refused, field(o.version))
	return code
}

// errNoReceipt gives oneLine something to fold when the remote's failure is that
// it said nothing this tool recognises.
var errNoReceipt = fmt.Errorf("no receipt")
