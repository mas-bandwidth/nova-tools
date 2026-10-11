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

	"github.com/mas-bandwidth/nova-tools/pkg/subproc"
	"github.com/mas-bandwidth/nova-tools/pkg/testguard"
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
