package secrets

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// RunGate is the seat-rule gate as a verb: the store's shell gate, called by the
// workflow. It diffs --base..--head with git (no GitHub) and either prints
// "GATE APPROVE files=<n>" at exit 0 or "GATE REFUSE rule=<n> file=<f>: <why>"
// at exit 2.
func RunGate(storeDir, base, head string) (string, int) {
	if storeDir == "" {
		return "SECRETS REFUSED: missing --store <dir>", 2
	}
	if base == "" {
		return "SECRETS REFUSED: missing --base <git ref>", 2
	}
	if head == "" {
		return "SECRETS REFUSED: missing --head <git ref>", 2
	}

	changed, err := gitChangedFiles(storeDir, base, head)
	if err != nil {
		return gateRefuse(0, "", err.Error()), 2
	}
	if len(changed) == 0 {
		return "GATE APPROVE files=0", 0
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
			named := 0
			for _, hf := range headFiles {
				if isSeatYAML(hf) && re.MatchString(hf) {
					named++
				}
			}
			if named != 1 {
				return gateRefuse(ruleNum, ".sops.yaml", fmt.Sprintf("path_regex names %d seat files; expected exactly one", named)), 2
			}
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

	return fmt.Sprintf("GATE APPROVE files=%d", len(changed)), 0
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
