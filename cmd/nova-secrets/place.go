// place.go holds the place verb: its flags, its run and the helpers only it uses.

package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

func runPlaceCLI(args []string, s streams) int {
	fs := flag.NewFlagSet("place", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", storeUse)
	asFlag := fs.String("as", "", asUse)
	keyFlag := fs.String("key", "", keyUse)
	sopsFlag := fs.String("sops", "", sopsUse)
	machineFlag := fs.String("machine", "", "the fleet machine's `name`, a row of --machines (required)")
	secretFlag := fs.String("secret", "", "the key `NAME` in <store>/<as>.yaml whose value is copied (required)")
	pathFlag := fs.String("path", "", "the remote `path` written (default <home>/.config/nova-secrets/<NAME>.env, home from the machine's row)")
	machinesFlag := fs.String("machines", "", "the fleet registry `file`: one machine per line, name, ssh target, home, tab separated (default ~/.config/nova-tools/fleet.tsv)")
	receiptsFlag := fs.String("receipts", "", receipts)
	sshFlag := fs.String("ssh", "ssh", "`path` of the ssh program (default ssh)")
	dryRunFlag := fs.Bool("dry-run", false, dryRunHelp)
	if err := parseFlags(fs, args); err != nil {
		return s.refuse("place", 2, err)
	}

	okLine, err := secrets.RunPlace(secrets.PlaceInput{
		StoreDir:   *storeFlag,
		AsName:     *asFlag,
		KeyPath:    *keyFlag,
		SopsPath:   *sopsFlag,
		Machine:    *machineFlag,
		Secret:     *secretFlag,
		RemotePath: *pathFlag,
		Machines:   *machinesFlag,
		Receipts:   *receiptsFlag,
		SSH:        *sshFlag,
		DryRun:     *dryRunFlag,
	})
	if err != nil {
		return s.refuse("place", 2, err)
	}
	fmt.Fprintln(s.stdout, okLine)
	return 0
}
