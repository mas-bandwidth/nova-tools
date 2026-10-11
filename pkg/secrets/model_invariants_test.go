package secrets

// The TLA+ module tla/SecretsSeat.tla beside this package states the design as
// state: keygen, seat add, seal, seat inject, place and the gate as actions,
// hand edits of the store as outside events, and the invariants the verbs keep.
// Each test below drives the model's sequence over the package's fakes (the
// scripted sops of seat_test.go, the scripted sops, git and gh of seal_test.go
// and seatinject_test.go, the strict exec seam of seam_test.go) and checks one
// invariant's formula, then replays the recorded counterexample the matching
// tla/MCSecretsSeat*.cfg case carries: the Broken cases are reversals the code
// holds against, the Reach cases properties the code does not keep, pinned as
// the documented limit.

import (
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/testguard"
)

// modelPlaceFake is the small exec fake place runs through here: sops reaches
// the real fake sops of the fixture, while the ssh delivery succeeds and keeps
// the bytes it was handed on stdin, so the test sees the channel, not a host.
type modelPlaceFake struct {
	t        *testing.T
	sshStdin string
	sshCalls int
}

func (m *modelPlaceFake) run(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error) {
	if name == "ssh" {
		b, err := io.ReadAll(stdin)
		require.NoError(m.t, err)
		m.sshStdin = string(b)
		m.sshCalls++
		return []byte(""), nil
	}
	return realExecCommand(stdin, env, dir, name, args...)
}

// modelPlace drives one RunPlace of secret out of asName through the fake above
// and requires the value to arrive on the ssh child's stdin.

func modelPlace(t *testing.T, f *seatFixture, keyPath, asName, secret, value string) (string, *modelPlaceFake) {
	t.Helper()
	mf := &modelPlaceFake{t: t}
	machines := filepath.Join(f.dir, "fleet.tsv")
	require.NoError(t, os.WriteFile(machines, []byte("web-1\tweb-1.invalid\t/srv/web\n"), 0o600))
	receipts := filepath.Join(f.dir, "receipts")
	line, err := RunPlace(PlaceInput{
		StoreDir: f.storeDir, AsName: asName, KeyPath: keyPath, SopsPath: f.sopsPath,
		Machine: "web-1", Secret: secret, RemotePath: "/srv/web/.config/nova-secrets/" + secret + ".env",
		Machines: machines, Receipts: receipts, SSH: "ssh", Exec: mf.run,
		Now: func() time.Time { return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC) },
	})
	require.NoError(t, err, "RunPlace: %v", err)
	require.Equal(t, value, mf.sshStdin, "the value did not travel to ssh on stdin")
	return line, mf
}

// modelGitStore makes a fixture's store a real git working copy with its files
// committed, and returns the commit HEAD names: the model's head. place reads
// HEAD as files (place.go storeHead) and seal reads it as git (sealCarry
// preflight), so the tree a test measures is the tree git holds.
func modelGitStore(t *testing.T, storeDir string) string {
	t.Helper()
	gitC(t, storeDir, "init", "-b", "main")
	gitC(t, storeDir, "config", "user.name", "model-test")
	gitC(t, storeDir, "config", "user.email", "model-test@example.com")
	gitC(t, storeDir, "config", "commit.gpgsign", "false")
	gitC(t, storeDir, "add", "-A")
	gitC(t, storeDir, "commit", "-m", "base")
	return strings.TrimSpace(gitC(t, storeDir, "rev-parse", "HEAD"))
}

// modelCommit commits the working copy and returns the commit HEAD names: the
// model's Propose and the move of head an APPROVE carries, which no verb here
// performs on its own.
func modelCommit(t *testing.T, storeDir, message string) string {
	t.Helper()
	gitC(t, storeDir, "add", "-A")
	gitC(t, storeDir, "commit", "-m", message)
	return strings.TrimSpace(gitC(t, storeDir, "rev-parse", "HEAD"))
}

// modelSeatFilesAtHead returns every seat file HEAD holds, by name, with the
// bytes git holds for it: the model's head.file, which
// ValueNeverInTheClearAtRest measures with the gate's own plain-value reader.
func modelSeatFilesAtHead(t *testing.T, storeDir string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	for _, name := range strings.Split(gitC(t, storeDir, "ls-tree", "-r", "--name-only", "HEAD"), "\n") {
		if name = strings.TrimSpace(name); isSeatYAML(name) {
			data, err := gitShowFile(storeDir, "HEAD", name)
			require.NoError(t, err, "git show HEAD:%s: %v", name, err)
			files[name] = data
		}
	}
	return files
}

