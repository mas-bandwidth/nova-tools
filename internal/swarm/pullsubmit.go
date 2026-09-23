package swarm

// The per-bench puller of docs/SPEC-FLEET-KUBE.md Part 2 (`nova-swarm pull --submit`, one
// replica): it lists queue/lanes/ in lane order, takes one card by rename(<name>.card,
// taken/<worker>-<name>.card) -- two pullers cannot take one card because the rename
// decides -- and creates its Job. The Job's requests are the capacity line's two memory
// terms, enforced by the scheduler against the 25 GiB reserved floor; the load term is the
// puller's own: it declines to submit while cores*1.5 - load1 <= 0.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/lanes"
)

// The Job's requests and limits: the capacity line as scheduler facts.
const (
	JobCPURequest       = "1"
	JobMemoryRequest    = "2Gi"
	JobEphemeralRequest = "2Gi"
	JobMemoryLimit      = "2Gi"
	jobRequestGiB       = 2 // GiB each Job asks of memory and of ephemeral storage
	// KubeReservedFloorGiB is the floor kube-reserved/system-reserved holds back.
	KubeReservedFloorGiB = 25
)

// submitLanes is the order the puller reads the lanes in; the lanes are the order, the
// puller never chooses one.
var submitLanes = []string{lanes.Red, lanes.Green, lanes.Small, lanes.Next}

// SchedulerAdmits is how many Jobs of this shape the scheduler places on a node with
// free_gb of disk and memfree_gb of memory once the floor is reserved: floor(allocatable /
// request) for each resource, the smaller of the two, never negative. It is the two memory
// terms of AdmissionLine, enforced by Kubernetes instead of counted by a person.
func SchedulerAdmits(freeGB, memFreeGB int) int {
	disk := (freeGB - KubeReservedFloorGiB) / jobRequestGiB
	mem := memFreeGB / jobRequestGiB
	n := disk
	if mem < n {
		n = mem
	}
	if n < 0 {
		return 0
	}
	return n
}

// LoadHeadroom is the one term of the capacity line the puller enforces itself:
// cores*1.5 - load1. At or below zero the puller declines to submit.
func LoadHeadroom(cores int, load1 float64) float64 {
	return float64(cores)*1.5 - load1
}

// KubeJob is a batch/v1 Job manifest. It marshals to JSON, which is YAML, so a k3s
// auto-deploy manifests directory applies it as written.
type KubeJob map[string]any

// workerName is the expected worker-name format: one filename component of at most 63
// characters, [A-Za-z0-9._-], starting with a letter or digit. No separator, no dot or
// dot-dot, nothing that can leave <bench>/taken when it is joined into a path.
var workerName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// ValidWorker reports why worker cannot name a taken card, or nil when it is exactly one
// safe filename component in the expected worker-name format.
func ValidWorker(worker string) error {
	if worker == "" {
		return fmt.Errorf("worker name is empty")
	}
	if !workerName.MatchString(worker) || filepath.Base(worker) != worker {
		return fmt.Errorf("worker name %q is not one safe filename component ([A-Za-z0-9][A-Za-z0-9._-]{0,62})", worker)
	}
	return nil
}

// takenPath is <taken>/<worker>-<name>.card, refused unless it lies directly inside taken.
func takenPath(taken, worker, name string) (string, error) {
	to := filepath.Join(taken, worker+"-"+name+CardExt)
	if filepath.Dir(to) != filepath.Clean(taken) {
		return "", fmt.Errorf("taken path %q is not directly inside %s", to, taken)
	}
	return to, nil
}

// PullSubmitInput is one puller turn. Submit creates the Job; the verb writes it where
// k3s applies it, a test records it.
type PullSubmitInput struct {
	Bench  string
	Worker string
	Cores  int
	Load1  float64
	Image  string
	Runner string
	Submit func(name string, job KubeJob) error
}

// PullSubmitResult is what one turn did: declined on the load line, took nothing from an
// empty queue, or took Card from Lane into Taken and created Job.
type PullSubmitResult struct {
	Worker   string
	Declined bool
	Headroom float64
	Card     string
	Lane     string
	Taken    string
	Job      string
}

