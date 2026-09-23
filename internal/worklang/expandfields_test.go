package worklang_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// TestExpandNamesEveryMissingRequiredNodeField pins docs/ONBOARDING.md point 2:
// one refusal reports every missing required :node field, not just the first.
// The single-field wording is byte-for-byte unchanged; where two or three are
// missing the reasons are joined with "; " in source order -- :kind, then
// :output, then :budget. :id keeps its own immediate refusal, because a node
// with no :id cannot name itself in any of the other messages.
func TestExpandNamesEveryMissingRequiredNodeField(t *testing.T) {
	cases := []struct {
		name string
		plan string
		want string
	}{
		{
			name: "all three present fields missing",
			plan: `(:plan :version 1
 (:node :id "n1")
 (:clip :per-node))`,
			want: `:node n1 has no :kind; refusing to guess; :node n1 has no :output; refusing to guess; :node n1 has no :budget; a budget-less plan refuses`,
		},
		{
			name: "output and budget missing",
			plan: `(:plan :version 1
 (:node :id "n2" :kind docs)
 (:clip :per-node))`,
			want: `:node n2 has no :output; refusing to guess; :node n2 has no :budget; a budget-less plan refuses`,
		},
		{
			name: "only budget missing",
			plan: `(:plan :version 1
 (:node :id "n3" :kind docs
  :output (:branch "rowan/n3-a" :green ("test:a")))
 (:clip :per-node))`,
			want: `:node n3 has no :budget; a budget-less plan refuses`,
		},
		{
			name: "no id at all",
			plan: `(:plan :version 1
 (:node :kind docs)
 (:clip :per-node))`,
			want: `:node has no :id; refusing to guess`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := worklang.ParsePlan("work.work", []byte(tc.plan), worklang.DefaultLimits())
			if err != nil {
				t.Fatalf("fixture plan was refused at parse: %v", err)
			}
			_, err = worklang.ExpandPlan(plan)
			if err == nil {
				t.Fatal("a node missing required fields expanded instead of refusing")
			}
			ref := assertRefusal(t, err)
			if ref.Reason != tc.want {
				t.Errorf("refusal reason =\n%q\nwant\n%q", ref.Reason, tc.want)
			}
		})
	}
}
