package pulse

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Provenance holds the execution metadata for a harvested job.
type Provenance struct {
	Model string
	Route string
	Bench string
	Cost  string
}

// Table formats the provenance as a markdown table.
func (p Provenance) Table() string {
	model := strings.TrimSpace(p.Model)
	if model == "" {
		model = "-"
	}
	route := strings.TrimSpace(p.Route)
	if route == "" {
		route = "-"
	}
	bench := strings.TrimSpace(p.Bench)
	if bench == "" {
		bench = "-"
	}
	cost := strings.TrimSpace(p.Cost)
	if cost == "" {
		cost = "-"
	}
	return fmt.Sprintf("| model | route | bench | cost |\n| --- | --- | --- | --- |\n| %s | %s | %s | %s |",
		model, route, bench, cost)
}

// cleanResultSection extracts and orders the essential RESULT.md lines so that:
// line 1 is the contract line, line 2 is DONE, followed by BRANCH, REPO, prior,
// and the verbatim red and green lines, followed by any remaining result lines.
// If red: or green: lines are missing from resultLines, they are looked for in report.
func cleanResultSection(resultLines []string, report string) []string {
	if len(resultLines) == 0 {
		return nil
	}
	var out []string
	line1 := strings.TrimSpace(firstNonEmpty(resultLines))
	out = append(out, line1)

	var remaining []string
	foundLine1 := false
	for _, l := range resultLines {
		t := strings.TrimSpace(l)
		if !foundLine1 && t == line1 {
			foundLine1 = true
			continue
		}
		remaining = append(remaining, l)
	}

	doneLine := ""
	var rest []string
	for i, l := range remaining {
		t := strings.TrimSpace(l)
		if i == 0 && (strings.HasPrefix(t, "DONE") || strings.HasPrefix(t, "done")) {
			doneLine = t
			continue
		}
		if t == "DONE" && doneLine == "" {
			doneLine = t
			continue
		}
		rest = append(rest, l)
	}
	if doneLine != "" {
		out = append(out, doneLine)
	} else if len(remaining) > 0 && strings.TrimSpace(remaining[0]) == "DONE" {
		out = append(out, "DONE")
		rest = remaining[1:]
	}

	var headers []string
	var redLines []string
	var greenLines []string
	var others []string

	for _, l := range rest {
		t := strings.TrimSpace(l)
		lower := strings.ToLower(t)
		switch {
		case strings.HasPrefix(t, "BRANCH ") || strings.HasPrefix(t, "BRANCH:"):
			headers = append(headers, l)
		case strings.HasPrefix(t, "REPO ") || strings.HasPrefix(t, "REPO:"):
			headers = append(headers, l)
		case strings.HasPrefix(lower, "prior:") || strings.HasPrefix(lower, "prior "):
			headers = append(headers, l)
		case strings.HasPrefix(lower, "red:") || strings.HasPrefix(lower, "red "):
			redLines = append(redLines, l)
		case strings.HasPrefix(lower, "green:") || strings.HasPrefix(lower, "green "):
			greenLines = append(greenLines, l)
		default:
			others = append(others, l)
		}
	}

	if len(redLines) == 0 {
		if r := findRedLine(nil, report); r != "" {
			redLines = append(redLines, r)
		}
	}
	if len(greenLines) == 0 {
		if g := findGreenLine(nil, report); g != "" {
			greenLines = append(greenLines, g)
		}
	}

	out = append(out, headers...)
	out = append(out, redLines...)
	out = append(out, greenLines...)
	out = append(out, others...)
	return out
}

// findRedLine finds a red line in resultLines or report.
func findRedLine(resultLines []string, report string) string {
	for _, l := range resultLines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(strings.ToLower(t), "red:") || strings.HasPrefix(strings.ToLower(t), "red ") {
			return t
		}
	}
	for _, l := range strings.Split(report, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(strings.ToLower(t), "red:") || strings.HasPrefix(strings.ToLower(t), "red ") {
			return t
		}
		if strings.HasPrefix(strings.ToUpper(t), "EVIDENCE:") {
			parts := strings.Split(t[len("EVIDENCE:"):], "|")
			for _, p := range parts {
				pt := strings.TrimSpace(p)
				if strings.HasPrefix(strings.ToLower(pt), "red:") || strings.HasPrefix(strings.ToLower(pt), "red ") {
					return pt
				}
			}
		}
	}
	return ""
}

