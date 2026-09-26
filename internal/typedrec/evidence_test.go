package typedrec_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

func findRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repository root")
		}
		dir = parent
	}
}

// 1. TestSpecSwarmContractMatches verifies that docs/SPEC-SWARM.md matches
// typedrec.Contract.Markdown() byte for byte between the typedrec markers.
func TestSpecSwarmContractMatches(t *testing.T) {
	t.Parallel()

	root := findRoot(t)
	specPath := filepath.Join(root, "docs", "SPEC-SWARM.md")
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read SPEC-SWARM.md: %v", err)
	}
	s := string(data)
	const beginMarker = "<!-- typedrec:begin -->\n"
	const endMarker = "<!-- typedrec:end -->"
	begin := strings.Index(s, beginMarker)
	if begin == -1 {
		t.Fatal("<!-- typedrec:begin --> not found in docs/SPEC-SWARM.md")
	}
	begin += len(beginMarker)
	end := strings.Index(s[begin:], endMarker)
	if end == -1 {
		t.Fatal("<!-- typedrec:end --> not found in docs/SPEC-SWARM.md")
	}
	got := s[begin : begin+end]
	want := typedrec.Contract.Markdown()
	if got != want {
		t.Fatalf("docs/SPEC-SWARM.md drift:\n--- GOT ---\n%s\n--- WANT ---\n%s", got, want)
	}
}

// 2. TestRoundTripEachKind verifies that each kind's exemplar parses as valid.
func TestRoundTripEachKind(t *testing.T) {
	t.Parallel()

	kinds := []string{"fix", "recut", "port", "docs-guard", "report", "read"}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			ex := typedrec.Exemplar(kind)
			res := typedrec.ParseResult([]byte(ex))
			if !res.Valid {
				t.Fatalf("exemplar for %s failed validation: field=%s defect=%s line=%d", kind, res.Field, res.Defect, res.Line)
			}
			if res.Kind != kind {
				t.Fatalf("kind = %s, want %s", res.Kind, kind)
			}
			if res.Status != "DONE" {
				t.Fatalf("status = %s, want DONE", res.Status)
			}
			if res.Schema != "v2" {
				t.Fatalf("schema = %s, want v2", res.Schema)
			}
			if res.Check != "pass" {
				t.Fatalf("check = %s, want pass", res.Check)
			}
			// Check required fields are present in claims
			fields := typedrec.Fields(kind)
			for _, f := range fields {
				req := typedrec.Contract.RequirementFor(f, kind)
				if req == typedrec.ReqRequired || req == typedrec.ReqDone || (req == typedrec.ReqPass && res.Check == "pass") {
					if _, ok := res.Claims[f]; !ok {
						t.Errorf("missing claim %s in parsed exemplar for %s", f, kind)
					}
				}
			}
		})
	}
}

