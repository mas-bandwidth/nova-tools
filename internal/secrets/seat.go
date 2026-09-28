package secrets

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE CIRCLE THIS VERB BREAKS (the Air seat, 2026-09-18).
//
// `seal` folds one value into a seat file, and to do that it must first DECRYPT that
// file: sops rewrites the whole document, so the values already in it have to be read
// back. The only key that opens a seat's file is that seat's own. So a brand-new seat,
// whose bench has just run `keygen` and holds nothing else, cannot be given its first
// value by `seal` -- there is no file to open, and the moment there is one, only the
// new bench can open it, and the new bench is the one with nothing to seal from.
//
// The store's pull request #15 broke the circle by hand: a sops pipe out of a seat this
// machine COULD open, straight into the new seat's file. `seat add` is that pipe as a
// verb, with the refusals the hand pipe had to remember.
//
// WHAT IT DELIBERATELY DOES NOT DO: commit, push, or open a pull request. The recipient
// list is the grant, and a grant is reviewed. The verb leaves two changed files in the
// working copy and says so; the store's gate reads them.

// SeatAddOptions carries one `seat add` request and the test seam around it.
//
// KeyPath is THIS machine's key, the one that opens From. Pub is the NEW seat's public
// half, which arrives from the new bench's own `keygen` receipt -- never a private key,
// which never leaves the bench that made it.
type SeatAddOptions struct {
	StoreDir string
	AsName   string // the new seat
	Pub      string // the new seat's age public key
	From     string // a seat this machine can open
	Only     string // comma-separated key names to carry over
	KeyPath  string // this machine's key: the source seat's identity
	SopsPath string

	// Progress receives one short line per step that can take time (nil = silent).
	// It never carries a value: step names and public facts only.
	Progress io.Writer

	// Exec replaces the os/exec child process, as it does for seal.
	Exec execCommand
}

func (o SeatAddOptions) say(format string, a ...interface{}) {
	if o.Progress != nil {
		fmt.Fprintf(o.Progress, "seat add: "+format+"\n", a...)
	}
}

func (o SeatAddOptions) exec() execCommand {
	if o.Exec != nil {
		return o.Exec
	}
	return realExecCommand
}

