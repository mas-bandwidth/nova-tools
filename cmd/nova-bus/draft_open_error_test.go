package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDraftSubjectResolutionDistinguishesMissingValidAndCorruptOpen(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		open     string
		wantCode int
		wantOut  string
		wantErr  string
		avoidErr string
	}{
		{
			name:     "missing OPEN is an empty list",
			wantCode: 2,
			wantErr:  "is not an id on this bus, not a note that exists, and not the subject of a note on your open list",
			avoidErr: "read once with --full --advance",
		},
		{
			name:     "valid OPEN resolves a subject",
			open:     "valid",
			wantCode: 0,
			wantOut:  "Re: bo-abcdef012345",
			wantErr:  "DRAFT NOTE --re named the subject",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := draftOpenErrorBus(t)
			if tc.open == "valid" {
				writeDraftOpenTestFile(t, root, "from-ada/OPEN", busOpenTestLine())
			}
			r := invoke(t, "", "draft", "--bus", root, "--as", "Ada Vale", "--to", "Bo", "--re", "Question about the gate")
			if r.code != tc.wantCode {
				t.Fatalf("exit %d, want %d; stdout=%q stderr=%q", r.code, tc.wantCode, r.stdout, r.stderr)
			}
			if tc.wantOut != "" && !strings.Contains(r.stdout, tc.wantOut) {
				t.Errorf("stdout %q does not contain %q", r.stdout, tc.wantOut)
			}
			if tc.wantErr != "" && !strings.Contains(r.stderr, tc.wantErr) {
				t.Errorf("stderr %q does not contain %q", r.stderr, tc.wantErr)
			}
			if tc.avoidErr != "" && strings.Contains(r.stderr, tc.avoidErr) {
				t.Errorf("stderr misleadingly contains %q: %s", tc.avoidErr, r.stderr)
			}
		})
	}
}

func TestDraftSubjectResolutionRepairsMalformedOrUnreadableOpen(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		openContents string
		openDir      bool
		wantContext  string
		wantRepair   string
	}{
		{
			name:         "old format",
			openContents: "old-format-entry\n",
			wantContext:  "does not begin with \"" + bus.OpenHeader + "\"",
			wantRepair:   "rebuild it from the bus",
		},
		{
			name:         "malformed row",
			openContents: bus.OpenHeader + "\nshort\n",
			wantContext:  "tab-separated fields",
			wantRepair:   "rebuild it from the bus",
		},
		{
			name:        "read error",
			openDir:     true,
			wantContext: "cannot read",
			wantRepair:  "repair access to that OPEN path first",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := draftOpenErrorBus(t)
			openPath := filepath.Join(root, "from-ada", "OPEN")
			if tc.openDir {
				if err := os.Mkdir(openPath, 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				writeDraftOpenTestFile(t, root, "from-ada/OPEN", tc.openContents)
			}

			r := invoke(t, "", "draft", "--bus", root, "--as", "Ada Vale", "--to", "Bo", "--re", "Question about the gate")
			if r.code != 2 {
				t.Fatalf("exit %d, want 2; stdout=%q stderr=%q", r.code, r.stdout, r.stderr)
			}
			if r.stdout != "" {
				t.Errorf("refusal wrote draft bytes to stdout: %q", r.stdout)
			}
			for _, want := range []string{
				tc.wantContext,
				tc.wantRepair,
				"nova-bus inbox --bus " + shellQuote(root) + " --as 'Ada Vale' --receipt-max-words 23 --full --carry-history --advance --remote '<your-remote>' --branch '<your-branch>'",
				"replace the remote and branch placeholders",
				"--receipt-max-words 23 is the resolved positive word-count threshold for classifying short receipts",
				"--carry-history preserves existing history",
				"--advance moves and pushes the cursor",
				"; run: nova-bus inbox",
			} {
				if !strings.Contains(r.stderr, want) {
					t.Errorf("stderr %q does not contain %q", r.stderr, want)
				}
			}
			if strings.Contains(r.stderr, "not an id on this bus, not a note that exists, and not the subject") {
				t.Errorf("malformed OPEN was mislabeled as an unmatched subject: %s", r.stderr)
			}
			if tc.name == "old format" {
				assert.NotContains(t, r.stderr, "read once with --full --advance", "old OPEN error duplicated its legacy recovery remedy")
			}
		})
	}
}

func TestDraftOpenReadFailureWithoutThresholdUsesPlaceholder(t *testing.T) {
	t.Parallel()

	got := draftOpenReadFailure("/bus/path", bus.Participant{Name: "Ada Vale", Lane: "from-ada"}, 0, false, errors.New("bad OPEN"))
	require.Error(t, got)
	assert.ErrorContains(t, got, "<receipt-word-limit>")
	assert.ErrorContains(t, got, "replace the receipt-word-limit placeholder with a positive word-count threshold for classifying short receipts")
	assert.NotContains(t, got.Error(), "--receipt-max-words 0")
}

func TestDraftExplicitIDAndNewBypassCorruptOpen(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		re   string
		want string
	}{
		{name: "existing bus ID", re: "bo-abcdef012345", want: "Re: bo-abcdef012345"},
		{name: "new thread", re: "new", want: "Re: new"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := draftOpenErrorBus(t)
			writeDraftOpenTestFile(t, root, "from-ada/OPEN", "old-format-entry\n")
			r := invoke(t, "", "draft", "--bus", root, "--as", "Ada Vale", "--to", "Bo", "--re", tc.re).mustCode(t, 0)
			if !strings.Contains(r.stdout, tc.want) {
				t.Fatalf("stdout %q does not contain %q", r.stdout, tc.want)
			}
		})
	}
}

func draftOpenErrorBus(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "bus with space")
	writeDraftOpenTestFile(t, root, "participants.json", `{"participants":[{"name":"Ada Vale","lane":"from-ada","git_name":"Ada Vale","git_email":"ada@example.com"},{"name":"Bo","lane":"from-bo","git_name":"Bo","git_email":"bo@example.com"}]}`)
	writeDraftOpenTestFile(t, root, ".nova-bus/defaults", "receipt-max-words=23\n")
	if err := os.MkdirAll(filepath.Join(root, "from-ada"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeDraftOpenTestFile(t, root, "from-bo/question-abcdef012345.md", "From: Bo\nTo: Ada Vale\nDate: Wed Sep 9 12:34:56 UTC 2026\nId: bo-abcdef012345\nSubject: Question about the gate\n\nA question.\n")
	return root
}

func busOpenTestLine() string {
	return bus.OpenHeader + "\nbo-abcdef012345\tnote\t-\tBo\tto\t2026-09-09T12:34:56Z\tfrom-bo/question-abcdef012345.md\tQuestion about the gate\n"
}

func writeDraftOpenTestFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
