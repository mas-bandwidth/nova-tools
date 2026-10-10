package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// cmdSelftestLand lands a canned card on a scratch clone with this binary (docs/SPEC-SPRINT.md section 14).
func (a *app) cmdSelftestLand(args []string, stdout, stderr io.Writer) int {
	fs, _ := a.verbSetup("selftest land")
	binary := fs.String("binary", "", "the binary to test (default: this binary)")
	scratch := fs.String("scratch-dir", "", "scratch directory for the clone (default: temporary directory)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, "selftest land", argErr("takes no words ", err, pos...))
	}

	bin := *binary
	if bin == "" {
		exe := a.executable
		if exe == nil {
			exe = os.Executable
		}
		var err error
		bin, err = exe()
		if err != nil {
			return refuse(stderr, "selftest land", "cannot determine executable: "+err.Error())
		}
	}

	opts := []sprint.SelftestLandOption{
		sprint.WithSelftestBinary(bin),
		sprint.WithSelftestOutput(stdout, stderr),
	}
	if *scratch != "" {
		opts = append(opts, sprint.WithSelftestScratch(*scratch))
	}

	if err := sprint.SelftestLand(context.Background(), opts...); err != nil {
		fmt.Fprintf(stderr, "%s selftest land FAILED: %s\n", prog, oneline.Escape(err.Error()))
		return 1
	}

	fmt.Fprintln(stdout, "SELFTEST LAND OK canned card landed on scratch clone")
	return 0
}
