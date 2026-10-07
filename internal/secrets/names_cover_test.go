package secrets

// The unit cover for names.go. Lines is the rendering the names verb prints, and it
// is pure: a NamesReport in, the three line groups out. RunNames is reached too, over
// a store laid out by the package's own storeOf, with its negative --max refusal.
// Nothing here sleeps, reads the clock, opens a socket or starts a child.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNamesCoverLinesRendersEveryShape pins NamesReport.Lines: one NAME line per row
// shown, the MORE line exactly when --max cut the list, and the OK line's counts.
func TestNamesCoverLinesRendersEveryShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		report    NamesReport
		wantOK    string
		wantNames []string
		wantMore  string
	}{
		{
			name: "rows with both kinds of key and a MORE line",
			report: NamesReport{
				As: "rowan", StoreDir: "/store",
				Rows:  []NameRow{{Name: "API_KEY", Clear: false}, {Name: "DB_URL", Clear: true}},
				Total: 3, Sealed: 2, Clear: 1,
			},
			wantOK: "SECRETS NAMES OK as=rowan keys=3 shown=2 sealed=2 clear=1",
			wantNames: []string{
				"SECRETS NAME key=API_KEY clear=false",
				"SECRETS NAME key=DB_URL clear=true",
			},
			wantMore: "SECRETS NAMES MORE kind=key shown=2 total=3 run: nova-secrets names --store /store --as rowan --max 0",
		},
		{
			name: "every row shown carries no MORE line",
			report: NamesReport{
				As: "rowan", StoreDir: "/store",
				Rows:  []NameRow{{Name: "ONLY", Clear: true}},
				Total: 1, Sealed: 0, Clear: 1,
			},
			wantOK:    "SECRETS NAMES OK as=rowan keys=1 shown=1 sealed=0 clear=1",
			wantNames: []string{"SECRETS NAME key=ONLY clear=true"},
			wantMore:  "",
		},
		{
			name:      "an empty report is zero counts and no rows",
			report:    NamesReport{As: "rowan", StoreDir: "/store"},
			wantOK:    "SECRETS NAMES OK as=rowan keys=0 shown=0 sealed=0 clear=0",
			wantNames: nil,
			wantMore:  "",
		},
		{
			name: "a name, a seat and a store each render through oneline.Field",
			report: NamesReport{
				As: "row an", StoreDir: "/a b",
				Rows:  []NameRow{{Name: "API KEY", Clear: true}},
				Total: 2, Sealed: 1, Clear: 1,
			},
			wantOK:    "SECRETS NAMES OK as=row\\x20an keys=2 shown=1 sealed=1 clear=1",
			wantNames: []string{"SECRETS NAME key=API\\x20KEY clear=true"},
			wantMore:  "SECRETS NAMES MORE kind=key shown=1 total=2 run: nova-secrets names --store /a\\x20b --as row\\x20an --max 0",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			okLine, nameLines, moreLine := tc.report.Lines()
			assert.Equal(t, tc.wantOK, okLine)
			assert.Equal(t, tc.wantNames, nameLines)
			assert.Equal(t, tc.wantMore, moreLine)
		})
	}
}

// TestNamesCoverRunNamesReportsBoundedRows pins RunNames' main path over a seat file
// the package's storeOf writes: the keys sort, a positive --max cuts the rows, the
// counts still cover the whole file, and Lines names the way to widen the list.
func TestNamesCoverRunNamesReportsBoundedRows(t *testing.T) {
	t.Parallel()

	store := storeOf(t, map[string]string{
		".git/keep":  "",
		".sops.yaml": "",
		"rowan.yaml": "PLAIN: hello\nSEALED: ENC[abc]\n",
	})

	r, err := RunNames(store, "rowan", 1)
	require.NoError(t, err)
	assert.Equal(t, 2, r.Total, "the whole file's keys are counted")
	assert.Equal(t, 1, r.Sealed)
	assert.Equal(t, 1, r.Clear)
	require.Len(t, r.Rows, 1, "--max 1 shows one row")
	assert.Equal(t, "PLAIN", r.Rows[0].Name, "keys sort alphabetically")
	assert.True(t, r.Rows[0].Clear)

	okLine, nameLines, moreLine := r.Lines()
	assert.Equal(t, "SECRETS NAMES OK as=rowan keys=2 shown=1 sealed=1 clear=1", okLine)
	assert.Equal(t, []string{"SECRETS NAME key=PLAIN clear=true"}, nameLines)
	assert.Contains(t, moreLine, "shown=1 total=2")
	assert.Contains(t, moreLine, "--store "+store)
}

// TestNamesCoverRunNamesRefusesANegativeMax pins names.go's one refusal: a negative
// --max is rejected before any store is touched, naming the value it wants.
func TestNamesCoverRunNamesRefusesANegativeMax(t *testing.T) {
	t.Parallel()

	_, err := RunNames("", "", -1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--max -1 is negative; expected non-negative integer")
}