// PullSubmit is one puller turn: check the load line, take one card by rename, create its
// Job. A Job that cannot be created returns the card to its lane, so a failed submit never
// strands a taken card with no Job.
func PullSubmit(in PullSubmitInput) (PullSubmitResult, error) {
	worker := strings.TrimSpace(in.Worker)
	res := PullSubmitResult{Worker: worker, Headroom: LoadHeadroom(in.Cores, in.Load1)}
	// The worker is checked before any card is scanned or moved: it becomes part of the
	// taken path, so a separator or dot-dot would carry the card outside <bench>/taken.
	if err := ValidWorker(worker); err != nil {
		return res, err
	}
	if in.Submit == nil {
		return res, fmt.Errorf("no Job submitter")
	}
	if res.Headroom <= 0 {
		res.Declined = true
		return res, nil
	}
	taken := filepath.Join(in.Bench, "taken")
	for _, lane := range submitLanes {
		dir := filepath.Join(in.Bench, "queue", lanes.LanesDir, lane)
		matches, err := filepath.Glob(filepath.Join(dir, "*"+CardExt))
		if err != nil {
			return res, err
		}
		sort.Strings(matches)
		for _, from := range matches {
			if err := os.MkdirAll(taken, 0o755); err != nil {
				return res, err
			}
			name := strings.TrimSuffix(filepath.Base(from), CardExt)
			to, err := takenPath(taken, worker, name)
			if err != nil {
				return res, err
			}
			// rename replaces an existing destination: never land on a card already taken.
			if _, err := os.Lstat(to); err == nil {
				return res, fmt.Errorf("take %s: %s already exists; refusing to overwrite a taken card", name, to)
			} else if !errors.Is(err, os.ErrNotExist) {
				return res, err
			}
			if err := os.Rename(from, to); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue // another puller took this card first: the rename decided
				}
				return res, err
			}
			jobName := JobName(worker, name)
			if err := in.Submit(jobName, CardJob(jobName, in.Bench, name, to, in.Image, in.Runner)); err != nil {
				if _, statErr := os.Lstat(from); statErr == nil {
					return res, fmt.Errorf("submit %s: %v; %s is occupied, card left at %s", name, err, from, to)
				}
				if back := os.Rename(to, from); back != nil {
					return res, fmt.Errorf("submit %s: %v; return to %s: %v", name, err, lane, back)
				}
				return res, fmt.Errorf("submit %s: %v; card returned to %s", name, err, lane)
			}
			res.Card, res.Lane, res.Taken, res.Job = name, lane, to, jobName
			return res, nil
		}
	}
	return res, nil
}

// CardJob is the Job for one taken card: one container running the runner with PULL_CARD
// naming the taken card, the bench mounted at its own path, no retries (the pull is the
// retry), and the requests and limits that are the capacity line.
func CardJob(name, bench, card, taken, image, runner string) KubeJob {
	type m = map[string]any
	return KubeJob{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata":   m{"name": name, "labels": m{"nova.card": dnsLabel(card)}},
		"spec": m{"backoffLimit": 0, "template": m{"spec": m{
			"restartPolicy": "Never",
			"containers": []m{{
				"name":    "card",
				"image":   image,
				"command": []string{"sh", "-c", runner},
				"env":     []m{{"name": "PULL_CARD", "value": taken}, {"name": "PULL_LABEL", "value": card}},
				"resources": m{
					"requests": m{"cpu": JobCPURequest, "memory": JobMemoryRequest, "ephemeral-storage": JobEphemeralRequest},
					"limits":   m{"memory": JobMemoryLimit},
				},
				"volumeMounts": []m{{"name": "bench", "mountPath": bench}},
			}},
			"volumes": []m{{"name": "bench", "hostPath": m{"path": bench}}},
		}}},
	}
}

// JobName is a DNS-1123 label for the card's Job: lower case, [a-z0-9-], at most 63
// characters, starting and ending with an alphanumeric.
func JobName(worker, card string) string {
	name := dnsLabel("card-" + worker + "-" + card)
	if name == "" {
		return "card"
	}
	return name
}

// dnsLabel maps s onto [a-z0-9-], at most 63 characters, alphanumeric at both ends.
func dnsLabel(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := b.String()
	if len(out) > 63 {
		out = out[:63]
	}
	return strings.Trim(out, "-")
}
