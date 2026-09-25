// Package main -- the offline reader for docs/roadmaps/nova-work.sexp, the
// restricted-Lisp roadmap the engine and its clients share. Every nova-work
// verb that takes a roadmap reads this file through this one reader
// (nova-tools#2595): no verb re-parses a markdown view or duplicates the
// accepted parsing, and the worklang reader's refusal grammar is the one
// authority over what the file may hold.
//
// The file is a single form: (:schema ... :verification (:by-feature (...) ...)).
// The reader walks the :by-feature list and answers x/y z% per node. The
// structure here is the smallest one a query needs and nothing more -- the
// full roadmap surface lives in the lisp engine and its render projections.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// roadmapByFeature is one (:feature "<id>" :verified <n> :total <m> ...) row
// from the .sexp's :by-feature list. The two numbers are exactly what the
// reader answers, and the percent is derived at print time.
type roadmapByFeature struct {
	ID       string
	Verified int64
	Total    int64
}

// defaultRoadmapSexp is the path the engine's own spec (docs/SPEC-WORK.md:1913,
// docs/SPEC-WORK.md:7825) names: docs/roadmaps/nova-work.sexp. A roadmap verb
// that reads a roadmap reaches this path through one reader and no other path
// (nova-tools#2595).
const defaultRoadmapSexp = "docs/roadmaps/nova-work.sexp"

// readRoadmapByFeature reads the .sexp at PATH under the worklang reader's
// three bounds, walks the (:verification (:by-feature (...))) shape the engine
// writes, and answers one roadmapByFeature per :feature row in the order the
// file holds them. The reader is worklang.Read: a roadmap verb that reads a
// roadmap must call this function and no other reader, so a malformed .sexp
// refuses once, in one refusal line, at the reader's byte offset.
//
// A refusal from the reader -- a past bound, a forbidden token, an unbalanced
// form, a trailing byte -- is returned as an error carrying the reader's
// reason, so the caller can print it on stderr at exit 2.
func readRoadmapByFeature(path string) ([]roadmapByFeature, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// The reader's three bounds: the default 64 KiB / depth 64 / 4096 nodes is
	// too small for docs/roadmaps/nova-work.sexp at its current scale
	// (the :by-feature block alone holds 80 features). The reader admits a
	// past-byte refusal at the bound, and a roadmap verb reading the whole
	// roadmap file names the bound it read to. Widen to the size this file
	// expects; the reader still refuses a file past its byte ceiling.
	limits := worklang.Limits{MaxBytes: 1 << 20, MaxDepth: 64, MaxNodes: 1 << 14}
	form, err := worklang.Read(path, data, limits)
	if err != nil {
		return nil, err
	}
	if form.Kind != worklang.List {
		return nil, fmt.Errorf("plan %s: top-level form is not a list", path)
	}
	verification, ok := plistAny(form.List, "verification")
	if !ok || verification.Kind != worklang.List {
		return nil, fmt.Errorf("plan %s: no :verification list", path)
	}
	byFeature, ok := plistAny(verification.List, "by-feature")
	if !ok || byFeature.Kind != worklang.List {
		return nil, fmt.Errorf("plan %s: no :by-feature list", path)
	}
	out := make([]roadmapByFeature, 0, len(byFeature.List))
	for _, entry := range byFeature.List {
		if entry.Kind != worklang.List {
			continue
		}
		id, _ := plistString(entry.List, "feature")
		verified, vOk := plistInt(entry.List, "verified")
		total, tOk := plistInt(entry.List, "total")
		
		criteriaForm, hasCriteria := plistAny(entry.List, "criteria")
		if hasCriteria && criteriaForm.Kind == worklang.List {
			calcTotal := int64(len(criteriaForm.List))
			calcVerified := int64(0)
			for _, c := range criteriaForm.List {
				if c.Kind == worklang.List {
					st, _ := plistString(c.List, "state")
					if st == "verified" {
						calcVerified++
					}
				}
			}
			if vOk && tOk {
				if verified != calcVerified || total != calcTotal {
					return nil, fmt.Errorf("plan %s: feature %s counter %d/%d disagrees with criteria rows %d/%d", path, id, verified, total, calcVerified, calcTotal)
				}
			} else {
				verified = calcVerified
				total = calcTotal
				vOk = true
				tOk = true
			}
		}

		if id == "" || !vOk || !tOk {
			continue
		}
		out = append(out, roadmapByFeature{ID: id, Verified: verified, Total: total})
	}
	return out, nil
}

