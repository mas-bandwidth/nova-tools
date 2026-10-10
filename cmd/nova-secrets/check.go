// check.go holds the check verb: its flags, its run and the helpers only it uses.

package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

func runCheckCLI(args []string, s streams) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", storeUse)
	asFlag := fs.String("as", "", asUse)
	keyFlag := fs.String("key", "", keyUse)
	sopsFlag := fs.String("sops", "", sopsUse)
	maxFlag := fs.Int("max", 20, "`n` failures of each kind to list before a MORE line; 0 lists all (default 20)")
	if err := parseFlags(fs, args); err != nil {
		return s.refuse("check", 2, err)
	}

	okLine, failLines, moreLines, summaryLine, code, err := secrets.RunCheck(*storeFlag, *asFlag, *keyFlag, *sopsFlag, *maxFlag)
	if err != nil {
		return s.refuse("check", code, err)
	}

	if code == 0 {
		fmt.Fprintln(s.stdout, okLine)
		return 0
	}

	// Failure output
	for _, l := range failLines {
		fmt.Fprintln(s.stderr, l)
	}
	for _, m := range moreLines {
		fmt.Fprintln(s.stderr, m)
	}
	fmt.Fprintln(s.stderr, summaryLine)
	return code
}
