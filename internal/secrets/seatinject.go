package secrets

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE SECOND CIRCLE (the store's move to hetzner, 2026-09-27).
//
// `seat add` gives a NEW seat its first values out of a seat the coordinator can open.
// The night the store moved, every bench seat that already existed needed the store's
// new NOVA_REDIS_BENCH_PASSWORD, which the coordinator's seat held sealed; `seal` runs
// only where the target's own key lives, and `seat add` refuses a seat file that exists.
// So a value the coordinator holds could reach an existing seat by no verb at all, and
// Glenn's rule is that a hand fix is not a road.
//
// `seat inject` is `seat add`'s pipe pointed at an EXISTING seat's file: the named
// values come out of the source through sops, go straight into the encrypt of the
// target, and the ciphertext is carried to the store on the same road `seal` walks.
//
// WHAT THIS KEY CANNOT DO, said plainly: it cannot open the target. sops seals every
// value of a file under one data key, and only the file's recipients can recover it, so
// the verb cannot copy the target's other sealed values across unread. It re-seals them
// from the SOURCE instead -- the one plaintext this key reaches -- and refuses, naming
// the key, when the target holds a sealed name the source does not. A value permitted
// in the clear by the rule's unencrypted_regex is kept byte for byte from the target.

// SeatInjectOptions carries one `seat inject` request and the test seams around it.
type SeatInjectOptions struct {
	StoreDir string
	AsName   string // the existing seat receiving the values
	From     string // a seat this machine can open
	Only     string // comma-separated key names to deliver
	KeyPath  string // this machine's key: the source seat's identity
	SopsPath string
	GHPath   string
	GitPath  string

	NoPR bool

	// Progress receives one short line per step that can take time (nil = silent).
	// It never carries a value: step names and public facts only.
	Progress io.Writer

	Now   func() time.Time
	Check func(storeDir, asName, keyPath, sopsPath string) error

	// Exec replaces the os/exec child process, as it does for seal.
	Exec execCommand
}

func (o SeatInjectOptions) say(format string, a ...interface{}) {
	if o.Progress != nil {
		fmt.Fprintf(o.Progress, "seat inject: "+format+"\n", a...)
	}
}

func (o SeatInjectOptions) exec() execCommand {
	if o.Exec != nil {
		return o.Exec
	}
	return realExecCommand
}

// RunSeatInject re-seals the --only values out of the source seat into an existing
// seat's file, encrypted to that file's own recipients, and carries the change to the
// store as `seal` does. It returns the one OK line; no value appears on it, ever.
func RunSeatInject(opts SeatInjectOptions) (string, error) {
	names, err := seatInjectValidate(&opts)
	if err != nil {
		return "", err
	}

	sFi, err := os.Stat(opts.StoreDir)
	if err != nil || !sFi.IsDir() {
		return "", fmt.Errorf("store %s is not a directory", opts.StoreDir)
	}
	gFi, err := os.Stat(filepath.Join(opts.StoreDir, ".git"))
	if err != nil || !gFi.IsDir() {
		return "", fmt.Errorf("store %s has no .git directory; clone it: git clone <url> %s", opts.StoreDir, opts.StoreDir)
	}
	if _, err := os.Stat(filepath.Join(opts.StoreDir, ".sops.yaml")); err != nil {
		return "", fmt.Errorf("store %s carries no .sops.yaml", opts.StoreDir)
	}
	if err := CheckInvariant6(opts.KeyPath); err != nil {
		return "", err
	}
	if _, err := CheckSopsVersion(opts.SopsPath); err != nil {
		return "", err
	}
	recoveryKey, err := ReadRecoveryPub(opts.StoreDir)
	if err != nil {
		return "", fmt.Errorf("store %s: %w", opts.StoreDir, err)
	}

	seatFile := opts.AsName + ".yaml"
	targetFile := filepath.Join(opts.StoreDir, seatFile)
	if _, err := os.Stat(targetFile); err != nil {
		return "", fmt.Errorf("seat file %s is absent in %s; a seat with no file gets its first values from seat add, never inject; run: nova-secrets seat add --store %s --as %s --pub <the seat's age1… public key> --from %s --only %s --key %s --sops %s",
			seatFile, opts.StoreDir, opts.StoreDir, opts.AsName, opts.From, strings.Join(names, ","), opts.KeyPath, opts.SopsPath)
	}
	sourceFile := filepath.Join(opts.StoreDir, opts.From+".yaml")
	if _, err := os.Stat(sourceFile); err != nil {
		return "", fmt.Errorf("source seat file %s.yaml is absent in %s", opts.From, opts.StoreDir)
	}

	// The target's recipients, read from the file's own sops metadata: the two keys the
	// new ciphertext must open for. The rule in .sops.yaml is what sops encrypts to, so
	// the two are held equal before anything is encrypted -- a file whose metadata and
	// rule disagree is a pending updatekeys, and this verb never widens or narrows a grant.
	held, err := seatInjectTarget(opts.StoreDir, seatFile, recoveryKey)
	if err != nil {
		return "", err
	}

	// The read. A source this bench cannot open is refused naming the seat and the
	// key, before anything is written.
	run := opts.exec()
	opts.say("reading %s.yaml", opts.From)
	plaintext, err := sealDecrypt(run, opts.SopsPath, opts.KeyPath, sourceFile)
	if err != nil {
		return "", fmt.Errorf("source seat %s cannot be opened with %s on this machine: %s; run `seat inject` where a key for %s lives",
			opts.From, oneline.Field(opts.KeyPath), oneline.Err(err), opts.From)
	}
	document, err := seatInjectCompose(plaintext, held, names, opts.From, opts.AsName, opts.StoreDir)
	if err != nil {
		return "", err
	}

	opts.say("encrypting %d value(s) to %s's own recipients", len(names), seatFile)
	ciphertext, err := sealEncrypt(run, opts.SopsPath, opts.KeyPath, opts.StoreDir, seatFile, document)
	if err != nil {
		return "", err
	}

	joined := strings.Join(names, "+")
	branch := fmt.Sprintf("seal/%s-%s-%s", opts.AsName, joined, opts.Now().UTC().Format("20060102-150405"))
	commitMsg := fmt.Sprintf("inject %s into %s from %s", strings.Join(names, ","), seatFile, opts.From)
	prNum, merged, err := sealCarry{
		run:      run,
		storeDir: opts.StoreDir,
		gitPath:  opts.GitPath,
		ghPath:   opts.GHPath,
		seatFile: seatFile,
		branch:   branch,
		message:  commitMsg,
		title:    commitMsg,
		body: fmt.Sprintf("Injected with nova-secrets seat inject from %s: %d value(s) re-sealed to %s's own recipients, which are unchanged. No value was written to a file in the clear, to argv, or to output.",
			opts.From, len(names), seatFile),
		noPR: opts.NoPR,
		say:  opts.say,
		check: func() error {
			checkFn := opts.Check
			if checkFn == nil {
				checkFn = checkSeatDecrypts
			}
			return checkFn(opts.StoreDir, opts.AsName, opts.KeyPath, opts.SopsPath)
		},
	}.carry(ciphertext)
	if err != nil {
		return "", err
	}
	head := fmt.Sprintf("SECRETS SEAT INJECT OK seat=%s from=%s names=%d",
		oneline.Field(opts.AsName), oneline.Field(opts.From), len(names))
	switch {
	case opts.NoPR:
		return fmt.Sprintf("%s committed branch=%s", head, oneline.Field(branch)), nil
	case !merged:
		return fmt.Sprintf("%s pr=#%s open (gate not yet approved)", head, prNum), nil
	}
	return fmt.Sprintf("%s pr=#%s merged", head, prNum), nil
}

// seatInjectValidate checks the invocation and answers the --only names, sorted and
// de-duplicated so the branch name and the file do not depend on argument order.
func seatInjectValidate(opts *SeatInjectOptions) ([]string, error) {
	if opts.StoreDir == "" {
		return nil, fmt.Errorf("missing --store <dir>")
	}
	if opts.AsName == "" {
		return nil, fmt.Errorf("missing --as <seat>: the existing seat receiving the values")
	}
	if !IsValidAsName(opts.AsName) {
		return nil, fmt.Errorf("invalid seat name %q for --as: must match [A-Za-z0-9_-]+", opts.AsName)
	}
	if opts.From == "" {
		return nil, fmt.Errorf("missing --from <source-seat>: a seat this machine can open")
	}
	if !IsValidAsName(opts.From) {
		return nil, fmt.Errorf("invalid seat name %q for --from: must match [A-Za-z0-9_-]+", opts.From)
	}
	if opts.From == opts.AsName {
		return nil, fmt.Errorf("--from names %s, the seat receiving the values; a seat that can open its own file uses seal, and the source is a DIFFERENT seat this machine can open", opts.AsName)
	}
	if opts.KeyPath == "" {
		return nil, fmt.Errorf("missing --key <path>: this machine's key, the one that opens --from")
	}
	if opts.SopsPath == "" {
		return nil, fmt.Errorf("missing --sops <path>")
	}
	if strings.TrimSpace(opts.Only) == "" {
		return nil, fmt.Errorf("missing --only <NAME,…>: seat inject delivers the values it is told to deliver and no others")
	}
	if opts.GHPath == "" {
		opts.GHPath = "gh"
	}
	if opts.GitPath == "" {
		opts.GitPath = "git"
	}
	if opts.Now == nil {
		opts.Now = time.Now
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

// seatInjectHeld is what the target file says about itself without a key: the names
// it holds, in file order, and which of them stand in the clear (with the clear value,
// kept verbatim).
type seatInjectHeld struct {
	names []string
	clear map[string]string
}

// seatInjectTarget reads the target's recipients out of its sops metadata and holds
// them to the shape every seat file has -- exactly two, one the declared recovery key
// -- and to the rule sops will encrypt with. It then classifies every name the file
// holds: sealed, permitted in the clear by the rule, or a plain value the store's gate
// would refuse too.
func seatInjectTarget(storeDir, seatFile, recoveryKey string) (seatInjectHeld, error) {
	held := seatInjectHeld{clear: map[string]string{}}
	targetFile := filepath.Join(storeDir, seatFile)
	keys, recipients, hasSops, err := ParseStoreFileWithoutDecrypting(targetFile)
	if err != nil {
		return held, fmt.Errorf("seat file %s: %w", seatFile, err)
	}
	if !hasSops || len(recipients) == 0 {
		return held, fmt.Errorf("seat file %s carries no sops metadata naming its recipients, so there is nothing to encrypt to; a seat file is written only by sops (seal, seat add), never by hand", seatFile)
	}
	if len(recipients) != 2 {
		return held, fmt.Errorf("seat file %s names %d recipients in its sops metadata; expected exactly two, the seat's own key and the key recovery.pub declares (SPEC-SECRETS invariant 1)", seatFile, len(recipients))
	}
	if !containsString(recipients, recoveryKey) {
		return held, fmt.Errorf("seat file %s does not name the key recovery.pub declares among its recipients; the recovery key is always kept, so this file is re-sealed by sops updatekeys in a reviewed pull request first", seatFile)
	}
	cfg, err := ParseSopsConfig(storeDir)
	if err != nil {
		return held, fmt.Errorf("store %s: %w", storeDir, err)
	}
	rule, err := FindMatchingRule(cfg, seatFile)
	if err != nil {
		return held, fmt.Errorf(".sops.yaml carries no rule matching %s, so sops has no recipients to encrypt to; the rule is added in a pull request the store's gate reviews", seatFile)
	}
	if !sameKeySet(rule.Recipients, recipients) {
		return held, fmt.Errorf("the rule for %s names recipients its sops metadata does not; the rule is what sops encrypts to, so the file is brought to it first: sops updatekeys %s on a bench that opens it, in a pull request the gate reviews", seatFile, seatFile)
	}
	var unencRe *regexp.Regexp
	if rule.UnencryptedRegex != "" {
		if unencRe, err = regexp.Compile(rule.UnencryptedRegex); err != nil {
			return held, fmt.Errorf(".sops.yaml rule for %s carries unencrypted_regex %q, which is not a valid regular expression", seatFile, rule.UnencryptedRegex)
		}
	}
	for _, k := range keys {
		if !IsValidEnvVar(k.Name) {
			return held, fmt.Errorf("seat file %s holds %s, which is not a key name: must match [A-Z][A-Z0-9_]*", seatFile, oneline.Field(k.Name))
		}
		held.names = append(held.names, k.Name)
		if !k.Clear {
			continue
		}
		if unencRe == nil || !unencRe.MatchString(k.Name) {
			return held, fmt.Errorf("seat file %s holds key %s as a plain value, not encrypted, and the rule does not permit it in the clear; the store's gate refuses the file as it stands", seatFile, oneline.Field(k.Name))
		}
		held.clear[k.Name] = k.Value
	}
	return held, nil
}

// sameKeySet answers whether two recipient lists name the same keys, in any order.
func sameKeySet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, k := range a {
		if !containsString(b, k) {
			return false
		}
	}
	return true
}

// seatInjectCompose renders the target's new document: every name the target held, in
// its order, then every --only name it did not, sorted. A clear value is the target's
// own bytes; every sealed value is the source's current one, because the source is the
// only plaintext this key reaches. Values are single-quoted so a value that looks like
// YAML stays a string on the way through the pipe.
func seatInjectCompose(plaintext []byte, held seatInjectHeld, names []string, from, as, storeDir string) ([]byte, error) {
	values, _, err := ParseDecryptedSecrets(plaintext)
	if err != nil {
		return nil, fmt.Errorf("source seat %s: %w", from, err)
	}
	wanted := map[string]bool{}
	for _, n := range names {
		wanted[n] = true
	}
	var missingOnly, missingHeld []string
	var b strings.Builder
	write := func(n string) error {
		s, ok := values[n]
		if !ok || !s.Loaded() {
			if wanted[n] {
				missingOnly = append(missingOnly, n)
			} else {
				missingHeld = append(missingHeld, n)
			}
			return nil
		}
		if s.Empty() {
			return fmt.Errorf("source seat %s carries %s empty; an empty entry is not a credential", from, n)
		}
		return s.Use(func(v string) error {
			b.WriteString(n + ": " + yamlSingleQuote(v) + "\n")
			return nil
		})
	}
	seen := map[string]bool{}
	for _, n := range held.names {
		if seen[n] {
			continue
		}
		seen[n] = true
		if v, clear := held.clear[n]; clear && !wanted[n] {
			b.WriteString(n + ": " + v + "\n")
			continue
		}
		if err := write(n); err != nil {
			return nil, err
		}
	}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		if err := write(n); err != nil {
			return nil, err
		}
	}
	if len(missingOnly) > 0 {
		return nil, fmt.Errorf("source seat %s carries no %s; run: nova-secrets names --store %s --as %s",
			from, strings.Join(missingOnly, ", "), storeDir, from)
	}
	if len(missingHeld) > 0 {
		return nil, fmt.Errorf("seat %s holds %s sealed, which source seat %s does not carry; this key cannot open %s.yaml, so every sealed value it holds is re-sealed from the source, and the source must hold them all: seal %s into %s first (nova-secrets seal --store %s --as %s --name %s), or seal the target on its own bench",
			as, strings.Join(missingHeld, ", "), from, as, strings.Join(missingHeld, ", "), from, storeDir, from, missingHeld[0])
	}
	return []byte(b.String()), nil
}
