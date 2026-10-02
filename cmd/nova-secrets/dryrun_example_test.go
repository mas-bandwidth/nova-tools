package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The help banner's three --dry-run example lines, run as a reader types them -- `./secrets`
// under the directory they sit in, `~` their home, `/opt/homebrew/bin/sops` the sops they have --
// through the one comparator. The sops is a fake that answers --version and -d with
// fixture plaintext, so no real key, store or secret is read; the store is a throwaway directory with a .git
// answered by a fake git, and every example here writes nothing, which each test then checks.

const bech32Alphabet = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

// agePub is a key age's own alphabet makes valid, so the fixture needs no age-keygen.
func agePub(lead byte) string {
	return "age1" + string(lead) + (bech32Alphabet + bech32Alphabet[:26])[1:]
}

var (
	exWorker   = agePub('p')
	exLead     = agePub('q')
	exRecovery = agePub('z')
)

// exampleHome is the directory a reader sits in: ./secrets, ./fleet.tsv and ~/.config/nova-secrets/.
type exampleHome struct {
	bin, home, sops, path string
}

func newExampleHome(t *testing.T, plain string) exampleHome {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake sops runs through /bin/sh")
	}
	home := t.TempDir()
	h := exampleHome{bin: buildNovaSecrets(t), home: home, sops: filepath.Join(home, "fake-sops")}
	// The store stands on main with a clean tree: the two reads the dry run asks git. A fake
	// git first on PATH answers them, so the fixture makes no repository.
	fakeBin := filepath.Join(home, "fakebin")
	require.NoError(t, os.MkdirAll(fakeBin, 0o755))
	writeFakeExe(t, filepath.Join(fakeBin, "git"), "#!/bin/sh\nif [ \"$1\" = \"rev-parse\" ]; then echo main; exit 0; fi\nif [ \"$1\" = \"status\" ]; then exit 0; fi\nexit 1\n")
	h.path = fakeBin + string(os.PathListSeparator) + "/usr/bin:/bin"
	writeFakeExe(t, h.sops, "#!/bin/sh\ncase \"$1\" in\n--version) echo 'sops 3.13.3'; exit 0 ;;\n-d) printf '%s\\n' '"+plain+"'; exit 0 ;;\nesac\nexit 1\n")

	store := filepath.Join(home, "secrets")
	require.NoError(t, os.MkdirAll(store, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(store, ".git"), 0o755))
	for name, body := range map[string]string{
		"recovery.pub": exRecovery + "\n",
		".sops.yaml": "creation_rules:\n  - path_regex: ^worker\\.yaml$\n    age: " + exWorker + "," + exRecovery +
			"\n  - path_regex: ^lead\\.yaml$\n    age: " + exLead + "," + exRecovery + "\n",
		// What sops writes: a sealed value, then the recipients it sealed to.
		"worker.yaml": "API_KEY: ENC[AES256_GCM,data:x,iv:a,tag:b,type:str]\nsops:\n    age:\n        - recipient: " + exWorker + "\n        - recipient: " + exRecovery + "\n",
		"lead.yaml":   "API_KEY: ENC[AES256_GCM,data:y,iv:a,tag:b,type:str]\nsops:\n    age:\n        - recipient: " + exLead + "\n        - recipient: " + exRecovery + "\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(store, name), []byte(body), 0o644))
	}

	keyDir := filepath.Join(home, ".config", "nova-secrets")
	require.NoError(t, os.MkdirAll(keyDir, 0o700))
	for _, seat := range []string{"worker", "lead"} {
		require.NoError(t, os.WriteFile(filepath.Join(keyDir, seat+".key"), []byte("AGE-SECRET-KEY-FAKE\n# public key: x\n"), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(home, "fleet.tsv"), []byte("bench\tbench.example\t/home/bench\n"), 0o644))
	return h
}

// treeBytes is every file under dir, path to bytes, .git included: what writes nothing is compared by.
func treeBytes(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		out[p] = string(b)
		return err
	})
	require.NoError(t, err)
	return out
}

