package secrets

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each invariant check is pinned here in both directions: a store that breaks the
// rule is refused with the reason that names it, and the store beside it that keeps
// the rule passes. Every row was checked by breaking the line it pins and watching
// the row go red.

// seatRule is the creation rule for <seat>.yaml with the recipients given.
func seatRule(seat string, recipients ...string) CreationRule {
	return CreationRule{PathRegex: "^" + seat + `\.yaml$`, Recipients: recipients}
}

// reasons is every failure's reason, in the order the check reported them.
func reasons(fails []CheckFailure) []string {
	var out []string
	for _, f := range fails {
		out = append(out, f.Reason)
	}
	return out
}

// assertReasons holds that fails are exactly one failure per entry of want, in
// order, each carrying that entry in its reason; no entries is a pass.
func assertReasons(t *testing.T, fails []CheckFailure, want ...string) {
	t.Helper()
	got := reasons(fails)
	if !assert.Len(t, got, len(want), "failures %q, want one per %q", got, want) {
		return
	}
	for i := range want {
		assert.Contains(t, got[i], want[i], "failure %d", i)
	}
}

func TestCheckInvariant1HoldsEveryRuleToOneSeatKeyAndTheRecoveryKey(t *testing.T) {
	t.Parallel()
	rec := pubRecovery
	rules := func(r ...CreationRule) *SopsConfig { return &SopsConfig{CreationRules: r} }
	lacks := func(seat string) string {
		return "rule for ^" + seat + `\.yaml$ does not contain declared recovery key ` + rec
	}
	cases := []struct {
		name string
		cfg  *SopsConfig
		want []string
	}{
		{"a seat key and the recovery key pass", rules(seatRule("rowan", pubRowan, rec)), nil},
		{"the recovery key first passes too: order is free", rules(seatRule("rowan", rec, pubRowan)), nil},
		{"two rules, each with the recovery key, pass", rules(seatRule("rowan", pubRowan, rec), seatRule("air", pubAir, rec)), nil},
		{"a rule lacking the recovery key is refused", rules(seatRule("rowan", pubRowan, pubStranger)), []string{lacks("rowan")}},
		{"of several rules, only the one lacking it is refused", rules(seatRule("rowan", pubRowan, rec), seatRule("air", pubAir, pubStranger), seatRule("bo", pubStranger, rec)), []string{lacks("air")}},
		{"the recovery key in another rule does not cover this one", rules(seatRule("rowan", pubRowan, rec), seatRule("air", pubAir, pubRowan)), []string{lacks("air")}},
		{"an empty recipients list is refused", rules(seatRule("rowan")), []string{"has 0 recipients; expected exactly 2"}},
		{"the recovery key alone is refused", rules(seatRule("rowan", rec)), []string{"has 1 recipients; expected exactly 2"}},
		{"a third recipient is refused", rules(seatRule("rowan", pubRowan, pubAir, rec)), []string{"has 3 recipients; expected exactly 2"}},
		{"the recovery key twice is a duplicate, not a seat", rules(seatRule("rowan", rec, rec)), []string{"has duplicate recipient " + rec}},
		{"a recipient that is not an age key is refused", rules(seatRule("rowan", "age1notakey", rec)), []string{`recipient "age1notakey" is not a valid age public key`}},
		{"a non-age recipient is refused", rules(CreationRule{PathRegex: `^rowan\.yaml$`, Recipients: []string{pubRowan, rec}, NonAgeRecipients: []string{"pgp"}}), []string{`carries non-age recipient key "pgp"`}},
		{"a path_regex open at the end is refused", rules(CreationRule{PathRegex: `^rowan\.yaml`, Recipients: []string{pubRowan, rec}}), []string{"is not anchored at both ends"}},
		{"a path_regex open at the start is refused", rules(CreationRule{PathRegex: `rowan\.yaml$`, Recipients: []string{pubRowan, rec}}), []string{"is not anchored at both ends"}},
		{"an anchored path_regex that does not compile is refused", rules(CreationRule{PathRegex: `^(rowan\.yaml$`, Recipients: []string{pubRowan, rec}}), []string{"is not a valid regular expression"}},
		{"no rules at all is refused", rules(), []string{"carries no creation_rules"}},
		{"no config at all is refused", nil, []string{"carries no creation_rules"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assertReasons(t, CheckInvariant1("store", c.cfg, rec), c.want...)
		})
	}
}

