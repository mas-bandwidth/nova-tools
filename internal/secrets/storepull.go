package secrets

import (
	"bytes"
	"crypto"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
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
// owner; a key whose private half is not the key its public half describes (the comment in
// <key>.pub proves nothing about the key ssh will offer until the two are shown to be one key);
// and a key whose public half names anyone but the seat, which is a person's credential wherever
// the file was put.
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
	pubType, pubBlob, comment, err := readPublicHalf(key + ".pub")
	if err != nil {
		return "", fmt.Errorf("store pull: key %s: %w", key, err)
	}
	derived, err := privateKeyPublicBlob(key)
	if err != nil {
		return "", fmt.Errorf("store pull: key %s: %w", key, err)
	}
	if !bytes.Equal(derived, pubBlob) || wireLeadingString(derived) != pubType {
		return "", fmt.Errorf("store pull: key %s does not match its public half %s.pub; the seat's name in that file says nothing about the key ssh would offer, and a private key the seat's public half does not describe is a person's credential until shown otherwise", key, key)
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
	// The command is handed to git, which runs it against the store's host: this is a seam.
	testguard.RefuseHosts(ssh, "-i", key)
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

// readPublicHalf reads an authorized_keys-form public key: its type, its decoded wire-form
// blob, and the comment after them.
func readPublicHalf(pubPath string) (typ string, blob []byte, comment string, err error) {
	b, err := os.ReadFile(pubPath)
	if err != nil {
		return "", nil, "", fmt.Errorf("its public half %s is unreadable (the bench's key is generated with it beside): %w", pubPath, err)
	}
	fields := strings.Fields(strings.TrimSpace(string(b)))
	if len(fields) < 3 {
		return "", nil, "", fmt.Errorf("its public half %s carries no comment naming the seat it was generated for", pubPath)
	}
	blob, err = base64.StdEncoding.DecodeString(fields[1])
	if err != nil {
		return "", nil, "", fmt.Errorf("its public half %s is not an authorized_keys line: %w", pubPath, err)
	}
	return fields[0], blob, strings.Join(fields[2:], " "), nil
}

// privateKeyPublicBlob derives the SSH wire-form public key from the private key file itself, so
// the public half beside it can be checked against the key ssh will actually offer. It reads the
// OpenSSH format ssh-keygen writes and the PKCS#8, PKCS#1 and SEC 1 PEM forms, for ed25519, RSA
// and ECDSA keys. A passphrase-encrypted key is refused: the pull runs unattended with no agent,
// so a bench key never carries one, and its public half cannot be derived without it.
func privateKeyPublicBlob(keyPath string) ([]byte, error) {
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("its private half is not a PEM-armoured key")
	}
	if _, ok := block.Headers["Proc-Type"]; ok || block.Type == "ENCRYPTED PRIVATE KEY" {
		return nil, errEncryptedKey
	}
	var pub crypto.PublicKey
	switch block.Type {
	case "OPENSSH PRIVATE KEY":
		return keyV1PublicBlob(block.Bytes)
	case "PRIVATE KEY":
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("its private half does not parse: %w", err)
		}
		signer, ok := k.(crypto.Signer)
		if !ok {
			return nil, fmt.Errorf("its private half is a %T, not a signing key", k)
		}
		pub = signer.Public()
	case "RSA PRIVATE KEY":
		k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("its private half does not parse: %w", err)
		}
		pub = k.Public()
	case "EC PRIVATE KEY":
		k, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("its private half does not parse: %w", err)
		}
		pub = k.Public()
	default:
		return nil, fmt.Errorf("its private half is a %q block, not a key format ssh-keygen writes", block.Type)
	}
	return wirePublicKey(pub)
}

var errEncryptedKey = errors.New("its private half is passphrase-encrypted; the bench's key pulls unattended with no agent, so it never carries a passphrase")

