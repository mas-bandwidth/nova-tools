package pulse

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
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
	return typedrec.CleanResultSection(resultLines, report)
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
// If fullBody contains a provenance table, the table is preserved intact at the end
// by truncating the content preceding it.
func boundPRBody(fullBody string, max int) string {
	if max <= 0 {
		max = 4096
	}
	if len(fullBody) <= max {
		return fullBody
	}

	idx := strings.LastIndex(fullBody, "| model | route | bench | cost |")
	if idx == -1 {
		idx = strings.LastIndex(fullBody, "| Model | Route | Bench | Cost |")
	}
	if idx != -1 {
		table := fullBody[idx:]
		prefix := strings.TrimRight(fullBody[:idx], "\n")
		needed := len(table) + 2 // for "\n\n"
		if max > needed {
			allowedPrefix := max - needed
			if len(prefix) > allowedPrefix {
				prefix = strings.TrimRight(prefix[:allowedPrefix], "\n")
			}
			if prefix != "" {
				return prefix + "\n\n" + table
			}
			return table
		}
	}

	return fullBody[:max]
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
func extractJobProvenance(dir, benchName string, resultLines []string, usageRaw string) Provenance {
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
		if strings.TrimSpace(benchName) != "" {
			prov.Bench = strings.TrimSpace(benchName)
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
func extractHarvestProvenance(jobDir string, c CardRow, benchName string, resultLines []string) Provenance {
	prov := extractJobProvenance(jobDir, benchName, resultLines, "")
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
