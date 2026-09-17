package main

// `fleet plan` is the read-each-plan witness of SPEC-FLEET-KUBE, Part 1: for
// every file the bench module declares, it reads the host over ssh
// (`ssh <bench> sha256sum <path>`), compares the observed hash with the declared
// one, and prints one FLEET line per bench. A hand edit nobody declared is the
// drift the null_resource trigger cannot see, so the trigger is never read here;
// only the witnessed files are.
//
// The declaration is the module's own files.json, the same file the HCL
// for_each reads, so the tool and Terraform cannot disagree about what is
// managed. ssh comes from --ssh so a test puts a fake on PATH and no test makes
// a network call.

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/fleetdrift"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func cmdFleetPlan(args []string, stdout, stderr io.Writer) int {
	f := newFlags("fleet plan")
	module := f.fs.String("module", "", "")
	bench := f.fs.String("bench", "", "")
	ssh := f.fs.String("ssh", "ssh", "")
	timeout := f.fs.Int("timeout", 120, "")
	max := f.fs.Int("max", bounded.Default, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*module, "module", "the bench module directory holding files.json")
	f.want(*bench, "bench", "the bench name, which is also the ssh target")
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if f.refused(stderr) {
		return 2
	}

	manifest := filepath.Join(*module, "files.json")
	declared, err := fleetdrift.LoadManifest(manifest)
	if err != nil {
		fmt.Fprintf(stderr, "FLEET REFUSED: %s (the bench module declares its files in files.json)\n", oneline.Err(err))
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeout)*time.Second)
	defer cancel()

	observed := fleetdrift.SSHObserver(*ssh, *bench, time.Duration(*timeout)*time.Second)
	drifts, err := fleetdrift.Plan(ctx, declared, observed)
	if err != nil {
		fmt.Fprintf(stdout, "FLEET %s UNREACHABLE %s\n", oneline.Field(*bench), oneline.Escape(err.Error()))
		return 3
	}

	list := bounded.Capped(stdout, *max, "FLEET", "bench",
		"run: nova-pulse fleet plan --module <dir> --bench <name> --max 0")
	if len(drifts) == 0 {
		list.Line(fmt.Sprintf("FLEET %s PLAN CLEAN files=%d", oneline.Field(*bench), len(declared)))
		list.More()
		return 0
	}
	for _, d := range drifts {
		list.Line(fmt.Sprintf("FLEET %s DRIFT %s path=%s declared=%s observed=%s",
			oneline.Field(*bench), oneline.Field(d.Resource), oneline.Field(d.Path),
			oneline.Field(d.Declared), oneline.Field(d.Observed)))
	}
	list.More()
	return 2
}