// A seat file sealed for recipients, as sops writes one: values ENC[...], then the
// sops block naming who opens it. clear names keys left in the clear.
func sealedFor(recipients []string, clear ...string) string {
	var b strings.Builder
	b.WriteString("GH_TOKEN: ENC[AES256_GCM,data:x,iv:a,tag:b,type:str]\n")
	for _, k := range clear {
		b.WriteString(k + ": in-the-clear\n")
	}
	b.WriteString("sops:\n    age:\n")
	for _, r := range recipients {
		b.WriteString("        - recipient: " + r + "\n")
	}
	return b.String()
}

// storeOf writes files into a fresh directory and returns it.
func storeOf(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	return dir
}

func TestCheckInvariant2HoldsEachFilesRecipientsToItsRule(t *testing.T) {
	t.Parallel()
	cfg := &SopsConfig{CreationRules: []CreationRule{seatRule("rowan", pubRowan, pubRecovery)}}
	drift := "recipients differ from .sops.yaml; run: sops updatekeys rowan.yaml"
	cases := []struct {
		name  string
		files map[string]string
		file  string
		want  []string
	}{
		{"the rule's recipients pass", map[string]string{"rowan.yaml": sealedFor([]string{pubRowan, pubRecovery})}, "rowan.yaml", nil},
		{"in another order they pass", map[string]string{"rowan.yaml": sealedFor([]string{pubRecovery, pubRowan})}, "rowan.yaml", nil},
		{"one recipient short is drift", map[string]string{"rowan.yaml": sealedFor([]string{pubRowan})}, "rowan.yaml", []string{drift}},
		{"one recipient more is drift", map[string]string{"rowan.yaml": sealedFor([]string{pubRowan, pubRecovery, pubStranger})}, "rowan.yaml", []string{drift}},
		{"as many, one different, is drift", map[string]string{"rowan.yaml": sealedFor([]string{pubRowan, pubStranger})}, "rowan.yaml", []string{drift}},
		{"a file no rule matches is drift", map[string]string{"air.yaml": sealedFor([]string{pubAir, pubRecovery})}, "air.yaml", []string{"recipients differ from .sops.yaml; run: sops updatekeys air.yaml"}},
		{"a file that cannot be read is named", map[string]string{}, "rowan.yaml", []string{"unable to parse file"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fails := CheckInvariant2(storeOf(t, c.files), cfg, []string{c.file})
			assertReasons(t, fails, c.want...)
			for _, f := range fails {
				assert.Equal(t, "recipients-drift", f.Kind)
				assert.Equal(t, c.file, f.File)
			}
		})
	}
}

func TestCheckInvariant3HoldsEveryValueSealedOutsideTheClearList(t *testing.T) {
	t.Parallel()
	rule := seatRule("rowan", pubRowan, pubRecovery)
	rule.UnencryptedRegex = "^PUBLIC_"
	cfg := &SopsConfig{CreationRules: []CreationRule{rule}}
	both := []string{pubRowan, pubRecovery}
	cases := []struct {
		name          string
		body          string
		want          []string
		sealed, clear int
	}{
		{"every value sealed passes", sealedFor(both), nil, 1, 0},
		{"a clear value the rule lets be clear passes and is counted", sealedFor(both, "PUBLIC_URL"), nil, 1, 1},
		{"a clear value outside unencrypted_regex is refused", sealedFor(both, "API_KEY"), []string{"unencrypted key API_KEY outside unencrypted_regex"}, 0, 0},
		{"a file with no sops block is refused", "GH_TOKEN: ghp_plain\n", []string{"file is not sealed (missing sops metadata)"}, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fails, sealed, clear := CheckInvariant3(storeOf(t, map[string]string{"rowan.yaml": c.body}), cfg, []string{"rowan.yaml"})
			assertReasons(t, fails, c.want...)
			assert.Equal(t, c.sealed, sealed, "sealed count")
			assert.Equal(t, c.clear, clear, "clear count")
		})
	}
}

