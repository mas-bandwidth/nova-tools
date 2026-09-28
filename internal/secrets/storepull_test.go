package secrets

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// TestStorePullUsesBenchOwnedKey is docs/SPEC-SECRETS.md, "Additions from dogfooding", rule 7:
// the store is pulled on a bench over the bench's own SSH key, generated on that bench, never a
// person's credential, and never present inside the card wall. It drives the real pull path
// (PullStore runs git, git runs the ssh the options name) against a local bare repository
// reached through a fake ssh in t.TempDir(), so no host is reached and no real key is read:
// every key here is a throwaway generated in the test's own temp directory.
func TestStorePullUsesBenchOwnedKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake ssh is a POSIX shell script")
	}
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}

	const seat = "swarm-testbench"
	root := t.TempDir()
	origin, store := storePullFixture(t, gitBin, root)
	sshLog := filepath.Join(root, "ssh.log")
	fakeSSH := writeFakeSSH(t, root, sshLog)

	seatHome := filepath.Join(root, "home", seat)
	benchKey := writeThrowawayKey(t, filepath.Join(seatHome, ".ssh", "id_ed25519"), seat+"@testbench")
	personHome := filepath.Join(root, "home", "glenn")
	personKey := writeThrowawayKey(t, filepath.Join(personHome, ".ssh", "id_ed25519"), "glenn@laptop")
	// A person's key copied into the seat's own .ssh is still a person's key: its public half
	// names the person, and the refusal must not be fooled by where the file was put.
	copiedPersonKey := writeThrowawayKey(t, filepath.Join(seatHome, ".ssh", "id_glenn"), "glenn@laptop")
	wall := filepath.Join(root, "slot", "jobs", "card-1")
	wallKey := writeThrowawayKey(t, filepath.Join(wall, "work", ".ssh", "id_ed25519"), seat+"@testbench")
	// A seat whose home is itself inside the card wall: the key is the seat's own and still refused.
	wallSeatHome := filepath.Join(wall, "home")
	wallSeatKey := writeThrowawayKey(t, filepath.Join(wallSeatHome, ".ssh", "id_ed25519"), seat+"@testbench")
	// Stella's hold on #3240: a person's private key beside a seat-labeled public half. The comment
	// in the .pub is the seat's, but the key ssh would offer is the person's; the pull must see
	// that the two halves are not one key. Both the PEM form and the OpenSSH form ssh-keygen
	// writes are held.
	swappedKey := writeThrowawayKey(t, filepath.Join(seatHome, ".ssh", "id_swapped"), seat+"@testbench")
	copyFile(t, personKey, swappedKey)
	personOpenSSH := writeThrowawayOpenSSHKey(t, filepath.Join(personHome, ".ssh", "id_openssh"), "glenn@laptop")
	swappedOpenSSH := writeThrowawayOpenSSHKey(t, filepath.Join(seatHome, ".ssh", "id_swapped_openssh"), seat+"@testbench")
	copyFile(t, personOpenSSH, swappedOpenSSH)
	benchOpenSSH := writeThrowawayOpenSSHKey(t, filepath.Join(seatHome, ".ssh", "id_openssh"), seat+"@testbench")
	linkedKey := filepath.Join(seatHome, ".ssh", "id_link")
	if err := os.Symlink(personKey, linkedKey); err != nil {
		t.Fatal(err)
	}

	base := StorePullOptions{
		StoreDir: store,
		Seat:     seat,
		SeatHome: seatHome,
		Wall:     []string{wall},
		Git:      gitBin,
		SSH:      fakeSSH,
	}

	refused := []struct {
		name, key, home, want string
	}{
		{"a person's key from a person's home", personKey, seatHome, "not under the seat's own"},
		{"a person's key copied into the seat", copiedPersonKey, seatHome, "a person's credential"},
		{"a symlink to a person's key", linkedKey, seatHome, "symlink"},
		{"the seat's key inside the card wall", wallKey, seatHome, "inside the card wall"},
		{"a seat whose home is inside the card wall", wallSeatKey, wallSeatHome, "inside the card wall"},
		{"a person's private key under the seat's public half", swappedKey, seatHome, "does not match its public half"},
		{"a person's OpenSSH key under the seat's public half", swappedOpenSSH, seatHome, "does not match its public half"},
	}
	for _, c := range refused {
		t.Run("refuses "+c.name, func(t *testing.T) {
			_ = os.Remove(sshLog)
			o := base
			o.Key, o.SeatHome = c.key, c.home
			_, err := PullStore(o)
			if err == nil {
				t.Fatalf("PullStore accepted %s (%s); rule 7 says the store is pulled only over the bench's own key", c.name, c.key)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("PullStore refused %s but the refusal does not say %q: %v", c.name, c.want, err)
			}
			if _, statErr := os.Stat(sshLog); statErr == nil {
				t.Fatalf("PullStore refused %s but ssh was already started; the refusal must stand before any credential is offered", c.name)
			}
		})
	}

	for _, c := range []struct{ name, key string }{
		{"the bench's own key", benchKey},
		{"the bench's own OpenSSH-format key", benchOpenSSH},
	} {
		t.Run("pulls over "+c.name, func(t *testing.T) {
			_ = os.Remove(sshLog)
			t.Setenv("SSH_AUTH_SOCK", filepath.Join(root, "agent.sock"))
			o := base
			o.Key = c.key
			head, err := PullStore(o)
			if err != nil {
				t.Fatalf("PullStore refused %s: %v", c.name, err)
			}
			want := strings.TrimSpace(gitOut(t, gitBin, origin, "rev-parse", "HEAD"))
			if head != want {
				t.Fatalf("store head after the pull is %s, want the origin head %s", head, want)
			}
			logged, err := os.ReadFile(sshLog)
			if err != nil {
				t.Fatalf("the pull never ran the seat's ssh: %v", err)
			}
			argv := strings.Split(strings.TrimSpace(string(logged)), "\n")
			for _, w := range []string{"-i", c.key, "IdentitiesOnly=yes", "IdentityAgent=none", "agent=unset"} {
				if !containsLine(argv, w) {
					t.Fatalf("ssh argv/env %q does not carry %q; the pull must offer exactly the bench's key and nothing an agent holds", argv, w)
				}
			}
		})
	}
}

