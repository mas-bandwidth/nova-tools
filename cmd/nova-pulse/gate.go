package main

// nova-pulse gate: the mechanical red gate (rule C of #828). It reads the integration
// branch's latest CI run and writes or removes the STOP file; there is no model call and
// exactly one GATE line on stdout.

import (
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdGate(args []string, stdout, stderr io.Writer) int {
	f := newFlags("gate")
	repo := f.fs.String("repo", "", "")
	branch := f.fs.String("branch", "", "")
	root := f.fs.String("root", "", "")
	admission := f.fs.String("admission", "", "")
	timeout := f.fs.Int("timeout", 120, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*repo, "repo", "the owner/repo whose integration branch CI the gate reads")
	f.want(*branch, "branch", "the integration branch the gate reads")
	f.want(*root, "root", "the directory the STOP file lives in")
	f.want(*admission, "admission", "the admission name (issue or test), written on STOP's second line")
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.Gate(pulse.GateInput{
		Repo:      *repo,
		Branch:    *branch,
		Root:      *root,
		Admission: *admission,
		Timeout:   time.Duration(*timeout) * time.Second,
		Stdout:    stdout,
		Stderr:    stderr,
	})
}
