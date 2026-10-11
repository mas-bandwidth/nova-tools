package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// testHead and testBlob are fixture ids: a store commit this test's .git
// fixture names, and the git blob id place records for the sealed file.
const (
	testHead = "0123456789abcdef0123456789abcdef01234567"
	testBlob = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
)

// TestPlaceCoverDashAndFieldQuoteReceiptValues pins dash and field: a value the
// receipt file does not hold prints as "-", and a held value prints quoted so
// the printed line stays one line.
func TestPlaceCoverDashAndFieldQuoteReceiptValues(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"held", "seat.yaml", "seat.yaml"},
		{"empty", "", "-"},
		{"already-dash", "-", "-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, dash(tc.in))
			assert.Equal(t, tc.want, field(tc.in))
		})
	}
	assert.Equal(t, "a\\x20b", field("a b"), "a space is escaped, the line stays one line")
	assert.Equal(t, oneline.Field("-"), field(""))
}

// TestPlaceCoverStoreHeadReadsTheStoreRef pins storeHead: a detached HEAD and a
// symbolic HEAD both resolve to the commit the store stood on, and a store with
// no .git, an unresolvable ref or a non-commit HEAD answers "", the head=- the
// receipt records.
func TestPlaceCoverStoreHeadReadsTheStoreRef(t *testing.T) {
	t.Parallel()

	write := func(t *testing.T, storeDir, rel, content string) {
		t.Helper()
		path := filepath.Join(storeDir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}

	for _, tc := range []struct {
		name string
		head string
		refs map[string]string
		want string
	}{
		{"detached", testHead + "\n", nil, testHead},
		{"symbolic", "ref: refs/heads/main\n", map[string]string{".git/refs/heads/main": testHead + "\n"}, testHead},
		{"unresolvable-ref", "ref: refs/heads/absent\n", nil, ""},
		{"not-a-commit", "main\n", nil, ""},
		{"no-git", "", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			storeDir := t.TempDir()
			if tc.head != "" || tc.name != "no-git" {
				write(t, storeDir, ".git/HEAD", tc.head)
			}
			for ref, content := range tc.refs {
				write(t, storeDir, filepath.FromSlash(ref), content)
			}
			assert.Equal(t, tc.want, storeHead(storeDir))
		})
	}
}

// TestPlaceCoverReadFleetMachinesParsesRegistry pins ReadFleetMachines: the
// tab-separated registry parses with comments and blank lines skipped and the
// optional home kept, and every bad line is refused with the line number.
func TestPlaceCoverReadFleetMachinesParsesRegistry(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		content string
		absent  bool
		want    map[string]FleetMachine
		errPart string
	}{
		{
			name: "parses",
			content: "# fleet registry\n" +
				"web-1\tssh -p 2222 web-1.invalid\t/srv/web\n" +
				"\n" +
				"db-1\tdb-1.invalid\n",
			want: map[string]FleetMachine{
				"web-1": {Name: "web-1", Target: "ssh -p 2222 web-1.invalid", Home: "/srv/web"},
				"db-1":  {Name: "db-1", Target: "db-1.invalid"},
			},
		},
		{name: "one-field", content: "web-1\n", errPart: "wants at least 2 tab-separated fields"},
		{name: "empty-name", content: "\tweb-1.invalid\n", errPart: "wants a name and an ssh target"},
		{name: "empty-target", content: "web-1\t\n", errPart: "wants a name and an ssh target"},
		{name: "duplicate", content: "web-1\ta.invalid\nweb-1\tb.invalid\n", errPart: `machine "web-1" is listed twice`},
		{name: "no-machines", content: "# comment only\n\n", errPart: "no machines"},
		{name: "absent", absent: true, errPart: "no such file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := ""
			if !tc.absent {
				path = filepath.Join(t.TempDir(), "fleet.tsv")
				require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o600))
			}
			got, err := ReadFleetMachines(path)
			if tc.errPart != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errPart)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestPlaceMachinesRefusesGateRegistryFormat(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "machines.tsv")
	body := "node\tnode\tlinux/x64\tbench\tseat\t8\t-\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	_, err := ReadFleetMachines(path)
	require.Error(t, err, "place must refuse the gate registry instead of treating os/arch as a home directory")
	assert.Contains(t, err.Error(), "place machines file", "refusal = %q, want the place table format", err)
	assert.Contains(t, err.Error(), "name, ssh target, home", "refusal = %q, want the fields place needs", err)
}