// modelReceiptFields returns the six fields of one machine's receipt line:
// secret, path, file, head, blob, stamp (place.go placedReceipt).
func modelReceiptFields(t *testing.T, receipts, machine string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(receipts, machine+".receipt"))
	require.NoError(t, err, "no receipt was written: %v", err)
	fields := strings.Split(strings.TrimSpace(string(raw)), "\t")
	require.Len(t, fields, 6, "the receipt line wants 6 tab-separated fields (secret, path, file, head, blob, stamp):\n%s", raw)
	return fields
}

// modelPrivateHalves returns every path under dir whose bytes hold a private
// half: the model's keyAt[k] \cap {Store}, which PrivateKeyStaysHome requires
// to be empty for every key.
func modelPrivateHalves(t *testing.T, dir string) []string {
	t.Helper()
	found := []string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if filepath.Base(path) == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(b), "AGE-SECRET-KEY") {
			found = append(found, path)
		}
		return nil
	})
	require.NoError(t, err, "walking %s: %v", dir, err)
	return found
}

// modelRead reads a file the test requires to be there.
func modelRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err, "reading %s: %v", path, err)
	return string(b)
}

// modelBench lays out a bench and the store beside it: a key directory at 0700
// holding no key yet, an age-keygen a fake replaces, and a store whose rule and
// recovery key keygen reads. It returns the store, the key path and the
// age-keygen path.
func modelBench(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	storeDir := filepath.Join(dir, "store")
	mustMkdir(t, storeDir, 0755)
	mustMkdir(t, filepath.Join(storeDir, ".git"), 0755)
	mustWrite(t, filepath.Join(storeDir, ".sops.yaml"),
		"creation_rules:\n  - path_regex: ^air\\.yaml$\n    age: "+pubAir+","+pubRecovery+"\n", 0644)
	mustWrite(t, filepath.Join(storeDir, "recovery.pub"), pubRecovery+"\n", 0644)
	keyDir := filepath.Join(dir, "keys")
	mustMkdir(t, keyDir, 0700)
	return storeDir, filepath.Join(keyDir, "air.key"), executable(t, filepath.Join(dir, "age-keygen"))
}

// modelKeygenFake is the age-keygen of a bench: --version answers, and -o
// writes the private half to the key file with its public half as a comment, as
// the real tool does and as keygen.go reads it back.
func modelKeygenFake(t *testing.T, keyPath, pub string) execCommand {
	t.Helper()
	return func(stdin io.Reader, env []string, dir, name string, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte("1.3.2\n"), nil
		}
		require.Equal(t, []string{"-o", keyPath}, args, "age-keygen argv")
		require.NoError(t, os.WriteFile(keyPath, []byte("AGE-SECRET-KEY-1NEWSEAT\n# public key: "+pub+"\n"), 0o600))
		return nil, nil
	}
}

// modelSeal drives one RunSeal of value into name over the fixture's store as a
// real git working copy, and returns the OK line and the branch the ciphertext
// was committed on: seal's preflight reads the branch and the status with git,
// so the road the model's Seal walks is the real one.
func modelSeal(t *testing.T, f *sealFixture, name, value string) (string, string) {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	require.NoError(t, err)
	opts := f.options(t, name, value, true)
	opts.GitPath = gitBin
	line, err := RunSeal(opts)
	require.NoError(t, err, "RunSeal: %v", err)
	_, branch, found := strings.Cut(line, "branch=")
	require.True(t, found, "the OK line carries no branch=: %s", line)
	return line, strings.Fields(branch)[0]
}