// runExample executes the transcript as a reader would, from home, and compares it.
func runExample(t *testing.T, h exampleHome, transcript []string, volatile []onboarding.Field) {
	t.Helper()
	out, errOut, code := runNovaSecrets(h.bin, "help")
	require.Equal(t, 0, code, "`nova-secrets help` exits %d: %s", code, errOut)
	examples, err := onboarding.ExampleLines(out, "nova-secrets")
	require.NoError(t, err)
	documented := strings.TrimPrefix(transcript[0], "$ ")
	found := false
	for _, ex := range examples {
		if ex == documented {
			found = true
		}
	}
	require.True(t, found, "the banner's example block does not hold this transcript's command:\n  %s\nbanner:\n  %s", documented, strings.Join(examples, "\n  "))
	steps, err := onboarding.Steps("nova-secrets", transcript)
	require.NoError(t, err)
	before := treeBytes(t, h.home)
	var got []onboarding.Result
	for _, step := range steps {
		args := make([]string, 0, len(step.Args))
		for _, a := range step.Args {
			switch {
			case strings.HasPrefix(a, "~/"):
				a = filepath.Join(h.home, strings.TrimPrefix(a, "~/"))
			case a == "/opt/homebrew/bin/sops":
				a = h.sops
			}
			args = append(args, a)
		}
		var stdout, stderr bytes.Buffer
		cmd := exec.Command(h.bin, args...)
		cmd.Dir = h.home
		cmd.Env = []string{"PATH=" + h.path, "HOME=" + h.home}
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		code := 0
		if err := cmd.Run(); err != nil {
			exitErr, ok := err.(*exec.ExitError)
			require.True(t, ok, "the documented command\n  %s\ncould not be run: %v", step.Line, err)
			code = exitErr.ExitCode()
		}
		got = append(got, onboarding.Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()})
	}
	for _, p := range onboarding.CompareTranscript(steps, got, volatile) {
		t.Error(p)
	}
	after := treeBytes(t, h.home)
	assert.Len(t, after, len(before), "the dry run changed the file set: %d files before, %d after", len(before), len(after))
	for p, v := range before {
		assert.Equal(t, v, after[p], "the dry run changed %s", p)
	}
}

func TestTheHelpExampleIsWhatSealDryRunPrints(t *testing.T) {
	t.Parallel()
	h := newExampleHome(t, "API_KEY: old-value")
	runExample(t, h, []string{
		"$ nova-secrets seal --store ./secrets --as worker --key ~/.config/nova-secrets/worker.key --sops /opt/homebrew/bin/sops --name API_KEY --dry-run",
		"! seal: reading worker.yaml",
		"SECRETS SEAL PLAN write=secrets/worker.yaml action=replace name=API_KEY seat=worker recipients=" + exWorker + "," + exRecovery + " value=not read (dry run)",
		`SECRETS SEAL PLAN git store=./secrets from=main branch=seal/worker-API_KEY-20260930-120000 commit="seal API_KEY into worker.yaml"`,
		`SECRETS SEAL PLAN push remote=origin branch=seal/worker-API_KEY-20260930-120000 pr title="seal API_KEY into worker.yaml" then waits up to 2m for the gate's approval, merges --squash, pulls and checks the seat`,
		"SECRETS SEAL DRY-RUN OK name=API_KEY seat=worker nothing written, no value read, no push, no gh call",
	}, []onboarding.Field{{Name: "branch"}})
}

func TestTheHelpExampleIsWhatSeatInjectDryRunPrints(t *testing.T) {
	t.Parallel()
	h := newExampleHome(t, "API_KEY: source-value")
	runExample(t, h, []string{
		"$ nova-secrets seat inject --store ./secrets --as worker --from lead --only API_KEY --key ~/.config/nova-secrets/lead.key --sops /opt/homebrew/bin/sops --dry-run",
		"! seat inject: reading lead.yaml",
		"SECRETS SEAT INJECT PLAN write=secrets/worker.yaml from=lead deliver=API_KEY recipients=" + exWorker + "," + exRecovery + " keep-clear=- every other held name is re-sealed from the source; values not shown",
		`SECRETS SEAT INJECT PLAN git store=./secrets from=main branch=seal/worker-API_KEY-20260930-120000 commit="inject API_KEY into worker.yaml from lead"`,
		`SECRETS SEAT INJECT PLAN push remote=origin branch=seal/worker-API_KEY-20260930-120000 pr title="inject API_KEY into worker.yaml from lead" then waits up to 2m for the gate's approval, merges --squash, pulls and checks the seat`,
		"SECRETS SEAT INJECT DRY-RUN OK seat=worker from=lead names=1 nothing written, no push, no gh call",
	}, []onboarding.Field{{Name: "branch"}})
}

func TestTheHelpExampleIsWhatPlaceDryRunPrints(t *testing.T) {
	t.Parallel()
	h := newExampleHome(t, "API_KEY: the-value")
	remote := "/home/bench/.config/nova-secrets/API_KEY.env"
	runExample(t, h, []string{
		"$ nova-secrets place --store ./secrets --as worker --key ~/.config/nova-secrets/worker.key --sops /opt/homebrew/bin/sops --machine bench --secret API_KEY --machines ./fleet.tsv --dry-run",
		"SECRETS PLACE PLAN machine=bench secret=API_KEY path=" + remote + " mode=0600 file=worker.yaml head=- blob=" + blobOf(t, filepath.Join(h.home, "secrets", "worker.yaml")),
		"SECRETS PLACE PLAN ssh=ssh target=bench.example writes=" + remote + " the value travels on stdin, never in an argument",
		"SECRETS PLACE PLAN receipt=/home/you/.config/nova-secrets/placed/bench.receipt action=add",
		"SECRETS PLACE DRY-RUN OK machine=bench secret=API_KEY nothing written, no ssh run",
	}, []onboarding.Field{{Name: "tmpdir", Doc: "/home/you", Run: h.home}})
}