// findGreenLine finds a green line in resultLines or report.
func findGreenLine(resultLines []string, report string) string {
	for _, l := range resultLines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(strings.ToLower(t), "green:") || strings.HasPrefix(strings.ToLower(t), "green ") {
			return t
		}
	}
	for _, l := range strings.Split(report, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(strings.ToLower(t), "green:") || strings.HasPrefix(strings.ToLower(t), "green ") {
			return t
		}
		if strings.HasPrefix(strings.ToUpper(t), "EVIDENCE:") {
			parts := strings.Split(t[len("EVIDENCE:"):], "|")
			for _, p := range parts {
				pt := strings.TrimSpace(p)
				if strings.HasPrefix(strings.ToLower(pt), "green:") || strings.HasPrefix(strings.ToLower(pt), "green ") {
					return pt
				}
			}
		}
	}
	return ""
}

// constructPRBody builds the full PR body text from RESULT.md + REPORT + provenance table.
func constructPRBody(resultLines []string, report string, prov Provenance) string {
	resSection := cleanResultSection(resultLines, report)
	body := strings.Join(resSection, "\n")

	report = strings.TrimSpace(report)
	if report != "" {
		if body != "" {
			body += "\n\n" + report
		} else {
			body = report
		}
	}

	table := prov.Table()
	if body != "" {
		body += "\n\n" + table
	} else {
		body = table
	}

	return body
}

// boundPRBody truncates body to max bytes, ensuring MaxBodyBytes is cleanly respected.
func boundPRBody(fullBody string, max int) string {
	if max <= 0 {
		max = 4096
	}
	if len(fullBody) > max {
		return fullBody[:max]
	}
	return fullBody
}

// buildPRBody constructs the bounded PR body (a prefix of what was constructed/scanned).
func buildPRBody(resultLines []string, report string, prov Provenance, max int) string {
	return boundPRBody(constructPRBody(resultLines, report, prov), max)
}

// hasProvenanceTable reports whether text already contains the provenance table.
func hasProvenanceTable(s string) bool {
	return strings.Contains(s, "| model | route | bench | cost |") ||
		strings.Contains(s, "| Model | Route | Bench | Cost |")
}

// readJobReport reads REPORT.md or REPORT from dir.
func readJobReport(dir string) string {
	if dir == "" {
		return ""
	}
	for _, name := range []string{"REPORT.md", "REPORT"} {
		path := filepath.Join(dir, name)
		if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
			return string(raw)
		}
	}
	return ""
}

// tokenFromLines looks for key: value prefixes in lines.
func tokenFromLines(lines []string, prefixes ...string) string {
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		for _, p := range prefixes {
			if strings.HasPrefix(strings.ToLower(trimmed), strings.ToLower(p)) {
				return strings.TrimSpace(trimmed[len(p):])
			}
		}
	}
	return ""
}

// formatCost formats a cost string into $X.XX or preserves $ / -.
func formatCost(c string) string {
	c = strings.TrimSpace(c)
	if c == "" || c == "-" {
		return "-"
	}
	if strings.HasPrefix(c, "$") {
		return c
	}
	if _, err := strconv.ParseFloat(c, 64); err == nil {
		return "$" + c
	}
	return c
}

// localBenchName returns the bench name from $BENCH or short hostname.
func localBenchName() string {
	if b := strings.TrimSpace(os.Getenv("BENCH")); b != "" {
		return b
	}
	h, err := os.Hostname()
	if err != nil || strings.TrimSpace(h) == "" {
		return "local"
	}
	h = strings.TrimSpace(h)
	if dot := strings.IndexByte(h, '.'); dot > 0 {
		h = h[:dot]
	}
	return h
}