// gateProposal commits files, and removes names, as a proposal the store's HEAD
// does not hold: the commit is made on a side branch and the store returns to
// the branch it was on, so HEAD stands at the base while the returned sha names
// the tree RunGate judges. The model's pr is a tree head does not hold until an
// APPROVE merges it, and RunGate performs no merge, so a verdict must leave
// HEAD where it was.
func gateProposal(t *testing.T, dir string, files map[string]string, remove ...string) string {
	t.Helper()
	home := strings.TrimSpace(gateGit(t, dir, "rev-parse", "--abbrev-ref", "HEAD"))
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	gateGit(t, dir, "checkout", "-b", "proposal")
	for _, name := range remove {
		require.NoError(t, os.Remove(filepath.Join(dir, name)))
	}
	proposal := gateCommit(t, dir, files)
	gateGit(t, dir, "checkout", home)
	require.Equal(t, base, strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD")), "building the proposal moved HEAD")
	return proposal
}

// TestModelSecretsValueNeverInTheClearAtRest checks both conjuncts of
// ValueNeverInTheClearAtRest (tla/SecretsSeat.tla): \A s \in Seats :
// head.file[s].plain = {}, and every receipt's id is <<"blob", ...>> and never
// <<"digest", value>>. It replays MCSecretsSeatBrokenReceiptByValue.cfg, where
// a receipt names the placement by a hash of the value: the receipt names the
// sealed file by its blob id, no value reaches argv, a result line or a
// receipt, and no seat file HEAD holds carries a value in the clear.
func TestModelSecretsValueNeverInTheClearAtRest(t *testing.T) {
	t.Parallel()

	t.Run("a receipt names a blob, never a digest of the value", func(t *testing.T) {
		t.Parallel()
		defer testguard.AllowHosts()()

		f := newSeatFixture(t)
		lines, err := RunSeatAdd(f.options(t, "GH_TOKEN,DEEPSEEK_API_KEY"))
		require.NoError(t, err, "RunSeatAdd: %v", err)
		placeLine, mf := modelPlace(t, f, f.airKey, "air", "GH_TOKEN", "ghp_carried")
		require.Equal(t, 1, mf.sshCalls, "place ran %d ssh children, want 1", mf.sshCalls)

		fields := modelReceiptFields(t, filepath.Join(f.dir, "receipts"), "web-1")
		receipt := strings.Join(fields, "\t")
		sealed, err := os.ReadFile(filepath.Join(f.storeDir, "air.yaml"))
		require.NoError(t, err, "no air.yaml was written: %v", err)
		blobID := GitBlobSHA1(sealed)
		wantBlob := hex.EncodeToString(blobID[:])
		assert.Equal(t, "air.yaml", fields[2], "the receipt does not name the sealed file it was placed from:\n%s", receipt)
		assert.Equal(t, wantBlob, fields[4], "the receipt blob is not the blob id of the bytes place read:\n%s", receipt)
		if assert.Len(t, fields[4], 40, "the receipt blob is not a sha1 hex id:\n%s", receipt) {
			_, err := hex.DecodeString(fields[4])
			assert.NoError(t, err, "the receipt blob is not hex:\n%s", receipt)
		}

		argv, _ := os.ReadFile(f.sopsArgs)
		joined := strings.Join(lines, "\n") + "\n" + placeLine + "\n" + string(argv) + "\n" + receipt
		for _, secret := range []string{"ghp_carried", "sk_carried"} {
			assert.NotContains(t, joined, secret, "a value leaked out of the stdin channels:\n%s", joined)
		}
		assert.NotContains(t, receipt, "digest", "the receipt names a digest of the value:\n%s", receipt)
		assert.Contains(t, placeLine, "blob="+wantBlob, "the OK line does not carry the receipt's blob: %s", placeLine)
		assert.Contains(t, placeLine, "file=air.yaml", "the OK line does not carry the receipt's file: %s", placeLine)
	})

	t.Run("no seat file at HEAD holds a value in the clear", func(t *testing.T) {
		t.Parallel()
		skipPOSIXFakesOnWindows(t)
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git not available")
		}

		f := newSealFixture(t, "OTHER: keepme\nTARGET: old\n")
		modelGitStore(t, f.storeDir)
		const value = "model-value-never-at-rest"
		line, branch := modelSeal(t, f, "TARGET", value+"\n")
		// The model's Gate on APPROVE: head' = pr. seal commits the ciphertext on its
		// branch and returns the store to the branch it started on, so the merge the
		// model names is driven here and head then holds the seat file seal wrote.
		gitC(t, f.storeDir, "merge", "--ff-only", branch)

		assert.NotContains(t, line, value, "the value reached the OK line: %s", line)
		assert.NotContains(t, readMaybe(t, f.sopsArgs), value, "the value reached a child's argv:\n%s", readMaybe(t, f.sopsArgs))
		atHead := modelSeatFilesAtHead(t, f.storeDir)
		require.Contains(t, atHead, "rowan.yaml", "the seat file seal wrote is not at HEAD: %v", atHead)
		for name, data := range atHead {
			key, plain, err := firstPlainValue(data, "")
			require.NoError(t, err, "firstPlainValue(%s): %v", name, err)
			assert.False(t, plain, "head.file[%s].plain holds %q: a value rests in the clear at HEAD:\n%s", name, key, data)
			assert.NotContains(t, string(data), value, "the value rests in the clear at HEAD in %s:\n%s", name, data)
		}
	})
}

