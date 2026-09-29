package cardhdr_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

func TestRouteRank(t *testing.T) {
	t.Parallel()
	tests := []struct {
		route string
		want  int
	}{
		{"frontier", 3},
		{"Frontier", 3},
		{"pro", 2},
		{"PRO", 2},
		{"flash", 1},
		{"friend", 0},
		{"", 0},
		{"-", 0},
		{"other", 0},
	}
	for _, tc := range tests {
		if got := cardhdr.RouteRank(tc.route); got != tc.want {
			t.Errorf("RouteRank(%q) = %d, want %d", tc.route, got, tc.want)
		}
	}
}

func TestTiersCover(t *testing.T) {
	t.Parallel()
	tests := []struct {
		tiers string
		route string
		want  bool
	}{
		// frontier covers everything
		{"frontier", "frontier", true},
		{"frontier", "pro", true},
		{"frontier", "flash", true},
		{"frontier", "friend", true},
		{"frontier", "", true},

		// default flash,pro covers pro and flash, but not frontier
		{"flash,pro", "frontier", false},
		{"flash,pro", "pro", true},
		{"flash,pro", "flash", true},
		{"", "frontier", false}, // empty defaults to flash,pro
		{"", "pro", true},
		{"", "flash", true},

		// flash only
		{"flash", "frontier", false},
		{"flash", "pro", false},
		{"flash", "flash", true},
		{"flash", "friend", true},
	}
	for _, tc := range tests {
		if got := cardhdr.TiersCover(tc.tiers, tc.route); got != tc.want {
			t.Errorf("TiersCover(%q, %q) = %t, want %t", tc.tiers, tc.route, got, tc.want)
		}
	}
}
