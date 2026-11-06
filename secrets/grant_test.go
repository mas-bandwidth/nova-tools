package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fleet case: the harness key pubA is seat ada's, the control node's; bo is a machine whose key is pubB. ada
// grants into bo.grants.yaml, sealed to bo's pinned key and the pinned recovery key only.

type fleet struct {
	*harness
	pins string
}

func newFleet(t *testing.T) fleet {
	h := newHarness(t)
	h.seed("ada", map[string]string{"NOVA_BUS_PASSWORD": "bus-sealed-one", "ADA_ONLY": "ada-sealed-two"})
	require.Equal(t, 0, h.main("seat", "add", "bo", pubB, "--store", h.store), h.stderr.String())
	return fleet{h, filepath.Join(t.TempDir(), "pins")}
}

func (f fleet) grant(names string, extra ...string) []string {
	return append([]string{"grant", names, "--to", "bo", "--pins", f.pins, "--store", f.store, "--as", "ada", "--key", f.key, "--sops", "sops"}, extra...)
}

func (f fleet) first() []string {
	return f.grant("NOVA_BUS_PASSWORD", "--pub", pubB, "--recovery", pubRec)
}

func (f fleet) recipients(file string) []string {
	data, err := os.ReadFile(filepath.Join(f.store, file))
	require.NoError(f.t, err)
	rs, _ := recipientsOf(data)
	return rs
}

func (f fleet) exec(seat, pub, names string) int {
	f.keyPub = pub
	return f.main("exec", "--only", names, "--store", f.store, "--as", seat, "--key", f.key, "--sops", "sops", "--", "true")
}