// TestModelSecretsSeatFileNeverReplaced checks SeatFileNeverReplaced
// (tla/SecretsSeat.tla: lost = {}): no verb's write drops a value a seat file
// holds. It replays both recorded witnesses: MCSecretsSeatBrokenSeatAddReplaces
// (seat add treats an existing seat as new and writes over it) and
// MCSecretsSeatBrokenSeatAddFileCheck (seat add without seat.go:105's file
// check writes over a hand-written seat file, which has no rule, so the rule
// check lets it through). Both refusals leave the store byte for byte.
func TestModelSecretsSeatFileNeverReplaced(t *testing.T) {
	t.Parallel()

	t.Run("existing seat file", func(t *testing.T) {
		t.Parallel()
		f := newSeatFixture(t)
		mustWrite(t, filepath.Join(f.storeDir, "air.yaml"), "sops:\n", 0644)
		_, err := RunSeatAdd(f.options(t, "GH_TOKEN"))
		require.Error(t, err, "RunSeatAdd overwrote an existing seat file")
		assert.Contains(t, err.Error(), "air.yaml", "the refusal does not name the file: %v", err)
		assert.Equal(t, "sops:\n", f.read(t, "air.yaml"), "the existing file was touched")
		assert.NotContains(t, f.read(t, ".sops.yaml"), "air", "a refused run wrote a rule")
	})

	t.Run("hand-written file with no rule", func(t *testing.T) {
		t.Parallel()
		f := newSeatFixture(t)
		hand := fakeCipher([]string{pubRowan, pubRecovery}, "GH_TOKEN: ghp_hand\n")
		mustWrite(t, filepath.Join(f.storeDir, "air.yaml"), hand, 0644)
		cfgBefore := f.read(t, ".sops.yaml")
		_, err := RunSeatAdd(f.options(t, "GH_TOKEN"))
		require.Error(t, err, "RunSeatAdd wrote over a hand-written seat file with no rule")
		assert.Equal(t, hand, f.read(t, "air.yaml"), "the hand-written file was touched")
		assert.Equal(t, cfgBefore, f.read(t, ".sops.yaml"), "a refused run wrote a rule")
	})

	t.Run("inject keeps the names it was not told to carry", func(t *testing.T) {
		t.Parallel()
		f := newInjectFixture(t)
		_, err := RunSeatInject(f.options("NOVA_REDIS_BENCH_PASSWORD", true))
		require.NoError(t, err, "RunSeatInject: %v", err)
		air := f.read(t, "air.yaml")
		assert.Contains(t, air, "GH_TOKEN:", "inject dropped a value the target held:\n%s", air)
		assert.Contains(t, air, "NOVA_REDIS_BENCH_PASSWORD:", "inject dropped the value it carried:\n%s", air)
		assert.Equal(t, 1, strings.Count(air, "GH_TOKEN:"), "GH_TOKEN appears %d times, want 1:\n%s", strings.Count(air, "GH_TOKEN:"), air)
	})

	t.Run("seal keeps every name the file held", func(t *testing.T) {
		t.Parallel()
		skipPOSIXFakesOnWindows(t)
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git not available")
		}

		// The model's Seal: f.val is [old.val EXCEPT ![n] = v], so the document handed
		// to the encrypt holds every name the file held, with the sealed one replaced.
		f := newSealFixture(t, "OTHER: keepme\nTHIRD: alsokept\nTARGET: old\n")
		modelGitStore(t, f.storeDir)
		line, _ := modelSeal(t, f, "TARGET", "fresh\n")
		stdin := readMaybe(t, f.sopsStdin)
		for _, held := range []string{"OTHER: keepme", "THIRD: alsokept"} {
			assert.Contains(t, stdin, held, "seal dropped a name the seat file held, so lost # {}:\n%s", stdin)
		}
		assert.Equal(t, 1, strings.Count(stdin, "TARGET:"), "the re-sealed document holds %d TARGET lines, want 1:\n%s", strings.Count(stdin, "TARGET:"), stdin)
		assert.Contains(t, stdin, "TARGET: "+yamlSingleQuote("fresh"), "the value seal carried is not in the document it sealed:\n%s", stdin)
		assert.NotContains(t, line, "fresh", "the value reached the OK line: %s", line)
	})
}

