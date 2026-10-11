// keygen.go holds the keygen verb: its flags, its run and the helpers only it uses.

package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

func runKeygenCLI(args []string, s streams) int {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	asFlag := fs.String("as", "", "the seat `name` the key is for: letters, digits, - and _ (required)")
	keyFlag := fs.String("key", "", "`path` to write the new private key to; its directory exists with mode 0700 and the file does not (required)")
	ageKeygenFlag := fs.String("age-keygen", "", "`path` of the age-keygen program, as printed by: command -v age-keygen (required)")
	storeFlag := fs.String("store", "", "`dir` of the store, whose recovery.pub fills the rule's recovery key; without it the rule carries <recovery key>")
	if err := parseFlags(fs, args); err != nil {
		return s.refuse("keygen", 2, err)
	}

	// The order is the package's, not this function's: the rule block, then the next
	// step, then the OK line. A reader took a green keygen for a failure when the
	// verdict was at the top and the homework at the bottom.
	lines, err := secrets.RunKeygen(*asFlag, *keyFlag, *ageKeygenFlag, *storeFlag)
	if err != nil {
		return s.refuse("keygen", 2, err)
	}

	for _, l := range lines {
		fmt.Fprintln(s.stdout, l)
	}
	return 0
}
