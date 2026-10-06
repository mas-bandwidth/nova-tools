package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// ExtractUsage extracts the usage lines from a tool's help output.
// It finds headers matching (?i)^usage\b.*:, captures indented lines,
// strips the standard 2-space leading indentation, and stops when:
// 1. Column 0 non-empty text is encountered.
// 2. A section header (exit codes:, example:, flags:, setup:, what it prints:) is reached.
// 3. An empty line is reached, unless the next non-empty line begins another usage block.
func ExtractUsage(helpText string) string {
	lines := strings.Split(helpText, "\n")
	var usageLines []string
	inUsage := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if isUsageHeader(trimmed) {
			inUsage = true
			continue
		}

		if inUsage {
			if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
				inUsage = false
				continue
			}
			if isSectionHeader(line) {
				inUsage = false
				continue
			}
			if trimmed == "" {
				nextIsUsage := false
				for j := i + 1; j < len(lines); j++ {
					nt := strings.TrimSpace(lines[j])
					if nt == "" {
						continue
					}
					if isUsageHeader(nt) {
						nextIsUsage = true
					}
					break
				}
				if !nextIsUsage {
					inUsage = false
				}
				continue
			}

			cleaned := line
			if strings.HasPrefix(cleaned, "  ") {
				cleaned = cleaned[2:]
			} else if strings.HasPrefix(cleaned, " ") || strings.HasPrefix(cleaned, "\t") {
				cleaned = cleaned[1:]
			}
			usageLines = append(usageLines, cleaned)
		}
	}

	return strings.Join(usageLines, "\n")
}

func isUsageHeader(s string) bool {
	low := strings.ToLower(s)
	return strings.HasPrefix(low, "usage") && strings.HasSuffix(low, ":")
}

var sectionHeaderRe = regexp.MustCompile(`(?i)^\s*(exit codes?|example|flags|setup|what it prints):`)

func isSectionHeader(line string) bool {
	return sectionHeaderRe.MatchString(line)
}

// DiscoveredVerbs extracts all distinct declared verbs for a tool from its help output.
func DiscoveredVerbs(tool, helpText string) []string {
	var verbs []string
	seen := map[string]bool{}
	for _, line := range strings.Split(helpText, "\n") {
		t, verb, ok := declaredVerb(line)
		if !ok || t != tool {
			continue
		}
		if seen[verb] {
			continue
		}
		seen[verb] = true
		verbs = append(verbs, verb)
	}
	return verbs
}

func declaredVerb(line string) (tool, verb string, ok bool) {
	fields := strings.Fields(command(strings.TrimSpace(line)))
	if len(fields) > 0 && fields[0] == "$" {
		fields = fields[1:]
	}
	for len(fields) > 0 && isEnvPrefix(fields[0]) {
		fields = fields[1:]
	}
	if len(fields) == 0 || !isToolName(fields[0]) {
		return "", "", false
	}
	if len(fields) > 1 && fields[1] == "is" {
		return "", "", false
	}
	var words []string
	for _, f := range fields[1:] {
		if len(words) == 2 || !isBareWord(f) {
			break
		}
		words = append(words, f)
	}
	if len(words) == 2 && len(fields) > 3 && isBareWord(fields[3]) {
		words = words[:1]
	}
	return fields[0], strings.Join(words, " "), true
}

func command(line string) string {
	for i := 0; i+1 < len(line); i++ {
		if line[i] == ' ' && line[i+1] == ' ' {
			return line[:i]
		}
		if line[i] == '\t' {
			return line[:i]
		}
	}
	return line
}

