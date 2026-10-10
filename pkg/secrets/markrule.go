package secrets

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// seatMarkRegex is what a rule's unencrypted_regex carries so the mark stays in the clear.
const seatMarkRegex = "^" + SeatMarkKey + "$"

// seatMarkRule answers .sops.yaml's text with the rule that governs seatFile made to keep
// the mark in the clear, and whether that changed anything. A rule written before the mark
// existed (every seat ruled before commit 7488b87f1a) has no unencrypted_regex for it, so
// sops sealed the mark and the gate then refused every re-seal as a hand seal. The verb
// that writes the seat file now writes the rule's regex in the same commit, where the
// store's gate and its reviewer see it (SPEC-SECRETS "gate", the mark): a rule with no
// unencrypted_regex gets `^NOVA_SECRETS_WRITTEN_BY$`, and one with a regex R that does not
// already admit the mark gets `(?:R)|^NOVA_SECRETS_WRITTEN_BY$`. Nothing else in
// the file moves, and the result is parsed again and refused unless it is the same rule
// with the mark admitted. A config with no rule for the file is answered unchanged: sops
// refuses that seal with its own reason, as before.
func seatMarkRule(cfgText []byte, seatFile string) ([]byte, bool, error) {
	cfg, err := parseSopsConfig(bytes.NewReader(cfgText))
	if err != nil {
		return cfgText, false, nil // no rules to admit the mark in: sops refuses the seal with its own reason
	}
	rule, err := FindMatchingRule(cfg, seatFile)
	if err != nil || markAdmitted(rule.UnencryptedRegex) {
		return cfgText, false, nil
	}
	index := 0
	for i := range cfg.CreationRules {
		if &cfg.CreationRules[i] == rule {
			index = i
		}
	}

	lines := strings.SplitAfter(string(cfgText), "\n")
	start, end := ruleLines(lines, index)
	if start < 0 {
		return nil, false, fmt.Errorf(".sops.yaml: the rule for %s could not be found in the file's text to admit the mark", seatFile)
	}
	var out []string
	done := false
	for i := start; i < end; i++ {
		trimmed := strings.TrimSpace(lines[i])
		key := strings.TrimSpace(strings.TrimPrefix(trimmed, "-"))
		if !strings.HasPrefix(key, "unencrypted_regex:") {
			continue
		}
		at := strings.Index(lines[i], "unencrypted_regex:") + len("unencrypted_regex:")
		raw := strings.TrimRight(lines[i][at:], "\r\n")
		val := strings.TrimSpace(raw)
		var next string
		// The rule's own regex goes in a group of its own, so a flag at its front (`(?i)`)
		// stays inside it and admits no other spelling of the mark key.
		if n := len(val); n >= 2 && (val[0] == '"' || val[0] == '\'') && val[n-1] == val[0] {
			next = val[:1] + "(?:" + val[1:n-1] + ")|" + seatMarkRegex + val[n-1:]
		} else {
			next = "(?:" + val + ")|" + seatMarkRegex
		}
		out = append(append(append([]string{}, lines[:i]...), lines[i][:at]+" "+next+"\n"), lines[i+1:]...)
		done = true
		break
	}
	if !done {
		indent := ruleKeyIndent(lines, start, end)
		ins := indent + "unencrypted_regex: " + seatMarkRegex + "\n"
		first := lines[start]
		if !strings.HasSuffix(first, "\n") {
			first += "\n"
		}
		out = append(append(append(append([]string{}, lines[:start]...), first), ins), lines[start+1:]...)
	}
	updated := []byte(strings.Join(out, ""))

	// Fail closed: the same rules, in the same order, with only this one admitting the mark.
	again, err := parseSopsConfig(bytes.NewReader(updated))
	if err != nil || len(again.CreationRules) != len(cfg.CreationRules) {
		return nil, false, fmt.Errorf(".sops.yaml: admitting the mark for %s changed the rules' shape; nothing was written", seatFile)
	}
	for i := range cfg.CreationRules {
		a, b := cfg.CreationRules[i], again.CreationRules[i]
		same := a.PathRegex == b.PathRegex && strings.Join(a.Recipients, ",") == strings.Join(b.Recipients, ",")
		if i != index {
			same = same && a.UnencryptedRegex == b.UnencryptedRegex
		} else {
			same = same && markAdmitted(b.UnencryptedRegex)
		}
		if !same {
			return nil, false, fmt.Errorf(".sops.yaml: admitting the mark for %s changed rule %d; nothing was written", seatFile, i+1)
		}
	}
	return updated, true, nil
}

// seatMarkRuleIn is seatMarkRule on the store's own .sops.yaml.
func seatMarkRuleIn(storeDir, seatFile string) ([]byte, bool, error) {
	text, err := os.ReadFile(filepath.Join(storeDir, ".sops.yaml"))
	if err != nil {
		return nil, false, nil // sops reads the same file and refuses with its own reason
	}
	return seatMarkRule(text, seatFile)
}

// markAdmitted reports whether an unencrypted_regex keeps the mark key in the clear.
func markAdmitted(unencrypted string) bool {
	if unencrypted == "" {
		return false
	}
	re, err := regexp.Compile(unencrypted)
	return err == nil && re.MatchString(SeatMarkKey)
}

// ruleLines is the [start, end) of the index'th creation rule's lines, counted the way
// parseSopsConfig counts them; start is -1 when there is no such rule.
func ruleLines(lines []string, index int) (int, int) {
	in := false
	n := -1
	start := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "creation_rules:") {
			in = true
			continue
		}
		if !in {
			continue
		}
		top := line[0] != ' ' && line[0] != '\t' && line[0] != '-'
		item := strings.HasPrefix(trimmed, "-") && (len(trimmed) == 1 || trimmed[1] == ' ')
		if start >= 0 && (item || top) {
			return start, i
		}
		if item {
			n++
			if n == index {
				start = i
			}
		}
	}
	if start < 0 {
		return -1, -1
	}
	return start, len(lines)
}

// ruleKeyIndent is the indentation of a rule's keys: what follows "- " on its first line,
// or the next line's own when the item is a bare "-".
func ruleKeyIndent(lines []string, start, end int) string {
	first := lines[start]
	dash := strings.Index(first, "-")
	rest := first[dash+1:]
	if strings.TrimSpace(rest) != "" {
		return strings.Repeat(" ", dash+1+len(rest)-len(strings.TrimLeft(rest, " ")))
	}
	for i := start + 1; i < end; i++ {
		if t := strings.TrimSpace(lines[i]); t != "" && !strings.HasPrefix(t, "#") {
			return lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " "))]
		}
	}
	return strings.Repeat(" ", dash+2)
}