// TestModelSecretsOnlyRecipientsOpen checks OnlyRecipientsOpen
// (tla/SecretsSeat.tla: unnamed = {}): a key decrypts a file only when the
// file names it. It replays MCSecretsSeatBrokenInjectCopiesUnread, where
// inject keeps the target's other values by decrypting the target: the code
// never opens the target, re-seals its held names from the source instead,
// and refuses, naming the key, when the target holds a sealed name the source
// does not carry (seatinject.go:28-34).
func TestModelSecretsOnlyRecipientsOpen(t *testing.T) {
	t.Parallel()

	t.Run("a key opens only the files that name it", func(t *testing.T) {
		t.Parallel()
		f := newSeatFixture(t)
		_, err := RunSeatAdd(f.options(t, "GH_TOKEN"))
		require.NoError(t, err, "RunSeatAdd: %v", err)
		airFile := filepath.Join(f.storeDir, "air.yaml")
		_, err = sealDecrypt(realExecCommand, f.sopsPath, f.airKey, airFile)
		assert.NoError(t, err, "the named key cannot open the file: %v", err)
		_, err = sealDecrypt(realExecCommand, f.sopsPath, f.rowanKey, airFile)
		assert.Error(t, err, "a key the file does not name opened it")
	})

	t.Run("inject refuses a source this key cannot open", func(t *testing.T) {
		t.Parallel()
		f := newInjectFixture(t)
		opts := f.options("NOVA_REDIS_BENCH_PASSWORD", true)
		opts.KeyPath = f.airKey
		airBefore := f.read(t, "air.yaml")
		_, err := RunSeatInject(opts)
		require.Error(t, err, "RunSeatInject decrypted a source its key cannot open")
		assert.Equal(t, airBefore, f.read(t, "air.yaml"), "a refused run changed the target")
	})

	t.Run("inject refuses a name the source lacks", func(t *testing.T) {
		t.Parallel()
		f := newInjectFixture(t)
		mustWrite(t, filepath.Join(f.storeDir, "air.yaml"),
			injectTargetFile([]string{pubAir, pubRecovery}, injectSealedBody+"BENCH_ONLY: ENC[AES256_GCM,data:x,type:str]\n"), 0644)
		airBefore := f.read(t, "air.yaml")
		_, err := RunSeatInject(f.options("NOVA_REDIS_BENCH_PASSWORD", true))
		require.Error(t, err, "RunSeatInject copied a value it never read")
		assert.Contains(t, err.Error(), "BENCH_ONLY", "the refusal does not name the unread key: %v", err)
		assert.Equal(t, airBefore, f.read(t, "air.yaml"), "a refused run changed the target")
	})
}

// TestModelSecretsPrivateKeyStaysHome checks PrivateKeyStaysHome
// (tla/SecretsSeat.tla: \A s \in Seats : keyAt[s] \subseteq {keyHome[s]}, and
// keyAt[Recovery] = {Console}; seat.go:38-40, :183, keygen.go): a seat's
// private key is only on the bench whose keygen made it, it never reaches the
// store, and the recovery key never becomes a seat's own key. Keygen is the
// action that sets keyAt, so it is driven here as well as seat add, which
// MCSecretsSeatBrokenPubTakesPrivate reverses by taking a private key as --pub
// and writing it into the store.
func TestModelSecretsPrivateKeyStaysHome(t *testing.T) {
	t.Parallel()

	t.Run("keygen writes the private half only to the bench's key file", func(t *testing.T) {
		t.Parallel()
		storeDir, keyPath, ageKeygen := modelBench(t)

		// Keygen(s, m): keyHome' = [keyHome EXCEPT ![s] = m] and keyAt' = {m}, so the
		// private half is in the key file on that bench, mode 0600, and nowhere else;
		// the public half is not state and travels in the receipt.
		lines, err := runKeygen(modelKeygenFake(t, keyPath, pubAir), "air", keyPath, ageKeygen, storeDir)
		require.NoError(t, err, "runKeygen: %v", err)
		key, err := os.ReadFile(keyPath)
		require.NoError(t, err, "keygen wrote no key file: %v", err)
		assert.Contains(t, string(key), "AGE-SECRET-KEY", "the private half is not in the bench's key file:\n%s", key)
		fi, err := os.Stat(keyPath)
		require.NoError(t, err, "stat %s: %v", keyPath, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "the key file mode is %04o, want 0600", fi.Mode().Perm())
		joined := strings.Join(lines, "\n")
		assert.Contains(t, joined, "pub="+pubAir, "the receipt does not carry the public half:\n%s", joined)
		assert.NotContains(t, joined, "AGE-SECRET-KEY", "the private half reached a printed line:\n%s", joined)
		assert.Empty(t, modelPrivateHalves(t, storeDir), "keyAt[air] reached the store")
		// keyAt[Recovery] = {Console}: the store holds the recovery key's public half.
		assert.NotContains(t, modelRead(t, filepath.Join(storeDir, "recovery.pub")), "AGE-SECRET-KEY",
			"the store holds the recovery key's private half")
	})

	t.Run("keygen refuses a key file that exists", func(t *testing.T) {
		t.Parallel()
		storeDir, keyPath, ageKeygen := modelBench(t)
		_, err := runKeygen(modelKeygenFake(t, keyPath, pubAir), "air", keyPath, ageKeygen, storeDir)
		require.NoError(t, err, "runKeygen: %v", err)
		before := modelRead(t, keyPath)

		// One key per seat: Keygen requires keyHome[s] = None, so a second keygen over
		// the key file the first wrote refuses and leaves that file byte for byte.
		_, err = runKeygen(modelKeygenFake(t, keyPath, pubStranger), "air", keyPath, ageKeygen, storeDir)
		require.Error(t, err, "a second keygen wrote over the seat's key")
		assert.Contains(t, err.Error(), "already exists", "the refusal does not name the existing key file: %v", err)
		assert.Equal(t, before, modelRead(t, keyPath), "the refused keygen changed the key file")
		assert.Empty(t, modelPrivateHalves(t, storeDir), "keyAt[air] reached the store")
	})

	t.Run("the recovery key is never a seat's own key", func(t *testing.T) {
		t.Parallel()
		f := newSeatFixture(t)
		opts := f.options(t, "GH_TOKEN")
		opts.Pub = pubRecovery
		_, err := RunSeatAdd(opts)
		require.Error(t, err, "RunSeatAdd granted the recovery key its own seat rule")
	})

	t.Run("a private key is never a seat's public key", func(t *testing.T) {
		t.Parallel()
		f := newSeatFixture(t)
		opts := f.options(t, "GH_TOKEN")
		opts.Pub = "AGE-SECRET-KEY-1ROWANPRIVATEHALFNEVERLEAVESTHEBENCH"
		_, err := RunSeatAdd(opts)
		require.Error(t, err, "RunSeatAdd took a private key as --pub")
	})

	t.Run("the rule holds the seat's key and the recovery key", func(t *testing.T) {
		t.Parallel()
		f := newSeatFixture(t)
		_, err := RunSeatAdd(f.options(t, "GH_TOKEN"))
		require.NoError(t, err, "RunSeatAdd: %v", err)
		cfg := f.read(t, ".sops.yaml")
		assert.Contains(t, cfg, "age: "+pubAir+","+pubRecovery, "the rule is not the seat's key and the recovery key:\n%s", cfg)
		air := f.read(t, "air.yaml")
		assert.Contains(t, air, "recipient: "+pubAir, "air.yaml is not sealed to the seat:\n%s", air)
		assert.Contains(t, air, "recipient: "+pubRecovery, "air.yaml is not sealed to the recovery key:\n%s", air)
		assert.NotContains(t, air, "recipient: "+pubRowan, "air.yaml names a third key:\n%s", air)
	})

	t.Run("no private half reaches the store", func(t *testing.T) {
		t.Parallel()
		f := newSeatFixture(t)
		_, err := RunSeatAdd(f.options(t, "GH_TOKEN"))
		require.NoError(t, err, "RunSeatAdd: %v", err)
		assert.Empty(t, modelPrivateHalves(t, f.storeDir), "a private half reached the store")
	})
}

