package secrets

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/fleet"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// GateInput is one call of the gate: the store, the two refs, and -- optionally -- the
// fleet's machines registry, the only thing that can vouch for a seat nobody has seen before.
type GateInput struct {
	StoreDir     string
	Base         string
	Head         string
	MachinesPath string // the fleet machines registry; "" leaves the recipient rule dormant
}

// RunGate is the seat-rule gate as a verb: the store's shell gate, called by the
// workflow. It diffs --base..--head with git (no GitHub) and either prints
// "GATE APPROVE files=<n> machines=<registry|->" at exit 0 or
// "GATE FAILED rule=<n> check=<k> file=<f>: <why>" at exit 1; a gate that could not run
// prints "SECRETS GATE REFUSED: <why>; run: nova-secrets gate -h" at exit 2, so a CI
// step reads a broken change apart from a gate that never ran (skeleton contract 1.2).
// Independent inputs and findings are reported together in the check order
// (SPEC-SECRETS "gate").
func RunGate(in GateInput) (string, int) {
	storeDir := in.StoreDir
	base, head, fleetSeats, findings, legacy := gateInputFindings(in)
	if len(findings) > 0 {
		if len(findings) == 1 {
			return legacy, 2
		}
		return gateCouldNotRunFindings(findings), 2
	}
	// The two refs are resolved to commits before any diff or tree read (SPEC-SECRETS "gate").
	changed, err := gitChangedFiles(storeDir, base, head)
	if err != nil {
		return gateCouldNotRun(err.Error()), 2
	}
	if len(changed) == 0 {
		return gateApprove(0, in.MachinesPath), 0
	}
	sort.Strings(changed)
	var gateFindings []gateFinding

	// Check 3: No other file changes except README.md.
	for _, f := range changed {
		if f == ".sops.yaml" || f == "README.md" || isSeatYAML(f) {
			continue
		}
		gateFindings = append(gateFindings, gateFinding{check: 3, file: f, why: "only .sops.yaml, README.md and seat .yaml files may change"})
	}

	// The declared recovery key, read from the head tree.
	recoveryData, err := gitShowFile(storeDir, head, "recovery.pub")
	recoveryValid := err == nil
	if err != nil {
		gateFindings = append(gateFindings, gateFinding{file: "recovery.pub", why: "unreadable at " + oneline.Field(head)})
	} else if !IsValidAgePublicKey(strings.TrimSpace(string(recoveryData))) {
		recoveryValid = false
		gateFindings = append(gateFindings, gateFinding{file: "recovery.pub", why: "does not declare a single valid age public key"})
	}
	recoveryKey := strings.TrimSpace(string(recoveryData))

	// The head .sops.yaml, the rule set the gate measures against.
	sopsData, err := gitShowFile(storeDir, head, ".sops.yaml")
	sopsValid := err == nil
	if err != nil {
		gateFindings = append(gateFindings, gateFinding{file: ".sops.yaml", why: "unreadable at " + oneline.Field(head)})
	}
	var cfg *SopsConfig
	if sopsValid {
		cfg, err = parseSopsConfig(bytes.NewReader(sopsData))
		if err != nil {
			sopsValid = false
			gateFindings = append(gateFindings, gateFinding{file: ".sops.yaml", why: err.Error()})
		}
	}

	headFiles, err := gitTreeFiles(storeDir, head)
	headFilesValid := err == nil
	if err != nil {
		gateFindings = append(gateFindings, gateFinding{why: "unable to list the head tree: " + oneline.Escape(err.Error())})
	}
	if !recoveryValid || !sopsValid || !headFilesValid {
		baseFiles, baseErr := gitTreeFiles(storeDir, base)
		if baseErr != nil {
			gateFindings = append(gateFindings, gateFinding{why: "unable to list the base tree: " + oneline.Escape(baseErr.Error())})
		} else if headFilesValid {
			for _, bf := range baseFiles {
				if isSeatYAML(bf) && !slices.Contains(headFiles, bf) {
					gateFindings = append(gateFindings, gateFinding{check: 5, file: bf, why: "the seat file is in the store at the base and gone at the head; a seat is never removed here"})
				}
			}
		}
		return gateFailedFindings(gateFindings), 1
	}

	// Check 1: Every changed .sops.yaml rule: exactly two age recipients, one the
	// declared recovery key, and a path_regex naming exactly one seat file.
	if slices.Contains(changed, ".sops.yaml") {
		// The recipients the store already had. A key here is not a grant this pull request
		// makes, so resealing a seat or editing its rule asks the registry nothing.
		baseKeys, err := gateRecipientsAt(storeDir, base)
		baseKeysKnown := err == nil
		if err != nil {
			gateFindings = append(gateFindings, gateFinding{file: ".sops.yaml", why: err.Error()})
			baseKeys = map[string]bool{}
		}
		// The rules the store already had, for the unencrypted_regex comparison below.
		baseCfg := gateConfigAt(storeDir, base)
		for i := range cfg.CreationRules {
			rule := cfg.CreationRules[i]
			ruleNum := i + 1
			recipientProblem := ruleRecipientsProblem(rule.Recipients, recoveryKey)
			if recipientProblem != "" {
				gateFindings = append(gateFindings, gateFinding{rule: ruleNum, check: 1, file: ".sops.yaml", why: "rule " + recipientProblem})
			}
			re, err := regexp.Compile(rule.PathRegex)
			if err != nil {
				gateFindings = append(gateFindings, gateFinding{rule: ruleNum, check: 1, file: ".sops.yaml", why: fmt.Sprintf("path_regex %q is not a valid regular expression", rule.PathRegex)})
				continue
			}
			named, seatFile := 0, ""
			for _, hf := range headFiles {
				if isSeatYAML(hf) && re.MatchString(hf) {
					named++
					seatFile = hf
				}
			}
			if named != 1 {
				gateFindings = append(gateFindings, gateFinding{rule: ruleNum, check: 1, file: ".sops.yaml", why: fmt.Sprintf("path_regex names %d seat files; expected exactly one", named)})
				continue
			}

			// The rule may not widen the keys it keeps in the clear. The one sanctioned
			// change is admitting the mark key on a rule that lacked it, which seal and
			// seat inject commit beside the file they write (SPEC-SECRETS "gate", the
			// mark); every other change fails closed, because a widened or absent
			// unencrypted_regex is how a cleartext secret rides out under a rule the
			// gate otherwise approves.
			if baseCfg != nil {
				if bi := matchingRuleIndex(baseCfg, seatFile); bi >= 0 {
					baseRegex, headRegex := baseCfg.CreationRules[bi].UnencryptedRegex, rule.UnencryptedRegex
					if ruleRegexWidened(baseRegex, headRegex) {
						gateFindings = append(gateFindings, gateFinding{rule: ruleNum, check: 1, file: ".sops.yaml", why: fmt.Sprintf(
							"unencrypted_regex for %s changes from %q to %q; a rule may not widen the keys it keeps in the clear", seatFile, baseRegex, headRegex)})
					}
				}
			}

			// Check 4: A recipient key this pull request introduces is a GRANT, and a seat can
			// be brought up with no second human on the bench. The fleet's machines
			// registry is the only mechanical check that stands in for a human review:
			// the new key is permitted only when a machine in the registry carries this
			// file's seat, so the question "whose key is this, and does that machine
			// exist?" has a mechanical answer. With no --machines the rule is dormant
			// and the APPROVE line says so.
			if fleetSeats != nil && baseKeysKnown && recipientProblem == "" {
				seat := strings.TrimSuffix(seatFile, ".yaml")
				for _, key := range rule.Recipients {
					if key == recoveryKey || baseKeys[key] {
						continue
					}
					if !fleetSeats[seat] {
						gateFindings = append(gateFindings, gateFinding{rule: ruleNum, check: 4, file: seatFile, why: fmt.Sprintf(
							"rule adds a recipient no seat file rule named before, and no machine in %s carries the seat %s; add the machine's row (its seat column must read %s) or drop the rule",
							oneline.Field(in.MachinesPath), oneline.Field(seat), oneline.Field(seat))})
						break
					}
				}
			}
		}
	}

	// Check 5: Keep what exists. A seat file in the store at the base must still be in the store at
	// the head: removing one is how a seat would lose its credentials in a pull request whose
	// subject says it is adding one, and it is never part of adding a seat.
	baseFiles, err := gitTreeFiles(storeDir, base)
	if err != nil {
		gateFindings = append(gateFindings, gateFinding{why: "unable to list the base tree: " + oneline.Escape(err.Error())})
	} else {
		for _, bf := range baseFiles {
			if isSeatYAML(bf) && !slices.Contains(headFiles, bf) {
				gateFindings = append(gateFindings, gateFinding{check: 5, file: bf, why: "the seat file is in the store at the base and gone at the head; a seat is never removed here"})
			}
		}
	}

	// Check 2: Every changed seat file is encrypted and its rule exists.
	for _, f := range changed {
		if !isSeatYAML(f) {
			continue
		}
		if !slices.Contains(headFiles, f) {
			continue
		}
		ruleIdx := matchingRuleIndex(cfg, f)
		if ruleIdx < 0 {
			gateFindings = append(gateFindings, gateFinding{check: 2, file: f, why: "no creation rule in .sops.yaml matches this file"})
			continue
		}
		ruleNum := ruleIdx + 1
		data, err := gitShowFile(storeDir, head, f)
		if err != nil {
			gateFindings = append(gateFindings, gateFinding{rule: ruleNum, check: 2, file: f, why: "unreadable at " + oneline.Field(head)})
			continue
		}
		hasSops := bytes.Contains(data, []byte("sops:"))
		if !hasSops {
			gateFindings = append(gateFindings, gateFinding{rule: ruleNum, check: 2, file: f, why: "file is not encrypted (missing sops metadata)"})
		}
		_, recipients, _, err := parseStoreFile(bytes.NewReader(data))
		if err != nil {
			gateFindings = append(gateFindings, gateFinding{rule: ruleNum, check: 2, file: f, why: "unreadable sops metadata: " + oneline.Escape(err.Error())})
		} else if hasSops {
			if problem := seatFileRecipientsProblem(recipients, cfg.CreationRules[ruleIdx].Recipients, recoveryKey); problem != "" {
				gateFindings = append(gateFindings, gateFinding{rule: ruleNum, check: 2, file: f, why: problem})
			}
		}
		keys, err := plainValues(data, cfg.CreationRules[ruleIdx].UnencryptedRegex)
		if err != nil {
			unencryptedRegex := cfg.CreationRules[ruleIdx].UnencryptedRegex
			if _, regexErr := regexp.Compile(unencryptedRegex); regexErr != nil {
				gateFindings = append(gateFindings, gateFinding{rule: ruleNum, check: 2, file: f, why: fmt.Sprintf("unencrypted_regex %q is not a valid regular expression", unencryptedRegex)})
			} else {
				gateFindings = append(gateFindings, gateFinding{rule: ruleNum, check: 2, file: f, why: "unreadable seat file: " + oneline.Escape(err.Error())})
			}
		} else {
			for _, key := range keys {
				gateFindings = append(gateFindings, gateFinding{rule: ruleNum, check: 2, file: f, why: fmt.Sprintf("key %s is a plain value, not encrypted", oneline.Field(key))})
			}
		}
		// Check 2b: A seat file is written by a verb, never by hand: the mark every verb leaves
		// in the clear is what tells a verb-made file from a hand seal, whose bytes are
		// otherwise the same (SPEC-SECRETS "gate"; tla/SecretsSeat.tla on
		// sprint/md-secrets-h.w1.g1.e15, the MCSecretsSeatReachHandSeal config).
		if hasSops && !gateHasMark(data) {
			gateFindings = append(gateFindings, gateFinding{rule: ruleNum, check: 2, file: f, why: "the seat file was not written by a nova-secrets verb; seal it with nova-secrets seal or seat add, never by hand"})
		}
	}
	if len(gateFindings) > 0 {
		return gateFailedFindings(gateFindings), 1
	}

	return gateApprove(len(changed), in.MachinesPath), 0
}

