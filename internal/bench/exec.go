package bench

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// SSHOptions are on every ssh this package starts: BatchMode so a missing key
// is a refusal rather than a prompt nobody answers, ConnectTimeout so a
// sleeping bench costs seconds and the fallback is tried, and no agent
// forwarded to a bench.
var SSHOptions = []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "ForwardAgent=no"}

// Exec is the production transport: the system ssh, and nothing else. The
// copy is a tar stream this process writes onto ssh's stdin, so no rsync or
// local tar is needed here and the bench needs only tar. The lander's tree gate
// does not copy: it stages from the bench's mirror (stage_mirror.go).
type Exec struct{}

// Shell runs line on host through ssh.
func (Exec) Shell(ctx context.Context, host, line string, stdout, stderr io.Writer) (int, error) {
	return sshLine(ctx, host, line, nil, stdout, stderr)
}

// Copy copies src's contents into host:dst: dst is made, and the tree goes over
// as a tar stream on ssh's stdin that the bench's tar unpacks there.
func (e Exec) Copy(ctx context.Context, host, src, dst string, withGit bool, stderr io.Writer) error {
	_, err := e.CopySized(ctx, host, src, dst, withGit, stderr)
	return err
}

// CopySized is Copy, and how many bytes of tar stream it wrote. A refusal names its
// cause: WriteTree's own (a socket, a device), else the bench's tar exit, whose stderr
// the caller's stderr receives.
func (Exec) CopySized(ctx context.Context, host, src, dst string, withGit bool, stderr io.Writer) (int64, error) {
	pr, pw := io.Pipe()
	cw := &countingWriter{w: pw}
	wrote := make(chan error, 1)
	go func() {
		err := WriteTree(cw, src, withGit)
		wrote <- err
		// ignored: CloseWithError on a pipe writer always returns nil
		_ = pw.CloseWithError(err)
	}()
	code, err := sshLine(ctx, host, CopyLine(dst), pr, io.Discard, stderr)
	// Closing the read end unblocks a writer the child stopped reading; the writer's own
	// error is read next.
	_ = pr.Close() // ignored: closing a pipe's read end always returns nil
	werr := <-wrote
	if errors.Is(werr, io.ErrClosedPipe) {
		werr = nil // ignored: the bench stopped reading the stream, so its exit, below, is the cause
	}
	switch {
	case werr != nil:
		return cw.n, fmt.Errorf("tar stream: %w", werr)
	case err != nil:
		return cw.n, err
	case code != 0:
		return cw.n, fmt.Errorf("tar on the bench exit %d", code)
	}
	return cw.n, nil
}

