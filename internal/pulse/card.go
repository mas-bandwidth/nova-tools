package pulse

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// CardInfo holds parsed header and metadata for a card in the queue.
type CardInfo struct {
	Path         string
	Label        string
	Lane         string
	Priority     int
	Dependencies []string
}

// sexpDependsOnRegex matches :depends-on (...) in s-expressions.
var sexpDependsOnRegex = regexp.MustCompile(`:depends-on\s*\(([^)]*)\)`)

// sexpIDRegex matches :id "..." or :id <id> in s-expressions.
var sexpIDRegex = regexp.MustCompile(`:id\s+(?:"([^"]+)"|([^\s\(\)]+))`)

// sexpPriorityRegex matches :priority <int> in s-expressions.
var sexpPriorityRegex = regexp.MustCompile(`:priority\s+(-?\d+)`)

// ParseCardDependencies parses dependency identifiers from card content or card headers.
// It supports:
// 1. Header lines: `depends-on: <id1>, <id2>` (case-insensitive)
// 2. S-expression blocks: `:depends-on ("id1" "id2")` or `:depends-on (id1 id2)`
func ParseCardDependencies(content string) []string {
	var deps []string
	seen := map[string]bool{}

	addDep := func(dep string) {
		dep = strings.TrimSpace(dep)
		dep = strings.Trim(dep, `"'`)
		dep = strings.TrimSpace(dep)
		// "-" is the cut template's "no dependency" (FormatDependsOn), never a card id.
		if dep != "" && dep != "-" && !seen[dep] {
			seen[dep] = true
			deps = append(deps, dep)
		}
	}

	// 1. Line-by-line check for headers
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "depends-on:") {
			val := strings.TrimSpace(line[len("depends-on:"):])
			// Split by comma
			for _, part := range strings.Split(val, ",") {
				part = strings.TrimSpace(part)
				if part != "" {
					addDep(part)
				}
			}
		}
	}

	// 2. S-expression :depends-on (...) matching
	matches := sexpDependsOnRegex.FindAllStringSubmatch(content, -1)
	for _, m := range matches {
		if len(m) > 1 {
			inner := strings.TrimSpace(m[1])
			if inner == "" {
				continue
			}
			// S-expression tokens can be quoted strings or symbols separated by whitespace or commas
			tokens := parseSexpTokens(inner)
			for _, tok := range tokens {
				addDep(tok)
			}
		}
	}

	return deps
}

// parseSexpTokens extracts space/newline/comma separated tokens, stripping quotes.
func parseSexpTokens(s string) []string {
	var tokens []string
	var cur strings.Builder
	inQuotes := false
	var quoteChar rune

	for _, r := range s {
		switch {
		case r == '"' || r == '\'':
			if inQuotes && r == quoteChar {
				inQuotes = false
			} else if !inQuotes {
				inQuotes = true
				quoteChar = r
			}
		case (r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ',') && !inQuotes:
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

// ParseCardHeader parses metadata from card file content.
func ParseCardHeader(content, path string) *CardInfo {
	info := &CardInfo{
		Path:         path,
		Dependencies: ParseCardDependencies(content),
	}

	if path != "" {
		base := filepath.Base(path)
		info.Label = strings.TrimSuffix(base, ".md")
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lower := strings.ToLower(line)

		if strings.HasPrefix(lower, "result ") {
			f := strings.Fields(line)
			if len(f) > 1 && f[1] != "" {
				info.Label = f[1]
			}
		} else if strings.HasPrefix(lower, "lane:") {
			v := strings.TrimSpace(line[len("lane:"):])
			if v != "" {
				info.Lane = v
			}
		} else if strings.HasPrefix(lower, "priority:") {
			v := strings.TrimSpace(line[len("priority:"):])
			if n, err := strconv.Atoi(v); err == nil {
				info.Priority = n
			}
		}
	}

	// Also check s-expression priority if not set by header
	if info.Priority == 0 {
		if m := sexpPriorityRegex.FindStringSubmatch(content); len(m) > 1 {
			if n, err := strconv.Atoi(m[1]); err == nil {
				info.Priority = n
			}
		}
	}

	return info
}

// ParseCard reads and parses a card file at path.
func ParseCard(path string) (*CardInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseCardHeader(string(data), path), nil
}

// CardDependencies reads dependencies for the card file at path.
func CardDependencies(path string) []string {
	info, err := ParseCard(path)
	if err != nil {
		return nil
	}
	return info.Dependencies
}

// CardPriority reads priority for the card file at path.
func CardPriority(path string) int {
	info, err := ParseCard(path)
	if err != nil {
		return 0
	}
	return info.Priority
}

// CardLane reads the lane for the card file at path.
func CardLane(path string) string {
	info, err := ParseCard(path)
	if err != nil {
		return ""
	}
	return info.Lane
}

// ParseSexpDependencies parses an entire s-expression document (such as docs/roadmaps/nova-work.sexp)
// and returns a map from node :id to its slice of :depends-on IDs.
func ParseSexpDependencies(sexp string) map[string][]string {
	result := map[string][]string{}

	// Locate each node starting with ( and extract its :id and :depends-on
	// We scan for :id and find the enclosing or following :depends-on
	lines := strings.Split(sexp, "\n")
	currentID := ""

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if m := sexpIDRegex.FindStringSubmatch(line); len(m) > 0 {
			id := m[1]
			if id == "" && len(m) > 2 {
				id = m[2]
			}
			if id != "" {
				currentID = id
				if _, exists := result[currentID]; !exists {
					result[currentID] = []string{}
				}
			}
		}

		if currentID != "" && strings.Contains(line, ":depends-on") {
			// Gather until matching closing paren
			block := line
			for !strings.Contains(block, ")") && i+1 < len(lines) {
				i++
				block += " " + lines[i]
			}
			if sub := sexpDependsOnRegex.FindStringSubmatch(block); len(sub) > 1 {
				tokens := parseSexpTokens(sub[1])
				for _, tok := range tokens {
					tok = strings.Trim(tok, `"'`)
					if tok != "" {
						result[currentID] = append(result[currentID], tok)
					}
				}
			}
		}
	}

	return result
}
