package read_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
)

func TestFindOwedSentence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		lines   []string
		want    string
		wantErr bool
	}{
		{
			name:  "standard owed",
			lines: []string{"APPROVE who=fable head=11111111", "SCORE who=fable head=11111111 score=7/10", "Owed: fix timeout handling in retry loop"},
			want:  "fix timeout handling in retry loop",
		},
		{
			name:  "case insensitive and whitespace",
			lines: []string{"owed:   add regression test for edge case   "},
			want:  "add regression test for edge case",
		},
		{
			name:  "quoted sentence",
			lines: []string{`Owed: "handle empty inputs gracefully"`},
			want:  "handle empty inputs gracefully",
		},
		{
			name:  "multiple lines newest wins",
			lines: []string{"Owed: old debt", "Owed: newest debt"},
			want:  "newest debt",
		},
		{
			name:    "no owed sentence",
			lines:   []string{"SCORE who=fable head=11111111 score=7/10", "all looks good except nit"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := read.FindOwedSentence(tc.lines)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