// TestModelSecretsReceiptNamesCommittedBlob checks ReceiptNamesCommittedBlob
// (tla/SecretsSeat.tla; place.go:82-87), all three conjuncts of the receipt r:
// r.id[1] = "blob", objs[r.id[2]].val[n] = r.val -- the blob names the bytes
// whose value was placed -- and ~r.dirty => r.atHead: when the seat file held
// no uncommitted change, the blob is the one HEAD holds at that path. The
// sequence commits the seat add before the place, so the seat file is clean;
// the uncommitted leg is TestModelSecretsReachDirtyPlace.
func TestModelSecretsReceiptNamesCommittedBlob(t *testing.T) {
	t.Parallel()
	defer testguard.AllowHosts()()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	f := newSeatFixture(t)
	modelGitStore(t, f.storeDir)
	_, err := RunSeatAdd(f.options(t, "GH_TOKEN"))
	require.NoError(t, err, "RunSeatAdd: %v", err)
	// The model's Propose and the move of head an APPROVE carries: air.yaml reaches
	// HEAD, so the place that follows reads a seat file with no uncommitted change.
	head := modelCommit(t, f.storeDir, "air")
	line, _ := modelPlace(t, f, f.airKey, "air", "GH_TOKEN", "ghp_carried")

	fields := modelReceiptFields(t, filepath.Join(f.dir, "receipts"), "web-1")
	blob := fields[4]
	assert.Contains(t, line, "file=air.yaml", "the OK line does not carry the receipt's file: %s", line)
	assert.Contains(t, line, "head="+head, "the OK line does not carry the commit place started on: %s", line)
	assert.Contains(t, line, "blob="+blob, "the OK line does not carry the receipt's blob: %s", line)
	assert.Equal(t, head, fields[3], "the receipt's head is not the commit the store stood on")

	// ~r.dirty => r.atHead: the seat file was committed before the place, so the blob
	// the receipt names is the blob of that path at HEAD.
	atHead, err := gitShowFile(f.storeDir, head, "air.yaml")
	require.NoError(t, err, "HEAD holds no air.yaml: %v", err)
	headBlob := GitBlobSHA1(atHead)
	assert.Equal(t, hex.EncodeToString(headBlob[:]), blob,
		"the seat file was clean, so the receipt's blob must be the blob HEAD holds at that path")

	// objs[r.id[2]].val[n] = r.val: the bytes the receipt's blob names hold the value
	// that travelled, read back with the seat's own key.
	snapshot := filepath.Join(f.dir, "receipt-blob.yaml")
	require.NoError(t, os.WriteFile(snapshot, atHead, 0o600))
	dec, err := sealDecrypt(realExecCommand, f.sopsPath, f.airKey, snapshot)
	require.NoError(t, err, "the bytes the receipt names do not decrypt with the seat's key: %v", err)
	secretsMap, _, err := ParseDecryptedSecrets(dec)
	require.NoError(t, err, "ParseDecryptedSecrets of the bytes the receipt names: %v", err)
	sec, ok := secretsMap["GH_TOKEN"]
	require.True(t, ok, "the bytes the receipt names hold no GH_TOKEN:\n%s", dec)
	require.NoError(t, sec.Use(func(v string) error {
		assert.Equal(t, "ghp_carried", v, "the receipt's blob names bytes whose value is not the value placed")
		return nil
	}))
}

