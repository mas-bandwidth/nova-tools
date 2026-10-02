package update

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// exampleManifest is the manifest the `example` verb prints or writes: one tool,
// Go, whose installed and latest versions are both read on this machine, so a
// first run needs Go on PATH. Its apply column is real, and the help's example
// lines run it only under --dry-run.
const exampleManifest = Header + "\n" +
	"# The example manifest: Go, read with go version on both sides. Replace it with your own tools.\n" +
	"go\ttool\tgo version\tlocal:go version\tgo install golang.org/dl/go{version}@latest\tcaller\n"

// exampleVerb is `example`: the example manifest printed (an inspection), or
// written to --out (a local write) and the next command named. A file already
// holding the example is left as it is and reported unchanged, so the help's
// example lines can run again; a file holding anything else is never overwritten.
func exampleVerb(name, out string) *tool.Out {
	if out == "" {
		o := tool.Payload(exampleManifest)
		o.Verb = "example"
		return o
	}
	help := name + " example -h"
	unchanged := false
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	switch {
	case errors.Is(err, fs.ErrExist):
		held, readErr := os.ReadFile(out)
		if readErr != nil || string(held) != exampleManifest {
			return refused("example", help, fmt.Sprintf("%s exists and is not the example manifest; refusing to overwrite it (name another --out)", out))
		}
		unchanged = true
	case err != nil:
		return refused("example", help, fmt.Sprintf("cannot create --out %s (%s) (name a writable path)", out, err))
	default:
		_, err = f.WriteString(exampleManifest)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return refused("example", help, fmt.Sprintf("cannot write --out %s (%s) (name a writable path)", out, err))
		}
	}
	entries, err := Load(strings.NewReader(exampleManifest))
	o := &tool.Out{Verb: "example", Status: tool.OK}
	if err != nil {
		o.Status, o.Exit, o.Why = tool.Failed, 1, []string{"the example manifest does not load: " + err.Error()}
		return o
	}
	o.Fact("wrote", out).Fact("entries", len(entries)).Fact("unchanged", unchanged)
	return o.Note("next: " + name + " report --file " + out)
}
