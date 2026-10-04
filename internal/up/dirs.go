package up

import (
	"os"
	"strings"
)

// The dirs step: the root and the directories under it every later step
// writes into; keys/ is the login's alone (docs/SPEC-UP.md "Steps", 2).
func init() { Register(Step{Name: "dirs", Order: 20, Plan: planDirs, Apply: applyDirs}) }

var dirs = []struct {
	name string
	perm os.FileMode
}{{".", 0o755}, {"stores", 0o755}, {KeysDir, 0o700}, {LogsDir, 0o755}, {SmokeDir, 0o755}}

func planDirs(e *Env) Finding {
	var absent []string
	for _, d := range dirs {
		if !exists(e.Path(d.name)) {
			absent = append(absent, d.name)
		}
	}
	if len(absent) > 0 {
		return Finding{Create, e.Root + ": " + strings.Join(absent, " ")}
	}
	return Finding{OK, e.Root}
}

func applyDirs(e *Env) error {
	for _, d := range dirs {
		if err := os.MkdirAll(e.Path(d.name), d.perm); err != nil {
			return err
		}
		if err := os.Chmod(e.Path(d.name), d.perm); err != nil {
			return err
		}
	}
	return nil
}
