package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ParseIdentity reads the pool identity a loop's nova-config argv names: the
// owner before the first comma, the email after the last, the name between
// (a name may hold a comma); each is required and the email holds an @.
func TestParseIdentityReadsTheThreeColumnsOfAPoolIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, in string
		want     StagingIdentity
		bad      bool
	}{
		{"the three columns", "pool-owner,Pool Worker,pool@example.com", StagingIdentity{"pool-owner", "Pool Worker", "pool@example.com"}, false},
		{"a name with a comma", "o,Worker, Pool,w@example.com", StagingIdentity{"o", "Worker, Pool", "w@example.com"}, false},
		{"two columns", "o,w@example.com", StagingIdentity{}, true},
		{"no name", "o,,w@example.com", StagingIdentity{}, true},
		{"no email", "o,Worker,", StagingIdentity{}, true},
		{"an email with no @", "o,Worker,nobody", StagingIdentity{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseIdentity(tc.in)
			if tc.bad {
				assert.ErrorContains(t, err, "<owner>,<name>,<email>")
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
