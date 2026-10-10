package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// NovaRootOf is how a machine row's nova_root reaches layout.ResolveRoot: the
// row that names this host wins, a single shared root stands for the fleet,
// and two roots with no host row name none rather than pick one at random.
func TestNovaRootOfPicksTheMachineRowAndNamesNoneWhenAmbiguous(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		ws   []MachineWidth
		host string
		want string
	}{
		{"the row that names this host", []MachineWidth{{Machine: "a", NovaRoot: "~/one"}, {Machine: "b", NovaRoot: "~/two"}}, "a", "~/one"},
		{"one root and no host row", []MachineWidth{{Machine: "a", NovaRoot: "~/one"}, {Machine: "b"}}, "c", "~/one"},
		{"two roots and no host row", []MachineWidth{{Machine: "a", NovaRoot: "~/one"}, {Machine: "b", NovaRoot: "~/two"}}, "c", ""},
		{"two rows that share one root", []MachineWidth{{Machine: "a", NovaRoot: "~/one"}, {Machine: "b", NovaRoot: "~/one"}}, "c", "~/one"},
		{"no row sets a root", []MachineWidth{{Machine: "a"}, {Machine: "b"}}, "a", ""},
		{"no rows", nil, "a", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.want, NovaRootOf(c.ws, c.host))
		})
	}
}