// printRoadmapXY writes one line per roadmapByFeature row in the order the
// .sexp holds them: `<id> <verified>/<total> <pct>%`. The percent is the
// integer rounded result of 100*verified/total; a feature with total=0 prints
// `0%` (a percent of nothing is zero) and never divides. Each field is escaped
// through oneline.Field so a feature id with a space stays one machine-scannable
// line, the way the rest of the client's output grammar requires.
//
// filter is the optional --xy <node> value: empty prints every feature, a
// non-empty id prints only the matching row (and refuses at exit 1 when the
// id is not present, naming the missing id and the count the file holds).
func printRoadmapXY(w io.Writer, features []roadmapByFeature, filter string) int {
	if filter != "" {
		for _, f := range features {
			if f.ID == filter {
				fmt.Fprintf(w, "%s\n", oneline.Escape(formatRoadmapXYLine(f)))
				return 0
			}
		}
		
		// Check for epic rollup
		var epicFeats []roadmapByFeature
		for _, f := range features {
			if len(f.ID) > len(filter) && f.ID[:len(filter)] == filter && f.ID[len(filter)] == '-' {
				epicFeats = append(epicFeats, f)
			}
		}
		if len(epicFeats) > 0 {
			fmt.Fprintf(w, "%s\n", oneline.Escape(formatRollupLine(filter, epicFeats)))
			return 0
		}
		
		fmt.Fprintf(w, "QUERY FAIL xy=%s: no such feature in :by-feature (held=%d)\n",
			oneline.Field(filter), len(features))
		return 1
	}
	for _, f := range features {
		fmt.Fprintf(w, "%s\n", oneline.Escape(formatRoadmapXYLine(f)))
	}
	fmt.Fprintf(w, "%s\n", oneline.Escape(formatRollupLine("ROOT", features)))
	return 0
}

func formatRollupLine(id string, feats []roadmapByFeature) string {
	var fVerified, fTotal, cVerified, cTotal int64
	for _, f := range feats {
		fTotal++
		if f.Verified == f.Total && f.Total > 0 {
			fVerified++
		}
		cVerified += f.Verified
		cTotal += f.Total
	}
	fPct := int64(0)
	if fTotal > 0 {
		fPct = (100*fVerified + fTotal/2) / fTotal
	}
	cPct := int64(0)
	if cTotal > 0 {
		cPct = (100*cVerified + cTotal/2) / cTotal
	}
	return fmt.Sprintf("%s %d/%d %d%% %d/%d %d%%", oneline.Field(id), fVerified, fTotal, fPct, cVerified, cTotal, cPct)
}

func formatRoadmapXYLine(f roadmapByFeature) string {
	pct := int64(0)
	if f.Total > 0 {
		// Integer percent, rounded: the same arithmetic the :by-feature
		// block's own cells use elsewhere in the spec.
		pct = (100*f.Verified + f.Total/2) / f.Total
	}
	return fmt.Sprintf("%s %d/%d %d%%", oneline.Field(f.ID), f.Verified, f.Total, pct)
}

// plistAny reads a keyword/value plist at the worklang top level: it is the
// same lookup the worklang reader uses internally, exposed here so a roadmap
// verb can walk the (:verification (:by-feature ...)) shape without depending
// on internal/worklang's unexported helpers.
func plistAny(body []worklang.Form, key string) (worklang.Form, bool) {
	for i := 0; i+1 < len(body); i++ {
		if body[i].IsKeyword(key) {
			return body[i+1], true
		}
	}
	return worklang.Form{}, false
}

func plistString(body []worklang.Form, key string) (string, bool) {
	f, ok := plistAny(body, key)
	if !ok || f.Kind != worklang.String || f.Value == "" {
		return "", false
	}
	return f.Value, true
}

func plistInt(body []worklang.Form, key string) (int64, bool) {
	f, ok := plistAny(body, key)
	if !ok || f.Kind != worklang.Integer {
		return 0, false
	}
	return f.Int, true
}
