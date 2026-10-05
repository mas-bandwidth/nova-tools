package secrets

// The TLA+ module tla/SecretsSeat.tla beside this package states the design as
// state: keygen, seat add, seal, seat inject, place and the gate as actions,
// hand edits of the store as outside events, and the invariants the verbs keep.
// Each test below drives the model's sequence over the package's fakes (the
// scripted sops of seat_test.go, the scripted git and gh of seatinject_test.go
// and seal_test.go, the strict exec seam of seam_test.go) and checks one
// invariant, then replays the recorded counterexample the matching
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

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
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

// TestModelSecretsValueNeverInTheClearAtRest drives the model's sequence --
// seat add, then place -- and checks ValueNeverInTheClearAtRest
// (tla/SecretsSeat.tla): no value reaches argv, a receipt, or a result line;
// the value travels only on a child's stdin, and the receipt names the sealed
// file by its blob id, never by anything derived from the value (the
// MCSecretsSeatBrokenReceiptByValue.cfg witness names a receipt by a digest of
// the value instead).
func TestModelSecretsValueNeverInTheClearAtRest(t *testing.T) {
	t.Parallel()
	defer testguard.AllowHosts()()

	f := newSeatFixture(t)
	lines, err := RunSeatAdd(f.options(t, "GH_TOKEN,DEEPSEEK_API_KEY"))
	require.NoError(t, err, "RunSeatAdd: %v", err)
	placeLine, mf := modelPlace(t, f, f.airKey, "air", "GH_TOKEN", "ghp_carried")
	require.Equal(t, 1, mf.sshCalls, "place ran %d ssh children, want 1", mf.sshCalls)

	raw, err := os.ReadFile(filepath.Join(f.dir, "receipts", "web-1.receipt"))
	require.NoError(t, err, "no receipt was written: %v", err)
	receipt := string(raw)
	fields := strings.Split(strings.TrimSpace(receipt), "\t")
	require.Len(t, fields, 6, "the receipt line wants 6 tab-separated fields (secret, path, file, head, blob, stamp):\n%s", receipt)

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
// (tla/SecretsSeat.tla; seat.go:38-40, :183): a seat's private key never
// leaves the bench that made it, and the recovery key never becomes a seat's
// own key. It replays MCSecretsSeatBrokenPubTakesPrivate, where seat add takes
// a private key as --pub and writes it into the store.
func TestModelSecretsPrivateKeyStaysHome(t *testing.T) {
	t.Parallel()

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
		bad := []string{}
		err = filepath.WalkDir(f.storeDir, func(path string, d os.DirEntry, err error) error {
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
				bad = append(bad, path)
			}
			return nil
		})
		require.NoError(t, err, "walking the store: %v", err)
		assert.Empty(t, bad, "a private half reached the store: %v", bad)
	})
}

// TestModelSecretsReceiptNamesCommittedBlob checks ReceiptNamesCommittedBlob
// (tla/SecretsSeat.tla; place.go:82-87): a receipt's blob names the bytes
// whose value was placed, and is the blob HEAD held whenever the seat file
// held no uncommitted change. It pins the MCSecretsSeatReachDirtyPlace limit:
// place from a seat file with an uncommitted change records a blob HEAD
// lacks, so head stays no claim that the commit holds the placed bytes.
func TestModelSecretsReceiptNamesCommittedBlob(t *testing.T) {
	t.Parallel()
	defer testguard.AllowHosts()()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	f := newSeatFixture(t)
	gitC(t, f.storeDir, "init", "-b", "main")
	gitC(t, f.storeDir, "config", "user.name", "model-test")
	gitC(t, f.storeDir, "config", "user.email", "model-test@example.com")
	gitC(t, f.storeDir, "add", "-A")
	gitC(t, f.storeDir, "commit", "-m", "base")
	base := gitC(t, f.storeDir, "rev-parse", "HEAD")

	_, err := RunSeatAdd(f.options(t, "GH_TOKEN"))
	require.NoError(t, err, "RunSeatAdd: %v", err)
	line, _ := modelPlace(t, f, f.airKey, "air", "GH_TOKEN", "ghp_carried")

	sealed, err := os.ReadFile(filepath.Join(f.storeDir, "air.yaml"))
	require.NoError(t, err, "no air.yaml was written: %v", err)
	blobID := GitBlobSHA1(sealed)
	wantBlob := hex.EncodeToString(blobID[:])
	assert.Contains(t, line, "file=air.yaml", "the OK line does not carry the receipt's file: %s", line)
	assert.Contains(t, line, "head="+base, "the OK line does not carry the commit place started on: %s", line)
	assert.Contains(t, line, "blob="+wantBlob, "the OK line does not carry the blob of the bytes read: %s", line)

	// The seat file changed after the base commit, so HEAD cannot hold the
	// placed bytes: the receipt names what was read, not what HEAD holds.
	_, err = gitCErr(t, f.storeDir, "show", base+":air.yaml")
	assert.Error(t, err, "HEAD holds air.yaml; the dirty leg wants bytes HEAD lacks")
}