// TestPlaceCoverReceiptPathNamesTheMachineFile pins receiptPath: one machine's
// receipt file lives at <dir>/<machine>.receipt.
func TestPlaceCoverReceiptPathNamesTheMachineFile(t *testing.T) {
	t.Parallel()

	assert.Equal(t, filepath.Join("/receipts", "web-1.receipt"), receiptPath("/receipts", "web-1"))
}

// TestPlaceCoverReadReceiptsParsesMachineLines pins readReceipts: an absent file
// is zero receipts, the six-field line is read field for field with "-" fields
// cleared, an older build's four-field line keeps no digest, and a wrong line is
// refused with the remedy.
func TestPlaceCoverReadReceiptsParsesMachineLines(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		content string
		isDir   bool
		want    []placedReceipt
		errPart string
	}{
		{
			name:    "absent",
			want:    nil,
			errPart: "",
		},
		{
			name:    "six-fields",
			content: "TOKEN\t/srv/web/.config/nova-secrets/TOKEN.env\tseat.yaml\t" + testHead + "\t" + testBlob + "\t2026-01-01T00:00:00Z\n",
			want: []placedReceipt{{
				Secret: "TOKEN", Path: "/srv/web/.config/nova-secrets/TOKEN.env",
				File: "seat.yaml", Head: testHead, Blob: testBlob, Stamp: "2026-01-01T00:00:00Z",
			}},
		},
		{
			name:    "dash-fields",
			content: "TOKEN\t/path\t-\t-\t-\t2026-01-01T00:00:00Z\n",
			want: []placedReceipt{{
				Secret: "TOKEN", Path: "/path", Stamp: "2026-01-01T00:00:00Z", Unknown: true,
			}},
		},
		{
			name:    "legacy-four-fields",
			content: "TOKEN\t/old\tdigest-of-the-value\t2026-01-01T00:00:00Z\n",
			want: []placedReceipt{{
				Secret: "TOKEN", Path: "/old", Stamp: "2026-01-01T00:00:00Z", Unknown: true, Legacy: true,
			}},
		},
		{
			name:    "bad-count",
			content: "TOKEN\t/path\tseat.yaml\t" + testHead + "\textra\n",
			errPart: "want 6 tab-separated fields",
		},
		{
			name:    "unreadable",
			isDir:   true,
			errPart: "is a directory",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if tc.isDir {
				require.NoError(t, os.MkdirAll(receiptPath(dir, "web-1"), 0o700))
			} else if tc.content != "" {
				require.NoError(t, os.WriteFile(receiptPath(dir, "web-1"), []byte(tc.content), 0o600))
			}
			got, err := readReceipts(dir, "web-1")
			if tc.errPart != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errPart)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestPlaceCoverWriteReceiptReplacesAndKeepsSixFields pins writeReceipt: a new
// secret appends, the same secret replaces, the file lands at 0600, and an
// older build's digest line is rewritten in the six-field form without the
// digest; a receipts directory that cannot be created is refused.
func TestPlaceCoverWriteReceiptReplacesAndKeepsSixFields(t *testing.T) {
	t.Parallel()

	want := placedReceipt{
		Secret: "TOKEN", Path: "/srv/web/.config/nova-secrets/TOKEN.env",
		File: "seat.yaml", Head: testHead, Blob: testBlob, Stamp: "2026-01-01T00:00:00Z",
	}

	t.Run("append-at-0600", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, writeReceipt(dir, "web-1", want))
		got, err := readReceipts(dir, "web-1")
		require.NoError(t, err)
		assert.Equal(t, []placedReceipt{want}, got)
		info, err := os.Stat(receiptPath(dir, "web-1"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})

	t.Run("replace", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		first := want
		require.NoError(t, writeReceipt(dir, "web-1", first))
		second := want
		second.Path = "/elsewhere/.config/nova-secrets/TOKEN.env"
		require.NoError(t, writeReceipt(dir, "web-1", second))
		got, err := readReceipts(dir, "web-1")
		require.NoError(t, err)
		assert.Equal(t, []placedReceipt{second}, got)
	})

	t.Run("rewrites-legacy-without-digest", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		legacy := "OLD\t/old\tdigest-of-the-value\t2026-01-01T00:00:00Z\n"
		require.NoError(t, os.WriteFile(receiptPath(dir, "web-1"), []byte(legacy), 0o600))
		require.NoError(t, writeReceipt(dir, "web-1", want))
		raw, err := os.ReadFile(receiptPath(dir, "web-1"))
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "digest-of-the-value")
		assert.Contains(t, string(raw), "OLD\t/old\t-\t-\t-\t2026-01-01T00:00:00Z")
		got, err := readReceipts(dir, "web-1")
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "OLD", got[0].Secret)
		assert.True(t, got[0].Unknown, "the digest is gone, so the identity is unknown")
		assert.False(t, got[0].Legacy, "rewritten in the six-field form, the line is no longer legacy")
		assert.Equal(t, "TOKEN", got[1].Secret)
	})

	t.Run("refuses-blocked-directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		blocked := filepath.Join(dir, "blocked")
		require.NoError(t, os.WriteFile(blocked, []byte("a file, not a directory"), 0o600))
		err := writeReceipt(filepath.Join(blocked, "sub"), "web-1", want)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot create receipts directory")
	})

	t.Run("refuses-unreadable-receipt", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.MkdirAll(receiptPath(dir, "web-1"), 0o700))
		err := writeReceipt(dir, "web-1", want)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is a directory")
	})
}

