package up

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"time"
)

// The smoke step: one card's whole flow on its own twin, the first run of
// docs/CLI.md's nova-sprint section, beside a throwaway repository made for
// it under the root, ending in the card landed. Its record is when it landed
// and the twin's summary line; a run that finds the record plans ok and runs
// nothing (docs/SPEC-UP.md "Steps", 8).
func init() { Register(Step{Name: "smoke", Order: 90, Plan: planSmoke, Apply: applySmoke}) }

const (
	smokeCard   = "s1-1.w1@1"
	smokeTwin   = "smoke/sprint.twin"
	smokeRepo   = "smoke/repo"
	smokeRecord = "smoke/landed"
	smokeLanded = "1/1 100.0%"
)

// smokeFlow is the first run, each line one nova-sprint call; the last tick completes the landing.
var smokeFlow = []string{
	"init --readers reader-a,reader-b --members m1",
	"add --stream s1 --count 1 --one",
	"start",
	"tick",
	"tick",
	"take --as m1 --epoch 0",
	"finish --as m1 " + smokeCard + " --epoch 0 --report done",
	"tick",
	"read --as reader-a --begin --epoch 0",
	"read --as reader-a --ok --epoch 0",
	"tick",
	"merge --stream s1 --batch 1",
	"tick",
}

func planSmoke(e *Env) Finding {
	if b, err := os.ReadFile(e.Path(smokeRecord)); err == nil && len(b) > 0 {
		return Finding{OK, "card " + smokeCard + " landed: " + strings.TrimSpace(string(b))}
	}
	return Finding{Create, "one card on " + e.Path(smokeTwin) + " beside the throwaway repository " + e.Path(smokeRepo)}
}

func applySmoke(e *Env) error {
	git := e.path("git")
	if !exists(e.Path(smokeRepo, ".git")) {
		if _, err := e.Run(Cmd{Name: git, Args: []string{"init", "-q", "-b", "main", e.Path(smokeRepo)}}); err != nil {
			return err
		}
		if _, err := e.Run(Cmd{Name: git, Args: []string{"-C", e.Path(smokeRepo), "-c", "user.name=nova-up", "-c", "user.email=nova-up@example.invalid",
			"commit", "-q", "--allow-empty", "-m", "nova-up: the smoke's throwaway repository"}}); err != nil {
			return err
		}
	}
	// A twin a run left half-way is begun again: the flow starts from init.
	if err := os.Remove(e.Path(smokeTwin)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var last string
	for _, l := range smokeFlow {
		out, err := e.Run(Cmd{Name: e.path("nova-sprint"), Args: strings.Fields(l), Dir: e.Path(smokeRepo), Env: sprintEnv(e.Path(smokeTwin))})
		if err != nil {
			return err
		}
		last = out
	}
	// The sprint's summary, `landed/all percent -> ETA ...`, is the tick's last line.
	lines := strings.Split(strings.TrimSpace(last), "\n")
	summary := lines[len(lines)-1]
	if !strings.HasPrefix(summary, smokeLanded) {
		return errors.New("the smoke's last tick did not land the card (its summary is " + summary + "); its twin is " + e.Path(smokeTwin))
	}
	return writeFile(e.Path(smokeRecord), []byte(e.Now().UTC().Format(time.RFC3339)+" "+summary+"\n"), 0o644)
}
