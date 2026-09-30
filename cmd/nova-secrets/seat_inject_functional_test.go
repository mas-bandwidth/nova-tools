//go:build functional

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The store's move to hetzner, 2026-09-27: every bench seat that already existed
// needed the store's new NOVA_REDIS_BENCH_PASSWORD, sealed into the coordinator's
// seat. `seal` runs only where the target's key lives; `seat add` refuses a seat
// file that exists. These tests hold `seat inject` to the hand pipe it replaces,
// against the real sops and the real age: the bench's file opens with the bench's
// key alone afterwards, holds the new value beside the names it had, and the
// change sits on a seal branch the gate approves.

// injectStore is a store with two seats: ada, the coordinator's, and bo, a
// bench's. Both hold GH_TOKEN; ada holds the new redis password and bo the old.
type injectStore struct {
	storeDir string
	recovery keyPair
	ada      keyPair
	bo       keyPair
}

func newInjectStore(t *testing.T, td, sopsPath string) injectStore {
	t.Helper()
	s := injectStore{storeDir: filepath.Join(td, "secrets")}
	if err := os.MkdirAll(s.storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	initGitStore(t, s.storeDir)
	s.recovery = genKey(t, td, "recovery")
	s.ada = genKey(t, td, "ada")
	s.bo = genKey(t, td, "bo")
	if err := os.WriteFile(filepath.Join(s.storeDir, "recovery.pub"), []byte(s.recovery.pubKey+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf("creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: %s,%s\n  - path_regex: ^bo\\.yaml$\n    age: %s,%s\n",
		s.ada.pubKey, s.recovery.pubKey, s.bo.pubKey, s.recovery.pubKey)
	if err := os.WriteFile(filepath.Join(s.storeDir, ".sops.yaml"), []byte(cfg), 0644); err != nil {
		t.Fatal(err)
	}
	sealFileWithSops(t, sopsPath, filepath.Join(s.storeDir, "ada.yaml"),
		[]string{s.ada.pubKey, s.recovery.pubKey},
		"GH_TOKEN: carried-token\nNOVA_REDIS_BENCH_PASSWORD: redis-new\nLEFT_BEHIND: stays-home\n")
	sealFileWithSops(t, sopsPath, filepath.Join(s.storeDir, "bo.yaml"),
		[]string{s.bo.pubKey, s.recovery.pubKey},
		"GH_TOKEN: carried-token\nNOVA_REDIS_BENCH_PASSWORD: redis-old\n")
	commitAndPush(t, s.storeDir)
	return s
}

// injectValues are every value the fixture holds; none may reach a stream.
var injectValues = []string{"carried-token", "redis-new", "redis-old", "stays-home"}

func TestSeatInjectReSealsAValueIntoAnExistingSeat(t *testing.T) {
	t.Parallel()

	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)
	td := t.TempDir()
	s := newInjectStore(t, td, sopsPath)

	out, errOut, code := runNovaSecrets(bin, "seat", "inject",
		"--store", s.storeDir, "--as", "bo", "--from", "ada", "--only", "NOVA_REDIS_BENCH_PASSWORD",
		"--key", s.ada.privPath, "--sops", sopsPath, "--no-pr")
	if code != 0 {
		t.Fatalf("seat inject exited %d: %s", code, errOut)
	}
	line := strings.TrimSpace(out)
	if !strings.HasPrefix(line, "SECRETS SEAT INJECT OK seat=bo from=ada names=1 committed branch=seal/bo-NOVA_REDIS_BENCH_PASSWORD-") {
		t.Errorf("unexpected OK line: %s", line)
	}
	for _, v := range injectValues {
		if strings.Contains(out, v) || strings.Contains(errOut, v) {
			t.Errorf("a value reached a stream:\nstdout:\n%s\nstderr:\n%s", out, errOut)
		}
	}
	branch := strings.TrimPrefix(line[strings.LastIndex(line, " ")+1:], "branch=")

	// The store is back where it was: on main, clean, so exec is not refused later.
	if got := strings.TrimSpace(runCmd(t, s.storeDir, "git", "rev-parse", "--abbrev-ref", "HEAD")); got != "main" {
		t.Errorf("the store is on %s, want main", got)
	}
	if st := strings.TrimSpace(runCmd(t, s.storeDir, "git", "status", "--porcelain")); st != "" {
		t.Errorf("the store is not clean after --no-pr:\n%s", st)
	}

	// The commit on the seal branch: the bench's key opens it, the new value is in,
	// the name it had is still there, and the coordinator's key opens nothing.
	sealed := runCmd(t, s.storeDir, "git", "show", branch+":bo.yaml")
	blob := filepath.Join(td, "bo-sealed.yaml")
	if err := os.WriteFile(blob, []byte(sealed), 0600); err != nil {
		t.Fatal(err)
	}
	plain := sopsDecrypt(t, sopsPath, s.bo.privPath, blob)
	if !strings.Contains(plain, "NOVA_REDIS_BENCH_PASSWORD: redis-new") || !strings.Contains(plain, "GH_TOKEN: carried-token") {
		t.Errorf("the bench's file does not hold the new value beside the name it had:\n%s", plain)
	}
	if strings.Contains(plain, "redis-old") || strings.Contains(plain, "LEFT_BEHIND") {
		t.Errorf("the old value survived, or a name nobody asked for came along:\n%s", plain)
	}
	for _, key := range []string{s.bo.pubKey, s.recovery.pubKey} {
		if !strings.Contains(sealed, "recipient: "+key) {
			t.Errorf("the re-sealed file does not name recipient %s", key)
		}
	}
	if strings.Contains(sealed, "recipient: "+s.ada.pubKey) {
		t.Error("the re-sealed file names the coordinator's key; the target's recipients were widened")
	}
	cmd := exec.Command(sopsPath, "-d", blob)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "SOPS_AGE_KEY_FILE=" + s.ada.privPath, "HOME=" + td}
	if _, err := cmd.Output(); err == nil {
		t.Error("the coordinator's key opens the bench's file after inject")
	}

	// And the store's own gate approves the branch as it stands.
	out, errOut, code = runNovaSecrets(bin, "gate", "--store", s.storeDir, "--base", "main", "--head", branch)
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(out), "GATE APPROVE files=1 ") {
		t.Errorf("the gate does not approve the inject branch (exit %d):\n%s%s", code, out, errOut)
	}
}