// TestPlaceCoverPlaceDryRunPlansWithoutWriting pins placeDryRun: the plan names
// the machine, target, path, mode and sealed file, and the receipt's action is
// add with no receipt on file, unchanged when the receipt already records this
// sealed file at this path, and replace for the same secret sealed elsewhere;
// nothing is written and an unreadable receipt file is refused.
func TestPlaceCoverPlaceDryRunPlansWithoutWriting(t *testing.T) {
	t.Parallel()

	in := func(receipts string) PlaceInput {
		return PlaceInput{Machine: "web-1", Secret: "TOKEN", Receipts: receipts, SSH: "ssh", DryRun: true}
	}
	machine := FleetMachine{Name: "web-1", Target: "web-1.invalid", Home: "/srv/web"}
	planFor := placedReceipt{
		Secret: "TOKEN", Path: "/srv/web/.config/nova-secrets/TOKEN.env",
		File: "seat.yaml", Head: testHead, Blob: testBlob,
	}

	for _, tc := range []struct {
		name     string
		content  string
		isDir    bool
		wantPlan string
	}{
		{"add", "", false, "action=add"},
		{"unchanged", "TOKEN\t/srv/web/.config/nova-secrets/TOKEN.env\tseat.yaml\t-\t" + testBlob + "\t2026-01-01T00:00:00Z\n", false, "action=unchanged"},
		{"replace", "TOKEN\t/old\tseat.yaml\t-\totherblobotherblobotherblobotherblobotherblob\t2026-01-01T00:00:00Z\n", false, "action=replace"},
		{"other-secret", "OTHER\t/old\tseat.yaml\t-\t" + testBlob + "\t2026-01-01T00:00:00Z\n", false, "action=add"},
		{"unreadable", "", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if tc.isDir {
				require.NoError(t, os.MkdirAll(receiptPath(dir, "web-1"), 0o700))
			} else if tc.content != "" {
				require.NoError(t, os.WriteFile(receiptPath(dir, "web-1"), []byte(tc.content), 0o600))
			}
			got, err := placeDryRun(in(dir), machine, planFor)
			if tc.isDir {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "is a directory")
				return
			}
			require.NoError(t, err)
			lines := strings.Split(got, "\n")
			require.Len(t, lines, 4)
			assert.Contains(t, got, "mode=0600")
			assert.Contains(t, got, "target=web-1.invalid")
			assert.Contains(t, got, "file=seat.yaml")
			assert.Contains(t, got, "head="+testHead)
			assert.Contains(t, got, "blob="+testBlob)
			assert.Contains(t, got, tc.wantPlan)
			assert.Equal(t, "SECRETS PLACE DRY-RUN OK machine=web-1 secret=TOKEN nothing written, no ssh run", lines[3])
			if tc.name == "add" {
				assert.NoFileExists(t, receiptPath(dir, "web-1"))
			}
		})
	}
}