// RunSeatAdd re-seals the named values out of a source seat into a new seat's file and
// writes that seat's rule. It returns the receipt as ordered lines, the verdict last.
// No value appears on any of them, ever.
func RunSeatAdd(opts SeatAddOptions) ([]string, error) {
	names, err := seatAddValidate(&opts)
	if err != nil {
		return nil, err
	}

	sFi, err := os.Stat(opts.StoreDir)
	if err != nil || !sFi.IsDir() {
		return nil, fmt.Errorf("store %s is not a directory", opts.StoreDir)
	}
	gFi, err := os.Stat(filepath.Join(opts.StoreDir, ".git"))
	if err != nil || !gFi.IsDir() {
		return nil, fmt.Errorf("store %s has no .git directory; clone it: git clone <url> %s", opts.StoreDir, opts.StoreDir)
	}
	configPath := filepath.Join(opts.StoreDir, ".sops.yaml")
	original, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("store %s carries no .sops.yaml", opts.StoreDir)
	}
	if err := CheckInvariant6(opts.KeyPath); err != nil {
		return nil, err
	}
	if _, err := CheckSopsVersion(opts.SopsPath); err != nil {
		return nil, err
	}
	recoveryKey, err := ReadRecoveryPub(opts.StoreDir)
	if err != nil {
		return nil, fmt.Errorf("store %s: %w", opts.StoreDir, err)
	}
	if recoveryKey == opts.Pub {
		return nil, fmt.Errorf("--pub is the store's recovery key; a seat's rule names its own key and the recovery key, not the recovery key twice")
	}

	seatFile := opts.AsName + ".yaml"
	targetFile := filepath.Join(opts.StoreDir, seatFile)
	if _, err := os.Stat(targetFile); err == nil {
		return nil, fmt.Errorf("seat file %s already exists in %s; refusing to overwrite (a rewrite of a seat file drops every value it holds)", seatFile, opts.StoreDir)
	}
	sourceFile := filepath.Join(opts.StoreDir, opts.From+".yaml")
	if _, err := os.Stat(sourceFile); err != nil {
		return nil, fmt.Errorf("source seat file %s.yaml is absent in %s", opts.From, opts.StoreDir)
	}
	if err := seatAddRuleIsFree(original, seatFile); err != nil {
		return nil, err
	}

	// The read. A source this bench cannot open is the whole reason the verb exists, so
	// its refusal names the seat and the key and stops before anything is written.
	run := opts.exec()
	opts.say("reading %s.yaml", opts.From)
	plaintext, err := sealDecrypt(run, opts.SopsPath, opts.KeyPath, sourceFile)
	if err != nil {
		return nil, fmt.Errorf("source seat %s cannot be opened with %s on this machine: %s; run `seat add` where a key for %s lives",
			opts.From, oneline.Field(opts.KeyPath), oneline.Err(err), opts.From)
	}
	carried, err := seatAddSelect(plaintext, names, opts.From)
	if err != nil {
		return nil, err
	}

	// The rule goes in BEFORE the encrypt: sops picks recipients out of .sops.yaml by
	// path_regex, so a file written before its rule is a file written to nobody. If
	// anything after this fails, the config goes back exactly as it was.
	opts.say("writing the rule for %s into .sops.yaml", seatFile)
	updated := seatAddAppendRule(original, opts.AsName, opts.Pub, recoveryKey)
	if err := atomicWriteFile(configPath, updated, 0644); err != nil {
		return nil, err
	}
	// A restore that fails silently would leave a grant in the store with no file under
	// it and nothing said about it, which is the one outcome worse than the failure that
	// caused it. It is named in the error the caller sees.
	restore := func(cause error) error {
		if err := atomicWriteFile(configPath, original, 0644); err != nil {
			return fmt.Errorf("%s, AND .sops.yaml could not be put back: %s -- the rule for %s is still in the working copy with no file under it; remove it by hand (git checkout -- .sops.yaml)",
				oneline.Err(cause), oneline.Err(err), seatFile)
		}
		return cause
	}

	opts.say("encrypting %d value(s) to the new seat's recipients", len(names))
	ciphertext, err := sealEncrypt(run, opts.SopsPath, opts.KeyPath, opts.StoreDir, seatFile, carried)
	if err != nil {
		return nil, restore(err)
	}
	if err := atomicWriteFile(targetFile, ciphertext, 0600); err != nil {
		return nil, restore(err)
	}

	ruleNum := seatAddRuleNumber(updated, seatFile)
	return []string{
		fmt.Sprintf("SECRETS SEAT ADD NEXT: commit .sops.yaml and %s on a branch and open the pull request the store's gate reviews", seatFile),
		fmt.Sprintf("SECRETS SEAT ADD OK as=%s from=%s keys=%d file=%s rule=%d",
			oneline.Field(opts.AsName), oneline.Field(opts.From), len(names), oneline.Field(seatFile), ruleNum),
	}, nil
}

