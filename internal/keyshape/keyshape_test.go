package keyshape

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every fixture is BUILT here, at test time, and is a valid credential for nothing: the
// shapes are public forms, so a literal in the tree would be a key-shaped string in the
// repository for no reason.
func fixture(prefix string, n int) string {
	return prefix + strings.Repeat("A", n)
}

func TestTheShapesLoad(t *testing.T) {
	list, err := Shapes()
	if err != nil {
		t.Fatalf("keyshapes.txt: %v", err)
	}
	if len(list) < 10 {
		t.Fatalf("only %d shapes loaded", len(list))
	}
	seen := map[string]bool{}
	for _, s := range list {
		if seen[s.Name] {
			t.Fatalf("two rows named %s", s.Name)
		}
		seen[s.Name] = true
	}
	for _, want := range []string{"forge-token", "age-secret-key", "openai-api-key", "xai-api-key", "google-api-key", "pem-private-key"} {
		if !seen[want] {
			t.Fatalf("the shape %s is not on the list", want)
		}
	}
}

func TestEachShapeCatchesItsOwnForm(t *testing.T) {
	cases := []struct{ shape, text string }{
		{"forge-token", fixture("ghp_", 30)},
		{"forge-fine-grained-token", fixture("github_pat_", 30)},
		{"age-secret-key", fixture("AGE-SECRET-KEY-1", 20)},
		{"openai-api-key", fixture("sk-", 32)},
		{"anthropic-api-key", fixture("sk-ant-", 30)},
		{"xai-api-key", fixture("xai-", 30)},
		{"google-api-key", fixture("AIza", 35)},
		{"pem-private-key", "-----BEGIN OPENSSH PRIVATE KEY-----"},
	}
	for _, c := range cases {
		found, err := ScanText("RESULT.md", "out: "+c.text+"\n", nil)
		if err != nil {
			t.Fatal(err)
		}
		hit := false
		for _, f := range found {
			if f.Shape == c.shape {
				hit = true
			}
		}
		if !hit {
			t.Fatalf("%s was not caught in %d findings", c.shape, len(found))
		}
	}
}

// TestTheScanNeverPrintsWhatItMatched is the rule the whole package exists under: a
// finding's text is a shape name, a path and a line, and the value is in none of them.
func TestTheScanNeverPrintsWhatItMatched(t *testing.T) {
	secret := fixture("ghp_", 30)
	found, err := ScanText("RESULT.md", "out: "+secret+"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("nothing was caught, so this test proves nothing")
	}
	for _, f := range found {
		if strings.Contains(f.String(), secret) {
			t.Fatalf("the finding quotes the key: %s", f.String())
		}
		if strings.Contains(f.String(), secret[:12]) {
			t.Fatalf("the finding quotes a prefix of the key: %s", f.String())
		}
	}
}

// TestASecretNamedVariablesValueIsCaughtByLengthAndName is the shape-less half: the key
// this fleet holds may not match any published prefix, and the check still finds it --
// reporting the VARIABLE's name and the value's LENGTH, never the value.
func TestASecretNamedVariablesValueIsCaughtByLengthAndName(t *testing.T) {
	value := "zzq" + strings.Repeat("7", 29) // no published prefix; 32 characters
	env := []string{"SEAT_PROVIDER_KEY=" + value, "PATH=/usr/bin", "HOME=/home/x"}
	found, err := ScanText("RESULT.md", "line one\nout: "+value+"\nline three\n", env)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("want one finding, got %d: %v", len(found), found)
	}
	f := found[0]
	if f.Shape != "env-value" || f.Name != "SEAT_PROVIDER_KEY" || f.Line != 2 || f.Len != len(value) {
		t.Fatalf("finding = %+v", f)
	}
	if strings.Contains(f.String(), value) {
		t.Fatalf("the finding quotes the value: %s", f.String())
	}
}

// TestPlainProseIsNoFinding: a shape that matched prose would be switched off within a
// week, so the ordinary text a card writes must pass.
func TestPlainProseIsNoFinding(t *testing.T) {
	prose := `RESULT: fix the thing ok
DONE
BRANCH rowan/fix-1814
REPO mas-bandwidth/nova-tools
red: TestThing -- the key is never printed, only its length
green: TestThing
files: internal/pulse/harvest.go
usd=0.0013 tokens_in=6176 sha=d5666ab9 eyJ
`
	env := []string{"SHORT_KEY=abc", "EMPTY_TOKEN="}
	found, err := ScanText("RESULT.md", prose, env)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("prose was called a secret: %v", found)
	}
}

func TestScanFileIsQuietAboutAMissingFileAndLoudAboutAnUnreadableOne(t *testing.T) {
	dir := t.TempDir()
	found, err := ScanFile(filepath.Join(dir, "nothing.md"), nil)
	if err != nil || len(found) != 0 {
		t.Fatalf("a missing file: %v %v", found, err)
	}
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ScanFile(filepath.Join(dir, "adir"), nil); err == nil {
		t.Fatal("a directory read as a clean file; a check that could not run must not read as a check that passed")
	}
}

func TestSecretNameIsTheArgvLogsPredicate(t *testing.T) {
	for _, name := range []string{"DEEPSEEK_API_KEY", "GH_TOKEN", "SOPS_AGE_SECRET", "lower_case_key", "MiXeD_ToKeN"} {
		if !SecretName(name) {
			t.Fatalf("%s is not recognised as a secret name", name)
		}
	}
	// The predicate is deliberately blunt -- it is a substring test, so MONKEY carries
	// KEY -- and blunt in the safe direction: it over-scrubs and never under-scrubs.
	for _, name := range []string{"PATH", "HOME", "LANG", "TERM", "NOVA_SWARM_JOB", "XDG_DATA_HOME"} {
		if SecretName(name) {
			t.Fatalf("%s was called a secret name", name)
		}
	}
	if !SecretName("MONKEY") {
		t.Fatal("the predicate stopped being a substring test; cmd/nova-swarm's argv log and shell shim assume it is one")
	}
}