func TestGrantIsWriteOnly(t *testing.T) {
	t.Parallel()
	f := newFleet(t)
	require.Equal(t, 0, f.main(f.first()...), f.stderr.String())
	assert.Equal(t, "SECRETS GRANT OK from=ada to=bo names=1 changed=1 committed; run: git -C "+f.store+" push\n", f.stdout.String())
	assert.Equal(t, []string{pubB, pubRec}, f.recipients("bo.grants.yaml"), "the granting key is no recipient")
	assert.Equal(t, map[string]string{"NOVA_BUS_PASSWORD": "bus-sealed-one"}, f.open("bo.grants"))
	enc := f.calls[slices.IndexFunc(f.calls, func(c call) bool { return c.name == "sops" && c.args[0] == "--config" })]
	assert.Equal(t, []string{"--config", os.DevNull, "-e", "--age", pubB + "," + pubRec, "--filename-override", "bo.grants.yaml"}, enc.args[:7], "sops is given the keys; .sops.yaml chooses none")
	for name, want := range map[string]string{"bo.pub": pubB + "\n", "recovery.pub": pubRec + "\n"} {
		b, err := os.ReadFile(filepath.Join(f.pins, name))
		require.NoError(t, err)
		assert.Equal(t, want, string(b))
	}
	ledger, err := os.ReadFile(filepath.Join(f.pins, "bo.ledger"))
	require.NoError(t, err)
	assert.Regexp(t, `^NOVA_BUS_PASSWORD [0-9a-f]{64}\n#sealed [0-9a-f]{64}\n$`, string(ledger), "one HMAC per name, then the digest of the file written")
	fi, err := os.Stat(filepath.Join(f.pins, "bo.ledger"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	f.noLeak("bus-sealed-one", "ada-sealed-two")
	assert.NotContains(t, string(ledger), "bus-sealed-one")

	// bo opens it with its own key; the control node's key cannot.
	require.Equal(t, 0, f.exec("bo", pubB, "NOVA_BUS_PASSWORD"), f.stderr.String())
	assert.Equal(t, "bus-sealed-one", envMap(f.env)["NOVA_BUS_PASSWORD"])
	assert.Equal(t, 125, f.exec("bo", pubA, "NOVA_BUS_PASSWORD"))
	// a grants file that changed since it was sealed (sops exit 51): the remedy is a fresh grant, not git checkout
	f.failDec, f.decExit = true, 51
	assert.Equal(t, 125, f.exec("bo", pubB, "NOVA_BUS_PASSWORD"))
	assert.Contains(t, f.stderr.String(), "(exit status 51): the file changed since it was sealed (MAC mismatch); a fresh grant from the control node writes it again: nova-secrets grant <names> --to bo --pins <dir> --store "+f.store)
	f.failDec, f.decExit = false, 0

	// Again: nothing changes, nothing is committed. A changed value is rewritten; a new name keeps the old one.
	f.keyPub = pubA
	n := len(f.gitCalls())
	require.Equal(t, 0, f.main(f.grant("NOVA_BUS_PASSWORD")...), f.stderr.String())
	assert.Equal(t, "SECRETS GRANT OK from=ada to=bo names=1 changed=0 unchanged\n", f.stdout.String())

	assert.Len(t, f.gitCalls(), n)
	// a pusher re-seals the file to the same two keys with another value: not the bytes this node wrote, so rewritten
	require.NoError(t, os.WriteFile(filepath.Join(f.store, "bo.grants.yaml"), sealed(map[string]string{"NOVA_BUS_PASSWORD": "forged-by-pusher"}, pubB, pubRec), 0o600))
	f.stdout.Reset()
	require.Equal(t, 0, f.main(f.grant("NOVA_BUS_PASSWORD")...), f.stderr.String())
	assert.Contains(t, f.stdout.String(), "changed=1 committed")
	assert.Equal(t, map[string]string{"NOVA_BUS_PASSWORD": "bus-sealed-one"}, f.open("bo.grants"))
	// a pusher deletes the file: the ledger's names are written again
	require.NoError(t, os.Remove(filepath.Join(f.store, "bo.grants.yaml")))
	f.stdout.Reset()
	require.Equal(t, 0, f.main(f.grant("NOVA_BUS_PASSWORD")...), f.stderr.String())
	assert.Contains(t, f.stdout.String(), "names=1 changed=1 committed")
	assert.Equal(t, map[string]string{"NOVA_BUS_PASSWORD": "bus-sealed-one"}, f.open("bo.grants"))
	f.seed("ada", map[string]string{"NOVA_BUS_PASSWORD": "bus-sealed-new", "ADA_ONLY": "ada-sealed-two", "TOKEN": "tok-sealed-three"})
	require.Equal(t, 0, f.main(f.grant("TOKEN")...), f.stderr.String())
	assert.Contains(t, f.stdout.String(), "names=2 changed=1 committed")
	assert.Equal(t, map[string]string{"NOVA_BUS_PASSWORD": "bus-sealed-new", "TOKEN": "tok-sealed-three"}, f.open("bo.grants"))
	f.noLeak("bus-sealed-new", "tok-sealed-three", "ada-sealed-two")
}

// A hostile .sops.yaml chooses nothing: grant names the keys itself.
func TestGrantIgnoresWhatTheRuleAdds(t *testing.T) {
	t.Parallel()
	for name, edit := range map[string]func(string) string{
		"an extra age key": func(c string) string { return strings.Replace(c, pubB+","+pubRec, pubB+","+pubRec+","+pubC, 1) },
		"key_groups": func(c string) string {
			return strings.Replace(c, "    age: "+pubB, "    key_groups:\n      - age: ["+pubC+"]\n    age: "+pubB, 1)
		},
		"a catch-all rule": func(c string) string { return c + "  - age: " + pubC + "\n" },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFleet(t)
			cfg, err := os.ReadFile(filepath.Join(f.store, ".sops.yaml"))
			require.NoError(t, err)
			f.sopsYAML(edit(string(cfg)))
			require.Equal(t, 0, f.main(f.first()...), f.stderr.String())
			assert.Equal(t, []string{pubB, pubRec}, f.recipients("bo.grants.yaml"))
		})
	}
}

