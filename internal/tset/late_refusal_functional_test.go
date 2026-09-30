//go:build functional

package tset

import (
	"reflect"
	"testing"
)

// Each request first plans valid changes to work, merge, and fleet. Its last
// table alone causes the refusal. The public writer must discard every earlier
// planned change, as must the independent Mem twin.
func TestLateFourthTableRefusals(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		code   string
		fourth map[string]any
		ids    []string
		cells  []string
		rows   []string
	}{
		{name: "revision", code: "REVISION", fourth: commitProbeMove("reader", "0", false),
			ids: []string{"base-reader"}},
		{name: "occupied", code: "OCCUPIED", fourth: map[string]any{
			"kind": "rows", "t": "reader", "del": []string{"r"},
		}, rows: []string{"r"}},
		{name: "cellfull", code: "CELLFULL", fourth: map[string]any{
			"kind": "count", "t": "reader", "cells": []string{"r:ready"},
			"max": []int{0},
		}, cells: []string{"r:ready"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newTSetFixture(t)
			model := commitProbeSetup(t, fx, false)
			entries := append(commitProbeMoves(false)[:3], tc.fourth)
			if len(entries) != 4 || entries[3]["t"] != "reader" {
				t.Fatal("late refusal is no longer on the fourth table")
			}
			reply := commitProbeRefusal(t, fx.Client, model, fx.Space, tc.code, entries, nil)
			detail := comparableDetail(reply.Detail)
			if detail.EntryIndex == nil || *detail.EntryIndex != 3 ||
				detail.Table != "reader" ||
				!reflect.DeepEqual(detail.IDs, tc.ids) ||
				!reflect.DeepEqual(detail.Cells, tc.cells) ||
				!reflect.DeepEqual(detail.Rows, tc.rows) {
				t.Errorf("%s refusal was not attributed to the fourth table: %+v", tc.code, reply.Detail)
			}
		})
	}
}
