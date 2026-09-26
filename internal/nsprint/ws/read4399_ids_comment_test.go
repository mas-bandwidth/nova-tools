package ws_test

// Cold read of #4399 at 7c119d2db (rowan-opus): scope park --ids @file reads
// its ids through verbflag's Parse (ResolveIDs) and then ws.ReadIDs. The file
// form ws.ReadIDs documents ('#' to end of line a comment, duplicates
// dropped) is what dev's TestReadIDs holds, but through the one Parse the
// comment's words become ids and ReadIDs then cuts the joined list at the
// first '#': "a b / # comment / c" parks a and b only.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

func TestRead4399IDsFileCommentThroughTheOneParse(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ids")
	if err := os.WriteFile(path, []byte("a b\n# comment\nc,a  # trailing\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := verbflag.New("scope park")
	idsArg := fs.String("ids", "", verbflag.HelpIDs)
	if err := fs.Parse([]string{"--ids", "@" + path}); err != nil {
		t.Fatal(err)
	}
	ids, err := ws.ReadIDs(*idsArg, nil)
	if err != nil || strings.Join(ids, " ") != "a b c" {
		t.Fatalf("scope park --ids @file read %q (%v) from %q, want a b c", ids, err, *idsArg)
	}
}
