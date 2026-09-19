package secrets

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
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
// "GATE REFUSE rule=<n> file=<f>: <why>" at exit 2.
func RunGate(in GateInput) (string, int) {
	storeDir, base, head := in.StoreDir, in.Base, in.Head
	if storeDir == "" {
		return "SECRETS REFUSED: missing --store <dir>", 2
	}
	if base == "" {
		return "SECRETS REFUSED: missing --base <git ref>", 2
	}
	if head == "" {
		return "SECRETS REFUSED: missing --head <git ref>", 2
	}

	// The registry is read FIRST and read WHOLE, before any judgement leans on it: half a
	// registry is the half that lets a recipient through, so unreadable or malformed is a
	// refusal here and never a rule that quietly did not run.
	fleetSeats, err := gateFleetSeats(in.MachinesPath)
	if err != nil {
		return gateRefuse(0, in.MachinesPath, err.Error()), 2
	}

	changed, err := gitChangedFiles(storeDir, base, head)
	if err != nil {
		return gateRefuse(0, "", err.Error()), 2
	}
	if len(changed) == 0 {
		return gateApprove(0, in.MachinesPath), 0
	}

	// 3. No other file changes except README.md.
	for _, f := range changed {
		if f == ".sops.yaml" || f == "README.md" || isSeatYAML(f) {
			continue
		}
		return gateRefuse(0, f, "only .sops.yaml, README.md and seat .yaml files may change"), 2
	}

	// The declared recovery key, read from the head tree.
	recoveryData, err := gitShowFile(storeDir, head, "recovery.pub")
	if err != nil {
		return gateRefuse(0, "recovery.pub", "unreadable at "+oneline.Field(head)), 2
	}
	recoveryKey := strings.TrimSpace(string(recoveryData))
	if !IsValidAgePublicKey(recoveryKey) {
		return gateRefuse(0, "recovery.pub", "does not declare a single valid age public key"), 2
	}

	// The head .sops.yaml, the rule set the gate measures against.
	sopsData, err := gitShowFile(storeDir, head, ".sops.yaml")
	if err != nil {
		return gateRefuse(0, ".sops.yaml", "unreadable at "+oneline.Field(head)), 2
	}
	cfg, err := parseSopsConfig(bytes.NewReader(sopsData))
	if err != nil {
		return gateRefuse(0, ".sops.yaml", err.Error()), 2
	}

	headFiles, err := gitTreeFiles(storeDir, head)
	if err != nil {
		return gateRefuse(0, "", "unable to list the head tree: "+oneline.Escape(err.Error())), 2
	}

	// 1. Every changed .sops.yaml rule: exactly two age recipients, one the
	// declared recovery key, and a path_regex naming exactly one seat file.
	if containsString(changed, ".sops.yaml") {
		// The recipients the store already had. A key here is not a grant this pull request
		// makes, so resealing a seat or editing its rule asks the registry nothing.
		baseKeys, err := gateRecipientsAt(storeDir, base)
		if err != nil {
			return gateRefuse(0, ".sops.yaml", err.Error()), 2
		}
		for i := range cfg.CreationRules {
			rule := cfg.CreationRules[i]
			ruleNum := i + 1
			if len(rule.Recipients) != 2 {
				return gateRefuse(ruleNum, ".sops.yaml", fmt.Sprintf("rule has %d age recipients; expected exactly two", len(rule.Recipients))), 2
			}
			if !containsString(rule.Recipients, recoveryKey) {
				return gateRefuse(ruleNum, ".sops.yaml", "rule recipients do not include the key recovery.pub declares"), 2
			}
			re, err := regexp.Compile(rule.PathRegex)
			if err != nil {
				return gateRefuse(ruleNum, ".sops.yaml", fmt.Sprintf("path_regex %q is not a valid regular expression", rule.PathRegex)), 2
			}
			named, seatFile := 0, ""
			for _, hf := range headFiles {
				if isSeatYAML(hf) && re.MatchString(hf) {
					named++
					seatFile = hf
				}
			}
			if named != 1 {
				return gateRefuse(ruleNum, ".sops.yaml", fmt.Sprintf("path_regex names %d seat files; expected exactly one", named)), 2
			}

			// 4. A recipient key this pull request introduces is a GRANT, and the review that
			// used to catch it is gone (Glenn 2026-09-18: a seat is set up with no second
			// human). The fleet's machines registry stands in its place: the new key is
			// permitted only when a machine in the registry carries this file's seat, so the
			// question "whose key is this, and does that machine exist?" has a mechanical
			// answer. With no --machines the rule is dormant and the APPROVE line says so.
			if fleetSeats != nil {
				seat := strings.TrimSuffix(seatFile, ".yaml")
				for _, key := range rule.Recipients {
					if key == recoveryKey || baseKeys[key] {
						continue
					}
					if !fleetSeats[seat] {
						return gateRefuse(ruleNum, seatFile, fmt.Sprintf(
							"rule adds a recipient no seat file rule named before, and no machine in %s carries the seat %s; add the machine's row (its seat column must read %s) or drop the rule",
							oneline.Field(in.MachinesPath), oneline.Field(seat), oneline.Field(seat))), 2
					}
				}
			}
		}
	}

	// 5. Keep what exists. A seat file in the store at the base must still be in the store at
	// the head: removing one is how a seat would lose its credentials in a pull request whose
	// subject says it is adding one, and it is never part of adding a seat.
	baseFiles, err := gitTreeFiles(storeDir, base)
	if err != nil {
		return gateRefuse(0, "", "unable to list the base tree: "+oneline.Escape(err.Error())), 2
	}
	for _, bf := range baseFiles {
		if isSeatYAML(bf) && !containsString(headFiles, bf) {
			return gateRefuse(0, bf, "the seat file is in the store at the base and gone at the head; a seat is never removed here"), 2
		}
	}

	// 2. Every changed seat file is encrypted and its rule exists.
	for _, f := range changed {
		if !isSeatYAML(f) {
			continue
		}
		ruleIdx := matchingRuleIndex(cfg, f)
		if ruleIdx < 0 {
			return gateRefuse(0, f, "no creation rule in .sops.yaml matches this file"), 2
		}
		ruleNum := ruleIdx + 1
		data, err := gitShowFile(storeDir, head, f)
		if err != nil {
			return gateRefuse(ruleNum, f, "unreadable at "+oneline.Field(head)), 2
		}
		if !bytes.Contains(data, []byte("sops:")) {
			return gateRefuse(ruleNum, f, "file is not encrypted (missing sops metadata)"), 2
		}
		if key, plain := firstPlainValue(data, cfg.CreationRules[ruleIdx].UnencryptedRegex); plain {
			return gateRefuse(ruleNum, f, fmt.Sprintf("key %s is a plain value, not encrypted", oneline.Field(key))), 2
		}
	}

	return gateApprove(len(changed), in.MachinesPath), 0
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

// gateRefuse formats one refusal line: GATE REFUSE rule=<n> file=<f>: <why>.
func gateRefuse(ruleNum int, file, why string) string {
	return fmt.Sprintf("GATE REFUSE rule=%d file=%s: %s", ruleNum, oneline.Field(file), oneline.Escape(why))
}

// isSeatYAML reports whether path is a seat file: a *.yaml that is not .sops.yaml.
func isSeatYAML(path string) bool {
	return strings.HasSuffix(path, ".yaml") && path != ".sops.yaml"
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
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

// firstPlainValue returns the first root-level key whose value is not encrypted
// and not permitted in the clear by unencryptedRegex.
func firstPlainValue(data []byte, unencryptedRegex string) (string, bool) {
	var unencRe *regexp.Regexp
	if unencryptedRegex != "" {
		unencRe, _ = regexp.Compile(unencryptedRegex)
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if key == "" || val == "" || key == "sops" {
			continue
		}
		if strings.HasPrefix(val, "ENC[") {
			continue
		}
		if unencRe != nil && unencRe.MatchString(key) {
			continue
		}
		return key, true
	}
	return "", false
}

// gitChangedFiles lists the files that differ between base and head.
func gitChangedFiles(storeDir, base, head string) ([]string, error) {
	out, err := exec.Command("git", "-C", storeDir, "diff", "--name-only", base, head).Output()
	if err != nil {
		return nil, fmt.Errorf("git diff %s %s failed: %v", base, head, err)
	}
	return splitLines(out), nil
}

// gitTreeFiles lists every path in the tree at ref.
func gitTreeFiles(storeDir, ref string) ([]string, error) {
	out, err := exec.Command("git", "-C", storeDir, "ls-tree", "-r", "--name-only", ref).Output()
	if err != nil {
		return nil, err
	}
	files := splitLines(out)
	sort.Strings(files)
	return files, nil
}

// gitShowFile reads one file's bytes out of the tree at ref.
func gitShowFile(storeDir, ref, path string) ([]byte, error) {
	return exec.Command("git", "-C", storeDir, "show", ref+":"+path).Output()
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