// 3. TestLegacyAdapterPreservesMeaning tests the legacy adapter against 10 distinct legacy formats.
func TestLegacyAdapterPreservesMeaning(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		content    string
		wantStatus string
		wantBranch string
		wantRepo   string
	}{
		{
			name:       "legacy space syntax DONE",
			content:    "card c1\nDONE\nBRANCH nova/s1/c1-a1\nREPO mas-bandwidth/nova-tools\nPATHS internal/pkg\nCHECK pass\nRED fail\nGREEN pass\n",
			wantStatus: "DONE",
			wantBranch: "nova/s1/c1-a1",
			wantRepo:   "mas-bandwidth/nova-tools",
		},
		{
			name:       "legacy colon without SCHEMA",
			content:    "card c2\nDONE\nBRANCH: nova/s1/c2-a1\nREPO: mas-bandwidth/nova-tools\nPATHS: cmd/tool\nCHECK: pass\nRED: bad\nGREEN: good\n",
			wantStatus: "DONE",
			wantBranch: "nova/s1/c2-a1",
			wantRepo:   "mas-bandwidth/nova-tools",
		},
		{
			name:       "legacy ABSTAIN with reason",
			content:    "card c3\nABSTAIN could not reproduce\nREPO mas-bandwidth/nova-tools\n",
			wantStatus: "ABSTAIN",
			wantRepo:   "mas-bandwidth/nova-tools",
		},
		{
			name:       "legacy BLOCKED with reason",
			content:    "card c4\nBLOCKED missing credentials\nREPO mas-bandwidth/nova-tools\n",
			wantStatus: "BLOCKED",
			wantRepo:   "mas-bandwidth/nova-tools",
		},
		{
			name:       "legacy DONE with CHECK fail",
			content:    "card c5\nDONE\nBRANCH nova/s1/c5-a1\nREPO mas-bandwidth/nova-tools\nCHECK fail\nRED tests failed\n",
			wantStatus: "DONE",
			wantBranch: "nova/s1/c5-a1",
			wantRepo:   "mas-bandwidth/nova-tools",
		},
		{
			name:       "legacy DONE with CHECK not-run",
			content:    "card c6\nDONE\nBRANCH nova/s1/c6-a1\nREPO mas-bandwidth/nova-tools\nCHECK not-run\n",
			wantStatus: "DONE",
			wantBranch: "nova/s1/c6-a1",
			wantRepo:   "mas-bandwidth/nova-tools",
		},
		{
			name:       "legacy carriage returns CRLF",
			content:    "card c7\r\nDONE\r\nBRANCH nova/s1/c7-a1\r\nREPO mas-bandwidth/nova-tools\r\nCHECK pass\r\n",
			wantStatus: "DONE",
			wantBranch: "nova/s1/c7-a1",
			wantRepo:   "mas-bandwidth/nova-tools",
		},
		{
			name:       "legacy with comments and prose",
			content:    "card c8\nDONE\n# A comment\nBRANCH nova/s1/c8-a1\nREPO mas-bandwidth/nova-tools\nCHECK pass\n\nSome explanation here.\n",
			wantStatus: "DONE",
			wantBranch: "nova/s1/c8-a1",
			wantRepo:   "mas-bandwidth/nova-tools",
		},
		{
			name:       "legacy with extra whitespace",
			content:    "card c9\nDONE\n  BRANCH  nova/s1/c9-a1  \n  REPO  mas-bandwidth/nova-tools  \nCHECK pass\n",
			wantStatus: "DONE",
			wantBranch: "nova/s1/c9-a1",
			wantRepo:   "mas-bandwidth/nova-tools",
		},
		{
			name:       "legacy disposition claim in text",
			content:    "card c10\nDONE\nBRANCH nova/s1/c10-a1\nREPO mas-bandwidth/nova-tools\nDISPOSITION who=stella head=1234567890123456789012345678901234567890 verdict=APPROVE\n",
			wantStatus: "DONE",
			wantBranch: "nova/s1/c10-a1",
			wantRepo:   "mas-bandwidth/nova-tools",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.content)
			st := typedrec.Status(raw)
			if st != tc.wantStatus {
				t.Fatalf("Status = %q, want %q", st, tc.wantStatus)
			}
			legSt := typedrec.LegacyStatus(raw)
			if legSt != tc.wantStatus {
				t.Fatalf("LegacyStatus = %q, want %q", legSt, tc.wantStatus)
			}
			lines := strings.Split(tc.content, "\n")
			branch, repo := typedrec.BranchAndRepo(lines)
			if tc.wantBranch != "" && branch != tc.wantBranch {
				t.Fatalf("Branch = %q, want %q", branch, tc.wantBranch)
			}
			if tc.wantRepo != "" && repo != tc.wantRepo {
				t.Fatalf("Repo = %q, want %q", repo, tc.wantRepo)
			}
		})
	}
}

func makeDoc(kind, status, check string, fields map[string]string, evidence string) string {
	var b strings.Builder
	b.WriteString("RESULT CARD-100 sha=1234567890ab repo/name: description\n")
	if status == "DONE" {
		b.WriteString("DONE\n")
	} else {
		b.WriteString(status + "\n")
	}
	b.WriteString("SCHEMA: v2\n")
	b.WriteString("KIND: " + kind + "\n")
	b.WriteString("ATTEMPT: 1\n")
	b.WriteString("CHECK: " + check + "\n")
	b.WriteString("REPO: mas-bandwidth/nova-tools\n")

	for k, v := range fields {
		if k == "SCHEMA" || k == "KIND" || k == "ATTEMPT" || k == "CHECK" || k == "REPO" {
			continue
		}
		b.WriteString(k + ": " + v + "\n")
	}

	if evidence != "" {
		b.WriteString(evidence)
	}
	return b.String()
}