func TestGrantRefusals(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		setup func(f fleet)
		args  func(f fleet) []string
		code  int
		want  string
	}{
		"no pin, none given": {nil, func(f fleet) []string { return f.grant("NOVA_BUS_PASSWORD", "--recovery", pubRec) },
			2, "SECRETS GRANT REFUSED reason=no-pin: no pin for bo in @pins; pin it once with --pub"},
		"no recovery pin": {nil, func(f fleet) []string { return f.grant("NOVA_BUS_PASSWORD", "--pub", pubB) },
			2, "SECRETS GRANT REFUSED reason=no-pin: no pin for recovery in @pins; pin it once with --recovery"},
		"a --pub that is not the pin": {func(f fleet) { f.pin("bo.pub", pubB) }, func(f fleet) []string { return f.grant("NOVA_BUS_PASSWORD", "--pub", pubC, "--recovery", pubRec) },
			2, "SECRETS GRANT REFUSED reason=key-changed: the rule for bo.yaml in .sops.yaml does not name --pub's key " + pubC + " first (it names \"" + pubB + "\")"},
		"the rule's key replaced": {func(f fleet) {
			cfg, _ := os.ReadFile(filepath.Join(f.store, ".sops.yaml"))
			f.sopsYAML(strings.Replace(string(cfg), "age: "+pubB, "age: "+pubC, 1))
		}, func(f fleet) []string { return f.first() },
			2, "SECRETS GRANT REFUSED reason=key-changed: the rule for bo.yaml in .sops.yaml does not name --pub's key " + pubB + " first (it names \"" + pubC + "\")"},
		"the rule's key replaced after the pin": {func(f fleet) {
			f.pin("bo.pub", pubB)
			cfg, _ := os.ReadFile(filepath.Join(f.store, ".sops.yaml"))
			f.sopsYAML(strings.Replace(string(cfg), "age: "+pubB, "age: "+pubC, 1))
		}, func(f fleet) []string { return f.grant("NOVA_BUS_PASSWORD", "--recovery", pubRec) },
			2, "SECRETS GRANT REFUSED reason=key-changed: the rule for bo.yaml in .sops.yaml does not name the pinned key " + pubB + " first"},
		"the store's recovery.pub replaced": {func(f fleet) {
			require.NoError(f.t, os.WriteFile(filepath.Join(f.store, "recovery.pub"), []byte(pubC+"\n"), 0o644))
		}, func(f fleet) []string { return f.first() },
			2, "SECRETS GRANT REFUSED reason=key-changed: @store/recovery.pub is not the recovery key given, " + pubRec},
		"an anchor in .sops.yaml": {func(f fleet) {
			f.sopsYAML("x: &k " + pubB + "\ncreation_rules:\n  - path_regex: ^bo\\.yaml$\n    age: *k\n")
		}, func(f fleet) []string { return f.first() },
			2, "SECRETS GRANT REFUSED reason=untrusted-rule: @store/.sops.yaml is missing or not a plain YAML map (anchors, aliases and merge keys are refused)"},
		"sops seals to another key": {func(f fleet) { f.extraKey = pubC }, func(f fleet) []string { return f.first() },
			2, "SECRETS GRANT REFUSED reason=wrong-recipients: sops sealed bo.grants.yaml to 3 age keys"},
		"a symlinked grants file": {func(f fleet) {
			f.seed("cy", map[string]string{"CY": "cy-sealed-four"})
			require.NoError(f.t, os.Symlink("cy.yaml", filepath.Join(f.store, "bo.grants.yaml")))
		}, func(f fleet) []string { return f.first() },
			2, "SECRETS GRANT REFUSED reason=not-regular: @store/bo.grants.yaml is not a regular file"},
		"a name the operator lacks": {nil, func(f fleet) []string { return f.grant("NOVA_BUS_PASSWORD,NOPE", "--pub", pubB, "--recovery", pubRec) },
			2, "SECRETS GRANT REFUSED reason=no-name: ada holds no NOPE to grant"},
		"a pin directory others can write": {func(f fleet) {
			require.NoError(f.t, os.MkdirAll(f.pins, 0o700))
			require.NoError(f.t, os.Chmod(f.pins, 0o777))
		}, func(f fleet) []string { return f.first() },
			2, "SECRETS GRANT REFUSED reason=no-pin: pin directory @pins must be a directory of yours that no one else can write"},
		"no --pins": {nil, func(f fleet) []string {
			return []string{"grant", "X", "--to", "bo", "--store", f.store, "--as", "ada", "--key", f.key}
		}, 2, "nova-secrets grant: missing --to"},
		"--to is --as": {nil, func(f fleet) []string {
			return []string{"grant", "X", "--to", "ada", "--pins", f.pins, "--store", f.store, "--as", "ada", "--key", f.key}
		}, 2, "nova-secrets grant: --to \"ada\": another seat's name"},
		"--to is --as in another case": {nil, func(f fleet) []string {
			return []string{"grant", "X", "--to", "Ada", "--pins", f.pins, "--store", f.store, "--as", "ada", "--key", f.key}
		}, 2, "nova-secrets grant: --to \"Ada\": another seat's name"},
		"--to recovery": {nil, func(f fleet) []string {
			return []string{"grant", "X", "--to", "recovery", "--pins", f.pins, "--store", f.store, "--as", "ada", "--key", f.key}
		}, 2, "nova-secrets grant: --to \"recovery\": another seat's name"},
		"pins inside the store": {nil, func(f fleet) []string {
			return []string{"grant", "X", "--to", "bo", "--pins", filepath.Join(f.store, "pins"), "--store", f.store, "--as", "ada", "--key", f.key, "--pub", pubB, "--recovery", pubRec}
		}, 2, "SECRETS GRANT REFUSED reason=no-pin: pin directory @store/pins is the store or inside it"},
		"pins are the store": {nil, func(f fleet) []string {
			return []string{"grant", "X", "--to", "bo", "--pins", f.store, "--store", f.store, "--as", "ada", "--key", f.key, "--pub", pubB, "--recovery", pubRec}
		}, 2, "SECRETS GRANT REFUSED reason=no-pin: pin directory @store is the store or inside it"},
		"pins inside a git work tree": {func(f fleet) { f.top = filepath.Dir(f.pins) }, func(f fleet) []string { return f.first() },
			2, "SECRETS GRANT REFUSED reason=no-pin: pin directory @pins is inside the git work tree"},
		"the seat pin is this node's own key": {nil, func(f fleet) []string { return f.grant("NOVA_BUS_PASSWORD", "--pub", pubA, "--recovery", pubRec) },
			2, "SECRETS GRANT REFUSED reason=no-pin: the pin for bo is this node's own key " + pubA},
		"the recovery pin given is not the store's": {nil, func(f fleet) []string { return f.grant("NOVA_BUS_PASSWORD", "--pub", pubB, "--recovery", pubC) },
			2, "SECRETS GRANT REFUSED reason=key-changed: @store/recovery.pub is not the recovery key given, " + pubC},
		"the granting key is the recovery key": {func(f fleet) { f.keyPub = pubRec }, func(f fleet) []string { return f.first() },
			2, "SECRETS GRANT REFUSED reason=recovery-key: the recovery key is for recovery only; use the seat's own key"},
		"--pub for a seat with no rule writes no pin": {nil, func(f fleet) []string {
			return []string{"grant", "NOVA_BUS_PASSWORD", "--to", "dd", "--pins", f.pins, "--store", f.store, "--as", "ada", "--key", f.key, "--pub", pubC, "--recovery", pubRec}
		}, 2, "SECRETS GRANT REFUSED reason=key-changed: the rule for dd.yaml in .sops.yaml does not name --pub's key " + pubC + " first (it names \"\")"},
		"a symlinked ledger": {func(f fleet) {
			f.pin("bo.pub", pubB)
			require.NoError(f.t, os.Symlink(filepath.Join(f.store, ".sops.yaml"), filepath.Join(f.pins, "bo.ledger")))
		}, func(f fleet) []string { return f.grant("NOVA_BUS_PASSWORD", "--recovery", pubRec) },
			2, "SECRETS GRANT REFUSED reason=not-regular: @pins/bo.ledger is not a regular file of yours (a symlink?)"},
		"a name added to the grants file in the store": {func(f fleet) {
			require.Equal(f.t, 0, f.main(f.first()...), f.stderr.String())
			f.seed("bo.grants", map[string]string{"NOVA_BUS_PASSWORD": "bus-sealed-one", "ADA_ONLY": "x"})
		}, func(f fleet) []string { return f.grant("NOVA_BUS_PASSWORD") },
			2, "SECRETS GRANT REFUSED reason=grants-changed: bo.grants.yaml names 2 value(s), not the 1 this node's ledger granted"},
		"--pub on another verb": {nil, func(f fleet) []string { return append(f.set("X"), "--pub", pubB) },
			2, "nova-secrets set: unknown flag --pub"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFleet(t)
			if c.setup != nil {
				c.setup(f)
			}
			f.calls = nil
			before, _ := os.ReadFile(filepath.Join(f.store, "bo.grants.yaml"))
			assert.Equal(t, c.code, f.main(c.args(f)...))
			if strings.Contains(name, "recovery key") || strings.Contains(name, "recovery pin given") {
				assert.NoFileExists(t, filepath.Join(f.pins, "recovery.pub"), "a refused recovery pin is never written")
			}
			assert.NoFileExists(t, filepath.Join(f.pins, "dd.pub"), "a refused --pub is never written")
			want := strings.NewReplacer("@store", f.store, "@pins", f.pins).Replace(c.want)
			if name == "no --pins" {
				want = "missing --pins <dir> (or NOVA_SECRETS_PINS)"
			}
			assert.Contains(t, f.stderr.String(), want)
			assert.Equal(t, 1, strings.Count(f.stderr.String(), "\n"), "one line")
			after, _ := os.ReadFile(filepath.Join(f.store, "bo.grants.yaml"))
			assert.Equal(t, before, after, "nothing is written")
			f.noLeak("bus-sealed-one", "ada-sealed-two", "cy-sealed-four")
		})
	}
}