func TestCheckInvariant4HoldsTheKeyToOpenExactlyTheFilesThatListIt(t *testing.T) {
	t.Parallel()
	f := newSeatFixture(t)
	store := storeOf(t, map[string]string{
		"rowan.yaml": sealedFor([]string{pubRowan, pubRecovery}),
		"air.yaml":   sealedFor([]string{pubAir, pubRecovery}),
	})
	files := []string{"air.yaml", "rowan.yaml"}
	cases := []struct {
		name string
		sops string
		want []string
	}{
		{"its own file opens and the other does not: pass", f.sopsPath, nil},
		{"its own file does not open: refused", sharedSopsOpensNone, []string{"file should open with this key and does not"}},
		{"a file that does not list it opens: refused", sharedSopsOpensAll, []string{"file opened with this key but recipients do not list it"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fails, mine, foreign := CheckInvariant4(store, c.sops, f.rowanKey, pubRowan, files)
			assertReasons(t, fails, c.want...)
			assert.Equal(t, 1, mine, "mine count")
			assert.Equal(t, 1, foreign, "foreign count")
		})
	}
}

func TestCheckInvariant5FindsAPrivateKeyStoredInTheStore(t *testing.T) {
	t.Parallel()
	store := storeOf(t, map[string]string{
		"notes/stray.txt": "# created: today\nAGE-SECRET-KEY-1QQQQ\n",
		"rowan.yaml":      sealedFor([]string{pubRowan, pubRecovery}),
		".git/objects/x":  "AGE-SECRET-KEY-1 inside git's own files is git's business\n",
	})
	fails := CheckInvariant5(store, filepath.Join(t.TempDir(), "rowan.key"))
	assertReasons(t, fails, "private key found in store")
	if assert.Len(t, fails, 1) {
		assert.Equal(t, filepath.Join("notes", "stray.txt"), fails[0].File)
	}
}

func TestCheckInvariant6HoldsTheKeyFileTo0600AndItsDirectoryTo0700(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes")
	}
	cases := []struct {
		name      string
		file, dir os.FileMode
		want      string
	}{
		{"0600 in 0700 passes", 0o600, 0o700, ""},
		{"a key readable by others is refused", 0o644, 0o700, "mode is 0644; expected 0600; run: chmod 600"},
		{"a key directory open to others is refused", 0o600, 0o755, "mode is 0755; expected 0700; run: chmod 700"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), "keys")
			require.NoError(t, os.Mkdir(dir, 0o700))
			key := filepath.Join(dir, "rowan.key")
			require.NoError(t, os.WriteFile(key, []byte("# public key: "+pubRowan+"\n"), 0o600))
			require.NoError(t, os.Chmod(key, c.file))
			require.NoError(t, os.Chmod(dir, c.dir))
			err := CheckInvariant6(key)
			if c.want == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}
}

func TestCheckInvariant7FindsPlaintextInAnUntrackedFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		body    string
		tracked bool
		want    []string
	}{
		{"an untracked file holding a plain value is refused", "API_KEY: sk-plain\n", false, []string{"untracked file contains unencrypted secret"}},
		{"an untracked file holding only sealed values passes", "API_KEY: ENC[AES256_GCM,data:x,iv:a,tag:b,type:str]\n", false, nil},
		{"a tracked file is the other invariants' to judge", "API_KEY: sk-plain\n", true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			store := storeOf(t, map[string]string{"drop.env": c.body})
			fails := CheckInvariant7(store, map[string]bool{"drop.env": c.tracked})
			assertReasons(t, fails, c.want...)
		})
	}
}