// 4. TestRefusalDefects tests one row per defect and kind-specific field.
func TestRefusalDefects(t *testing.T) {
	t.Parallel()

	t.Run("missing required field", func(t *testing.T) {
		reqFields := []string{"SCHEMA", "ATTEMPT", "CHECK", "REPO", "BRANCH", "PATHS", "RED", "GREEN"}
		for _, f := range reqFields {
			doc := makeDoc("fix", "DONE", "pass", map[string]string{
				"BRANCH": "nova/s1/c1-a1",
				"PATHS":  "internal/pkg",
				"RED":    "failed",
				"GREEN":  "passed",
			}, "## Gates\n- pass\n## Left owed\n- none\n")

			// Remove the field line
			var filtered []string
			for _, line := range strings.Split(doc, "\n") {
				if strings.HasPrefix(line, f+":") {
					continue
				}
				filtered = append(filtered, line)
			}
			res := typedrec.ParseResult([]byte(strings.Join(filtered, "\n")))
			if res.Valid {
				t.Fatalf("expected invalid for missing %s", f)
			}
			if res.Defect != typedrec.DefectMissing || res.Field != f {
				t.Fatalf("field %s got field=%s defect=%s, want %s missing", f, res.Field, res.Defect, f)
			}
		}
	})

	t.Run("unknown field", func(t *testing.T) {
		doc := makeDoc("fix", "DONE", "pass", map[string]string{
			"BRANCH": "nova/s1/c1-a1",
			"PATHS":  "internal/pkg",
			"RED":    "failed",
			"GREEN":  "passed",
			"HEAD":   "0123456789abcdef0123456789abcdef01234567",
		}, "## Gates\n- pass\n## Left owed\n- none\n")
		res := typedrec.ParseResult([]byte(doc))
		if res.Valid {
			t.Fatal("expected invalid for unknown field HEAD on fix")
		}
		if res.Defect != typedrec.DefectUnknown || res.Field != "HEAD" {
			t.Fatalf("got field=%s defect=%s, want HEAD unknown", res.Field, res.Defect)
		}
	})

	t.Run("duplicate field", func(t *testing.T) {
		doc := makeDoc("fix", "DONE", "pass", map[string]string{
			"BRANCH": "nova/s1/c1-a1",
			"PATHS":  "internal/pkg",
			"RED":    "failed",
			"GREEN":  "passed",
		}, "## Gates\n- pass\n## Left owed\n- none\n")
		doc = strings.Replace(doc, "REPO: mas-bandwidth/nova-tools\n", "REPO: mas-bandwidth/nova-tools\nREPO: mas-bandwidth/nova-tools\n", 1)
		res := typedrec.ParseResult([]byte(doc))
		if res.Valid {
			t.Fatal("expected invalid for duplicate REPO")
		}
		if res.Defect != typedrec.DefectDuplicate || res.Field != "REPO" {
			t.Fatalf("got field=%s defect=%s, want REPO duplicate", res.Field, res.Defect)
		}
	})

	t.Run("malformed field", func(t *testing.T) {
		cases := []struct {
			field string
			fMap  map[string]string
		}{
			{"ATTEMPT", map[string]string{"ATTEMPT": "abc", "BRANCH": "nova/s1/c1-a1", "PATHS": "internal/pkg", "RED": "f", "GREEN": "p"}},
			{"REPO", map[string]string{"REPO": "bad repo!", "BRANCH": "nova/s1/c1-a1", "PATHS": "internal/pkg", "RED": "f", "GREEN": "p"}},
			{"CHECK", map[string]string{"CHECK": "maybe", "BRANCH": "nova/s1/c1-a1", "PATHS": "internal/pkg", "RED": "f", "GREEN": "p"}},
			{"PATHS", map[string]string{"PATHS": "../escape", "BRANCH": "nova/s1/c1-a1", "RED": "f", "GREEN": "p"}},
		}
		for _, tc := range cases {
			doc := makeDoc("fix", "DONE", "pass", tc.fMap, "## Gates\n- pass\n## Left owed\n- none\n")
			// If replacing REPO or ATTEMPT or CHECK:
			for k, v := range tc.fMap {
				if k == "REPO" || k == "ATTEMPT" || k == "CHECK" {
					doc = strings.Replace(doc, k+": "+findDefault(k), k+": "+v, 1)
				}
			}
			res := typedrec.ParseResult([]byte(doc))
			if res.Valid {
				t.Fatalf("expected invalid for malformed %s", tc.field)
			}
			if res.Defect != typedrec.DefectMalformed || res.Field != tc.field {
				t.Fatalf("field %s got field=%s defect=%s, want %s malformed", tc.field, res.Field, res.Defect, tc.field)
			}
		}
	})

	t.Run("contradictory field", func(t *testing.T) {
		doc := makeDoc("fix", "DONE", "pass", map[string]string{
			"BRANCH": "nova/s1/c1-a1",
			"PATHS":  "internal/pkg",
			"RED":    "failed",
			"GREEN":  "passed",
		}, "## Gates\n- pass\n## Left owed\n- none\n")
		res := typedrec.ParseResult([]byte(doc), typedrec.ParseOptions{
			ExpectedRepo: "other/repo",
		})
		if res.Valid {
			t.Fatal("expected invalid for contradictory repo")
		}
		if res.Defect != typedrec.DefectContradictory || res.Field != "REPO" {
			t.Fatalf("got field=%s defect=%s, want REPO contradictory", res.Field, res.Defect)
		}
	})

	t.Run("oversized file", func(t *testing.T) {
		doc := makeDoc("fix", "DONE", "pass", map[string]string{
			"BRANCH": "nova/s1/c1-a1",
			"PATHS":  "internal/pkg",
			"RED":    "failed",
			"GREEN":  "passed",
		}, "## Gates\n- pass\n## Left owed\n- none\n")
		big := make([]byte, typedrec.MaxFileSize+10)
		copy(big, doc)
		res := typedrec.ParseResult(big)
		if res.Valid {
			t.Fatal("expected invalid for oversized file")
		}
		if res.Defect != typedrec.DefectOversized || res.Field != "file" {
			t.Fatalf("got field=%s defect=%s, want file oversized", res.Field, res.Defect)
		}
	})

	t.Run("oversized evidence", func(t *testing.T) {
		doc := makeDoc("fix", "DONE", "pass", map[string]string{
			"BRANCH": "nova/s1/c1-a1",
			"PATHS":  "internal/pkg",
			"RED":    "failed",
			"GREEN":  "passed",
		}, "## Gates\n- pass\n## Left owed\n- none\n")
		bigEv := doc + "\n" + strings.Repeat("x", typedrec.MaxEvidenceSize+10)
		res := typedrec.ParseResult([]byte(bigEv))
		if res.Valid {
			t.Fatal("expected invalid for oversized evidence")
		}
		if res.Defect != typedrec.DefectOversized || res.Field != "evidence" {
			t.Fatalf("got field=%s defect=%s, want evidence oversized", res.Field, res.Defect)
		}
	})
}

