package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// orderOnly: the arguments of a set that moves rows and nothing else (twin catch-up).
func TestOrderOnlyReadsTheSetsArguments(t *testing.T) {
	t.Parallel()
	for args, want := range map[string]bool{
		`["t-fleet","{\"row_order\":[\"a\",\"b\"],\"row_sort\":{\"manual\":true}}"]`: true,
		`["t-fleet","{\"row_move\":{\"item\":\"a\"}}"]`:                              true,
		`["t-fleet","{\"row_order\":[\"a\"],\"footer\":\"x\"}"]`:                     false,
		`["t-fleet","{\"columns\":{}}"]`:                                             false,
		`["t-fleet","{}"]`:                                                           false,
		`["t-fleet"]`:                                                                false,
		`not json`:                                                                   false,
	} {
		assert.Equal(t, want, orderOnly(args), args)
	}
}