// TestModelSecretsGateRefusesEveryRuleBreak checks GateRefusesEveryRuleBreak
// (tla/SecretsSeat.tla: merged = {}; gate.go:25-31): HEAD moves only to a tree
// that breaks none of the gate's checks, in the order RunGate runs them
// (3, 1, 5, 2). It replays MCSecretsSeatBrokenGateMergesAny, where the gate
// merges the proposal on any verdict: here every refusal leaves HEAD where it
// was, and only an APPROVE moves it.
func TestModelSecretsGateRefusesEveryRuleBreak(t *testing.T) {
	t.Parallel()

	sealed := gateSealedFile()
	for _, tc := range []struct {
		name     string
		base     map[string]string
		head     map[string]string
		remove   string
		wantCode int
		want     []string
	}{
		{"approve a good seat", nil,
			map[string]string{".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey), "rowan.yaml": sealed},
			"", 0, []string{"GATE APPROVE files=2"}},
		{"refuse another file", nil,
			map[string]string{".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey), "rowan.yaml": sealed, "notes.txt": "a change outside the gate\n"},
			"", 2, []string{"GATE REFUSE rule=0", "notes.txt"}},
		{"refuse a third recipient", nil,
			map[string]string{".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey, gateThirdKey), "rowan.yaml": sealed},
			"", 2, []string{"GATE REFUSE rule=1"}},
		{"refuse a removed seat", map[string]string{".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey), "rowan.yaml": sealed},
			map[string]string{}, "rowan.yaml", 2, []string{"GATE REFUSE", "rowan.yaml"}},
		{"refuse a plain value", nil,
			map[string]string{".sops.yaml": gateGoodSops(gateSeatKey, gateRecoveryKey), "rowan.yaml": sealed + "GH_TOKEN: sk-live-notencrypted\n"},
			"", 2, []string{"GATE REFUSE", "rowan.yaml"}},
		{"refuse a file with no rule", map[string]string{".sops.yaml": "creation_rules:\n  - path_regex: ^mini\\.yaml$\n    age: " + gateSeatKey + "," + gateRecoveryKey + "\n"},
			map[string]string{"rowan.yaml": sealed},
			"", 2, []string{"GATE REFUSE rule=0", "rowan.yaml"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := gateStart(t)
			if len(tc.base) > 0 {
				gateCommit(t, dir, tc.base)
			}
			base := strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD"))
			if tc.remove != "" {
				require.NoError(t, os.Remove(filepath.Join(dir, tc.remove)))
			}
			head := gateCommit(t, dir, tc.head)
			line, code := RunGate(GateInput{StoreDir: dir, Base: base, Head: head})
			assert.Equal(t, tc.wantCode, code, "RunGate code = %d, want %d (line=%q)", code, tc.wantCode, line)
			for _, w := range tc.want {
				assert.Contains(t, line, w, "RunGate line = %q, want %q", line, w)
			}
			// A refusal merges nothing: HEAD stands where the proposal found it.
			if tc.wantCode != 0 {
				assert.Equal(t, head, strings.TrimSpace(gateGit(t, dir, "rev-parse", "HEAD")), "a refused proposal moved HEAD")
			}
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
		assert.Equal(t, 2, code, "the gate approved a hand-written plain entry (line=%q)", line)
		assert.Contains(t, line, "GATE REFUSE", "RunGate line = %q, want REFUSE", line)
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
		{"rule-breaking proposal", base, bad, "GATE REFUSE", 2},
		{"proposal naming nothing", base, "does-not-exist", "GATE REFUSE", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			line, code := RunGate(GateInput{StoreDir: dir, Base: tc.base, Head: tc.head})
			assert.Equal(t, tc.code, code, "no verdict for %s (line=%q)", tc.name, line)
			assert.Contains(t, line, tc.want, "no verdict line for %s: %q", tc.name, line)
		})
	}
}