// gateHasMark reports whether the file carries a root key SeatMarkKey in the clear whose
// value is a verb's mark, `<seal|seat add|seat inject> <version>` (isSeatMark). The mark is
// not a signature: a hand can copy it, and the spec says so; it catches the seal made by
// accident, not the forger.
func gateHasMark(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		if val, ok := strings.CutPrefix(line, SeatMarkKey+":"); ok && isSeatMark(val) {
			return true
		}
	}
	return false
}

// gateApprove formats the one approval line. It carries the registry it read, or `-`, so an
// APPROVE is never mistaken for the fleet having vouched for a seat when no fleet was asked.
func gateApprove(files int, machinesPath string) string {
	registry := "-"
	if machinesPath != "" {
		registry = oneline.Field(machinesPath)
	}
	return fmt.Sprintf("GATE APPROVE files=%d machines=%s", files, registry)
}

// gateFleetSeats reads the machines registry whole and returns the set of seats the fleet
// carries. A path of "" returns a nil set: the recipient rule is dormant, not satisfied.
func gateFleetSeats(machinesPath string) (map[string]bool, error) {
	if machinesPath == "" {
		return nil, nil
	}
	reg, err := fleet.ReadRegistry(machinesPath)
	if err != nil {
		return nil, fmt.Errorf("the machines registry does not read: %s", err.Error())
	}
	seats := map[string]bool{}
	for _, m := range reg.Machines() {
		// A machine with no seat writes `-`, which the registry reads as "". That is an
		// answer, not a seat name, and it vouches for nothing.
		if m.Seat != "" {
			seats[m.Seat] = true
		}
	}
	return seats, nil
}

