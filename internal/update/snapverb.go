package update

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// snapshotVerb reports the manifest --file names, one count per run. The
// earlier verb wrote a draft manifest by scanning a directory of nova-*
// executables and running each `version` command; that reports every executable
// found on the directory -- 32 where the person adopted 16 -- and it wrote a
// file the person then had to promote by hand. What a person has adopted is the
// hand-written --file manifest itself, so the verb now reads that file and
// counts its entries: the adopted count, not the scan.
func snapshotVerb(name string, args []string, out, errs io.Writer) int {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var file string
	fs.StringVar(&file, "file", "", "manifest")
	if err := fs.Parse(args); err != nil {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("%s (run %s help)", err, name))
	}
	if file == "" {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("missing --file; refusing to guess (supply --file <manifest>; run: %s help)", name))
	}
	if len(fs.Args()) != 0 {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("snapshot takes no positional arguments (run %s help)", name))
	}
	f, err := os.Open(file)
	if err != nil {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("cannot open %s (supply a readable --file: %s)", file, manifestShape))
	}
	entries, err := Load(f)
	f.Close()
	if err != nil {
		return refusal(errs, "SNAPSHOT", fmt.Errorf("%s: %w", file, err))
	}
	fmt.Fprintf(out, "SNAPSHOT OK known=%d file=%s\n", len(entries), field(file))
	return 0
}
