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

func extractMarkdown(form worklang.Form) (string, error) {
	var out bytes.Buffer

	verification, ok := plistAny(form.List, "verification")
	if !ok {
		return "", fmt.Errorf("missing verification form")
	}
	revStr, ok := plistString(verification.List, "revision")
	if !ok {
		return "", fmt.Errorf("missing revision string")
	}
	revShort := revStr
	if len(revShort) > 8 {
		revShort = revShort[:8]
	}
	suite, ok := plistString(verification.List, "suite")
	if !ok {
		return "", fmt.Errorf("missing suite string")
	}
	suiteResult, ok := plistString(verification.List, "suite-result")
	if !ok {
		return "", fmt.Errorf("missing suite-result string")
	}

	if strings.HasPrefix(suite, "cd lisp/nova-work && ") {
		suite = "lisp/nova-work/" + strings.TrimPrefix(suite, "cd lisp/nova-work && ./")
	}

	var total, pass int
	_, err := fmt.Sscanf(suiteResult, "NOVA-WORK SLICE1 total=%d pass=%d", &total, &pass)
	if err != nil {
		return "", fmt.Errorf("failed to parse suiteResult: %w", err)
	}
	testsCount := ""
	if total > 0 {
		testsCount = fmt.Sprintf("%d/%d", pass, total)
	}

	byFeature, ok := plistAny(verification.List, "by-feature")
	if !ok {
		return "", fmt.Errorf("missing by-feature form")
	}
	featureStates := make(map[string]roadmapByFeature)
	featureCriteria := make(map[string][]worklang.Form)
	featureTests := make(map[string]string)

	for _, entry := range byFeature.List {
		if entry.Kind != worklang.List {
			continue
		}
		id, idOk := plistString(entry.List, "feature")
		verified, vOk := plistInt(entry.List, "verified")
		total, tOk := plistInt(entry.List, "total")
		tests, testsOk := plistString(entry.List, "tests")
		if !idOk || !vOk || !tOk {
			continue
		}
		featureStates[id] = roadmapByFeature{ID: id, Verified: verified, Total: total}
		if testsOk {
			featureTests[id] = tests
		}

		crit, critOk := plistAny(entry.List, "criteria")
		if critOk && crit.Kind == worklang.List {
			featureCriteria[id] = crit.List
		}
	}

	epics, ok := plistAny(form.List, "epics")
	if !ok {
		return "", fmt.Errorf("missing epics form")
	}
	for i, epicNode := range epics.List {
		if epicNode.Kind != worklang.List {
			continue
		}
		epicId, idOk := plistString(epicNode.List, "id")
		epicTitle, titleOk := plistString(epicNode.List, "title")
		if !idOk || !titleOk {
			continue
		}

		if i > 0 {
			fmt.Fprintf(&out, "\n<a id=\"%s\"></a>\n\n", oneline.Escape(strings.ToLower(epicId)))
		}

		fmt.Fprintf(&out, "### %s\n\n", oneline.Escape(epicTitle))
		fmt.Fprint(&out, "| Feature | Criteria verified | Verified |\n")
		fmt.Fprint(&out, "|---|:---:|:---:|\n")

		features, featOk := plistAny(epicNode.List, "features")
		if !featOk {
			continue
		}
		for _, featNode := range features.List {
			if featNode.Kind != worklang.List {
				continue
			}
			fId, fIdOk := plistString(featNode.List, "id")
			fTitle, fTitleOk := plistString(featNode.List, "title")
			if !fIdOk || !fTitleOk {
				continue
			}
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
			fId, fIdOk := plistString(featNode.List, "id")
			fTitle, fTitleOk := plistString(featNode.List, "title")
			if !fIdOk || !fTitleOk {
				continue
			}

			if j > 0 {
				fmt.Fprint(&out, "\n")
			}

			fmt.Fprintf(&out, "**%s — %s**\n\n", oneline.Escape(fId), oneline.Escape(fTitle))

			depsList, depsOk := plistAny(featNode.List, "depends-on")
			if depsOk && depsList.Kind == worklang.List && len(depsList.List) > 0 {
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
				cState, stateOk := plistString(critNode.List, "state")
				cText, textOk := plistString(critNode.List, "text")
				if !stateOk || !textOk {
					continue
				}
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

	return out.String(), nil
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

	md, err := extractMarkdown(form)
	if err != nil {
		fmt.Fprintf(stderr, "ROADMAP FAIL extracting markdown: %v\n", oneline.Err(err))
		return 2
	}

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