// TestSeatInjectRefusesASeatWithNoFile: the door for a seat that has no file is
// seat add, and the refusal spells it.
func TestSeatInjectRefusesASeatWithNoFile(t *testing.T) {
	t.Parallel()

	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)
	td := t.TempDir()
	s := newInjectStore(t, td, sopsPath)
	_, errOut, code := runNovaSecrets(bin, "seat", "inject",
		"--store", s.storeDir, "--as", "mini", "--from", "ada", "--only", "NOVA_REDIS_BENCH_PASSWORD",
		"--key", s.ada.privPath, "--sops", sopsPath, "--no-pr")
	if code != 2 {
		t.Fatalf("seat inject exited %d, want 2: %s", code, errOut)
	}
	if !strings.HasPrefix(errOut, "SECRETS SEAT INJECT FAIL ") || !strings.Contains(errOut, "mini.yaml") || !strings.Contains(errOut, "nova-secrets seat add") {
		t.Errorf("the refusal does not name the file and the seat add remedy: %s", errOut)
	}
	if st := strings.TrimSpace(runCmd(t, s.storeDir, "git", "status", "--porcelain")); st != "" {
		t.Errorf("a refused run changed the store:\n%s", st)
	}
}

// TestTheHelpExampleIsWhatSeatInjectPrints runs the banner's own example, as a
// reader would type it -- `./secrets` under the directory they sit in, `~` their
// home, `/opt/homebrew/bin/sops` the sops they have -- through the one comparator.
//
// The transcript is held here beside the test that runs it: this tool's section
// of the transcripts document is not yet converted to the one comparator (#1657),
// so this example's transcript claims nothing about that section. The `$` line is
// asserted to BE the banner's example; the branch's stamp is the one declared
// run-owned value.
func TestTheHelpExampleIsWhatSeatInjectPrints(t *testing.T) {
	t.Parallel()

	seatInjectTranscript := []string{
		"$ nova-secrets seat inject --store ./secrets --as bo --from ada --only NOVA_REDIS_BENCH_PASSWORD --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops --no-pr",
		"! seat inject: reading ada.yaml",
		"! seat inject: encrypting 1 value(s) to bo.yaml's own recipients",
		"! seat inject: returning the store to its branch",
		"SECRETS SEAT INJECT OK seat=bo from=ada names=1 committed branch=seal/bo-NOVA_REDIS_BENCH_PASSWORD-20260927-013000",
	}

	sopsPath := findSops(t)
	bin := buildNovaSecrets(t)
	td := t.TempDir()
	home := filepath.Join(td, "home")
	if err := os.MkdirAll(home, 0755); err != nil {
		t.Fatal(err)
	}
	s := newInjectStore(t, home, sopsPath) // the store is ~/secrets, the keys under ~/keys
	keyDir := filepath.Join(home, ".config", "nova-secrets")
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		t.Fatal(err)
	}
	adaKey, err := os.ReadFile(s.ada.privPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keyDir, "ada.key"), adaKey, 0600); err != nil {
		t.Fatal(err)
	}

	out, errOut, code := runNovaSecrets(bin, "help")
	if code != 0 {
		t.Fatalf("`nova-secrets help` exits %d, want 0; stderr: %s", code, errOut)
	}
	examples, err := onboarding.ExampleLines(out, "nova-secrets")
	if err != nil {
		t.Fatalf("%v\n\nwhat the banner printed:\n%s", err, out)
	}
	documented := strings.TrimPrefix(seatInjectTranscript[0], "$ ")
	found := false
	for _, ex := range examples {
		if ex == documented {
			found = true
		}
	}
	if !found {
		t.Fatalf("the banner's example block does not hold this transcript's command:\n  %s\nbanner:\n  %s", documented, strings.Join(examples, "\n  "))
	}

	steps, err := onboarding.Steps("nova-secrets", seatInjectTranscript)
	if err != nil {
		t.Fatal(err)
	}
	run := func(step onboarding.Step) (onboarding.Result, error) {
		args := make([]string, 0, len(step.Args))
		for _, a := range step.Args {
			switch {
			case strings.HasPrefix(a, "~/"):
				a = filepath.Join(home, strings.TrimPrefix(a, "~/"))
			case a == "/opt/homebrew/bin/sops":
				a = sopsPath // the sops this bench has, where the reader's is
			}
			args = append(args, a)
		}
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(bin, args...)
		cmd.Dir = home
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else if err != nil {
			return onboarding.Result{}, err
		}
		return onboarding.Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}, nil
	}
	got := make([]onboarding.Result, 0, len(steps))
	for _, step := range steps {
		res, err := run(step)
		if err != nil {
			t.Fatalf("the documented command\n  %s\ncould not be run: %v", step.Line, err)
		}
		got = append(got, res)
	}
	for _, p := range onboarding.CompareTranscript(steps, got, []onboarding.Field{{Name: "branch"}}) {
		t.Error(p)
	}
	for _, res := range got {
		for _, v := range injectValues {
			if strings.Contains(res.Stdout, v) || strings.Contains(res.Stderr, v) {
				t.Errorf("a value reached a stream:\nstdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			}
		}
	}
}