func (f fleet) pin(name, key string) {
	require.NoError(f.t, os.MkdirAll(f.pins, 0o700))
	require.NoError(f.t, os.WriteFile(filepath.Join(f.pins, name), []byte(key+"\n"), 0o644))
}

// A seat reads its own file and its grants file; a name in both, or a seat file that is a symlink, is refused.
func TestSeatFilesAreReadAsThemselves(t *testing.T) {
	t.Parallel()
	f := newFleet(t)
	require.Equal(t, 0, f.main(f.first()...), f.stderr.String())
	f.keyPub, f.stdin = pubB, "bo-sealed-five\n"
	require.Equal(t, 0, f.main("set", "BO_OWN", "--store", f.store, "--as", "bo", "--key", f.key, "--pins", f.harness.pins, "--sops", "sops"), f.stderr.String())
	require.Equal(t, 0, f.exec("bo", pubB, "BO_OWN,NOVA_BUS_PASSWORD"), f.stderr.String())
	require.Equal(t, 0, f.main("names", "--store", f.store, "--as", "bo"))
	assert.Equal(t, "BO_OWN\nNOVA_BUS_PASSWORD\n", f.stdout.String())

	f.stdin = "bo-sealed-six\n"
	assert.Equal(t, 2, f.main("set", "NOVA_BUS_PASSWORD", "--store", f.store, "--as", "bo", "--key", f.key, "--pins", f.harness.pins, "--sops", "sops"))
	assert.Contains(t, f.stderr.String(), "SECRETS SET REFUSED reason=duplicate-name: NOVA_BUS_PASSWORD is granted to bo already")
	f.seed("bo", map[string]string{"BO_OWN": "bo-sealed-five", "NOVA_BUS_PASSWORD": "bo-sealed-six"})
	assert.Equal(t, 125, f.exec("bo", pubB, "BO_OWN"))
	assert.Contains(t, f.stderr.String(), "SECRETS EXEC REFUSED reason=duplicate-name: NOVA_BUS_PASSWORD is in both bo.yaml and bo.grants.yaml")
	assert.Equal(t, 2, f.main("names", "--store", f.store, "--as", "bo"))
	assert.Contains(t, f.stderr.String(), "reason=duplicate-name")

	require.NoError(t, os.Remove(filepath.Join(f.store, "bo.yaml")))
	f.seed("cy", map[string]string{"CY": "cy-sealed-four"})
	require.NoError(t, os.Symlink("cy.yaml", filepath.Join(f.store, "bo.yaml")))
	assert.Equal(t, 125, f.exec("bo", pubB, "NOVA_BUS_PASSWORD"))
	assert.Contains(t, f.stderr.String(), "SECRETS EXEC REFUSED reason=not-regular: "+f.store+"/bo.yaml is not a regular file")
	f.noLeak("bus-sealed-one", "bo-sealed-five", "bo-sealed-six", "cy-sealed-four")
}

