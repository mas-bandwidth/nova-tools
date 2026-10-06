package ci

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// acceptance_records_test.go holds the release acceptance records to one shape.
// A record is docs/acceptance/<release>/<name>.md, one measured requirement of
// that release, written so a stranger can read what was asked, how it was
// measured, and the numbers as the tool printed them:
//
//   - the title is "# <release> acceptance: <requirement in words>";
//   - a field list follows: Requirement, Release (the directory's name),
//     Measured (RFC 3339, UTC), From, Tool and Verdict (MET or NOT MET);
//   - then the sections "## Method", "## Results" and "## Raw", in that order;
//   - Raw holds at least one non-empty fenced block: the tool's own output.
//
// The v1.0.0 records the release requires are named in acceptanceRequired, so
// a record that was never written is red here rather than missing quietly. A
// required record may name fields of its own, beyond the common ones: the
// public-dashboard load is gated on the server's latency, and the far
// vantage's latency is recorded beside it, with the kernel settings in force,
// because a 166 ms path decides the far number and the server does not.

var acceptanceRequired = map[string]map[string][]string{
	"v1.0.0": {"public-dashboard-load.md": {"Server latency", "Far latency", "Kernel"}},
}

var (
	acceptanceRelease = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	acceptanceField   = regexp.MustCompile(`^- ([A-Z][A-Za-z ]*): (.+)$`)
	acceptanceFields  = []string{"Requirement", "Release", "Measured", "From", "Tool", "Verdict"}
	acceptanceOrder   = []string{"## Method", "## Results", "## Raw"}
)

func TestAcceptanceRecordsAreWellFormed(t *testing.T) {
	t.Parallel()

	root := filepath.Join(repoRoot(t), "docs", "acceptance")
	for release, records := range acceptanceRequired {
		for name, fields := range records {
			data, err := os.ReadFile(filepath.Join(root, release, name))
			require.NoError(t, err, "%s requires the acceptance record %s", release, name)
			got := acceptanceRecordFields(string(data))
			for _, field := range fields {
				assert.NotEmpty(t, got[field], "%s/%s carries the field %q", release, name, field)
			}
		}
	}

	records, err := filepath.Glob(filepath.Join(root, "*", "*.md"))
	require.NoError(t, err)
	require.NotEmpty(t, records)
	for _, path := range records {
		path := path
		release := filepath.Base(filepath.Dir(path))
		t.Run(release+"/"+filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			require.Regexp(t, acceptanceRelease, release, "an acceptance directory is named for its release")
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			checkAcceptanceRecord(t, release, string(data))
		})
	}
}

func checkAcceptanceRecord(t *testing.T, release, text string) {
	t.Helper()
	lines := strings.Split(text, "\n")
	require.NotEmpty(t, lines)
	prefix := "# " + release + " acceptance: "
	require.True(t, strings.HasPrefix(lines[0], prefix) && len(lines[0]) > len(prefix),
		"line 1 is %q followed by the requirement, got %q", prefix, lines[0])

	fields := acceptanceRecordFields(text)
	for _, name := range acceptanceFields {
		assert.NotEmpty(t, fields[name], "the field list carries %q", name)
	}
	assert.Equal(t, release, fields["Release"], "Release names the record's directory")
	if measured, ok := fields["Measured"]; ok {
		at, err := time.Parse(time.RFC3339, measured)
		if assert.NoError(t, err, "Measured is RFC 3339") {
			assert.Equal(t, time.UTC, at.Location(), "Measured is in UTC")
		}
	}
	assert.Contains(t, []string{"MET", "NOT MET"}, fields["Verdict"], "Verdict is MET or NOT MET")

	at := -1
	for _, heading := range acceptanceOrder {
		next := -1
		for i, line := range lines {
			if i > at && strings.TrimSpace(line) == heading {
				next = i
				break
			}
		}
		require.NotEqual(t, -1, next, "the record has %q after the sections before it", heading)
		at = next
	}

	raw := lines[at+1:]
	open, body := false, 0
	for _, line := range raw {
		if strings.HasPrefix(line, "```") {
			if open && body > 0 {
				return
			}
			open, body = !open, 0
			continue
		}
		if open && strings.TrimSpace(line) != "" {
			body++
		}
	}
	t.Fatalf("## Raw holds no closed, non-empty fenced block")
}

// acceptanceRecordFields reads the field list: every "- Name: value" line
// before the first section.
func acceptanceRecordFields(text string) map[string]string {
	fields := map[string]string{}
	lines := strings.Split(text, "\n")
	for _, line := range lines[min(1, len(lines)):] {
		if strings.HasPrefix(line, "## ") {
			break
		}
		if m := acceptanceField.FindStringSubmatch(line); m != nil {
			fields[m[1]] = strings.TrimSpace(m[2])
		}
	}
	return fields
}