// parseUsageLines extracts model, provider, usd from usage.tsv lines.
func parseUsageLines(lines []string) (model, provider, usd string) {
	if len(lines) < 2 {
		return "", "", ""
	}
	head := strings.Split(lines[0], "\t")
	modelIdx, provIdx, usdIdx := -1, -1, -1
	for i, col := range head {
		switch strings.TrimSpace(col) {
		case "model":
			modelIdx = i
		case "provider":
			provIdx = i
		case "usd":
			usdIdx = i
		}
	}
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		parts := strings.Split(l, "\t")
		if modelIdx >= 0 && modelIdx < len(parts) && strings.TrimSpace(parts[modelIdx]) != "-" {
			model = strings.TrimSpace(parts[modelIdx])
		}
		if provIdx >= 0 && provIdx < len(parts) && strings.TrimSpace(parts[provIdx]) != "-" {
			provider = strings.TrimSpace(parts[provIdx])
		}
		if usdIdx >= 0 && usdIdx < len(parts) && strings.TrimSpace(parts[usdIdx]) != "-" {
			usd = strings.TrimSpace(parts[usdIdx])
		}
		break
	}
	return model, provider, usd
}

// extractJobProvenance resolves the Provenance for a job.
func extractJobProvenance(dir, bench string, resultLines []string, usageRaw string) Provenance {
	var prov Provenance
	prov.Model = tokenFromLines(resultLines, "model:", "MODEL:")
	prov.Route = tokenFromLines(resultLines, "route:", "ROUTE:")
	prov.Bench = tokenFromLines(resultLines, "bench:", "BENCH:")
	prov.Cost = tokenFromLines(resultLines, "cost:", "COST:", "usd:", "USD:")

	var uModel, uProv, uUsd string
	if usageRaw != "" {
		uModel, uProv, uUsd = parseUsageLines(strings.Split(strings.TrimRight(usageRaw, "\n"), "\n"))
	} else if dir != "" {
		if raw, err := os.ReadFile(filepath.Join(dir, "usage.tsv")); err == nil {
			uModel, uProv, uUsd = parseUsageLines(strings.Split(strings.TrimRight(string(raw), "\n"), "\n"))
		}
	}

	if prov.Model == "" {
		prov.Model = uModel
	}
	if prov.Route == "" {
		if uProv != "" && uModel != "" {
			prov.Route = uProv + "/" + uModel
		} else if uProv != "" {
			prov.Route = uProv
		} else if prov.Model != "" {
			prov.Route = prov.Model
		}
	}
	if prov.Bench == "" {
		if strings.TrimSpace(bench) != "" {
			prov.Bench = strings.TrimSpace(bench)
		} else {
			prov.Bench = localBenchName()
		}
	}
	if prov.Cost == "" {
		if uUsd != "" {
			prov.Cost = formatCost(uUsd)
		}
	} else {
		prov.Cost = formatCost(prov.Cost)
	}

	if prov.Model == "" {
		prov.Model = "-"
	}
	if prov.Route == "" {
		prov.Route = "-"
	}
	if prov.Bench == "" {
		prov.Bench = "-"
	}
	if prov.Cost == "" {
		prov.Cost = "-"
	}
	return prov
}

// extractProvenanceFromDir extracts provenance using a directory and optional card file.
func extractProvenanceFromDir(dir, cardPath, inBench string, resultLines []string) Provenance {
	prov := extractJobProvenance(dir, inBench, resultLines, "")
	if cardPath != "" && prov.Route == "-" {
		if raw, err := os.ReadFile(cardPath); err == nil {
			for _, l := range strings.Split(string(raw), "\n") {
				if m, ok := strings.CutPrefix(strings.TrimSpace(l), "MODEL: "); ok && strings.TrimSpace(m) != "" {
					prov.Route = strings.TrimSpace(m)
					if prov.Model == "-" {
						prov.Model = prov.Route
					}
					break
				}
			}
		}
	}
	return prov
}

// extractHarvestProvenance extracts provenance for Harvest using CardRow and jobDir.
func extractHarvestProvenance(jobDir string, c CardRow, bench string, resultLines []string) Provenance {
	prov := extractJobProvenance(jobDir, bench, resultLines, "")
	if prov.Model == "-" && c.Model != "" && c.Model != "-" {
		prov.Model = c.Model
	}
	if prov.Route == "-" {
		if c.Card != "" {
			if raw, err := os.ReadFile(c.Card); err == nil {
				for _, l := range strings.Split(string(raw), "\n") {
					if m, ok := strings.CutPrefix(strings.TrimSpace(l), "MODEL: "); ok && strings.TrimSpace(m) != "" {
						prov.Route = strings.TrimSpace(m)
						break
					}
				}
			}
		}
		if prov.Route == "-" && c.Model != "" && c.Model != "-" {
			prov.Route = c.Model
		}
	}
	return prov
}
