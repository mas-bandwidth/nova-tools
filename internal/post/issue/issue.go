package issue

import (
	"fmt"
	"strings"
)

// Section is a v2 card schema section that can be filed as an issue.
type Section struct {
	raw    string
	header map[string]string
}

// ParseSection reads a v2 card schema section and validates its structure.
func ParseSection(raw string) (*Section, error) {
	s := &Section{raw: raw, header: make(map[string]string)}
	lines := strings.Split(raw, "\n")

	// Skip empty leading lines
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}

	// Parse metadata block (KEY: value lines or RESULT line)
	pos := start

	// Check for RESULT line first (special case without colon)
	if pos < len(lines) {
		line := lines[pos]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "RESULT ") || trimmed == "RESULT" {
			// Extract RESULT value
			if strings.HasPrefix(trimmed, "RESULT ") {
				s.header["RESULT"] = strings.TrimSpace(trimmed[7:])
			}
			pos++
		}
	}

	// Parse remaining metadata block (KEY: value lines)
	for pos < len(lines) {
		line := lines[pos]
		trimmed := strings.TrimSpace(line)

		// Empty line ends the header
		if trimmed == "" {
			pos++
			break
		}

		// Check if it's a header line (contains colon)
		if !strings.Contains(trimmed, ":") {
			// Non-empty line without colon ends the header
			break
		}

		// Parse KEY: value (first colon only)
		colonIdx := strings.Index(trimmed, ":")
		key := strings.TrimSpace(trimmed[:colonIdx])
		value := strings.TrimSpace(trimmed[colonIdx+1:])

		// Validate key is uppercase (with exceptions for base-repo, base-sha)
		if key != "base-repo" && key != "base-sha" {
			// Check if key is entirely uppercase
			upperKey := strings.ToUpper(key)
			if key != upperKey {
				// It's not uppercase - check if it's a known lowercase key
				if key != "base-repo" && key != "base-sha" {
					return nil, fmt.Errorf("bad-key casing line=%d key=%q; use uppercase %q instead", pos+1, key, upperKey)
				}
			}
		}

		// Check for duplicate
		if _, exists := s.header[key]; exists {
			return nil, fmt.Errorf("duplicate-key line=%d key=%q", pos+1, key)
		}

		s.header[key] = value
		pos++
	}

	return s, nil
}

var (
	fieldResult = "RESULT"
	fieldKind   = "KIND"
	fieldSchema = "SCHEMA"
)

// RequiredFields returns the set of fields that must be present.
func RequiredFields() []string {
	return []string{fieldResult, fieldKind, fieldSchema}
}

// Validate checks that all required fields are present.
func (s *Section) Validate() error {
	for _, field := range RequiredFields() {
		if _, ok := s.header[field]; !ok {
			return fmt.Errorf("missing-required-field field=%q; section must include %s", field, field)
		}
	}
	return nil
}

// Body returns the raw section text for the issue body.
func (s *Section) Body() string {
	return s.raw
}

// Title extracts a title from the RESULT field.
func (s *Section) Title() string {
	if result, ok := s.header["RESULT"]; ok {
		return result
	}
	return "Issue from report section"
}

// Field returns the value of a header field.
func (s *Section) Field(key string) string {
	return s.header[key]
}

// AllFields returns all parsed header fields.
func (s *Section) AllFields() map[string]string {
	return s.header
}

// FilableIssue represents an issue ready to be filed.
type FilableIssue struct {
	Section *Section
	Repo    string
	Owner   string
}

// NewFilableIssue creates a filedable issue from a section and repo info.
func NewFilableIssue(section *Section, owner, repo string) *FilableIssue {
	return &FilableIssue{
		Section: section,
		Owner:   owner,
		Repo:    repo,
	}
}

// IssueBody returns the body suitable for a GitHub issue.
// It is the raw section text, byte for byte.
func (i *FilableIssue) IssueBody() string {
	return i.Section.Body()
}

// IssueTitle returns the title for the GitHub issue.
func (i *FilableIssue) IssueTitle() string {
	return i.Section.Title()
}
