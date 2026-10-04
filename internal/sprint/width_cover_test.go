package sprint

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Unit coverage for the width text and the default from a machine's cores
// (internal/sprint/width.go): WidthText, the fleet table's width cell of a
// member's control card, and WidthOfCores, the default a machine row without
// a width takes from its logical cores. All pure: hand-made cards, no store,
// clock, sleep, subprocess, network or live service.

// widthCoverCtl is a member's control card as the fleet table holds it, with
// the width field it names (empty when it names none).
func widthCoverCtl(member, width string) *Card {
	c := &Card{ID: CtlID(member), Row: member, Col: Ctl, Fields: map[string]string{}}
	if width != "" {
		c.Fields[FieldWidth] = width
	}
	return c
}

// TestWidthCoverWidthTextRendersTheControlCardsWidth pins WidthText: the
// control card's width field rendered as the fleet table's width cell; a
// width that is not one, and a member with no control card at all, keep the
// default (DefaultWidth).
func TestWidthCoverWidthTextRendersTheControlCardsWidth(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		ctl  *Card
		want string
	}{
		{
			name: "main: a control card naming width 8 renders 8",
			ctl:  widthCoverCtl("m1", "8"),
			want: "8",
		},
		{
			name: "main: a control card naming the default width renders it",
			ctl:  widthCoverCtl("m2", strconv.Itoa(DefaultWidth)),
			want: strconv.Itoa(DefaultWidth),
		},
		{
			name: "refusal: a control card naming no width keeps the default",
			ctl:  widthCoverCtl("m3", ""),
			want: strconv.Itoa(DefaultWidth),
		},
		{
			name: "refusal: a width past MaxWidth keeps the default",
			ctl:  widthCoverCtl("m4", strconv.Itoa(MaxWidth+1)),
			want: strconv.Itoa(DefaultWidth),
		},
		{
			name: "refusal: a width that is not a whole number keeps the default",
			ctl:  widthCoverCtl("m5", "wide"),
			want: strconv.Itoa(DefaultWidth),
		},
		{
			name: "refusal: a member with no control card at all keeps the default",
			ctl:  nil,
			want: strconv.Itoa(DefaultWidth),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, WidthText(tc.ctl))
		})
	}
}

// TestWidthCoverWidthOfCoresHalvesTheCores pins WidthOfCores's main path: the
// default is half the machine's logical cores (the owner, 2026-10-02: "default
// is CPUs/2"), at least 1 and at most MaxWidth.
func TestWidthCoverWidthOfCoresHalvesTheCores(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		cores int
		want  int
	}{
		{
			name:  "main: sixteen cores default to eight",
			cores: 16,
			want:  8,
		},
		{
			name:  "main: three cores round down to one",
			cores: 3,
			want:  1,
		},
		{
			name:  "main: one core is the floor of one",
			cores: 1,
			want:  1,
		},
		{
			name:  "main: twice MaxWidth's cores cap at MaxWidth",
			cores: 2 * MaxWidth,
			want:  MaxWidth,
		},
		{
			name:  "main: cores past the cap stay at MaxWidth",
			cores: 3 * MaxWidth,
			want:  MaxWidth,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, WidthOfCores(tc.cores))
		})
	}
}

// TestWidthCoverWidthOfCoresRefusesUnknownCores pins WidthOfCores's refusal:
// 0 when the cores are not known (no beat has reported them), so fleet sync
// writes no width for the machine row.
func TestWidthCoverWidthOfCoresRefusesUnknownCores(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		cores int
	}{
		{
			name:  "refusal: cores of zero mean the beat has not reported them",
			cores: 0,
		},
		{
			name:  "refusal: negative cores mean the cores are not known",
			cores: -1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Zero(t, WidthOfCores(tc.cores))
		})
	}
}
