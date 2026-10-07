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

// extractUsage splits a tool's help into command lines and NOTE sentences.
// It finds headers matching (?i)^usage\b.*:, captures indented lines,
// strips the standard leading indentation, and stops when:
// 1. Column 0 non-empty text is encountered.
// 2. A section header (exit codes:, example:, flags:, setup:, what it prints:) is reached.
// 3. An empty line is reached, unless the next non-empty line begins another usage block.
// A NOTE sentence is recorded separately so the command fence holds only commands.
func extractUsage(helpText string) (commands, notes string) {
	lines := strings.Split(helpText, "\n")
	var usageLines []string
	var noteLines []string
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
			if isNoteLine(cleaned) {
				noteLines = append(noteLines, cleaned)
				continue
			}
			usageLines = append(usageLines, cleaned)
		}
	}

	return strings.Join(usageLines, "\n"), strings.Join(noteLines, "\n")
}

// ExtractUsage extracts command lines from a tool's help usage block.
// A NOTE sentence is not a command; ExtractUsageNotes returns those.
func ExtractUsage(helpText string) string {
	commands, _ := extractUsage(helpText)
	return commands
}

// ExtractUsageNotes returns NOTE sentences that help printed inside the usage
// block. They are not commands and do not belong in the generated command fence.
func ExtractUsageNotes(helpText string) string {
	_, notes := extractUsage(helpText)
	return notes
}

func isNoteLine(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	if fields[0] == "NOTE:" || strings.HasPrefix(fields[0], "NOTE:") {
		return true
	}
	return len(fields) >= 2 && isToolName(fields[0]) && (fields[1] == "NOTE:" || strings.HasPrefix(fields[1], "NOTE:"))
}

func isUsageHeader(s string) bool {
	low := strings.ToLower(s)
	return strings.HasPrefix(low, "usage") && strings.HasSuffix(low, ":")
}

var sectionHeaderRe = regexp.MustCompile(`(?i)^\s*(exit codes?|example|flags|setup|what it prints):`)

func isSectionHeader(line string) bool {
	return sectionHeaderRe.MatchString(line)
}

// DiscoveredVerbs extracts distinct verb routes from declared usage, excluding
// examples whose positional arguments are not verbs. Nested routes and pipe
// alternatives each get their own help probe.
func DiscoveredVerbs(tool, helpText string) []string {
	lines := strings.Split(ExtractUsage(helpText), "\n")
	commandIndent := -1
	for _, line := range lines {
		name, _ := declaredVerbs(line)
		if name != tool {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if commandIndent < 0 || indent < commandIndent {
			commandIndent = indent
		}
	}
	var verbs []string
	seen := map[string]bool{}
	for _, line := range lines {
		// Deeper lines continue a description, even when they name this tool.
		if len(line)-len(strings.TrimLeft(line, " \t")) != commandIndent {
			continue
		}
		name, routes := declaredVerbs(line)
		if name != tool {
			continue
		}
		for _, verb := range routes {
			if !seen[verb] {
				seen[verb] = true
				verbs = append(verbs, verb)
			}
		}
	}
	return verbs
}

func declaredVerbs(line string) (string, []string) {
	fields := strings.Fields(command(strings.TrimSpace(line)))
	if len(fields) > 0 && fields[0] == "$" {
		fields = fields[1:]
	}
	for len(fields) > 0 && isEnvPrefix(fields[0]) {
		fields = fields[1:]
	}
	if len(fields) == 0 || !isToolName(fields[0]) {
		return "", nil
	}
	routes := []string{""}
	for i, word := range fields[1:] {
		// A bracketed default verb is declared, such as [run].
		if i == 0 && strings.HasPrefix(word, "[") && strings.HasSuffix(word, "]") {
			word = strings.TrimSuffix(strings.TrimPrefix(word, "["), "]")
		}
		alternatives := strings.Split(word, "|")
		valid := true
		for _, alternative := range alternatives {
			if !isBareWord(alternative) {
				valid = false
				break
			}
		}
		if !valid {
			break
		}
		var next []string
		for _, route := range routes {
			for _, alternative := range alternatives {
				next = append(next, strings.TrimSpace(route+" "+alternative))
			}
		}
		routes = next
	}
	return fields[0], routes
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
			return nil, fmt.Errorf("reading binary %s: %w", binPath, err)
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
// with the generated usage block. notes, when set, are written after the command
// fence and before the end marker: a generated command block contains only commands.
func UpdateDoc(docContent, tool, usage, notes string) (string, error) {
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

	if strings.Count(docContent, beginMarker) != 1 || strings.Count(docContent, endMarker) != 1 {
		return "", fmt.Errorf("expected one marker pair for %s", tool)
	}

	if beginIdx >= endIdx {
		return "", fmt.Errorf("marker %s appears after %s", beginMarker, endMarker)
	}

	var b strings.Builder
	b.WriteString(beginMarker)
	b.WriteString("\n```\n")
	b.WriteString(usage)
	b.WriteString("\n```\n")
	if notes != "" {
		b.WriteString(notes)
		b.WriteString("\n")
	}
	b.WriteString(endMarker)

	before := docContent[:beginIdx]
	after := docContent[endIdx+len(endMarker):]

	return before + b.String() + after, nil
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

	var tools []string
	for t := range bins {
		tools = append(tools, t)
	}
	sort.Strings(tools)

	updatedDoc := docContent
	var diffDetails []string

	for _, tool := range tools {
		binPath := bins[tool]

		helpCmd := subproc.Context(ctx, binPath, "help")
		out, err := helpCmd.CombinedOutput()
		if err != nil {
			return false, "", fmt.Errorf("reading %s help: %w\n%s", tool, err, out)
		}
		helpText := string(out)

		verbs := DiscoveredVerbs(tool, helpText)
		if err := VerifyVerbs(ctx, binPath, tool, verbs); err != nil {
			return false, "", fmt.Errorf("verifying %s verbs: %w", tool, err)
		}

		usage := ExtractUsage(helpText)
		if usage == "" {
			return false, "", fmt.Errorf("%s help contains no usage commands", tool)
		}
		notes := ExtractUsageNotes(helpText)

		newDoc, err := UpdateDoc(updatedDoc, tool, usage, notes)
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
