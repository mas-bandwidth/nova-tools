package ghevent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCommentHeadComesFromVisibleDisposition(t *testing.T) {
	t.Parallel()
	bad := strings.Repeat("1", 40)
	good := strings.Repeat("2", 40)
	old := "DISPOSITION who=reader head=" + bad + " verdict=HOLD"
	live := "DISPOSITION who=reader head=" + good + " verdict=APPROVE"
	for _, tc := range []struct{ name, body, want string }{
		{"quote then current", "> " + old + "\n" + live, good},
		{"code then current", "```text\n" + old + "\n```\n" + live, good},
		{"CRLF code then current", "  ```text\r\n" + old + "\r\n```\r\n" + live, good},
		{"only unclosed code", "```\n" + old, ""},
		{"only quotation", "\t> " + old, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			payload, err := json.Marshal(map[string]any{"action": "created", "issue": map[string]any{"number": 42}, "comment": map[string]any{"id": 7, "body": tc.body}, "repository": map[string]any{"full_name": "owner/project"}, "sender": map[string]any{"login": "reader"}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := Decode("issue_comment", payload)
			if err != nil {
				t.Fatal(err)
			}
			if got.Head != tc.want {
				t.Fatalf("head=%q want %q", got.Head, tc.want)
			}
		})
	}
}
