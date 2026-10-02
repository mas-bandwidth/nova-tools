package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// The help banner's example lines that are not dry runs, run as a reader types them from
// the directory they sit in -- `./secrets` a store under it, `~` their home,
// `/opt/homebrew/bin/sops` and `/opt/homebrew/bin/age-keygen` the tools they have, $BO_PUB the
// key bo's own keygen printed -- through the comparator (docs/ONBOARDING.md point 6). The
// store is a real git repository with an upstream; sops, age-keygen, ssh and gh are fakes,
// so no real key, secret, machine or forge is touched. seat inject, which commits, is run
// against the real sops and age by TestTheHelpExampleIsWhatSeatInjectPrints.

// sittingHome is the directory a reader sits in, with one seat, ada, sealed in ./secrets.
type sittingHome struct {
	bin, home, sops, ageKeygen, path, bo string
}

func newSittingHome(t *testing.T) sittingHome {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fakes run through /bin/sh")
	}
	home := t.TempDir()
	h := sittingHome{bin: buildNovaSecrets(t), home: home, sops: filepath.Join(home, "fake-sops"), ageKeygen: writeFakeAgeKeygen(t), bo: agePub('q')}
	fakeBin := filepath.Join(home, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	// gh says it ran and whether the token reached it; ssh swallows the value it is handed.
	writeFakeExe(t, filepath.Join(fakeBin, "gh"), "#!/bin/sh\necho \"gh $* token=${GH_TOKEN:+set}\"\n")
	writeFakeExe(t, filepath.Join(fakeBin, "ssh"), "#!/bin/sh\ncat >/dev/null\nexit 0\n")
	h.path = fakeBin + string(os.PathListSeparator) + "/usr/bin:/bin"
	writeFakeExe(t, h.sops, "#!/bin/sh\ncase \"$1\" in\n--version) echo 'sops 3.13.3'; exit 0 ;;\n"+
		"-d) printf 'GH_TOKEN: gh-value\\nDEEPSEEK_API_KEY: ds-value\\nNOVA_REDIS_BENCH_PASSWORD: pw-value\\n'; exit 0 ;;\nesac\nexit 0\n")

	ada, recovery := agePub('p'), agePub('z')
	store := filepath.Join(home, "secrets")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitStore(t, store)
	for name, body := range map[string]string{
		"recovery.pub": recovery + "\n",
		".sops.yaml":   "creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: " + ada + "," + recovery + "\n",
		"ada.yaml": "GH_TOKEN: ENC[AES256_GCM,data:x,iv:a,tag:b,type:str]\nDEEPSEEK_API_KEY: ENC[AES256_GCM,data:y,iv:a,tag:b,type:str]\n" +
			"NOVA_REDIS_BENCH_PASSWORD: ENC[AES256_GCM,data:z,iv:a,tag:b,type:str]\nsops:\n    age:\n        - recipient: " + ada + "\n        - recipient: " + recovery + "\n",
	} {
		if err := os.WriteFile(filepath.Join(store, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commitAndPush(t, store)
	keyDir := filepath.Join(home, ".config", "nova-secrets")
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keyDir, "ada.key"), []byte("AGE-SECRET-KEY-FAKE\n# public key: "+ada+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "fleet.tsv"), []byte("bench-a\tbench-a.example\t/home/bench\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return h
}

// runSitting runs the steps in order, from home, as a reader would, and compares each.
func runSitting(t *testing.T, h sittingHome, banner []string, sitting []onboarding.Step) {
	t.Helper()
	norms := []onboarding.Norm{onboarding.Path("/home/you", h.home)}
	for _, n := range [][3]string{
		{"the commit the store stands on", `head=[0-9a-f]{7,40}`, "head=-"},
		{"the instant of the placing", `stamp=\S+`, "stamp=-"},
	} {
		norm, err := onboarding.Elide(n[0], n[1], n[2])
		if err != nil {
			t.Fatal(err)
		}
		norms = append(norms, norm)
	}
	for _, s := range sitting {
		line := strings.TrimPrefix(s.Line, "$ ")
		found := false
		for _, ex := range banner {
			found = found || ex == line
		}
		if !found {
			t.Fatalf("the banner's example block does not hold this sitting's command:\n  %s", line)
		}
		words, err := onboarding.SplitShell(line)
		if err != nil {
			t.Fatal(err)
		}
		var args []string
		for _, a := range words[1:] {
			switch {
			case strings.HasPrefix(a, "~/"):
				a = filepath.Join(h.home, strings.TrimPrefix(a, "~/"))
			case a == "/opt/homebrew/bin/sops":
				a = h.sops
			case a == "/opt/homebrew/bin/age-keygen":
				a = h.ageKeygen
			case a == "$BO_PUB":
				a = h.bo
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
			if !ok {
				t.Fatalf("the example\n  %s\ncould not be run: %v", line, err)
			}
			code = exitErr.ExitCode()
		}
		if code != 0 {
			t.Errorf("the example %s exits %d\nstdout: %s\nstderr: %s", line, code, stdout.String(), stderr.String())
		}
		for _, p := range onboarding.Compare(s, onboarding.Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}, norms) {
			t.Error(p)
		}
	}
}

func bannerExamples(t *testing.T) []string {
	t.Helper()
	out, errOut, code := runNovaSecrets(buildNovaSecrets(t), "help")
	if code != 0 {
		t.Fatalf("`nova-secrets help` exits %d: %s", code, errOut)
	}
	examples, err := onboarding.ExampleLines(out, "nova-secrets")
	if err != nil {
		t.Fatal(err)
	}
	return examples
}

// TestTheHelpExampleIsWhatKeygenPrints: keygen in a home with no key yet.
func TestTheHelpExampleIsWhatKeygenPrints(t *testing.T) {
	t.Parallel()
	h := newSittingHome(t)
	if err := os.Remove(filepath.Join(h.home, ".config", "nova-secrets", "ada.key")); err != nil {
		t.Fatal(err)
	}
	pub := "age1fake000000000000000000000000000000000000000000000000000000000"
	runSitting(t, h, bannerExamples(t), []onboarding.Step{
		{Line: "$ nova-secrets keygen --as ada --key ~/.config/nova-secrets/ada.key --age-keygen /opt/homebrew/bin/age-keygen", Want: []string{
			"SECRETS RULE   creation_rules:",
			`SECRETS RULE     - path_regex: ^ada\.yaml$`,
			"SECRETS RULE       age: " + pub + ",<recovery key>",
			"SECRETS RULE NOTE  placeholder: no --store, so <recovery key> is filled by `nova-secrets seat add`",
			"SECRETS RULE NEXT: add these two lines to .sops.yaml (or run `nova-secrets seat add`)",
			"SECRETS KEYGEN OK as=ada key=/home/you/.config/nova-secrets/ada.key mode=0600 pub=" + pub,
			"Done. Your new key is at /home/you/.config/nova-secrets/ada.key. Nothing failed.",
			"Next: send this public key to whoever seals your seat: " + pub,
		}},
	})
}

// TestTheHelpExamplesAreWhatTheSeatsVerbsPrint: names, check, exec, place, placed and seat
// add, in the banner's order, in one sitting.
func TestTheHelpExamplesAreWhatTheSeatsVerbsPrint(t *testing.T) {
	t.Parallel()
	h := newSittingHome(t)
	placed := "machine=bench-a secret=DEEPSEEK_API_KEY path=/home/bench/.config/nova-secrets/DEEPSEEK_API_KEY.env sha256=e54ba31e7c194b583223ccf5875280256f521f5d0810de4e745702d732ea7111 stamp=-"
	runSitting(t, h, bannerExamples(t), []onboarding.Step{
		{Line: "$ nova-secrets names --store ./secrets --as ada", Want: []string{
			"SECRETS NAME key=DEEPSEEK_API_KEY clear=false",
			"SECRETS NAME key=GH_TOKEN clear=false",
			"SECRETS NAME key=NOVA_REDIS_BENCH_PASSWORD clear=false",
			"SECRETS NAMES OK as=ada keys=3 shown=3 sealed=3 clear=0",
		}},
		{Line: "$ nova-secrets check --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops", Want: []string{
			"SECRETS CHECK OK as=ada recipients=2 files=1 sealed=1 mine=1 foreign=0 clear=0 head=-",
		}},
		{Line: "$ nova-secrets exec --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops --only GH_TOKEN --require GH_TOKEN -- gh api user", Want: []string{
			"gh api user token=set",
			"! SECRETS EXEC OK as=ada keys=1 only=1 required=1 file=secrets/ada.yaml head=- cmd=gh",
		}},
		{Line: "$ nova-secrets place --store ./secrets --as ada --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops --machine bench-a --secret DEEPSEEK_API_KEY --machines ./fleet.tsv", Want: []string{
			"SECRETS PLACE OK " + placed,
		}},
		{Line: "$ nova-secrets placed --machine bench-a", Want: []string{
			"SECRETS PLACED OK machine=bench-a count=1",
			"SECRETS PLACED ITEM " + placed,
		}},
		{Line: "$ nova-secrets seat add --store ./secrets --as bo --pub $BO_PUB --from ada --only GH_TOKEN,DEEPSEEK_API_KEY --key ~/.config/nova-secrets/ada.key --sops /opt/homebrew/bin/sops", Want: []string{
			"SECRETS SEAT ADD NEXT: commit .sops.yaml and bo.yaml on a branch and open the pull request the store's gate reviews",
			"SECRETS SEAT ADD OK as=bo from=ada keys=2 file=bo.yaml rule=2",
			"! seat add: reading ada.yaml",
			"! seat add: writing the rule for bo.yaml into .sops.yaml",
			"! seat add: encrypting 2 value(s) to the new seat's recipients",
		}},
	})
}
