package up

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
)

// The layout of a root (docs/SPEC-UP.md "The root"). Every path a step
// writes is one of these, under the root, but for the one unit file the
// service manager reads from its own directory (redis.go).
const (
	Seat       = "coordinator" // the coordinator's seat in the secrets store and the sprint's actor
	SprintTwin = "stores/sprint.twin"
	SecretsDir = "secrets"
	SecretsGit = "secrets.git" // the secrets store's upstream: a bare repository beside it
	KeysDir    = "keys"
	SeatFile   = "seat.env"
	SmokeDir   = "smoke"
	LogsDir    = "logs"
)

// KeyFile is the seat's age identity under the root.
func KeyFile() string { return filepath.Join(KeysDir, Seat+".key") }

// exists reports whether path is there; an error other than not-there is reported as there,
// so a plan never says create over a file it could not read.
func exists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, fs.ErrNotExist)
}

// fileState is the plan of one file that should hold want: create when it is
// absent, change when it differs, ok when it holds want.
func fileState(path string, want []byte) State {
	got, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Create
	case err != nil || !bytes.Equal(got, want):
		return Change
	}
	return OK
}

// writeFile writes data to path whole (atomicfile), making its directory.
func writeFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicfile.WriteFile(path, data, perm)
}

// field is the value of key=<v> in a tool's typed line, or "".
func field(out, key string) string {
	for _, w := range strings.Fields(out) {
		if v, ok := strings.CutPrefix(w, key+"="); ok {
			return v
		}
	}
	return ""
}

// program is a program a step needs on PATH: how to check it and how to install it.
type program struct {
	name    string
	version []string // the arguments that print its version
	brew    string   // the Homebrew formula on darwin; "" installs with go
	apt     string   // the apt package on linux; "" installs with go
	goPkg   string   // the go install path of a tool built from Go; "" for a tool of this repository
}

// install is the one command that provides t on goos.
func (t program) install(goos string) string {
	switch {
	case goos == "darwin" && t.brew != "":
		return "brew install " + t.brew
	case goos == "linux" && t.apt != "":
		return "sudo apt-get install -y " + t.apt
	}
	if t.goPkg != "" {
		return fmt.Sprintf("go install %s@latest", t.goPkg)
	}
	if bi, ok := debug.ReadBuildInfo(); ok && strings.Contains(bi.Main.Path, ".") {
		return fmt.Sprintf("go install %s/cmd/%s@latest", bi.Main.Path, t.name)
	}
	return "go install ./cmd/" + t.name + " (in a checkout of this repository)"
}

// novaTool is a tool of this repository, installed from the module nova-up was built from.
func novaTool(name string) program { return program{name: name, version: []string{"version"}} }

// tools is every program a --local setup runs, in the order the binaries step checks them.
var tools = []program{
	{name: "git", version: []string{"version"}, brew: "git", apt: "git"},
	{name: "redis-server", version: []string{"--version"}, brew: "redis", apt: "redis-server"},
	{name: "sops", version: []string{"--version"}, brew: "sops", goPkg: "github.com/getsops/sops/v3/cmd/sops"},
	{name: "age-keygen", version: []string{"--version"}, brew: "age", apt: "age"},
	novaTool("nova-sprint"),
	novaTool("nova-secrets"),
	novaTool("nova-redis"),
	novaTool("nova-bus"),
}

// path is the program's path on PATH, or its bare name when it is not there
// (a plan that reaches it has already stopped on the binaries step).
func (e *Env) path(name string) string {
	if p, err := e.Exec.LookPath(name); err == nil {
		return p
	}
	return name
}