// set writes only through the rule seat add writes, and only what sops sealed to exactly that rule's keys.
func TestSetChecksItsRuleAndWhatSopsSealed(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		setup func(h *harness)
		want  string
	}{
		"an extra key in the rule": {func(h *harness) {
			h.sopsYAML("creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: " + pubA + "," + pubRec + "," + pubC + "\n")
		}, "SECRETS SET REFUSED reason=untrusted-rule: the rule for ada.yaml is not the one seat add writes"},
		"pgp in the rule": {func(h *harness) {
			h.sopsYAML("creation_rules:\n  - path_regex: ^ada\\.yaml$\n    pgp: ABCDEF\n    age: " + pubA + "," + pubRec + "\n")
		}, "SECRETS SET REFUSED reason=untrusted-rule: the rule for ada.yaml is not the one seat add writes"},
		"a merge key": {func(h *harness) {
			h.sopsYAML("base: &b {age: " + pubC + "}\ncreation_rules:\n  - path_regex: ^ada\\.yaml$\n    <<: *b\n")
		}, "SECRETS SET REFUSED reason=untrusted-rule: " + "@store/.sops.yaml is missing or not a plain YAML map"},
		"sops seals to another key": {func(h *harness) { h.extraKey = pubC },
			"SECRETS SET REFUSED reason=wrong-recipients: sops sealed ada.yaml to 3 age keys"},
		"the rule's seat key replaced": {func(h *harness) {
			h.sopsYAML("creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: " + pubC + "," + pubRec + "\n")
		}, "SECRETS SET REFUSED reason=not-recipient: key " + pubA + " is not the key in .sops.yaml for ada"},
		"the rule's seat key replaced after the first set": {func(h *harness) {
			h.seed("ada", map[string]string{"OLD": "old-sealed-one"})
			h.sopsYAML("creation_rules:\n  - path_regex: ^ada\\.yaml$\n    age: " + pubC + "," + pubRec + "\n")
		}, "SECRETS SET REFUSED reason=key-changed: the rule for ada.yaml names " + pubC + ", not --key's " + pubA},
		"the store's recovery.pub replaced": {func(h *harness) {
			require.NoError(h.t, os.WriteFile(filepath.Join(h.store, "recovery.pub"), []byte(pubC+"\n"), 0o644))
		}, "SECRETS SET REFUSED reason=key-changed: @store/recovery.pub is not the pinned recovery key"},
		"the key is the recovery key": {func(h *harness) { h.keyPub = pubRec },
			"SECRETS SET REFUSED reason=recovery-key: the recovery key is for recovery only; use the seat's own key"},
		"a diagnosis of a file with no recipients": {func(h *harness) {
			require.NoError(h.t, os.WriteFile(filepath.Join(h.store, "ada.yaml"), sealed(map[string]string{"OLD": "old-sealed-one"}), 0o600))
			h.failDec = true
		}, "SECRETS SET REFUSED reason=not-recipient: key " + pubA + " is the key in .sops.yaml for ada but not among ada.yaml's 0 recipients"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			c.setup(h)
			h.stdin = "value-new-xyz\n"
			before, _ := os.ReadFile(filepath.Join(h.store, "ada.yaml"))
			assert.Equal(t, 2, h.main(h.set("NEW")...))
			assert.Contains(t, h.stderr.String(), strings.ReplaceAll(c.want, "@store", h.store))
			after, _ := os.ReadFile(filepath.Join(h.store, "ada.yaml"))
			assert.Equal(t, before, after, "nothing is written")
			h.noLeak("value-new-xyz")
		})
	}
}

func TestInitWritesTheRecoveryPin(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.notRepo = true
	store, pins := filepath.Join(t.TempDir(), "s"), filepath.Join(t.TempDir(), "pins")
	require.Equal(t, 0, h.main("init", "--store", store, "--recovery", pubRec, "--pins", pins), h.stderr.String())
	b, err := os.ReadFile(filepath.Join(pins, "recovery.pub"))
	require.NoError(t, err)
	assert.Equal(t, pubRec+"\n", string(b))
	assert.NoFileExists(t, filepath.Join(store, "pins"), "pins live outside the store")
}