// gateRecipientsAt returns every age recipient any creation rule names at ref. A store with
// no .sops.yaml there -- the first seat of all -- has none, which is not an error.
func gateRecipientsAt(storeDir, ref string) (map[string]bool, error) {
	keys := map[string]bool{}
	data, err := gitShowFile(storeDir, ref, ".sops.yaml")
	if err != nil {
		return keys, nil
	}
	cfg, err := parseSopsConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("unreadable at %s: %s", oneline.Field(ref), oneline.Escape(err.Error()))
	}
	for i := range cfg.CreationRules {
		for _, k := range cfg.CreationRules[i].Recipients {
			keys[k] = true
		}
	}
	return keys, nil
}

// gateConfigAt parses the .sops.yaml at ref, or returns nil when there is none or it does
// not parse: a store with no rule set there is the first seat of all, and an unparseable
// one is already refused as a rule finding on its own.
func gateConfigAt(storeDir, ref string) *SopsConfig {
	data, err := gitShowFile(storeDir, ref, ".sops.yaml")
	if err != nil {
		return nil
	}
	cfg, err := parseSopsConfig(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return cfg
}

// ruleRegexWidened reports whether a rule's unencrypted_regex differs from the base rule's.
// A change in unencrypted_regex fails, as it changes the keys in the clear.
// The one sanctioned change is admitting the mark key on a rule that lacked it,
// which seal and seat inject commit beside the file they write (SPEC-SECRETS "gate", the mark).
func ruleRegexWidened(base, head string) bool {
	if base == head {
		return false
	}
	if base == "" && head == seatMarkRegex {
		return false
	}
	return true
}

// gateFailed formats one verdict line: GATE FAILED rule=<n> check=<k> file=<f>: <why>
// (SPEC-SECRETS "gate"; tla/SecretsSeat.tla on sprint/md-secrets-h.w1.g1.e15). The gate
// ran and judged the diff, so the line leads with the FAILED status word and the verb
// exits 1 (skeleton contract 1.2, STANDARD §2).
// The check is the gate check that failed (1-5, defined in SPEC-SECRETS.md gate section;
// check=0 means the gate refused before any numbered check ran).
func gateFailed(ruleNum, checkNum int, file, why string) string {
	return fmt.Sprintf("GATE FAILED rule=%d check=%d file=%s: %s", ruleNum, checkNum, oneline.Field(file), oneline.Escape(why))
}

// gateCouldNotRun formats one setup line: SECRETS GATE REFUSED: <why>; run: nova-secrets
// gate -h. The gate did not run -- a missing flag, a ref that names no commit, an
// unreadable registry or a git failure -- and exits 2 (skeleton contract 1.2).
func gateCouldNotRun(why string) string {
	return "SECRETS GATE REFUSED: " + oneline.WithRemedy(why, "nova-secrets gate -h")
}

type gateFinding struct {
	rule, check int
	file, why   string
}

// gateInputFindings validates independent gate inputs once and returns the resolved refs
// and parsed registry used by the checks. Ref and registry reads are single snapshots
// (SPEC-SECRETS "gate"). Every input problem is a gate that could not run: exit 2.
func gateInputFindings(in GateInput) (base, head string, fleetSeats map[string]bool, findings []gateFinding, legacy string) {
	storeDir, base, head := in.StoreDir, in.Base, in.Head
	missing := preflight("", need{storeDir, "--store <dir>", false}, need{base, "--base <git ref>", false}, need{head, "--head <git ref>", false})
	if missing != nil {
		findings = append(findings, gateFinding{why: missing.Error()})
		legacy = gateCouldNotRun(missing.Error())
	}
	optionShapedRef := false
	for _, r := range []struct{ flag, ref string }{{"--base", base}, {"--head", head}} {
		if r.ref == "" || !strings.HasPrefix(r.ref, "-") {
			continue
		}
		optionShapedRef = true
		message := fmt.Sprintf("%s %s begins with \"-\", the shape of an option, not a git ref; pass a branch, tag or commit; run: nova-secrets gate -h", r.flag, oneline.Field(r.ref))
		findings = append(findings, gateFinding{why: message})
		legacy = "SECRETS GATE REFUSED: " + message
	}
	resolve := func(flagName, ref string) string {
		if ref == "" || optionShapedRef {
			return ""
		}
		if storeDir == "" {
			return ""
		}
		resolved, err := gateResolveCommit(storeDir, flagName, ref)
		if err != nil {
			findings = append(findings, gateFinding{why: err.Error()})
			legacy = gateCouldNotRun(err.Error())
			return ""
		}
		return resolved
	}
	base = resolve("--base", base)
	head = resolve("--head", head)
	if in.MachinesPath != "" {
		var err error
		fleetSeats, err = gateFleetSeats(in.MachinesPath)
		if err != nil {
			findings = append(findings, gateFinding{file: in.MachinesPath, why: err.Error()})
			legacy = gateCouldNotRun(err.Error())
		}
	}
	return base, head, fleetSeats, findings, legacy
}

// gateFailedFindings keeps the established line for one finding and lists every additional
// independent finding in order for one combined verdict (SPEC-SECRETS "gate").
func gateFailedFindings(findings []gateFinding) string {
	if len(findings) == 1 {
		f := findings[0]
		return gateFailed(f.rule, f.check, f.file, f.why)
	}
	first := findings[0]
	items := make([]string, 0, len(findings)-1)
	for _, f := range findings[1:] {
		items = append(items, fmt.Sprintf("rule=%d check=%d file=%s: %s", f.rule, f.check, oneline.Field(f.file), f.why))
	}
	why := fmt.Sprintf("%s; additional findings=%d: %s", first.why, len(items), strings.Join(items, "; "))
	return gateFailed(first.rule, first.check, first.file, why)
}

// gateCouldNotRunFindings joins every independent input problem on one SECRETS GATE
// REFUSED line with the gate's help as the remedy, for the one-invocation, all-problems
// rule (ONBOARDING point 2) while the exit stays 2.
func gateCouldNotRunFindings(findings []gateFinding) string {
	whys := make([]string, 0, len(findings))
	for _, f := range findings {
		whys = append(whys, f.why)
	}
	return gateCouldNotRun(strings.Join(whys, "; "))
}

// isSeatYAML reports whether path is a seat file: a root-level `<seat>.yaml`, never
// `.sops.yaml` and never a `.yaml` under a subdirectory. A nested `.yaml` (sub/evil.yaml)
// is not a seat file, so check 3 refuses a change to one as a change to an unknown file.
func isSeatYAML(path string) bool {
	// A seat file must be root-level: no directory components.
	if strings.Contains(path, "/") || strings.Contains(path, "\\") {
		return false
	}
	return strings.HasSuffix(path, ".yaml") && path != ".sops.yaml"
}

// matchingRuleIndex returns the index of the creation rule whose path_regex
// matches relPath, or -1 when none does.
func matchingRuleIndex(cfg *SopsConfig, relPath string) int {
	cleanRel := strings.TrimPrefix(filepathSlash(relPath), "./")
	for i := range cfg.CreationRules {
		r := &cfg.CreationRules[i]
		if r.PathRegex == "" {
			continue
		}
		re, err := regexp.Compile(r.PathRegex)
		if err != nil {
			continue
		}
		if re.MatchString(cleanRel) {
			return i
		}
	}
	return -1
}

func filepathSlash(p string) string { return strings.ReplaceAll(p, "\\", "/") }

// plainValues returns every key whose value is not encrypted and is not permitted in the
// clear by unencryptedRegex (SPEC-SECRETS "gate"). A key at the root and a key nested in an
// indented map are read the same way: a cleartext value is a plain value wherever it sits.
// The sops metadata block is the one indented shape skipped whole, because its keys are the
// envelope's and not the seat's.
func plainValues(data []byte, unencryptedRegex string) ([]string, error) {
	var unencRe *regexp.Regexp
	if unencryptedRegex != "" {
		var err error
		unencRe, err = regexp.Compile(unencryptedRegex)
		if err != nil {
			return nil, err
		}
	}
	var keys []string
	inSops := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if inSops {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				continue
			}
			inSops = false
		}
		key, val, found := strings.Cut(trimmed, ":")

		if !found {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if key == "" {
			continue
		}
		if key == "sops" && !(strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			inSops = true
			continue
		}
		if val == "" {
			continue
		}
		if strings.HasPrefix(val, "ENC[") {
			continue
		}
		// The mark key holds a verb's mark and nothing else in the clear, whatever the rule
		// admits: other cleartext under it is a plain value.
		if key == SeatMarkKey {
			if !isSeatMark(val) {
				keys = append(keys, key)
			}
			continue
		}
		if unencRe != nil && unencRe.MatchString(key) {
			continue
		}
		keys = append(keys, key)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

// gateResolveCommit turns one ref into the SHA of the commit it names in the store, or
// refuses it. A ref beginning with "-" is the shape of an option, not a ref, and is refused
// before git sees it (RunGate refuses it first, as argv; this is the second wall); anything
// else is asked of `git rev-parse --verify` behind --end-of-options, so git reads it only as
// a revision.
func gateResolveCommit(storeDir, flagName, ref string) (string, error) {
	if strings.HasPrefix(ref, "-") {
		return "", fmt.Errorf("%s %s begins with \"-\", the shape of an option, not a git ref", flagName, oneline.Field(ref))
	}
	res, err := gitrun.Run(context.Background(), storeGit(storeDir), "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	sha := strings.TrimSpace(string(res.Stdout))
	if err != nil || sha == "" || strings.HasPrefix(sha, "-") {
		return "", fmt.Errorf("%s %s does not name a commit in the store %s", flagName, oneline.Field(ref), oneline.Field(storeDir))
	}
	return sha, nil
}

// gitChangedFiles lists the files that differ between base and head.
func gitChangedFiles(storeDir, base, head string) ([]string, error) {
	res, err := gitrun.Run(context.Background(), storeGit(storeDir), "diff", "--name-only", "--end-of-options", base, head, "--")
	if err != nil {
		return nil, fmt.Errorf("git diff %s %s failed: %v", base, head, err)
	}
	return splitLines(res.Stdout), nil
}

// gitTreeFiles lists every path in the tree at ref.
func gitTreeFiles(storeDir, ref string) ([]string, error) {
	res, err := gitrun.Run(context.Background(), storeGit(storeDir), "ls-tree", "-r", "--name-only", "--end-of-options", ref)
	if err != nil {
		return nil, err
	}
	files := splitLines(res.Stdout)
	sort.Strings(files)
	return files, nil
}

// gitShowFile reads one file's bytes out of the tree at ref.
func gitShowFile(storeDir, ref, path string) ([]byte, error) {
	res, err := gitrun.Run(context.Background(), storeGit(storeDir), "show", "--end-of-options", ref+":"+path)
	return res.Stdout, err
}

func splitLines(out []byte) []string {
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}
