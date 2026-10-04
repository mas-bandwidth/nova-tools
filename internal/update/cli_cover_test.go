package update

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCliCoverSafeRevision(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		rev  string
		want string
	}{
		{"hex revision is unchanged", "b9f9e0b100caf9d9ae7e236f057be2a853c0432c", "b9f9e0b100caf9d9ae7e236f057be2a853c0432c"},
		{"short hex is unchanged", "b9f9e0b", "b9f9e0b"},
		{"slashes fold to underscores", "feature/branch", "feature_branch"},
		{"colon, space and at sign fold", "refs/heads/x:y z@1", "refs_heads_x_y_z_1"},
		{"hyphen underscore dot are kept", "v1.2.3-rc_1", "v1.2.3-rc_1"},
		{"empty is refused a name", "", "revision"},
		{"single dot climbs out and is refused", ".", "revision"},
		{"double dot climbs out and is refused", "..", "revision"},
		{"leading slash still folds to a usable name", "/main", "_main"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, safeRevision(tc.rev))
		})
	}
}