func findDefault(k string) string {
	switch k {
	case "REPO":
		return "mas-bandwidth/nova-tools"
	case "ATTEMPT":
		return "1"
	case "CHECK":
		return "pass"
	}
	return ""
}

// 5. TestEvidenceRows tests the 12 evidence-row cases defined in rev 3.
func TestEvidenceRows(t *testing.T) {
	t.Parallel()

	fixFields := map[string]string{
		"BRANCH": "nova/s1/c1-a1",
		"PATHS":  "internal/pkg",
		"RED":    "failed",
		"GREEN":  "passed",
	}

	t.Run("blank lines between two rows count 2", func(t *testing.T) {
		doc := makeDoc("fix", "DONE", "pass", fixFields, "## Gates\n- row 1\n\n\n- row 2\n## Left owed\n- none\n")
		res := typedrec.ParseResult([]byte(doc))
		if !res.Valid {
			t.Fatalf("expected valid, got defect=%s field=%s line=%d", res.Defect, res.Field, res.Line)
		}
		if len(res.Sections["Gates"]) != 2 {
			t.Fatalf("Gates rows = %d, want 2", len(res.Sections["Gates"]))
		}
	})

	t.Run("prose paragraph alone under ## Gates is missing at heading line", func(t *testing.T) {
		doc := makeDoc("fix", "DONE", "pass", fixFields, "## Gates\nThis is prose without bullets.\n## Left owed\n- none\n")
		res := typedrec.ParseResult([]byte(doc))
		if res.Valid {
			t.Fatal("expected missing rows for ## Gates")
		}
		if res.Defect != typedrec.DefectMissing || res.Field != "## Gates" {
			t.Fatalf("got field=%s defect=%s, want ## Gates missing", res.Field, res.Defect)
		}
	})

	t.Run("nested bullet under a row counts 1", func(t *testing.T) {
		doc := makeDoc("fix", "DONE", "pass", fixFields, "## Gates\n- row 1\n  - nested continuation\n## Left owed\n- none\n")
		res := typedrec.ParseResult([]byte(doc))
		if !res.Valid {
			t.Fatalf("expected valid, got defect=%s field=%s line=%d", res.Defect, res.Field, res.Line)
		}
		if len(res.Sections["Gates"]) != 1 {
			t.Fatalf("Gates rows = %d, want 1", len(res.Sections["Gates"]))
		}
	})

	t.Run("asterisk, plus and numbered bullets count 0", func(t *testing.T) {
		doc := makeDoc("fix", "DONE", "pass", fixFields, "## Gates\n* asterisk\n+ plus\n1. numbered\n## Left owed\n- none\n")
		res := typedrec.ParseResult([]byte(doc))
		if res.Valid {
			t.Fatal("expected invalid when only non-hyphen bullets are present")
		}
		if res.Defect != typedrec.DefectMissing || res.Field != "## Gates" {
			t.Fatalf("got field=%s defect=%s, want ## Gates missing", res.Field, res.Defect)
		}
	})

	t.Run("three-line table under ## Findings with FINDINGS=1 is contradictory", func(t *testing.T) {
		readFields := map[string]string{
			"PR":       "123",
			"HEAD":     "0123456789abcdef0123456789abcdef01234567",
			"FINDINGS": "1",
			"FLOOR":    "HIGH",
			"SUGGEST":  "HOLD",
		}
		doc := makeDoc("read", "DONE", "pass", readFields, "## Findings\n| Col1 | Col2 |\n|---|---|\n| a | b |\n")
		res := typedrec.ParseResult([]byte(doc))
		if res.Valid {
			t.Fatal("expected contradictory for table rows under ## Findings")
		}
		if res.Defect != typedrec.DefectContradictory || res.Field != "FINDINGS" {
			t.Fatalf("got field=%s defect=%s, want FINDINGS contradictory", res.Field, res.Defect)
		}
	})

	t.Run("second ## Gates is duplicate at second heading line", func(t *testing.T) {
		doc := makeDoc("fix", "DONE", "pass", fixFields, "## Gates\n- row 1\n## Gates\n- duplicate row\n## Left owed\n- none\n")
		res := typedrec.ParseResult([]byte(doc))
		if res.Valid {
			t.Fatal("expected duplicate ## Gates")
		}
		if res.Defect != typedrec.DefectDuplicate || res.Field != "## Gates" {
			t.Fatalf("got field=%s defect=%s, want ## Gates duplicate", res.Field, res.Defect)
		}
	})

	t.Run("FINDINGS=0 with heading and none is valid", func(t *testing.T) {
		readFields := map[string]string{
			"PR":       "123",
			"HEAD":     "0123456789abcdef0123456789abcdef01234567",
			"FINDINGS": "0",
			"FLOOR":    "NONE",
			"SUGGEST":  "APPROVE",
		}
		doc := makeDoc("read", "DONE", "pass", readFields, "## Findings\nnone.\n")
		res := typedrec.ParseResult([]byte(doc))
		if !res.Valid {
			t.Fatalf("expected valid for FINDINGS=0 with 'none.', got defect=%s field=%s line=%d", res.Defect, res.Field, res.Line)
		}
	})

	t.Run("FINDINGS=0 with one row is contradictory", func(t *testing.T) {
		readFields := map[string]string{
			"PR":       "123",
			"HEAD":     "0123456789abcdef0123456789abcdef01234567",
			"FINDINGS": "0",
			"FLOOR":    "NONE",
			"SUGGEST":  "APPROVE",
		}
		doc := makeDoc("read", "DONE", "pass", readFields, "## Findings\n- finding 1\n")
		res := typedrec.ParseResult([]byte(doc))
		if res.Valid {
			t.Fatal("expected contradictory for FINDINGS=0 with 1 row")
		}
		if res.Defect != typedrec.DefectContradictory || res.Field != "FINDINGS" {
			t.Fatalf("got field=%s defect=%s, want FINDINGS contradictory", res.Field, res.Defect)
		}
	})

	t.Run("FINDINGS=0 with no heading is missing line 0", func(t *testing.T) {
		readFields := map[string]string{
			"PR":       "123",
			"HEAD":     "0123456789abcdef0123456789abcdef01234567",
			"FINDINGS": "0",
			"FLOOR":    "NONE",
			"SUGGEST":  "APPROVE",
		}
		doc := makeDoc("read", "DONE", "pass", readFields, "")
		res := typedrec.ParseResult([]byte(doc))
		if res.Valid {
			t.Fatal("expected missing for FINDINGS=0 without heading")
		}
		if res.Defect != typedrec.DefectMissing || res.Field != "## Findings" || res.Line != 0 {
			t.Fatalf("got field=%s defect=%s line=%d, want ## Findings missing line 0", res.Field, res.Defect, res.Line)
		}
	})

	t.Run("fence holding bullet and ## Gates adds no row and no heading", func(t *testing.T) {
		doc := makeDoc("fix", "DONE", "pass", fixFields, "## Notes\n```\n## Gates\n- all tests pass\n```\n## Left owed\n- none\n")
		res := typedrec.ParseResult([]byte(doc))
		if res.Valid {
			t.Fatal("expected missing ## Gates when enclosed in code fence")
		}
		if res.Defect != typedrec.DefectMissing || res.Field != "## Gates" {
			t.Fatalf("got field=%s defect=%s, want ## Gates missing", res.Field, res.Defect)
		}
	})

	t.Run("### Sub inside section is neither row nor section end", func(t *testing.T) {
		doc := makeDoc("fix", "DONE", "pass", fixFields, "## Gates\n### Subheading\n- row 1\n## Left owed\n- none\n")
		res := typedrec.ParseResult([]byte(doc))
		if !res.Valid {
			t.Fatalf("expected valid with ### sub, got defect=%s field=%s line=%d", res.Defect, res.Field, res.Line)
		}
		if len(res.Sections["Gates"]) != 1 {
			t.Fatalf("Gates rows = %d, want 1", len(res.Sections["Gates"]))
		}
	})

	t.Run("trailing space or lowercase heading is missing", func(t *testing.T) {
		readFields := map[string]string{
			"PR":       "123",
			"HEAD":     "0123456789abcdef0123456789abcdef01234567",
			"FINDINGS": "1",
			"FLOOR":    "HIGH",
			"SUGGEST":  "HOLD",
		}
		docSpace := makeDoc("read", "DONE", "pass", readFields, "## Findings \n- row 1\n")
		res1 := typedrec.ParseResult([]byte(docSpace))
		if res1.Valid || res1.Defect != typedrec.DefectMissing {
			t.Fatalf("expected missing for trailing space heading, got valid=%v defect=%s", res1.Valid, res1.Defect)
		}

		docLower := makeDoc("read", "DONE", "pass", readFields, "## findings\n- row 1\n")
		res2 := typedrec.ParseResult([]byte(docLower))
		if res2.Valid || res2.Defect != typedrec.DefectMissing {
			t.Fatalf("expected missing for lowercase heading, got valid=%v defect=%s", res2.Valid, res2.Defect)
		}
	})

	t.Run("-x and hyphen space are not rows", func(t *testing.T) {
		doc := makeDoc("fix", "DONE", "pass", fixFields, "## Gates\n-x\n- \n## Left owed\n- none\n")
		res := typedrec.ParseResult([]byte(doc))
		if res.Valid {
			t.Fatal("expected missing for -x and empty '- '")
		}
		if res.Defect != typedrec.DefectMissing || res.Field != "## Gates" {
			t.Fatalf("got field=%s defect=%s, want ## Gates missing", res.Field, res.Defect)
		}
	})

	t.Run("PROBES count matching vs mismatch", func(t *testing.T) {
		// Valid PROBES=2 with 2 rows
		reportFields2 := map[string]string{
			"PROBES": "2",
		}
		doc2 := makeDoc("report", "DONE", "pass", reportFields2, "## Probes\n- probe 1\n- probe 2\n## Summary\n- summary\n")
		resValid := typedrec.ParseResult([]byte(doc2))
		if !resValid.Valid {
			t.Fatalf("expected valid for PROBES=2 with 2 rows, got defect=%s field=%s", resValid.Defect, resValid.Field)
		}

		// Contradictory PROBES=2 with 3 rows
		doc3 := makeDoc("report", "DONE", "pass", reportFields2, "## Probes\n- probe 1\n- probe 2\n- probe 3\n## Summary\n- summary\n")
		resInvalid := typedrec.ParseResult([]byte(doc3))
		if resInvalid.Valid {
			t.Fatal("expected contradictory for PROBES=2 with 3 rows")
		}
		if resInvalid.Defect != typedrec.DefectContradictory || resInvalid.Field != "PROBES" {
			t.Fatalf("got field=%s defect=%s, want PROBES contradictory", resInvalid.Field, resInvalid.Defect)
		}
	})
}