// keyV1PublicBlob reads an openssh-key-v1 body and derives the public key from its PRIVATE
// section (the cleartext public copy in the header is not what ssh signs with, so it is not
// trusted), checking that section is internally one key.
func keyV1PublicBlob(b []byte) ([]byte, error) {
	const magic = "openssh-key-v1\x00"
	if !bytes.HasPrefix(b, []byte(magic)) {
		return nil, fmt.Errorf("its private half is not an openssh-key-v1 key")
	}
	r := &wireReader{b: b[len(magic):]}
	cipher, kdf := string(r.str()), string(r.str())
	r.str() // kdf options
	n := r.u32()
	if r.err == nil && (cipher != "none" || kdf != "none") {
		return nil, errEncryptedKey
	}
	if r.err == nil && n != 1 {
		return nil, fmt.Errorf("its private half holds %d keys; a bench key file holds one", n)
	}
	r.str() // the header's public copy
	priv := &wireReader{b: r.str()}
	if r.err != nil {
		return nil, fmt.Errorf("its private half is truncated: %w", r.err)
	}
	if c1, c2 := priv.u32(), priv.u32(); priv.err == nil && c1 != c2 {
		return nil, fmt.Errorf("its private half fails its check integers")
	}
	typ := string(priv.str())
	var pub crypto.PublicKey
	switch typ {
	case "ssh-ed25519":
		pk, sk := priv.str(), priv.str()
		if priv.err != nil || len(pk) != ed25519.PublicKeySize || len(sk) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("its ed25519 private half is malformed")
		}
		derived := ed25519.NewKeyFromSeed(sk[:ed25519.SeedSize]).Public().(ed25519.PublicKey)
		if !bytes.Equal(derived, pk) || !bytes.Equal(sk[ed25519.SeedSize:], pk) {
			return nil, fmt.Errorf("its ed25519 private half is not one key (seed and public point differ)")
		}
		pub = derived
	case "ssh-rsa":
		nn, e, d, _, p, q := priv.mpint(), priv.mpint(), priv.mpint(), priv.mpint(), priv.mpint(), priv.mpint()
		if priv.err != nil || !e.IsInt64() {
			return nil, fmt.Errorf("its RSA private half is malformed")
		}
		k := &rsa.PrivateKey{PublicKey: rsa.PublicKey{N: nn, E: int(e.Int64())}, D: d, Primes: []*big.Int{p, q}}
		if err := k.Validate(); err != nil {
			return nil, fmt.Errorf("its RSA private half is not one key: %w", err)
		}
		pub = &k.PublicKey
	case "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521":
		curveName, q, d := string(priv.str()), priv.str(), priv.mpint()
		curve, ok := ecdhCurves[curveName]
		if priv.err != nil || !ok || "ecdsa-sha2-"+curveName != typ {
			return nil, fmt.Errorf("its ECDSA private half is malformed")
		}
		sk, err := curve.NewPrivateKey(d.FillBytes(make([]byte, ecdhScalarSize[curveName])))
		if err != nil {
			return nil, fmt.Errorf("its ECDSA private half is malformed: %w", err)
		}
		if !bytes.Equal(sk.PublicKey().Bytes(), q) {
			return nil, fmt.Errorf("its ECDSA private half is not one key (scalar and public point differ)")
		}
		return wireStrings([]byte(typ), []byte(curveName), q), nil
	default:
		return nil, fmt.Errorf("its private half is a %q key, not one a bench generates", typ)
	}
	return wirePublicKey(pub)
}

var ecdhCurves = map[string]ecdh.Curve{"nistp256": ecdh.P256(), "nistp384": ecdh.P384(), "nistp521": ecdh.P521()}
var ecdhScalarSize = map[string]int{"nistp256": 32, "nistp384": 48, "nistp521": 66}

// wirePublicKey is the SSH wire form (RFC 4253 section 6.6, RFC 5656) of a public key.
func wirePublicKey(pub crypto.PublicKey) ([]byte, error) {
	switch k := pub.(type) {
	case ed25519.PublicKey:
		return wireStrings([]byte("ssh-ed25519"), k), nil
	case *rsa.PublicKey:
		return wireStrings([]byte("ssh-rsa"), mpintBytes(big.NewInt(int64(k.E))), mpintBytes(k.N)), nil
	case *ecdsa.PublicKey:
		e, err := k.ECDH()
		if err != nil {
			return nil, fmt.Errorf("its ECDSA key is on no curve ssh uses: %w", err)
		}
		for name, c := range ecdhCurves {
			if e.Curve() == c {
				return wireStrings([]byte("ecdsa-sha2-"+name), []byte(name), e.Bytes()), nil
			}
		}
		return nil, fmt.Errorf("its ECDSA key is on no curve ssh uses")
	default:
		return nil, fmt.Errorf("its private half is a %T key, not one a bench generates", pub)
	}
}

// wireStrings concatenates length-prefixed strings.
func wireStrings(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = binary.BigEndian.AppendUint32(out, uint32(len(p)))
		out = append(out, p...)
	}
	return out
}

// mpintBytes is the body of an SSH mpint for a non-negative integer: big-endian, with a leading
// zero byte when the top bit would otherwise read as a sign.
func mpintBytes(n *big.Int) []byte {
	b := n.Bytes()
	if len(b) > 0 && b[0]&0x80 != 0 {
		b = append([]byte{0}, b...)
	}
	return b
}

// wireLeadingString returns the leading string of a wire-form blob (a public key's type).
func wireLeadingString(blob []byte) string {
	r := &wireReader{b: blob}
	s := r.str()
	if r.err != nil {
		return ""
	}
	return string(s)
}

// wireReader reads SSH wire-form fields; the first short read sticks in err.
type wireReader struct {
	b   []byte
	err error
}

func (r *wireReader) u32() uint32 {
	if r.err != nil || len(r.b) < 4 {
		r.err = errors.New("short read")
		return 0
	}
	v := binary.BigEndian.Uint32(r.b)
	r.b = r.b[4:]
	return v
}

func (r *wireReader) str() []byte {
	n := r.u32()
	if r.err != nil || uint64(n) > uint64(len(r.b)) {
		r.err = errors.New("short read")
		return nil
	}
	s := r.b[:n]
	r.b = r.b[n:]
	return s
}

func (r *wireReader) mpint() *big.Int {
	return new(big.Int).SetBytes(r.str())
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