func isEnvPrefix(s string) bool {
	name, _, found := strings.Cut(s, "=")
	if !found || name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

func isToolName(s string) bool {
	if !strings.HasPrefix(s, "nova-") || len(s) <= len("nova-") {
		return false
	}
	return isBareWord(s)
}

func isBareWord(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9', r == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// VerifyVerbs runs each verb's -h (or help <verb> for nova-fuse) using the given binary.
func VerifyVerbs(ctx context.Context, binPath, tool string, verbs []string) error {
	for _, v := range verbs {
		var args []string
		if tool == "nova-fuse" {
			args = append([]string{"help"}, strings.Fields(v)...)
		} else if v == "help" {
			args = []string{"help"}
		} else if v == "" {
			args = []string{"-h"}
		} else {
			args = append(strings.Fields(v), "-h")
		}

		cmd := subproc.Context(ctx, binPath, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %s (running %s %s) failed: %v\n%s", tool, v, filepath.Base(binPath), strings.Join(args, " "), err, out)
		}
	}
	return nil
}

// FindToolBinaries finds all nova-* binaries in binDir, returning a map of toolName -> binaryPath.
func FindToolBinaries(binDir string) (map[string]string, error) {
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return nil, fmt.Errorf("reading bin dir %s: %w", binDir, err)
	}

	bins := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if runtime.GOOS == "windows" {
			name = strings.TrimSuffix(name, ".exe")
		}
		if !isToolName(name) {
			continue
		}
		binPath := filepath.Join(binDir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		if runtime.GOOS == "windows" {
			if !strings.HasSuffix(strings.ToLower(e.Name()), ".exe") {
				continue
			}
		} else {
			if info.Mode().Perm()&0o111 == 0 {
				continue
			}
		}
		bins[name] = binPath
	}
	return bins, nil
}

// UpdateDoc replaces the content between <!-- clidoc:begin <tool> --> and <!-- clidoc:end <tool> -->
// with the generated usage block.
func UpdateDoc(docContent, tool, usage string) (string, error) {
	beginMarker := fmt.Sprintf("<!-- clidoc:begin %s -->", tool)
	endMarker := fmt.Sprintf("<!-- clidoc:end %s -->", tool)

	beginIdx := strings.Index(docContent, beginMarker)
	if beginIdx == -1 {
		return "", fmt.Errorf("missing marker %s in doc", beginMarker)
	}

	endIdx := strings.Index(docContent, endMarker)
	if endIdx == -1 {
		return "", fmt.Errorf("missing marker %s in doc", endMarker)
	}

	if beginIdx >= endIdx {
		return "", fmt.Errorf("marker %s appears after %s", beginMarker, endMarker)
	}

	newBlock := fmt.Sprintf("%s\n```\n%s\n```\n%s", beginMarker, usage, endMarker)

	before := docContent[:beginIdx]
	after := docContent[endIdx+len(endMarker):]

	return before + newBlock + after, nil
}

// ProcessAll processes all tools found in binDir and updates or checks docPath.
func ProcessAll(ctx context.Context, docPath, binDir string, checkOnly bool) (bool, string, error) {
	docBytes, err := os.ReadFile(docPath)
	if err != nil {
		return false, "", fmt.Errorf("reading doc %s: %w", docPath, err)
	}
	docContent := string(docBytes)

	bins, err := FindToolBinaries(binDir)
	if err != nil {
		return false, "", err
	}
	if len(bins) == 0 {
		return false, "", fmt.Errorf("no nova-* binaries found in %s", binDir)
	}

	markerRe := regexp.MustCompile(`<!-- clidoc:begin (nova-[a-z0-9-]+) -->`)
	matches := markerRe.FindAllStringSubmatch(docContent, -1)
	for _, m := range matches {
		toolName := m[1]
		if _, ok := bins[toolName]; !ok {
			return false, "", fmt.Errorf("doc has marker for %s, but binary not found in %s", toolName, binDir)
		}
	}

	seen := map[string]bool{}
	var tools []string
	for _, m := range matches {
		toolName := m[1]
		if !seen[toolName] {
			seen[toolName] = true
			tools = append(tools, toolName)
		}
	}
	sort.Strings(tools)

	updatedDoc := docContent
	var diffDetails []string

	for _, tool := range tools {
		binPath := bins[tool]

		helpCmd := subproc.Context(ctx, binPath, "help")
		out, err := helpCmd.CombinedOutput()
		if err != nil && len(out) == 0 {
			return false, "", fmt.Errorf("reading %s help: %w", tool, err)
		}
		helpText := string(out)

		verbs := DiscoveredVerbs(tool, helpText)
		if err := VerifyVerbs(ctx, binPath, tool, verbs); err != nil {
			return false, "", fmt.Errorf("verifying %s verbs: %w", tool, err)
		}

		usage := ExtractUsage(helpText)

		newDoc, err := UpdateDoc(updatedDoc, tool, usage)
		if err != nil {
			return false, "", err
		}
		if newDoc != updatedDoc {
			diffDetails = append(diffDetails, fmt.Sprintf("%s section in %s differed from tool help", tool, docPath))
			updatedDoc = newDoc
		}
	}

	if checkOnly {
		if updatedDoc == docContent {
			return true, "", nil
		}
		return false, strings.Join(diffDetails, "\n"), nil
	}

	if updatedDoc != docContent {
		if err := os.WriteFile(docPath, []byte(updatedDoc), 0o644); err != nil {
			return false, "", fmt.Errorf("writing updated doc %s: %w", docPath, err)
		}
	}

	return true, "", nil
}