// seatAddValidate checks the invocation and answers the --only names, sorted and
// de-duplicated so the file this verb writes does not depend on argument order.
func seatAddValidate(opts *SeatAddOptions) ([]string, error) {
	if opts.StoreDir == "" {
		return nil, fmt.Errorf("missing --store <dir>")
	}
	if opts.AsName == "" {
		return nil, fmt.Errorf("missing --as <seat>: the seat being added")
	}
	if !IsValidAsName(opts.AsName) {
		return nil, fmt.Errorf("invalid seat name %q for --as: must match [A-Za-z0-9_-]+", opts.AsName)
	}
	if opts.From == "" {
		return nil, fmt.Errorf("missing --from <source-seat>: a seat this machine can already open")
	}
	if !IsValidAsName(opts.From) {
		return nil, fmt.Errorf("invalid seat name %q for --from: must match [A-Za-z0-9_-]+", opts.From)
	}
	if opts.From == opts.AsName {
		return nil, fmt.Errorf("--from names %s, the seat being added; the source is a DIFFERENT seat, one this machine can already open", opts.AsName)
	}
	if opts.Pub == "" {
		return nil, fmt.Errorf("missing --pub <age1…>: the new seat's public key, from its own keygen receipt")
	}
	if !IsValidAgePublicKey(opts.Pub) {
		return nil, fmt.Errorf("--pub is not an age public key; expected age1… of 62 characters, got %d", len(opts.Pub))
	}
	if strings.HasPrefix(opts.Pub, "AGE-SECRET-KEY") {
		return nil, fmt.Errorf("--pub was handed a PRIVATE key; a seat's private half never leaves the bench that made it")
	}
	if opts.KeyPath == "" {
		return nil, fmt.Errorf("missing --key <path>: this machine's key, the one that opens --from")
	}
	if opts.SopsPath == "" {
		return nil, fmt.Errorf("missing --sops <path>")
	}
	if strings.TrimSpace(opts.Only) == "" {
		return nil, fmt.Errorf("missing --only <NAME,…>: seat add carries the values it is told to carry and no others")
	}

	seen := map[string]bool{}
	var names []string
	for _, raw := range strings.Split(opts.Only, ",") {
		n := strings.TrimSpace(raw)
		if n == "" {
			continue
		}
		if !IsValidEnvVar(n) {
			return nil, fmt.Errorf("--only names %q, which is not a key name: must match [A-Z][A-Z0-9_]*", n)
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("--only <NAME,…> named no keys")
	}
	sort.Strings(names)
	return names, nil
}

// seatAddSelect takes the named values out of the source plaintext and renders the new
// seat's document. Values are single-quoted so a value that looks like YAML stays a
// string on the way through the pipe.
func seatAddSelect(plaintext []byte, names []string, from string) ([]byte, error) {
	values, _, err := ParseDecryptedSecrets(plaintext)
	if err != nil {
		return nil, fmt.Errorf("source seat %s: %w", from, err)
	}
	var missing []string
	var b strings.Builder
	for _, n := range names {
		s, ok := values[n]
		if !ok || !s.Loaded() {
			missing = append(missing, n)
			continue
		}
		if s.Empty() {
			return nil, fmt.Errorf("source seat %s carries %s empty; an empty entry is not a credential", from, n)
		}
		err := s.Use(func(v string) error {
			b.WriteString(n + ": " + yamlSingleQuote(v) + "\n")
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("source seat %s carries no %s; run: nova-secrets names --store <dir> --as %s",
			from, strings.Join(missing, ", "), from)
	}
	return []byte(b.String()), nil
}

// yamlSingleQuote renders a value as a YAML single-quoted scalar.
func yamlSingleQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

// seatAddRuleIsFree refuses when .sops.yaml already governs the new seat's file. The
// rule is the grant; a verb that rewrites one is a recipient edit nobody reviewed.
func seatAddRuleIsFree(config []byte, seatFile string) error {
	if !seatAddHasCreationRules(config) {
		return fmt.Errorf(".sops.yaml carries no `creation_rules:` line to add a rule under")
	}
	cfg, err := parseSopsConfig(bytes.NewReader(config))
	if err != nil {
		// No rules at all yet: an empty `creation_rules:` is a store with room.
		return nil
	}
	if rule, err := FindMatchingRule(cfg, seatFile); err == nil && rule != nil {
		return fmt.Errorf(".sops.yaml already carries a rule matching %s (path_regex %s); seat add never edits a rule that exists -- change it in a pull request the store's gate reviews",
			seatFile, rule.PathRegex)
	}
	return nil
}

func seatAddHasCreationRules(config []byte) bool {
	for _, line := range strings.Split(string(config), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "creation_rules:") {
			return true
		}
	}
	return false
}

// seatAddAppendRule adds one rule, in the shape invariant 1 demands and keygen prints:
// anchored at both ends, this seat's key and the recovery key and nothing else.
func seatAddAppendRule(config []byte, asName, pub, recoveryKey string) []byte {
	out := string(config)
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	out += fmt.Sprintf("  - path_regex: ^%s\\.yaml$\n    age: %s,%s\n", regexp.QuoteMeta(asName), pub, recoveryKey)
	return []byte(out)
}

// seatAddRuleNumber answers the 1-based position of the rule governing seatFile, which
// is the number `nova-secrets gate` prints when it has something to say about it.
func seatAddRuleNumber(config []byte, seatFile string) int {
	cfg, err := parseSopsConfig(bytes.NewReader(config))
	if err != nil {
		return 0
	}
	for i := range cfg.CreationRules {
		r := &cfg.CreationRules[i]
		if r.PathRegex == "" {
			continue
		}
		re, err := regexp.Compile(r.PathRegex)
		if err != nil {
			continue
		}
		if re.MatchString(seatFile) {
			return i + 1
		}
	}
	return 0
}
