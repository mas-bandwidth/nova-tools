package workreconcile

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SprintRef points to a specific issue in a repository.
type SprintRef struct {
	Repo        string `json:"repo"`
	IssueNumber int    `json:"issue_number"`
}

func (r SprintRef) String() string {
	return fmt.Sprintf("%s#%d", r.Repo, r.IssueNumber)
}

// SprintPriority represents one ranked priority item from the sprint list.
type SprintPriority struct {
	Rank    int         `json:"rank"`
	Section string      `json:"section"`
	Title   string      `json:"title"`
	Refs    []SprintRef `json:"refs"`
	RawText string      `json:"raw_text"`
}

// SprintRowStatus holds the evaluation result for one priority row against imported issue nodes.
type SprintRowStatus struct {
	Rank             int         `json:"rank"`
	Section          string      `json:"section"`
	Title            string      `json:"title"`
	State            string      `json:"state"` // "done", "open", "blocked"
	Refs             []SprintRef `json:"refs"`
	UnresolvedIssues []SprintRef `json:"unresolved_issues"`
}

// SprintQueryResult provides the bounded query result over sprint priority nodes.
type SprintQueryResult struct {
	TotalPriorities           int               `json:"total_priorities"`
	CompletedPriorities       int               `json:"completed_priorities"`
	CompletionPercentage      float64           `json:"completion_percentage"`
	TotalReferencedIssues     int               `json:"total_referenced_issues"`
	CompletedReferencedIssues int               `json:"completed_referenced_issues"`
	IssueCompletionPercentage float64           `json:"issue_completion_percentage"`
	Rows                      []SprintRowStatus `json:"rows"`
}

// SummaryText returns a deterministic report of the sprint priority query.
func (r *SprintQueryResult) SummaryText() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("SPRINT PRIORITY QUERY: %d / %d rows complete (%.1f%%) | %d / %d issues closed (%.1f%%)\n",
		r.CompletedPriorities, r.TotalPriorities, r.CompletionPercentage,
		r.CompletedReferencedIssues, r.TotalReferencedIssues, r.IssueCompletionPercentage))

	for _, row := range r.Rows {
		if row.State == "done" {
			sb.WriteString(fmt.Sprintf("ROW %02d DONE rank=%d refs=%v title=%q\n",
				row.Rank, row.Rank, formatRefs(row.Refs), row.Title))
		} else {
			sb.WriteString(fmt.Sprintf("ROW %02d %s rank=%d refs=%v unresolved=%v title=%q\n",
				row.Rank, strings.ToUpper(row.State), row.Rank, formatRefs(row.Refs), formatRefs(row.UnresolvedIssues), row.Title))
		}
	}
	return sb.String()
}

func formatRefs(refs []SprintRef) string {
	if len(refs) == 0 {
		return "[]"
	}
	var s []string
	for _, r := range refs {
		s = append(s, r.String())
	}
	return "[" + strings.Join(s, ", ") + "]"
}

var (
	sectionHeaderRegex = regexp.MustCompile(`^[A-Z]\.\s+([A-Z]+):`)
	priorityLineRegex  = regexp.MustCompile(`^\s*(\d+)\.\s+(.*)$`)
	issueRefRegex      = regexp.MustCompile(`(?:([a-zA-Z0-9_-]+)#)?#?(\d+)`)
)

// ParseSprintPriorities parses the human-written sprint priorities text
// (such as reports/sprint-priorities-2026-09-20.txt) into structured SprintPriority items.
func ParseSprintPriorities(text string, defaultRepo string) ([]SprintPriority, error) {
	if defaultRepo == "" {
		defaultRepo = "mas-bandwidth/nova-tools"
	}

	scanner := bufio.NewScanner(strings.NewReader(text))
	var priorities []SprintPriority
	currentSection := ""

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "---") || strings.HasPrefix(trimmed, "===") {
			continue
		}

		// Detect section headers, e.g. "A. RELIABLE: ..."
		if sectionHeaderRegex.MatchString(trimmed) {
			currentSection = trimmed
			continue
		}

		// Match priority lines: e.g. " 1. #1945  fill: fail-closed load..."
		// or "15. schema#1376  go leg: a card-added test..."
		m := priorityLineRegex.FindStringSubmatch(line)
		if len(m) == 3 {
			rank, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			// Only accept numbered sprint rows 1..50
			if rank < 1 || rank > 50 {
				continue
			}

			content := strings.TrimSpace(m[2])
			// Extract title and refs
			refs, title := extractRefsAndTitle(content, defaultRepo)

			priorities = append(priorities, SprintPriority{
				Rank:    rank,
				Section: currentSection,
				Title:   title,
				Refs:    refs,
				RawText: line,
			})
		}
	}

	return priorities, scanner.Err()
}