// TestModelSecretsReachDirtyPlace replays the recorded reach case
// MCSecretsSeatReachDirtyPlace.cfg, the limit ReceiptBlobAtHead states and
// place does not keep (tla/SecretsSeat.tla): placing from a seat file with an
// uncommitted change records a blob HEAD lacks, so the receipt's head is no
// claim that the commit holds the placed bytes. ReceiptNamesCommittedBlob
// still holds on this leg -- ~r.dirty => r.atHead says nothing when r.dirty --
// and the blob still names the bytes whose value was placed.
func TestModelSecretsReachDirtyPlace(t *testing.T) {
	t.Parallel()
	defer testguard.AllowHosts()()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	f := newSeatFixture(t)
	base := modelGitStore(t, f.storeDir)
	_, err := RunSeatAdd(f.options(t, "GH_TOKEN"))
	require.NoError(t, err, "RunSeatAdd: %v", err)
	// Nothing is committed: wc.file[air] # head.file[air], so r.dirty = TRUE and the
	// blob the receipt names is one HEAD has no object for at that path.
	line, _ := modelPlace(t, f, f.airKey, "air", "GH_TOKEN", "ghp_carried")

	fields := modelReceiptFields(t, filepath.Join(f.dir, "receipts"), "web-1")
	assert.Equal(t, base, fields[3], "the receipt's head is not the commit place started on")
	assert.Contains(t, line, "head="+base, "the OK line does not carry the commit place started on: %s", line)
	assert.Contains(t, line, "blob="+fields[4], "the OK line does not carry the receipt's blob: %s", line)
	_, err = gitCErr(t, f.storeDir, "show", base+":air.yaml")
	assert.Error(t, err, "HEAD holds air.yaml; the reach case wants a blob HEAD lacks")

	// The receipt still names the bytes place read: the working copy's, which HEAD
	// does not hold, so r.id[1] = "blob" and objs[r.id[2]].val[n] = r.val hold.
	sealed, err := os.ReadFile(filepath.Join(f.storeDir, "air.yaml"))
	require.NoError(t, err, "no air.yaml was written: %v", err)
	blobID := GitBlobSHA1(sealed)
	assert.Equal(t, hex.EncodeToString(blobID[:]), fields[4], "the receipt does not name the bytes place read")
}

