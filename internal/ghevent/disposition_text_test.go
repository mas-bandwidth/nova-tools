package ghevent

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/textbody"
)

func TestGheventDoesNotImportMerge(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for filename, file := range pkg.Files {
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if strings.Contains(path, "internal/merge") {
					t.Fatalf("file %s imports %s; ghevent must not depend on internal/merge", filename, path)
				}
			}
		}
	}
}

func TestCommentHeadComesFromVisibleDisposition(t *testing.T) {
	t.Parallel()
	bad := strings.Repeat("1", 40)
	good := strings.Repeat("2", 40)
	old := "DISPOSITION who=reader head=" + bad + " verdict=HOLD"
	live := "DISPOSITION who=reader head=" + good + " verdict=APPROVE"
	for _, tc := range []struct{ name, body, filtered, want string }{
		{"quote then current", "> " + old + "\n" + live, live, good},
		{"code then current", "```text\n" + old + "\n```\n" + live, live, good},
		{"CRLF code then current", "  ```text\r\n" + old + "\r\n```\r\n" + live, live, good},
		{"only unclosed code", "```\n" + old, "", ""},
		{"only quotation", "\t> " + old, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// A typed record parser already rejects a leading >. Assert the
			// filtered bytes as well, so those cases also reject an identity
			// filter instead of accidentally passing on the parser alone.
			if got := textbody.StripQuotedAndCode(tc.body); got != tc.filtered {
				t.Fatalf("filtered=%q want %q", got, tc.filtered)
			}
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
