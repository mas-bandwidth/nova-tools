// gate.go holds the gate verb: its flags, its run and the helpers only it uses.

package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

func runGateCLI(args []string, s streams) int {
	fs := flag.NewFlagSet("gate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", "`dir` of the store checkout the pull request is against (required)")
	baseFlag := fs.String("base", "", "the pull request's base commit, a git `ref` (required)")
	headFlag := fs.String("head", "", "the pull request's head commit, a git `ref` (required)")
	machinesFlag := fs.String("machines", "", "the fleet machines registry `file`, whose seat column vouches for a new recipient; without it that rule does not run and APPROVE says machines=-")

	// Every gate flag takes one value. Package flag keeps the LAST of a repeated flag, so
	// `--head <ref> --head <other>` judged a diff the caller did not name first. A flag
	// named twice is refused before any ref is read.
	repeated := ""
	if err := parseOnce(fs, args, &repeated); err != nil {
		if repeated != "" {
			err = fmt.Errorf("--%s is given more than once; every gate flag takes one value", repeated)
		}
		return s.refuse("gate", 2, err)
	}

	if len(fs.Args()) > 0 {
		return s.refuse("gate", 2, fmt.Errorf("unexpected argument %s; gate takes flags only", oneline.Quote(fs.Args()[0])))
	}

	line, code := secrets.RunGate(secrets.GateInput{
		StoreDir:     *storeFlag,
		Base:         *baseFlag,
		Head:         *headFlag,
		MachinesPath: *machinesFlag,
	})
	if code != 0 {
		fmt.Fprintln(s.stderr, line)
	} else {
		fmt.Fprintln(s.stdout, line)
	}
	return code
}

// parseOnce is verbflag.Parse with every flag of fs taking one value. Each value is
// wrapped in a onceValue for the parse and unwrapped when it returns, on every path --
// the -h path included, which unwinds as verbflag's Help panic to the dispatcher's
// Recover: the help it prints then reads the flags' own types, not the wrapper's.
func parseOnce(fs *flag.FlagSet, args []string, repeated *string) error {
	orig := map[string]flag.Value{}
	fs.VisitAll(func(f *flag.Flag) {
		orig[f.Name] = f.Value
		f.Value = &onceValue{Value: f.Value, name: f.Name, repeated: repeated}
	})
	defer fs.VisitAll(func(f *flag.Flag) { f.Value = orig[f.Name] })
	return parseVerb(fs, args)
}