// storePullFixture builds a bare origin with two commits and a store clone one commit behind,
// whose origin is an ssh:// URL so git must go through the ssh the pull names.
func storePullFixture(t *testing.T, gitBin, root string) (origin, store string) {
	t.Helper()
	origin = filepath.Join(root, "origin.git")
	work := filepath.Join(root, "work")
	store = filepath.Join(root, "store")
	gitOut(t, gitBin, root, "init", "-q", "--bare", "-b", "main", origin)
	gitOut(t, gitBin, root, "init", "-q", "-b", "main", work)
	commit := func(msg string) {
		if err := os.WriteFile(filepath.Join(work, "rowan.yaml"), []byte(msg+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitOut(t, gitBin, work, "add", "rowan.yaml")
		gitOut(t, gitBin, work, "-c", "user.name=t", "-c", "user.email=t@example.test", "commit", "-q", "-m", msg)
	}
	commit("one")
	gitOut(t, gitBin, work, "push", "-q", origin, "main")
	gitOut(t, gitBin, root, "clone", "-q", origin, store)
	gitOut(t, gitBin, store, "remote", "set-url", "origin", "ssh://store.test"+origin)
	commit("two")
	gitOut(t, gitBin, work, "push", "-q", origin, "main")
	return origin, store
}

// writeFakeSSH writes an ssh that records its argv and whether an agent socket reached it,
// then runs the remote command locally, which is what git-over-ssh asks the far side to do.
func writeFakeSSH(t *testing.T, root, logPath string) string {
	t.Helper()
	p := filepath.Join(root, "bin", "ssh")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"log=" + shellQuote(logPath) + "\n" +
		"printf '%s\\n' \"$@\" > \"$log\"\n" +
		"if [ -n \"$SSH_AUTH_SOCK\" ]; then echo agent=set >> \"$log\"; else echo agent=unset >> \"$log\"; fi\n" +
		"for a; do last=$a; done\n" +
		"exec sh -c \"$last\"\n"
	if err := testbin.WriteExecutable(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// writeThrowawayKey generates an ed25519 key in the test's temp directory: the private half
// PEM-encoded, mode 0600, and the public half in authorized_keys form with the given comment.
func writeThrowawayKey(t *testing.T, path, comment string) string {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	var wire []byte
	for _, part := range [][]byte{[]byte("ssh-ed25519"), pub} {
		wire = binary.BigEndian.AppendUint32(wire, uint32(len(part)))
		wire = append(wire, part...)
	}
	line := "ssh-ed25519 " + base64.StdEncoding.EncodeToString(wire) + " " + comment + "\n"
	if err := os.WriteFile(path+".pub", []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeThrowawayOpenSSHKey generates an ed25519 key in the test's temp directory in the
// unencrypted openssh-key-v1 form ssh-keygen writes (PROTOCOL.key), mode 0600, with its public
// half beside it in authorized_keys form.
func writeThrowawayOpenSSHKey(t *testing.T, path, comment string) string {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pubBlob := testWire([]byte("ssh-ed25519"), pub)
	var check [4]byte
	if _, err := rand.Read(check[:]); err != nil {
		t.Fatal(err)
	}
	section := append(append([]byte{}, check[:]...), check[:]...)
	section = append(section, testWire([]byte("ssh-ed25519"), pub, priv, []byte(comment))...)
	for i := byte(1); len(section)%8 != 0; i++ {
		section = append(section, i)
	}
	body := []byte("openssh-key-v1\x00")
	body = append(body, testWire([]byte("none"), []byte("none"), nil)...)
	body = binary.BigEndian.AppendUint32(body, 1)
	body = append(body, testWire(pubBlob, section)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "OPENSSH PRIVATE KEY", Bytes: body}), 0o600); err != nil {
		t.Fatal(err)
	}
	line := "ssh-ed25519 " + base64.StdEncoding.EncodeToString(pubBlob) + " " + comment + "\n"
	if err := os.WriteFile(path+".pub", []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// testWire concatenates SSH length-prefixed strings; the test's own copy, so the fixture does not
// lean on the code under test.
func testWire(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = binary.BigEndian.AppendUint32(out, uint32(len(p)))
		out = append(out, p...)
	}
	return out
}

// copyFile overwrites dst's contents with src's, keeping dst's mode.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func gitOut(t *testing.T, gitBin, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(gitBin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

// TestStorePullKeyRefusesAMalformedKeyWithoutPanicking is Stella's hold on #3240 at 31113798:
// the OpenSSH ECDSA parser took the private scalar as an unbounded mpint and FillBytes panics
// when it does not fit the curve's scalar width, so a corrupt or hostile key file crashed the
// caller instead of being refused. Every case here is an openssh-key-v1 file whose private
// section is malformed; each must come back as an error, never a panic.
func TestStorePullKeyRefusesAMalformedKeyWithoutPanicking(t *testing.T) {
	t.Parallel()

	wide := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = 0x7f
		}
		return b
	}
	// A syntactically valid uncompressed point is not needed: the scalar is refused first.
	point := append([]byte{0x04}, make([]byte, 64)...)
	cases := []struct {
		name    string
		typ     string
		private [][]byte
	}{
		{"nistp256 scalar of 33 bytes", "ecdsa-sha2-nistp256", [][]byte{[]byte("nistp256"), point, wide(33)}},
		{"nistp256 scalar of 4096 bytes", "ecdsa-sha2-nistp256", [][]byte{[]byte("nistp256"), point, wide(4096)}},
		{"nistp384 scalar of 49 bytes", "ecdsa-sha2-nistp384", [][]byte{[]byte("nistp384"), point, wide(49)}},
		{"nistp521 scalar of 67 bytes", "ecdsa-sha2-nistp521", [][]byte{[]byte("nistp521"), point, wide(67)}},
		{"nistp256 empty scalar", "ecdsa-sha2-nistp256", [][]byte{[]byte("nistp256"), point, nil}},
		{"rsa with every integer zero", "ssh-rsa", [][]byte{nil, nil, nil, nil, nil, nil}},
		{"rsa with an oversized exponent", "ssh-rsa", [][]byte{wide(256), wide(9), wide(256), nil, wide(128), wide(128)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "id_malformed")
			writeOpenSSHKeyFile(t, path, c.typ, c.private...)
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("a malformed %s key panicked the parser instead of being refused: %v", c.typ, r)
					}
				}()
				_, err = privateKeyPublicBlob(path)
			}()
			if err == nil {
				t.Fatalf("a malformed %s key was accepted", c.typ)
			}
		})
	}
}

// writeOpenSSHKeyFile writes an unencrypted openssh-key-v1 file whose private section carries typ
// and then the given fields, verbatim, so a test can build a key ssh-keygen would never write.
func writeOpenSSHKeyFile(t *testing.T, path, typ string, fields ...[]byte) {
	t.Helper()
	section := []byte{1, 2, 3, 4, 1, 2, 3, 4}
	section = append(section, testWire(append([][]byte{[]byte(typ)}, append(fields, []byte("x@y"))...)...)...)
	for i := byte(1); len(section)%8 != 0; i++ {
		section = append(section, i)
	}
	body := []byte("openssh-key-v1\x00")
	body = append(body, testWire([]byte("none"), []byte("none"), nil)...)
	body = binary.BigEndian.AppendUint32(body, 1)
	body = append(body, testWire(testWire([]byte(typ)), section)...)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "OPENSSH PRIVATE KEY", Bytes: body}), 0o600); err != nil {
		t.Fatal(err)
	}
}
