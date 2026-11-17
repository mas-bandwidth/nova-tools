package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

const resourcesHelp = `nova-fleet resources [--json]

Query fleet resource capacities, free headroom, active/paused leases, timed
claims, and queued waiters from nova-reserve serve in one snapshot RPC (no SSH
fan-out).
  --json  print the raw snapshot JSON object
Exit: 0 ok; 2 could not query nova-reserve.
`

type reserveSnapshot struct {
	Op             string                `json:"op"`
	ReconnectUntil string                `json:"reconnect_until"`
	Machines       []reserveMachineState `json:"machines"`
	Queue          []any                 `json:"queue,omitempty"`
}

type reserveMachineState struct {
	Host        string         `json:"host"`
	Address     string         `json:"address,omitempty"`
	User        string         `json:"user,omitempty"`
	Online      bool           `json:"online"`
	Tags        []string       `json:"tags"`
	UsableCores int            `json:"usable_cores"`
	Resources   map[string]int `json:"resources"`
	Free        map[string]int `json:"free"`
	Leases      []struct {
		Paused   bool           `json:"paused"`
		Sublease bool           `json:"sublease"`
		Needs    map[string]int `json:"needs"`
	} `json:"leases"`
	Claims []any `json:"claims"`
	Queue  []any `json:"queue"`
}

var (
	resourcesSeamMu sync.Mutex
	queryReserveRaw = defaultQueryReserveSnapshot
)

func defaultQueryReserveSnapshot() (string, error) {
	bin, err := lookPath("nova-reserve")
	if err != nil {
		return "", fmt.Errorf("nova-reserve not on PATH; run: nova-fleet check")
	}
	out, why, code := outCmd(bin, []string{"status", "--json"}, os.Environ())
	if code != 0 {
		msg := strings.TrimSpace(why)
		if msg == "" {
			msg = fmt.Sprintf("nova-reserve status --json exited %d", code)
		}
		return "", fmt.Errorf("%s", msg)
	}
	return strings.TrimSpace(out), nil
}

func runResources(args []string, stdout, stderr io.Writer) int {
	jsonOut := false
	for _, a := range args {
		switch a {
		case "-h", "--help":
			fmt.Fprint(stdout, resourcesHelp)
			return 0
		case "--json":
			jsonOut = true
		default:
			fmt.Fprintf(stderr, "nova-fleet resources: unknown flag or argument %s; run: nova-fleet resources -h\n", a)
			return 2
		}
	}

	resourcesSeamMu.Lock()
	fetch := queryReserveRaw
	resourcesSeamMu.Unlock()

	raw, err := fetch()
	if err != nil {
		fmt.Fprintf(stderr, "nova-fleet resources: %v; run: nova-reserve status -h\n", err)
		return 2
	}
	var snap reserveSnapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		fmt.Fprintf(stderr, "nova-fleet resources: invalid snapshot JSON (%v); run: nova-reserve status --json\n", err)
		return 2
	}
	if jsonOut {
		fmt.Fprintln(stdout, raw)
		return 0
	}
	for _, m := range snap.Machines {
		status := "ok"
		if !m.Online {
			status = "offline"
		}
		usedCPU, usedMem := 0, 0
		for _, l := range m.Leases {
			if !l.Paused && !l.Sublease {
				usedCPU += l.Needs["cpu"]
				usedMem += l.Needs["memory"]
			}
		}
		tags := cmp.Or(strings.Join(m.Tags, ","), "-")
		fmt.Fprintf(stdout, "RESOURCE %s status=%s cpu=%d/%d memory=%d/%d free_cpu=%d free_memory=%d leases=%d claims=%d queued=%d tags=%s\n",
			m.Host, status, usedCPU, m.Resources["cpu"], usedMem, m.Resources["memory"],
			m.Free["cpu"], m.Free["memory"], len(m.Leases), len(m.Claims), len(m.Queue), tags)
	}
	return 0
}