// TestPlaceCoverRunPlacedListsReceiptsAndNotesLegacy pins RunPlaced: a machine
// with nothing placed answers count=0, receipts list sorted with their sealed
// identity, a missing --machine is refused, and an older build's line lists as
// identity=unknown with a NOTE about the digest its file still holds.
func TestPlaceCoverRunPlacedListsReceiptsAndNotesLegacy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		machine   string
		content   string
		isDir     bool
		wantOK    string
		wantItems []string
		wantErr   string
	}{
		{
			name:    "refuses-missing-machine",
			wantErr: "missing --machine",
		},
		{
			name:    "empty",
			machine: "web-1",
			wantOK:  "SECRETS PLACED OK machine=web-1 count=0",
		},
		{
			name:    "lists-sorted",
			machine: "web-1",
			content: "TOKEN\t/srv/web/.config/nova-secrets/TOKEN.env\tseat.yaml\t" + testHead + "\t" + testBlob + "\t2026-01-01T00:00:00Z\n" +
				"OTHER\t/other\tseat.yaml\t-\t" + testBlob + "\t2026-01-02T00:00:00Z\n",
			wantOK: "SECRETS PLACED OK machine=web-1 count=2",
			wantItems: []string{
				"SECRETS PLACED ITEM machine=web-1 secret=OTHER path=/other file=seat.yaml head=- blob=" + testBlob + " stamp=2026-01-02T00:00:00Z",
				"SECRETS PLACED ITEM machine=web-1 secret=TOKEN path=/srv/web/.config/nova-secrets/TOKEN.env file=seat.yaml head=" + testHead + " blob=" + testBlob + " stamp=2026-01-01T00:00:00Z",
			},
		},
		{
			name:    "legacy-note",
			machine: "web-1",
			content: "TOKEN\t/old\tdigest-of-the-value\t2026-01-01T00:00:00Z\n",
			wantOK:  "SECRETS PLACED OK machine=web-1 count=1",
			wantItems: []string{
				"SECRETS PLACED ITEM machine=web-1 secret=TOKEN path=/old file=- head=- blob=- stamp=2026-01-01T00:00:00Z identity=unknown",
				"NOTE 1 receipt(s) for web-1 carry no sealed-file identity",
			},
		},
		{
			name:    "refuses-unreadable",
			machine: "web-1",
			isDir:   true,
			wantErr: "is a directory",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if tc.isDir {
				require.NoError(t, os.MkdirAll(receiptPath(dir, "web-1"), 0o700))
			} else if tc.content != "" {
				require.NoError(t, os.WriteFile(receiptPath(dir, "web-1"), []byte(tc.content), 0o600))
			}
			okLine, items, err := RunPlaced(PlacedInput{Machine: tc.machine, Receipts: dir})
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantOK, okLine)
			require.Len(t, items, len(tc.wantItems))
			for i, want := range tc.wantItems {
				if strings.Contains(want, "NOTE") {
					assert.Contains(t, items[i], "SECRETS PLACED NOTE")
					assert.Contains(t, items[i], "older build")
					assert.Contains(t, items[i], "still hold a hash")
					continue
				}
				assert.Equal(t, want, items[i])
			}
		})
	}
}

// TestPlaceCoverDefaultPathsTrackHome pins defaultFleetFile and
// defaultReceiptsDir: both defaults sit under the caller's HOME and nothing
// else, and with no HOME there is no default for the caller to refuse.
func TestPlaceCoverDefaultPathsTrackHome(t *testing.T) {
	t.Parallel()

	home := os.Getenv("HOME")
	wantFleet, wantPlaced := "", ""
	if home != "" {
		wantFleet = filepath.Join(home, ".config", "nova-tools", "fleet.tsv")
		wantPlaced = filepath.Join(home, ".config", "nova-secrets", "placed")
	}
	assert.Equal(t, wantFleet, defaultFleetFile())
	assert.Equal(t, wantPlaced, defaultReceiptsDir())
}
