// init.go holds the init verb: its flags, its run and the helpers only it uses.

package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/mas-bandwidth/nova-tools/internal/fuse"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// cmdInit makes an empty box where none is. It is the one way a box comes into being
// clear, and it never replaces a box: a box already at the path, blown or not, readable
// or not, is left as it is and the run exits 1, because replacing a box is the lockdown
// reset this tool does not have.
//
// tla/FuseBox.tla is the model of the box these verbs act on: its invariants are
// that the gate answers only from a box it read and from every --box named, that
// only a lift, init or a hand-edit makes a surface clear, that init never
// replaces a box and that a lockdown always blows (MCFuseBox*.cfg, five reversed
// witnesses).
func cmdInit(rest []string, stdout, stderr io.Writer, inv invocation) int {
	box, positional, ok, parsed, dry := parseWrite("init", rest, stderr, inv.getenv)
	if !parsed {
		return 2
	}
	if len(positional) > 0 {
		refuse(stderr, " init", fmt.Sprintf("unexpected argument %q", positional[0]))
		ok = false
	}
	if !ok {
		return 2
	}
	exists := func() int {
		fmt.Fprintf(stderr, "INIT FAILED box=%s: something is already there, and init never replaces a box (a blown lockdown is replaced only in a live conversation with the person you work with); read it with %s\n", oneline.Field(box), oneline.Escape(boxRemedy("status", box)))
		return 1
	}
	// A dry run is this run's own plan: the creation's every check, refusing where
	// it would, and nothing written.
	create := fuse.CreateBox
	if dry {
		create = fuse.PlanCreateBox
	}
	if err := create(boxFile(inv.wd, box)); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return exists()
		}
		fmt.Fprintf(stderr, "INIT FAILED box=%s: could not make the box: %s\n", oneline.Field(box), oneline.Err(err))
		return 1
	}
	if dry {
		fmt.Fprintf(stdout, "INIT OK box=%s dry_run=true: nothing written; a real run would make an empty box there\n", oneline.Field(box))
		return 0
	}
	// Re-read. The exit code of a remedy is not evidence the remedy worked.
	after, err := fuse.ReadBox(boxFile(inv.wd, box))
	if err != nil || after.Lockdown != nil || len(after.Quarantine) != 0 {
		fmt.Fprintf(stderr, "INIT FAILED box=%s: made but unverifiable (%s): do not trust it; tell the person you work with\n", oneline.Field(box), oneline.Err(err))
		return 1
	}
	fmt.Fprintf(stdout, "INIT OK box=%s: an empty box, no fuse blown (verified by re-reading the box)\n", oneline.Field(box))
	return 0
}
