package swarm

import "testing"

// TestAskedQuestion checks that AskedQuestion recognises a final assistant turn
// that is a QUESTION to the user, as a pure function over the harness tail.
//
// The canary-run-3 tail ends with "Would you like me to proceed with moving
// RESULT.md into the nested repo and finalize the commit?" — a question that
// held the slot to the deadline because nobody answers a headless card. The
// three negative cases guard against false positives: a committed RESULT line,
// a question inside a fenced code block, and a rhetorical mid-transcript
// question followed by more work.
func TestAskedQuestion(t *testing.T) {
	cases := []struct {
		name string
		tail string
		want string
	}{
		{
			name: "canary_run_3_asks",
			tail: "Would you like me to proceed with moving RESULT.md into the nested repo and finalize the commit?",
			want: "Would you like me to proceed with moving RESULT.md into the nested repo and finalize the commit?",
		},
		{
			name: "committed_RESULT_line",
			tail: "RESULT: OK id=canary-run-3 sha=abc findings=0",
			want: "",
		},
		{
			name: "question_in_code_block",
			tail: "The JSON response was:\n```\n{\"question\": \"Is this correct?\"}\n```",
			want: "",
		},
		{
			name: "rhetorical_question_followed_by_work",
			tail: "Should I proceed with the analysis?\n$ run analysis --type full\nDone.",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AskedQuestion(c.tail)
			if got != c.want {
				t.Errorf("AskedQuestion(%q) = %q, want %q", c.tail, got, c.want)
			}
		})
	}
}
