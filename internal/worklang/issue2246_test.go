package worklang_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// TestIssue2246 pins docs/SPEC-WORKLANG.md behaviours 7 and 8 the pinned
// red-first list names but no test has ever proved: the (:derive) sweep and
// the (:fold) projection both expand into the same Card pass as a hand-written
// (:node), through the bounded reader the spec already fixes. The fact set is
// pinned in code -- 25 open no-PR issues plus one PR-bearing issue, N green
// sibling branches plus one non-green -- so the test never touches the
// network and never calls a model; the same facts and the same plan expand
// to byte-identical cards on every run.
func TestIssue2246(t *testing.T) {
	// worklang-derive-expands-to-one-node-per-issue: a (:derive :as
	// "schema/issue-{n}" :kind go-fix :from (:issues ...)) over a pinned fact
	// set of 25 open no-PR issues plus one issue with a PR yields exactly 25
	// nodes and 25 cards, one per open no-PR issue, with `{n}` -- and where
	// present `{slug}` and `{url}` -- substituted from the matched issue. The
	// PR-bearing issue is excluded; it does not mint a node.
	t.Run("worklang-derive-expands-to-one-node-per-issue", func(t *testing.T) {
		issues := []worklang.Issue{}
		for i := int64(1); i <= 25; i++ {
			issues = append(issues, worklang.Issue{
				Number: i,
				Slug:   fmt.Sprintf("schema-versioning-%d", i),
				URL:    fmt.Sprintf("https://github.com/mas-bandwidth/schema/issues/%d", i),
				Repo:   "mas-bandwidth/schema",
				Label:  "schema",
				State:  "open",
				HasPR:  false,
			})
		}
		issues = append(issues, worklang.Issue{
			Number:   26,
			Slug:     "schema-versioning-26-with-pr",
			URL:      "https://github.com/mas-bandwidth/schema/issues/26",
			Repo:     "mas-bandwidth/schema",
			Label:    "schema",
			State:    "open",
			HasPR:    true,
			PRNumber: 1201,
		})
		facts := &worklang.Facts{Issues: issues}

		const plan = `(:plan :version 1
 (:goal :id "g"
  :acceptance ((:id "a1" :kind :test :subject "test:schema/versioning@HEAD" :predicate :passes)))
 (:derive :as "schema/issue-{n}" :kind go-fix
  :from (:issues :repo "mas-bandwidth/schema" :label "schema" :state :open :has-pr false)
  :repo "mas-bandwidth/schema" :base "dev" :inputs ((:issue "{url}"))
  :output (:branch "rowan/{n}-{slug}" :green ("test:schema/versioning"))
  :budget (:minutes 30 :tokens 120000 :model-floor sonnet)
  :affinity (:bench local :route "deepseek-flash"))
 (:clip :per-node))`

		parsed, err := worklang.ParsePlan("work.work", []byte(plan), worklang.DefaultLimits())
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		cards, err := worklang.ExpandPlan(parsed, facts)
		if err != nil {
			t.Fatalf("(:derive) refused when the facts are pinned: %v", err)
		}
		if len(cards) != 25 {
			ids := make([]string, len(cards))
			for i, c := range cards {
				ids[i] = c.Node
			}
			t.Fatalf("(:derive) minted %d cards, want 25; ids=%v", len(cards), ids)
		}

		// Each card's id is the substituted:as template, one per matched
		// issue number, and the inputs carry the issue URL.
		wantIDs := make(map[string]bool, 25)
		for i := int64(1); i <= 25; i++ {
			wantIDs[fmt.Sprintf("schema/issue-%d", i)] = true
		}
		wantURLs := make(map[string]bool, 25)
		for i := int64(1); i <= 25; i++ {
			wantURLs[fmt.Sprintf("https://github.com/mas-bandwidth/schema/issues/%d", i)] = true
		}
		wantBranches := make(map[string]bool, 25)
		for i := int64(1); i <= 25; i++ {
			wantBranches[fmt.Sprintf("rowan/%d-schema-versioning-%d", i, i)] = true
		}

		seenID := map[string]bool{}
		seenURL := map[string]bool{}
		seenBranch := map[string]bool{}
		for _, c := range cards {
			if c.Kind != "go-fix" {
				t.Errorf("card %s kind = %q, want go-fix", c.Node, c.Kind)
			}
			if c.Repo != "mas-bandwidth/schema" {
				t.Errorf("card %s repo = %q, want mas-bandwidth/schema", c.Node, c.Repo)
			}
			if c.Base != "dev" {
				t.Errorf("card %s base = %q, want dev", c.Node, c.Base)
			}
			if !wantIDs[c.Node] {
				t.Errorf("card id %q is not a substituted :as template", c.Node)
			}
			seenID[c.Node] = true
			foundURL := false
			for _, in := range c.Inputs {
				if strings.HasPrefix(in, "issue ") {
					url := strings.TrimPrefix(in, "issue ")
					if wantURLs[url] {
						seenURL[url] = true
						foundURL = true
					}
				}
			}
			if !foundURL {
				t.Errorf("card %s inputs do not name a substituted issue URL: %v", c.Node, c.Inputs)
			}
			if !wantBranches[c.Branch] {
				t.Errorf("card %s branch %q is not the substituted :output :branch", c.Node, c.Branch)
			}
			seenBranch[c.Branch] = true
		}

		if len(seenID) != 25 {
			t.Errorf("distinct ids = %d, want 25", len(seenID))
		}
		if len(seenURL) != 25 {
			t.Errorf("distinct substituted URLs = %d, want 25", len(seenURL))
		}
		if len(seenBranch) != 25 {
			t.Errorf("distinct substituted branches = %d, want 25", len(seenBranch))
		}

		// The PR-bearing issue yields no card -- re-expand with the PR issue
		// filtered out and confirm the same 25 mint. The fact set itself with
		// the PR issue still in place must not include it (it is filtered
		// because its :has-pr is true).
		for _, c := range cards {
			if strings.Contains(c.Node, "26") {
				t.Errorf("the PR-bearing issue (number=26) minted a card; inputs=%v", c.Inputs)
			}
		}
	})

	// worklang-fold-expands-to-one-pr-node-over-n-branches: a (:fold :as
	// "round-7" :kind fold :over (:branches :prefix "rowan/" :base "dev"
	// :green true)) over N green sibling branches plus one non-green yields
	// one fold node whose :inputs are the N green branch artifacts. The
	// non-green sibling is excluded by the selector.
	t.Run("worklang-fold-expands-to-one-pr-node-over-n-branches", func(t *testing.T) {
		branches := []worklang.Branch{
			{Name: "rowan/a", Base: "dev", Green: true},
			{Name: "rowan/b", Base: "dev", Green: true},
			{Name: "rowan/c", Base: "dev", Green: true},
			{Name: "rowan/z-not-green", Base: "dev", Green: false},
		}
		facts := &worklang.Facts{Branches: branches}

		const plan = `(:plan :version 1
 (:goal :id "g"
  :acceptance ((:id "a1" :kind :merged :subject "pr:round-7" :predicate :merged-at)))
 (:fold :as "round-7" :kind fold
  :over (:branches :prefix "rowan/" :base "dev" :green true)
  :repo "mas-bandwidth/nova-tools" :base "dev" :inputs ((:artifact "{branch}"))
  :output (:branch "rowan/round-7" :green ("gate:merge-clean"))
  :budget (:minutes 45 :tokens 180000 :model-floor sonnet)
  :affinity (:bench local :route "deepseek-flash"))
 (:clip :per-node))`

		parsed, err := worklang.ParsePlan("work.work", []byte(plan), worklang.DefaultLimits())
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		cards, err := worklang.ExpandPlan(parsed, facts)
		if err != nil {
			t.Fatalf("(:fold) refused when the facts are pinned: %v", err)
		}
		if len(cards) != 1 {
			ids := make([]string, len(cards))
			for i, c := range cards {
				ids[i] = c.Node
			}
			t.Fatalf("(:fold) minted %d cards, want 1; ids=%v", len(cards), ids)
		}
		c := cards[0]
		if c.Node != "round-7" {
			t.Errorf("card id = %q, want the :as string \"round-7\"", c.Node)
		}
		if c.Kind != "fold" {
			t.Errorf("card kind = %q, want fold", c.Kind)
		}
		if c.Repo != "mas-bandwidth/nova-tools" {
			t.Errorf("card repo = %q, want mas-bandwidth/nova-tools", c.Repo)
		}
		if c.Base != "dev" {
			t.Errorf("card base = %q, want dev", c.Base)
		}
		if c.Branch != "rowan/round-7" {
			t.Errorf("card branch = %q, want rowan/round-7", c.Branch)
		}

		// The :inputs list names the N green branches as artifacts; the
		// non-green sibling is excluded.
		wantArtifacts := map[string]bool{
			"artifact rowan/a": true,
			"artifact rowan/b": true,
			"artifact rowan/c": true,
		}
		if len(c.Inputs) != 3 {
			t.Fatalf("inputs count = %d, want 3 (the three green branches); inputs=%v", len(c.Inputs), c.Inputs)
		}
		for _, in := range c.Inputs {
			if !wantArtifacts[in] {
				t.Errorf("inputs %q is not one of the green branch artifacts %v", in, wantArtifacts)
			}
		}
		for _, in := range c.Inputs {
			if strings.Contains(in, "not-green") {
				t.Errorf("the non-green sibling leaked into the inputs: %q", in)
			}
		}
	})
}