func extractRefsAndTitle(content string, defaultRepo string) ([]SprintRef, string) {
	// e.g. "#1945  fill: fail-closed load..."
	// or "#2045 + #2040  launch: attempt identity..."
	// or "#1615 #1635 #1712  the spend cap..."
	// or "schema#1376  go leg: ..."
	// or "Jev in use: #1775 ..."
	var refs []SprintRef
	seen := make(map[string]bool)

	// Split by double space or colon to separate lead issue references from title
	parts := strings.Fields(content)
	titleStartIdx := 0

	for i, token := range parts {
		cleaned := strings.Trim(token, " ,:;+")
		if cleaned == "" {
			continue
		}

		// Check if token matches repo#number or #number
		if strings.HasPrefix(cleaned, "#") || strings.Contains(cleaned, "#") {
			r := parseSingleRef(cleaned, defaultRepo)
			if r.IssueNumber > 0 {
				key := r.String()
				if !seen[key] {
					seen[key] = true
					refs = append(refs, r)
				}
				titleStartIdx = i + 1
				continue
			}
		}
		// If we already found some refs and hit a non-ref word (that isn't "+"), title begins here
		if len(refs) > 0 && token != "+" {
			titleStartIdx = i
			break
		}
	}

	title := ""
	if titleStartIdx < len(parts) {
		title = strings.Join(parts[titleStartIdx:], " ")
	} else {
		title = content
	}

	// Clean up title
	title = strings.TrimPrefix(title, ": ")
	title = strings.TrimPrefix(title, "- ")
	title = strings.TrimSpace(title)

	return refs, title
}

func parseSingleRef(token, defaultRepo string) SprintRef {
	// e.g. "#1945" or "schema#1376" or "mas-bandwidth/schema#1376"
	parts := strings.Split(token, "#")
	if len(parts) != 2 {
		return SprintRef{}
	}
	repoPart := parts[0]
	numPart := parts[1]

	num, err := strconv.Atoi(numPart)
	if err != nil || num <= 0 {
		return SprintRef{}
	}

	repo := defaultRepo
	if repoPart != "" {
		if strings.Contains(repoPart, "/") {
			repo = repoPart
		} else {
			// Shorthand "schema" -> "mas-bandwidth/schema"
			repo = "mas-bandwidth/" + repoPart
		}
	}

	return SprintRef{
		Repo:        repo,
		IssueNumber: num,
	}
}

// BuildSprintPriorityNodes creates work nodes in the store representing sprint priorities.
// Each priority node's Needs list contains the UIDs of the imported issue nodes it depends on.
func BuildSprintPriorityNodes(store *Store, priorities []SprintPriority) ([]*WorkNode, error) {
	var nodes []*WorkNode
	for _, p := range priorities {
		uid, err := MintUID()
		if err != nil {
			return nil, fmt.Errorf("minting uid for sprint priority row %d: %w", p.Rank, err)
		}

		var needUIDs []string
		for _, ref := range p.Refs {
			if node, ok := store.GetNodeByIssue(ref.Repo, ref.IssueNumber); ok {
				needUIDs = append(needUIDs, node.UID)
			}
		}

		node := &WorkNode{
			UID:         uid,
			ID:          fmt.Sprintf("sprint/priority-%02d", p.Rank),
			Provider:    "sprint",
			Repo:        "sprint-priorities",
			IssueNumber: p.Rank,
			Title:       p.Title,
			State:       "open",
			Needs:       needUIDs,
			Evidence: []string{
				fmt.Sprintf("sprint:section=%q", p.Section),
			},
		}

		if err := store.AddNode(node); err != nil {
			return nil, fmt.Errorf("adding sprint priority node %d: %w", p.Rank, err)
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// QuerySprintCompletion evaluates each sprint priority row against the imported issue nodes in the store.
// A row is considered complete ("done") if and only if all of its referenced issues exist in the store
// and are in a closed/settled state.
func QuerySprintCompletion(store *Store, priorities []SprintPriority) *SprintQueryResult {
	result := &SprintQueryResult{
		TotalPriorities: len(priorities),
	}

	allRefsSeen := make(map[string]bool)
	allRefsCompleted := make(map[string]bool)

	for _, p := range priorities {
		row := SprintRowStatus{
			Rank:    p.Rank,
			Section: p.Section,
			Title:   p.Title,
			Refs:    p.Refs,
		}

		rowAllDone := true
		if len(p.Refs) == 0 {
			// A row without issues can't be resolved mechanically from issue states
			rowAllDone = false
			row.State = "open"
		}

		for _, ref := range p.Refs {
			refKey := ref.String()
			allRefsSeen[refKey] = true

			node, found := store.GetNodeByIssue(ref.Repo, ref.IssueNumber)
			if !found {
				rowAllDone = false
				row.UnresolvedIssues = append(row.UnresolvedIssues, ref)
				continue
			}

			st := strings.ToLower(node.State)
			if st == "closed" || st == "settled" {
				allRefsCompleted[refKey] = true
			} else {
				rowAllDone = false
				row.UnresolvedIssues = append(row.UnresolvedIssues, ref)
			}
		}

		if rowAllDone && len(p.Refs) > 0 {
			row.State = "done"
			result.CompletedPriorities++
		} else if len(row.UnresolvedIssues) < len(p.Refs) && len(row.UnresolvedIssues) > 0 {
			row.State = "in_progress"
		} else {
			row.State = "open"
		}

		result.Rows = append(result.Rows, row)
	}

	result.TotalReferencedIssues = len(allRefsSeen)
	result.CompletedReferencedIssues = len(allRefsCompleted)

	if result.TotalPriorities > 0 {
		result.CompletionPercentage = (float64(result.CompletedPriorities) / float64(result.TotalPriorities)) * 100.0
	}
	if result.TotalReferencedIssues > 0 {
		result.IssueCompletionPercentage = (float64(result.CompletedReferencedIssues) / float64(result.TotalReferencedIssues)) * 100.0
	}

	return result
}