// 6. TestFailedCheckDoneStaysUnverified verifies that a DONE result with CHECK: fail
// is valid but requires no GREEN line and stays unverified.
func TestFailedCheckDoneStaysUnverified(t *testing.T) {
	t.Parallel()

	fixEx := typedrec.Exemplar("fix")
	// Replace CHECK: pass with CHECK: fail, remove GREEN: pass
	doc := strings.Replace(fixEx, "CHECK: pass\n", "CHECK: fail\n", 1)
	doc = strings.Replace(doc, "GREEN: go test ./internal/pulse -run TestEmptyQueueDoesNotPanic passed in 0.02s\n", "", 1)
	res := typedrec.ParseResult([]byte(doc))
	if !res.Valid {
		t.Fatalf("expected valid for DONE with CHECK: fail, got defect=%s field=%s line=%d", res.Defect, res.Field, res.Line)
	}
	if res.Status != "DONE" {
		t.Fatalf("status = %s, want DONE", res.Status)
	}
	if res.Check != "fail" {
		t.Fatalf("check = %s, want fail", res.Check)
	}
}

// 7. TestForgedProvenanceNotEffective verifies that forged card/machinery facts are rejected.
func TestForgedProvenanceNotEffective(t *testing.T) {
	t.Parallel()

	fixEx := typedrec.Exemplar("fix")

	// ParseResult options contradiction check
	opts := typedrec.ParseOptions{
		ExpectedRepo:   "mas-bandwidth/nova-tools",
		ExpectedBranch: "emma/fix-nil-queue",
	}

	// Forged branch
	docForgedBranch := strings.Replace(fixEx, "BRANCH: emma/fix-nil-queue\n", "BRANCH: emma/forged-branch\n", 1)
	res := typedrec.ParseResult([]byte(docForgedBranch), opts)
	if res.Valid {
		t.Fatal("expected invalid for forged branch")
	}
	if res.Defect != typedrec.DefectContradictory || res.Field != "BRANCH" {
		t.Fatalf("got field=%s defect=%s, want BRANCH contradictory", res.Field, res.Defect)
	}

	// Forged repo
	docForgedRepo := strings.Replace(fixEx, "REPO: mas-bandwidth/nova-tools\n", "REPO: mas-bandwidth/forged\n", 1)
	resRepo := typedrec.ParseResult([]byte(docForgedRepo), opts)
	if resRepo.Valid {
		t.Fatal("expected invalid for forged repo")
	}
	if resRepo.Defect != typedrec.DefectContradictory || resRepo.Field != "REPO" {
		t.Fatalf("got field=%s defect=%s, want REPO contradictory", resRepo.Field, resRepo.Defect)
	}
}
