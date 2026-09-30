package verbs

import (
	"context"
	"errors"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// Layer 1's lifecycle on the new path (the L1 contract amendment, lifecycle,
// 2026-09-30): init defines the namespace before its clock step, and teardown
// deletes it. Both go through Layer 1's two library functions, never a key
// written here.

// TableColumns are the four tables' columns as the lifecycle defines them, in
// the catalog's order (sprint.ViewOrder): the places a card can be (the upper
// design, section 1's tables). Define admits set columns only (the amendment,
// section 2: the twin models no derived kind), so the present init's text and
// formula columns (ci, state, since, done, okpct, status, load) are not
// columns here: a stream's and a member's state are fields of their control
// cards, in ctl.
var TableColumns = map[string][]string{
	sprint.Work:    {"waiting", "ready", "working", "review", "merging", "landed"},
	sprint.Readers: {"asked", "reading", "ok", "broken"},
	sprint.Merge:   {"queued", "merged", "stuck", "returned", "ctl"},
	sprint.Fleet:   {"ready", "working", "withdrawn", "ok", "failed", "ctl"},
}

// ViewName is the sprint's view: the name teardown confirms.
const ViewName = "sprint"

// DefineSpec is the sprint's namespace as init defines it: the four tables of
// TableColumns in the catalog's order, the view sprint, and the build of the
// library the caller expects on the store (fn.TSetBuild), which a store
// holding another build's library refuses BUILD.
func DefineSpec(names sprint.Names, build string) tset.DefineSpec {
	spec := tset.DefineSpec{Space: names.Prefix, Build: build, View: ViewName}
	for _, t := range sprint.ViewOrder {
		ts := tset.TableSpec{Name: t}
		for _, c := range TableColumns[t] {
			ts.Columns = append(ts.Columns, tset.ColumnSpec{Name: c, Kind: tset.ColumnKindSet})
		}
		spec.Tables = append(spec.Tables, ts)
	}
	return spec
}

// Define defines the sprint's namespace (init's first half). A namespace
// already defined (EXISTS) is not a refusal here: init goes on to its clock
// step, which says whether the sprint is made. It reports whether this call
// defined it.
func Define(ctx context.Context, lc tset.Lifecycle, names sprint.Names, build string) (bool, error) {
	_, err := lc.Define(ctx, DefineSpec(names, build))
	var rf *tset.Refusal
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &rf) && rf.Code == "EXISTS":
		return false, nil
	}
	return false, lifecycleErr("init", err)
}

// TeardownReq is teardown's request: the view's name, which must be sprint.
type TeardownReq struct{ Confirm string }

// Teardown deletes every key of the sprint's namespace but its lifecycle
// receipts (Layer 1's teardown, in bounded calls), confirmed by the view's
// name; refused while the machine runs (RUNNING) and NOSPACE when there is no
// sprint.
func Teardown(ctx context.Context, lc tset.Lifecycle, names sprint.Names, req TeardownReq) (Result, error) {
	const verb = "teardown"
	res := Result{Verb: verb}
	rep, err := lc.Teardown(ctx, names.Prefix, req.Confirm)
	res.Trips = rep.Calls
	if err != nil {
		return res, lifecycleErr(verb, err)
	}
	res.Said = fmt.Sprintf("teardown: the sprint is gone: %d keys deleted in %d calls; its lifecycle receipts stay", rep.Deleted, rep.Calls)
	return res, nil
}

// lifecycleErr is a lifecycle call's error as a verb's: Layer 1's refusal as
// the store's (Refused, not local), a reply that did not come back as
// Unknown, anything else as it is.
func lifecycleErr(verb string, err error) error {
	var rf *tset.Refusal
	if errors.As(err, &rf) {
		return &Refused{Verb: verb, Refusal: &sprintfn.Refusal{Code: rf.Code, Message: rf.Message,
			Detail: sprintfn.RefusalDetail{RefusalDetail: rf.Detail}}}
	}
	var un *tset.OutcomeUnknownError
	if errors.As(err, &un) {
		return &Unknown{Verb: verb, Err: err}
	}
	return err
}
