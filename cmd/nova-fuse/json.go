// json.go holds the --json rendering for check and quarantine (card
// fuse-check-and-receipts-json). JSON goes to stdout; exit codes are the
// text mode's codes unchanged (0 clear/done, 1 blown/failed, 2 refused).

package main

import (
	"encoding/json"
	"fmt"
	"io"
)

// emitJSON writes v as one JSON document on stdout.
func emitJSON(stdout io.Writer, v any) {
	data, err := json.Marshal(v)
	// ignored: the record types hold only strings, bools and pointers to them, so Marshal cannot fail; dropping the record beats printing half of one.
	if err != nil {
		return
	}
	fmt.Fprintln(stdout, string(data))
}

// emitRefusalJSON writes a refused JSON record, filling its remedy field
// with the house remedy when the caller left it empty.
func emitRefusalJSON(stdout io.Writer, v any) {
	switch d := v.(type) {
	case checkJSONDoc:
		if d.Remedy == "" {
			d.Remedy = "run: nova-fuse help"
		}
		v = d
	case quarantineJSONDoc:
		if d.Remedy == "" {
			d.Remedy = "run: nova-fuse help"
		}
		v = d
	}
	emitJSON(stdout, v)
}

type checkFuseJSON struct {
	Surface string `json:"surface"`
	Since   string `json:"since"`
	Reason  string `json:"reason"`
}

type checkJSONDoc struct {
	Verb           string         `json:"verb"`
	Status         string         `json:"status"` // clear | blown | refused
	Lockdown       string         `json:"lockdown,omitempty"`
	Surface        string         `json:"surface,omitempty"`
	SurfaceChecked bool           `json:"surface_checked"`
	Quarantine     *checkFuseJSON `json:"quarantine,omitempty"`
	LockdownFuse   *checkFuseJSON `json:"lockdown_fuse,omitempty"`
	Error          string         `json:"error,omitempty"`
	Remedy         string         `json:"remedy,omitempty"`
}

type quarantineJSONDoc struct {
	Verb    string `json:"verb"`
	Status  string `json:"status"` // ok | failed | refused
	Surface string `json:"surface,omitempty"`
	Since   string `json:"since,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Already bool   `json:"already,omitempty"`
	DryRun  bool   `json:"dry_run"`
	Error   string `json:"error,omitempty"`
	Remedy  string `json:"remedy,omitempty"`
}