// sshLine is this package's one ssh: line on host, stdin (nil for none) on
// its stdin. The int is the remote status, NoAnswer when ssh itself failed.
func sshLine(ctx context.Context, host, line string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	args := append(append(append([]string(nil), SSHOptions...), host), line)
	testguard.RefuseHosts("ssh", args...)
	cmd := subproc.Long(ctx, "ssh", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// CopyLine is the remote line of the copy step: make dst and unpack stdin there.
func CopyLine(dst string) string {
	return "mkdir -p " + Quote(dst) + " && tar -C " + Quote(dst) + " -xf -"
}

// WriteTree writes the tree under src to w as a tar stream, paths relative to
// src: directories, regular files and symlinks, each with its mode. The .git
// at the top of the tree is left out unless withGit; anything else that is
// not one of those three (a socket, a device) is refused, never skipped.
func WriteTree(w io.Writer, src string, withGit bool) error {
	tw := tar.NewWriter(w)
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".git" && !withGit {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil // a worktree's .git file
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			if link, err = os.Readlink(path); err != nil {
				return err
			}
		case info.IsDir(), info.Mode().IsRegular():
		default:
			return fmt.Errorf("%s is not a file, a directory or a symlink (%s); refusing to copy it", rel, info.Mode().Type())
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = rel
		if info.IsDir() {
			hdr.Name += "/"
		}
		hdr.Uname, hdr.Gname = "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		return errors.Join(err, f.Close())
	})
	return errors.Join(err, tw.Close())
}

// THE GATE'S TWO OUTCOMES (docs/SPEC-SPRINT.md section 7, the tree gate's fault). A gate run
// on a bench that ends red is either the tree's verdict (a test's own FAIL, a build or vet
// error) or the bench's fault: its git, its disk, its temporary directory, its ssh, its
// copy, its toolchain. On 2026-10-07 at 11:34 PM sixteen landings were refused in one pass
// for "the base fails the tree gate at its tip" when six class tests failed with "git
// ls-files from the repository root: exit status 128" on a copy staged without .git; the
// same evening a bench's /tmp at its disk quota and another's root at 40 MB free failed
// every gate there with "disk quota exceeded" and ENOSPC, each counted as a red tree. A
// fault of the bench is never a verdict on the tree: ClassifyGate tells them apart, and the
// lander steps to the next bench on a fault.

// The kinds of a bench's fault.
const (
	FaultGit       = "git"       // git could not read the tree's repository (exit status 128, not a git repository)
	FaultDisk      = "disk"      // a disk full or at its quota (ENOSPC, no space left, disk quota exceeded)
	FaultTmp       = "tmp"       // the same, in a temporary directory (/tmp, TMPDIR)
	FaultSSH       = "ssh"       // ssh ended the run (exit 255)
	FaultCopy      = "copy"      // the tree's copy or stage did not finish
	FaultToolchain = "toolchain" // no go toolchain on the bench
)

// Fault is one gate run a bench failed: the bench, the kind, and the first line of the
// output that says so.
type Fault struct {
	Host, Kind, What string
}

// Line is the fault as the lander says it: GATE FAULT bench=<m> kind=<k> what=<first line>.
func (f Fault) Line() string {
	return "GATE FAULT bench=" + f.Host + " kind=" + f.Kind + " what=" + f.What
}

// faultWhatCap bounds the line a fault quotes.
const faultWhatCap = 300

// ClassifyGate reads a gate run that ended red on host (its exit code and its output, or a
// refusal's text): the bench's fault and true when the output carries one of the bench's
// failures, else false, a red tree (a `--- FAIL:` line naming a test, a build or vet error,
// anything else). A bench's failure wins over a FAIL line it caused: the class tests that
// call git fail with git's exit status 128 on a tree with no .git, and that is the bench's.
// Exit 255 (ssh's own) and 127 (a command not found) are faults when no line names one.
func ClassifyGate(host string, code int, out string) (Fault, bool) {
	last := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		last = line
		if kind := lineFault(line); kind != "" {
			return Fault{Host: host, Kind: kind, What: capWhat(line)}, true
		}
	}
	switch code {
	case NoAnswer:
		return Fault{Host: host, Kind: FaultSSH, What: capWhat(orSaid(last, "ssh exit 255"))}, true
	case 127:
		return Fault{Host: host, Kind: FaultToolchain, What: capWhat(orSaid(last, "exit 127: a command the gate runs was not found"))}, true
	}
	return Fault{}, false
}

// lineFault is the kind of bench fault one line of a gate's output names, "" for none.
func lineFault(line string) string {
	l := strings.ToLower(line)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(l, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("no space left on device", "enospc", "disk quota exceeded", "edquot"):
		if has("/tmp", "tmpdir") {
			return FaultTmp
		}
		return FaultDisk
	case has("not a git repository", "error obtaining vcs status", "detected dubious ownership"),
		strings.Contains(l, "exit status 128") && strings.Contains(l, "git"):
		return FaultGit
	case has("go: command not found", "go: not found", `exec: "go": executable file not found`, "toolchain not available", "cannot find goroot"):
		return FaultToolchain
	case strings.HasPrefix(l, "copy refused:"):
		return FaultCopy
	}
	return ""
}

func capWhat(s string) string {
	if len(s) > faultWhatCap {
		return s[:faultWhatCap] + "..."
	}
	return s
}

func orSaid(s, none string) string {
	if s == "" {
		return none
	}
	return s
}

// DiskGuardLine is the remote line that runs a bench's disk-guard loop at once: the unit
// fleet/loops.yml installs on every member (nova-swarm disk-guard), started out of its
// period, under systemd or launchd, whichever the bench has.
const DiskGuardLine = "systemctl --user start nova-loop-disk-guard.service 2>/dev/null || launchctl kickstart \"gui/$(id -u)/com.nova.loop.disk-guard\""
