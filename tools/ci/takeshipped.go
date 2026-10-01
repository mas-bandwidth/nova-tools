package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// shippedTargets maps a hosted runner's image name to the platform of the
// release binary it smokes.
var shippedTargets = map[string]string{
	"ubuntu-latest":  "linux_amd64",
	"macos-latest":   "darwin_arm64",
	"windows-latest": "windows_amd64",
}

func init() {
	register(verb{
		name:    "take-shipped",
		summary: "copy this runner's shipped release binary out of the release artifact",
		help: `usage: go run ./tools/ci take-shipped --os RUNNER --tool NAME --stamp STAMP --dist DIR --out FILE

Takes THE BINARY THAT WOULD SHIP, not one built here: a binary built in the job is
a different binary from the released one (different flags, no -trimpath, CGO as
the host has it), and the smoke job's whole argument is that what a release ships
is the executable. It copies DIR/NAME_STAMP_TARGET (with .exe on windows) to FILE,
marks it executable, and runs "FILE version" (its verdict is not this verb's).

RUNNER is the hosted image (ubuntu-latest, macos-latest, windows-latest), which
names the shipped target (linux_amd64, darwin_arm64, windows_amd64). The release
name is <tool>_<stamp>_<goos>_<goarch>[.exe]. A binary that is not in the
artifact is never papered over: the directory is listed and the verb exits 1.

exit 0  the binary is in place
exit 1  the runner maps to no shipped target, or the binary is not in the artifact
exit 2  usage
`,
		do: func(e env, args []string) int { return takeShipped(e, osCmdRunner{}, args) },
	})
}

// takeShipped is the verb over a runner.
func takeShipped(e env, r cmdRunner, args []string) int {
	flags := map[string]string{}
	for i := 0; i < len(args); i += 2 {
		name := strings.TrimPrefix(args[i], "--")
		switch name {
		case "os", "tool", "stamp", "dist", "out":
		default:
			fmt.Fprintf(e.stderr, "take-shipped: unknown argument %q; usage: go run ./tools/ci help take-shipped\n", args[i])
			return 2
		}
		if !strings.HasPrefix(args[i], "--") || i+1 >= len(args) {
			fmt.Fprintf(e.stderr, "take-shipped: %s needs a value\n", args[i])
			return 2
		}
		flags[name] = args[i+1]
	}
	for _, f := range []string{"os", "tool", "stamp", "dist", "out"} {
		if flags[f] == "" {
			fmt.Fprintf(e.stderr, "take-shipped: --%s is required\n", f)
			return 2
		}
	}
	target, ok := shippedTargets[flags["os"]]
	if !ok {
		fmt.Fprintf(e.stdout, "no shipped target mapped for %s\n", flags["os"])
		return 1
	}
	src := filepath.Join(flags["dist"], fmt.Sprintf("%s_%s_%s", flags["tool"], flags["stamp"], target))
	if target == "windows_amd64" {
		src += ".exe"
	}
	in, err := os.Open(src)
	if err != nil {
		fmt.Fprintf(e.stdout, "%s is not in the artifact: ls -R %s\n", src, flags["dist"])
		filepath.WalkDir(flags["dist"], func(p string, d os.DirEntry, err error) error {
			if err == nil {
				fmt.Fprintln(e.stdout, p)
			}
			return nil
		})
		return 1
	}
	defer in.Close()
	out := flags["out"]
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fmt.Fprintf(e.stderr, "take-shipped: %v\n", err)
		return 1
	}
	dst, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		fmt.Fprintf(e.stderr, "take-shipped: %v\n", err)
		return 1
	}
	if _, err := io.Copy(dst, in); err != nil {
		dst.Close()
		fmt.Fprintf(e.stderr, "take-shipped: %v\n", err)
		return 1
	}
	if err := dst.Close(); err != nil {
		fmt.Fprintf(e.stderr, "take-shipped: %v\n", err)
		return 1
	}
	if err := os.Chmod(out, 0o755); err != nil {
		fmt.Fprintf(e.stderr, "take-shipped: %v\n", err)
		return 1
	}
	// The binary's own verdict on "version" is not this verb's: the smoke
	// assertions decide what it must do.
	r.Run(cmdSpec{Name: out, Args: []string{"version"}, Stdout: e.stdout, Stderr: e.stderr})
	return 0
}
