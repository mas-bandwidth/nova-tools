package secrets

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// storepull.go is the one boundary where the store's pull credential is chosen
// (docs/SPEC-SECRETS.md, "Additions from dogfooding", rule 7). The store is pulled on a bench
// over the bench's own SSH key, generated on that bench, its public half authorized on the
// GitHub account the bench acts as; never a person's credential; never present inside the card
// wall. The rule is held HERE, where the key is handed to ssh, rather than by reading a clone's
// remote URL afterwards: a URL says nothing about which key answered for it.

// StorePullOptions names everything the pull uses. Nothing is a default except the two
// binaries; every path is typed by the caller.
type StorePullOptions struct {
	StoreDir string   // the store's working copy on this bench
	Seat     string   // the bench seat pulling, e.g. swarm-studio
	SeatHome string   // the seat's own home; its key lives in <SeatHome>/.ssh
	Key      string   // the private half of the seat's SSH key
	Wall     []string // the card wall's directories (slot, job, work): the key may lie in none
	Git      string   // the git binary; empty is `git`
	SSH      string   // the ssh binary; empty is `ssh`
}

// StorePullKey checks that o.Key is the bench's own key, and returns its cleaned absolute path.
// It refuses, in this order: a key inside the card wall; a symlink (the classic way a person's
// key is lent to a bench); a key not under <SeatHome>/.ssh; a key readable by anyone but its
// owner; and a key whose public half names anyone but the seat, which is a person's credential
// wherever the file was put.
func StorePullKey(o StorePullOptions) (string, error) {
	seat := strings.TrimSpace(o.Seat)
	if seat == "" {
		return "", fmt.Errorf("store pull: no seat named; the store is pulled only over the bench seat's own key")
	}
	if !filepath.IsAbs(o.Key) || !filepath.IsAbs(o.SeatHome) {
		return "", fmt.Errorf("store pull: --key %q and the seat home %q must both be absolute paths", o.Key, o.SeatHome)
	}
	key := filepath.Clean(o.Key)
	for _, w := range o.Wall {
		if strings.TrimSpace(w) == "" {
			continue
		}
		if !filepath.IsAbs(w) {
			return "", fmt.Errorf("store pull: card wall directory %q is not an absolute path", w)
		}
		if within(key, w) || within(realPath(key), realPath(w)) {
			return "", fmt.Errorf("store pull: key %s lies inside the card wall %s; the bench's key is never present inside the card wall", key, filepath.Clean(w))
		}
	}
	fi, err := os.Lstat(key)
	if err != nil {
		return "", fmt.Errorf("store pull: key %s: %w", key, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("store pull: key %s is a symlink; the bench's own key is a file generated on the bench, not a link to somebody's", key)
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("store pull: key %s is not a regular file", key)
	}
	sshDir := filepath.Join(filepath.Clean(o.SeatHome), ".ssh")
	if !within(key, sshDir) || !within(realPath(key), realPath(sshDir)) {
		return "", fmt.Errorf("store pull: key %s is not under the seat's own %s; seat %s pulls the store only over the key generated for it on this bench", key, sshDir, seat)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("store pull: key %s is mode %04o; the bench's key is readable by its seat only (0600)", key, fi.Mode().Perm())
	}
	comment, err := publicKeyComment(key + ".pub")
	if err != nil {
		return "", fmt.Errorf("store pull: key %s: %w", key, err)
	}
	if comment != seat && !strings.HasPrefix(comment, seat+"@") {
		return "", fmt.Errorf("store pull: key %s names %q, not seat %s; that is a person's credential, and a person's key in a bench's clone is that person on that bench", key, comment, seat)
	}
	return key, nil
}

// StorePullSSHCommand is the GIT_SSH_COMMAND the pull runs under: the checked key and only it,
// with no agent, so a forwarded or loaded person's key is never offered in its place.
func StorePullSSHCommand(o StorePullOptions) (string, error) {
	key, err := StorePullKey(o)
	if err != nil {
		return "", err
	}
	ssh := o.SSH
	if ssh == "" {
		ssh = "ssh"
	}
	return strings.Join([]string{
		shellQuote(ssh), "-i", shellQuote(key),
		"-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none",
	}, " "), nil
}

// PullStore fast-forwards the store over the bench's own key and returns the new HEAD. The key
// is checked before any child starts, so a refused key is never offered to anybody.
func PullStore(o StorePullOptions) (string, error) {
	sshCmd, err := StorePullSSHCommand(o)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(o.StoreDir) {
		return "", fmt.Errorf("store pull: store %q is not an absolute path", o.StoreDir)
	}
	gitBin := o.Git
	if gitBin == "" {
		gitBin = "git"
	}
	sshBin := o.SSH
	if sshBin == "" {
		sshBin = "ssh"
	}
	testguard.RefuseHosts(sshBin, "-i", o.Key, "(git pull --ff-only in "+o.StoreDir+")")
	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "SSH_AUTH_SOCK=") || strings.HasPrefix(kv, "GIT_SSH=") ||
			strings.HasPrefix(kv, "GIT_SSH_COMMAND=") || strings.HasPrefix(kv, "GIT_ASKPASS=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_SSH_COMMAND="+sshCmd, "GIT_TERMINAL_PROMPT=0")
	run := func(args ...string) (string, error) {
		cmd := exec.Command(gitBin, append([]string{"-C", o.StoreDir, "-c", "core.sshCommand=" + sshCmd}, args...)...)
		cmd.Env = env
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("store pull: git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
		}
		return strings.TrimSpace(out.String()), nil
	}
	if _, err := run("pull", "--ff-only", "-q"); err != nil {
		return "", err
	}
	return run("rev-parse", "HEAD")
}

// publicKeyComment reads the comment of an authorized_keys-form public key: the text after the
// key type and its base64 body.
func publicKeyComment(pubPath string) (string, error) {
	b, err := os.ReadFile(pubPath)
	if err != nil {
		return "", fmt.Errorf("its public half %s is unreadable (the bench's key is generated with it beside): %w", pubPath, err)
	}
	fields := strings.Fields(strings.TrimSpace(string(b)))
	if len(fields) < 3 {
		return "", fmt.Errorf("its public half %s carries no comment naming the seat it was generated for", pubPath)
	}
	return strings.Join(fields[2:], " "), nil
}

// within reports whether path is dir or lies below it.
func within(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// realPath resolves symlinks in p's directory (macOS /tmp is /private/tmp); a path that does
// not resolve is returned cleaned.
func realPath(p string) string {
	dir, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return filepath.Clean(p)
	}
	return filepath.Join(dir, filepath.Base(p))
}

// shellQuote single-quotes s for the shell git runs GIT_SSH_COMMAND under.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
