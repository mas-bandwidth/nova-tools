package swarm

import (
	"os"
	"strings"
	"testing"
)

// DeepSeek's read 5, finding 1. The FIRST shape of the Windows identity fix kept one
// package-global pid->stamp map, and that map is a second way to lose a job: when a
// finished job's `identify` wrote its own stamp under a pid Windows had already re-issued
// to a LIVE job's supervisor, the dispatcher read that running supervisor as dead,
// finalized it `end=unknown` and freed its slot -- the very failure rule 17 exists to
// close, arriving by the door that was meant to close it.
//
// So the identity travels WITH the pid, from the record that recorded it, and this test is
// the tripwire: no file in the process layer may hold identity of its own. Two jobs whose
// supervisors' pids are learned in each other's window cannot interfere if there is nothing
// between them to interfere through.
func TestNoProcessLayerFileKeepsIdentityOfItsOwn(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"proc_windows.go", "proc_unix.go", "proc_other.go", "proc_linux.go",
		"proc_darwin.go", "proc_bsd.go", "deadline.go"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("the process layer wants %s: %v", name, err)
		}
		body := string(raw)
		for _, forbidden := range []string{"sync.Map", "map[int]", "var known", "func identify", "func noteChild"} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s holds %q: a pid's identity belongs to the job that recorded it, never to a table this file keeps",
					name, forbidden)
			}
		}
	}
	// And every question about a pid takes the identity beside it, so the caller cannot
	// ask one without saying which process it means.
	for _, decl := range []string{
		"func Alive(pid int, started string) bool",
		"func GroupAlive(pgid int, started string) bool",
		"func TerminateGroup(pgid int, started string)",
		"func KillGroup(pgid int, started string)",
	} {
		raw, err := os.ReadFile("proc_windows.go")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), decl) {
			t.Errorf("the process layer wants %q", decl)
		}
	}
}