// TestModelSecretsGateRefusesEveryRuleBreak checks GateRefusesEveryRuleBreak
// (tla/SecretsSeat.tla: merged = {}; gate.go:25-31): HEAD moves only to a tree
// that breaks none of the gate's checks, and a refusing verdict names the
// first check broken in the order RunGate runs them (3, 1, 5, 2). It replays
// MCSecretsSeatBrokenGateMergesAny, where the gate merges the proposal on any
// verdict: the proposal here is a commit HEAD does not hold, so HEAD stands at
// the base it was judged against on every verdict -- RunGate judges and merges
// nothing, the merge an APPROVE carries being the squash merge and the pull.
func TestModelSecretsGateRefusesEveryRuleBreak(t *testing.T) {
	t.Parallel()

	sealed := gateSealedFile()
	for _, tc := range []struct {
		name     string
		base     map[string]string
		proposal map[string]string
		remove   string
		wantCode int
		want     []string
	}{
		{"approve a good seat", nil,
			map[string]string{".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey), "rowan.yaml": sealed},
			"", 0, []string{"GATE APPROVE files=2"}},
		{"refuse another file", nil,
			map[string]string{".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey), "rowan.yaml": sealed, "notes.txt": "a change outside the gate\n"},
			"", 1, []string{"GATE FAILED rule=0", "notes.txt"}},
		{"refuse a third recipient", nil,
			map[string]string{".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey, gateThirdKey), "rowan.yaml": sealed},
			"", 1, []string{"GATE FAILED rule=1"}},
		{"refuse a removed seat", map[string]string{".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey), "rowan.yaml": sealed},
			map[string]string{}, "rowan.yaml", 1, []string{"GATE FAILED", "rowan.yaml"}},
		{"refuse a plain value", nil,
			map[string]string{".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey), "rowan.yaml": sealed + "GH_TOKEN: sk-live-notencrypted\n"},
			"", 1, []string{"GATE FAILED", "rowan.yaml"}},
		{"refuse a file with no rule", map[string]string{".sops.yaml": "creation_rules:\n  - path_regex: ^mini\\.yaml$\n    age: " + gateSeatKey + "," + gateRecoveryKey + "\n"},
			map[string]string{"rowan.yaml": sealed},
			"", 1, []string{"GATE FAILED rule=0", "rowan.yaml"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := gateStart(t)
			if len(tc.base) > 0 {
				gateCommit(t, dir, tc.base)
			}
			base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
			var remove []string
			if tc.remove != "" {
				remove = append(remove, tc.remove)
			}
			proposal := gateProposal(t, dir, tc.proposal, remove...)
			line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: proposal})
			assert.Equal(t, tc.wantCode, code, "RunGate code = %d, want %d (line=%q)", code, tc.wantCode, line)
			for _, w := range tc.want {
				assert.Contains(t, line, w, "RunGate line = %q, want %q", line, w)
			}
			// merged = {}: the gate judged the proposal and moved nothing, so a broken
			// check can never reach HEAD. A gate that merged on any verdict -- the
			// reversed witness -- would leave HEAD on the proposal it refused.
			assert.Equal(t, base, strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD")),
				"RunGate moved HEAD to the proposal it judged")
		})
	}
}

// TestModelSecretsNoHandRoad checks NoHandRoad (tla/SecretsSeat.tla): no entry
// of a seat file at HEAD was written by an outside edit; every value reaches
// the store by seat add, seal or inject. A hand-written plain entry is refused
// at the gate. It pins the MCSecretsSeatReachHandSeal limit beside it: a hand
// sops pipe with a key that opens the file writes a sealed entry the gate
// cannot tell from a verb's, so the gate approves it.
func TestModelSecretsNoHandRoad(t *testing.T) {
	t.Parallel()

	t.Run("a hand-written plain entry never reaches HEAD", func(t *testing.T) {
		t.Parallel()
		dir := gateStart(t)
		base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
		head := gateCommit(t, dir, map[string]string{
			".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey),
			"rowan.yaml": gateSealedFile() + "GH_TOKEN: sk-handwritten\n",
		})
		line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
		assert.Equal(t, 1, code, "the gate approved a hand-written plain entry (line=%q)", line)
		assert.Contains(t, line, "GATE FAILED", "RunGate line = %q, want FAILED", line)
	})

	t.Run("a hand-sealed entry is one the gate cannot tell", func(t *testing.T) {
		t.Parallel()
		dir := gateStart(t)
		base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
		// Sealed bytes no verb wrote, shaped exactly as a verb's: sops metadata
		// naming the rule's recipients and every value encrypted.
		head := gateCommit(t, dir, map[string]string{
			".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey),
			"rowan.yaml": gateSealedFile(),
		})
		line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
		assert.Equal(t, 0, code, "the gate refused sealed bytes shaped as a verb's (line=%q)", line)
		assert.Contains(t, line, "GATE APPROVE", "RunGate line = %q, want APPROVE", line)
	})
}

// TestModelSecretsEveryProposalJudged checks EveryProposalJudged
// (tla/SecretsSeat.tla, the liveness the MCSecretsSeatLive case carries): a
// proposal is judged, always. Every shape of proposal -- empty, good, rule
// breaking, and naming nothing -- comes back with a verdict line and an exit,
// never silence and never a hang.
func TestModelSecretsEveryProposalJudged(t *testing.T) {
	t.Parallel()

	dir := gateStart(t)
	base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
	good := gateCommit(t, dir, map[string]string{
		".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey),
		"rowan.yaml": gateSealedFile(),
	})
	bad := gateCommit(t, dir, map[string]string{"notes.txt": "a change outside the gate\n"})

	for _, tc := range []struct {
		name string
		base string
		head string
		want string
		code int
	}{
		{"empty proposal", base, base, "GATE APPROVE files=0", 0},
		{"good proposal", base, good, "GATE APPROVE", 0},
		{"rule-breaking proposal", base, bad, "GATE FAILED", 1},
		{"proposal naming nothing", base, "does-not-exist", "SECRETS GATE REFUSED", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			line, code := RunGate(GateInput{StoreDir: dir, Base: tc.base, Head: tc.head})
			assert.Equal(t, tc.code, code, "no verdict for %s (line=%q)", tc.name, line)
			assert.Contains(t, line, tc.want, "no verdict line for %s: %q", tc.name, line)
		})
	}
}
