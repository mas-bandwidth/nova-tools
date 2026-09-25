package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"

	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

func extractMarkdown(form worklang.Form) string {
	var out bytes.Buffer

	verification, _ := plistAny(form.List, "verification")
	revStr, _ := plistString(verification.List, "revision")
	revShort := revStr
	if len(revShort) > 8 {
		revShort = revShort[:8]
	}
	suite, _ := plistString(verification.List, "suite")
	suiteResult, _ := plistString(verification.List, "suite-result")

	if strings.HasPrefix(suite, "cd lisp/nova-work && ") {
		suite = "lisp/nova-work/" + strings.TrimPrefix(suite, "cd lisp/nova-work && ./")
	}

	var total, pass int
	fmt.Sscanf(suiteResult, "NOVA-WORK SLICE1 total=%d pass=%d", &total, &pass)
	testsCount := ""
	if total > 0 {
		testsCount = fmt.Sprintf("%d/%d", pass, total)
	}

	byFeature, _ := plistAny(verification.List, "by-feature")
	featureStates := make(map[string]roadmapByFeature)
	featureCriteria := make(map[string][]worklang.Form)
	featureTests := make(map[string]string)

	for _, entry := range byFeature.List {
		if entry.Kind != worklang.List {
			continue
		}
		id, _ := plistString(entry.List, "feature")
		verified, vOk := plistInt(entry.List, "verified")
		total, tOk := plistInt(entry.List, "total")
		tests, _ := plistString(entry.List, "tests")
		if id == "" || !vOk || !tOk {
			continue
		}
		featureStates[id] = roadmapByFeature{ID: id, Verified: verified, Total: total}
		featureTests[id] = tests

		crit, _ := plistAny(entry.List, "criteria")
		if crit.Kind == worklang.List {
			featureCriteria[id] = crit.List
		}
	}

	epics, _ := plistAny(form.List, "epics")
	for i, epicNode := range epics.List {
		if epicNode.Kind != worklang.List {
			continue
		}
		epicId, _ := plistString(epicNode.List, "id")
		epicTitle, _ := plistString(epicNode.List, "title")
		_ = epicId

		if i > 0 {
			fmt.Fprintf(&out, "\n<a id=\"%s\"></a>\n\n", oneline.Escape(strings.ToLower(epicId)))
		}

		fmt.Fprintf(&out, "### %s\n\n", oneline.Escape(epicTitle))
		fmt.Fprint(&out, "| Feature | Criteria verified | Verified |\n")
		fmt.Fprint(&out, "|---|:---:|:---:|\n")

		features, _ := plistAny(epicNode.List, "features")
		for _, featNode := range features.List {
			if featNode.Kind != worklang.List {
				continue
			}
			fId, _ := plistString(featNode.List, "id")
			fTitle, _ := plistString(featNode.List, "title")
			state := featureStates[fId]
			status := "❌"
			if state.Total > 0 && state.Verified == state.Total {
				status = "✅"
			}
			fmt.Fprintf(&out, "| %s — %s | %d/%d | %s |\n", oneline.Escape(fId), oneline.Escape(fTitle), state.Verified, state.Total, oneline.Escape(status))
		}

		fmt.Fprint(&out, "\n<details>\n<summary>Sub-features, prerequisites and acceptance scope</summary>\n\n")

		for j, featNode := range features.List {
			if featNode.Kind != worklang.List {
				continue
			}
			fId, _ := plistString(featNode.List, "id")
			fTitle, _ := plistString(featNode.List, "title")

			if j > 0 {
				fmt.Fprint(&out, "\n")
			}

			fmt.Fprintf(&out, "**%s — %s**\n\n", oneline.Escape(fId), oneline.Escape(fTitle))

			depsList, _ := plistAny(featNode.List, "depends-on")
			if depsList.Kind == worklang.List && len(depsList.List) > 0 {
				deps := []string{}
				for _, d := range depsList.List {
					if d.Kind == worklang.String {
						deps = append(deps, d.Value)
					}
				}
				fmt.Fprintf(&out, "Prerequisites: %s.\n\n", oneline.Escape(strings.Join(deps, ", ")))
			} else {
				fmt.Fprint(&out, "Prerequisites: none.\n\n")
			}

			critList := featureCriteria[fId]
			for _, critNode := range critList {
				if critNode.Kind != worklang.List {
					continue
				}
				cState, _ := plistString(critNode.List, "state")
				cText, _ := plistString(critNode.List, "text")
				mark := " "
				if cState == "verified" {
					mark = "x"
				}
				fmt.Fprintf(&out, "- [%s] %s\n", oneline.Escape(mark), oneline.Escape(cText))
			}
				fmt.Fprint(&out, "\n")

			srcList, ok := plistAny(featNode.List, "source-sections")
			if ok && srcList.Kind == worklang.List {
				srcs := []string{}
				for _, s := range srcList.List {
					if s.Kind == worklang.String {
						srcs = append(srcs, s.Value)
					}
				}
				fmt.Fprintf(&out, "Source sections: %s.\n\n", oneline.Escape(strings.Join(srcs, "; ")))
			}

			fTests := featureTests[fId]
			if fTests != "" {
				fmt.Fprintf(&out, "Verified criteria evidence at dev `%s`, suite `%s` (%s): %s.\n\n", oneline.Escape(revShort), oneline.Escape(suite), oneline.Escape(testsCount), oneline.Escape(fTests))
			}
		}
		fmt.Fprint(&out, "</details>\n")
	}

	return out.String()
}

func readRoadmapSexp() (*worklang.Form, error) {
	path := "docs/roadmaps/nova-work.sexp"
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	limits := worklang.Limits{MaxBytes: 1 << 20, MaxDepth: 64, MaxNodes: 1 << 14}
	form, err := worklang.Read(path, data, limits)
	return &form, err
}

func cmdRoadmapCheck(args []string, stdout, stderr io.Writer) int {
	formPtr, err := readRoadmapSexp()
	if err != nil {
		fmt.Fprintf(stderr, "ROADMAP FAIL: %v\n", oneline.Err(err))
		return 2
	}
	form := *formPtr

	md := extractMarkdown(form)

	rPath := "ROADMAP.md"
	rData, err := os.ReadFile(rPath)
	if err != nil {
		fmt.Fprintf(stderr, "ROADMAP FAIL: %v\n", oneline.Err(err))
		return 2
	}
	rContent := string(rData)
	startIdx := strings.Index(rContent, "### Canonical work data and restricted representation")
	endMarker := "\n## Future Plans (v2)"
	endIdx := strings.Index(rContent, endMarker)

	if startIdx == -1 || endIdx == -1 {
		fmt.Fprintln(stderr, "ROADMAP FAIL: could not find markers in ROADMAP.md")
		return 2
	}

	oldText := rContent[startIdx:endIdx]

	if oldText == md {
		fmt.Fprintln(stdout, "ROADMAP OK: generated text matches ROADMAP.md exactly")
		return 0
	}

	newRContent := rContent[:startIdx] + md + rContent[endIdx:]
	err = os.WriteFile(rPath, []byte(newRContent), 0644)
	if err != nil {
		fmt.Fprintf(stderr, "ROADMAP FAIL writing: %v\n", oneline.Err(err))
		return 2
	}

	fmt.Fprintln(stderr, "ROADMAP DIFFERENCE: ROADMAP.md was regenerated from sexp-owned parts because bytes drifted")
	return 1
}
