package pulse

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// SPEC-PULSE ## Fleet, rule R (pit stop 4, 2026-09-16): nothing enters the loop untested.
// `fleet add <bench>` is the one verb that admits a bench: it reads the fleet-probe record
// (the ci.yml fleet-probe job, one job per runner slot, read back by runner_name) and
// refuses the bench unless every runner name is green, naming the runner that is not and the
// run id. PULSE_ROOTS and the runner labels are written only by this verb, never by hand.

// FleetProbeJob is one fleet-probe job read back by runner_name.
type FleetProbeJob struct {
	Runner     string
	Conclusion string
}

// FleetProbeRecord is the read-back of one fleet-probe dispatch: the run id and the job of
// every runner slot.
type FleetProbeRecord struct {
	Run   string
	Bench string
	Jobs  []FleetProbeJob
}

// FleetAddInput is `fleet add <bench>`.
type FleetAddInput struct {
	Bench  string
	Queue  string
	Roots  string
	Probe  string
	Stdout io.Writer
	Stderr io.Writer
}

// FleetProbeRecordFile is where the fleet-probe read-back is kept under the queue unless
// the verb is given an explicit --probe path.
const FleetProbeRecordFile = "fleet-probe.tsv"

// FleetRootsFile and FleetLabelsFile are written by `fleet add` alone: the roots the loop
// holds and the runner labels the admitted bench brings. Nothing else writes them.
const (
	FleetRootsFile  = "PULSE_ROOTS"
	FleetLabelsFile = "runner-labels.tsv"
)

// ReadFleetProbeRecord reads the read-back of the fleet-probe job (ci.yml, #872): `run` and
// `bench` lines, then one `runner<TAB><name><TAB><conclusion>` line per runner slot. A line
// that is not one of those three fields is a refusal naming the file and the line, because a
// partially-read probe would read as a green one.
func ReadFleetProbeRecord(path string) (FleetProbeRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return FleetProbeRecord{}, err
	}
	var rec FleetProbeRecord
	for i, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimRight(line, "\r")
		if strings.TrimSpace(t) == "" {
			continue
		}
		f := strings.Split(t, "\t")
		switch f[0] {
		case "run":
			if len(f) < 2 {
				return rec, fmt.Errorf("%s line %d: run wants an id", path, i+1)
			}
			rec.Run = strings.TrimSpace(f[1])
		case "bench":
			if len(f) < 2 {
				return rec, fmt.Errorf("%s line %d: bench wants a label", path, i+1)
			}
			rec.Bench = strings.TrimSpace(f[1])
		case "runner":
			if len(f) < 3 {
				return rec, fmt.Errorf("%s line %d: runner wants name<TAB>conclusion", path, i+1)
			}
			rec.Jobs = append(rec.Jobs, FleetProbeJob{
				Runner:     strings.TrimSpace(f[1]),
				Conclusion: strings.TrimSpace(f[2]),
			})
		default:
			return rec, fmt.Errorf("%s line %d: unknown field %q", path, i+1, f[0])
		}
	}
	return rec, nil
}

// fleetConclusionGreen is the only conclusion that admits a runner.
func fleetConclusionGreen(conclusion string) bool {
	return strings.EqualFold(strings.TrimSpace(conclusion), "success")
}

// FleetAdd admits one bench or refuses it. A bench whose probe carries a job that is not
// green, or a probe with no jobs at all, is refused with the offending runner name and the
// run id on the line; a bench whose every runner name is green is admitted and its
// PULSE_ROOTS and runner labels written.
func FleetAdd(in FleetAddInput) int {
	out := in.Stdout
	if out == nil {
		out = io.Discard
	}
	errw := in.Stderr
	if errw == nil {
		errw = io.Discard
	}
	probe := in.Probe
	if strings.TrimSpace(probe) == "" {
		probe = filepath.Join(in.Queue, FleetProbeRecordFile)
	}
	rec, err := ReadFleetProbeRecord(probe)
	if err != nil {
		fmt.Fprintf(errw, "FLEET REFUSED bench=%s: the fleet-probe record is unreadable: %s\n",
			oneline.Field(in.Bench), oneline.Field(err.Error()))
		return 2
	}

	bad := ""
	green := 0
	for _, job := range rec.Jobs {
		if fleetConclusionGreen(job.Conclusion) {
			green++
			continue
		}
		if bad == "" {
			bad = job.Runner
		}
	}
	if len(rec.Jobs) == 0 || bad != "" {
		if bad == "" {
			bad = "-"
		}
		fmt.Fprintf(out, "FLEET REFUSED bench=%s runner=%s run=%s: the fleet-probe is not all green (green=%d of %d)\n",
			oneline.Field(in.Bench), oneline.Field(bad), oneline.Field(rec.Run), green, len(rec.Jobs))
		return 2
	}

	if err := writeFleetAdmission(in.Queue, in.Roots, rec.Jobs); err != nil {
		fmt.Fprintf(errw, "FLEET REFUSED bench=%s: %s\n", oneline.Field(in.Bench), oneline.Field(err.Error()))
		return 2
	}
	fmt.Fprintf(out, "FLEET ADD bench=%s run=%s runners=%d\n",
		oneline.Field(in.Bench), oneline.Field(rec.Run), len(rec.Jobs))
	return 0
}

// writeFleetAdmission writes the two values `fleet add` owns: PULSE_ROOTS, the roots the
// loop will hold, and runner-labels.tsv, one admitted runner per line.
func writeFleetAdmission(queue, roots string, jobs []FleetProbeJob) error {
	if strings.TrimSpace(queue) == "" {
		return fmt.Errorf("no queue directory to write %s into", FleetRootsFile)
	}
	if err := os.MkdirAll(queue, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(queue, FleetRootsFile), []byte(strings.TrimSpace(roots)+"\n"), 0o644); err != nil {
		return err
	}
	var b strings.Builder
	for _, job := range jobs {
		b.WriteString(job.Runner)
		b.WriteByte('\n')
	}
	return os.WriteFile(filepath.Join(queue, FleetLabelsFile), []byte(b.String()), 0o644)
}
