package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFriendTokensSumPerFriendFromTheResultUsage(t *testing.T) {
	t.Parallel()
	usage := ParseFriendUsage("usage: in=1000 out=2000 cache=300")
	assert.Equal(t, int64(3300), usage.Tokens.Total())
	assert.Equal(t, int64(4600), FriendTokensFromCards([]*Card{
		{Fields: map[string]string{FieldUsage: "input=1000 output=2000 cache_read=300"}},
		{Fields: map[string]string{FieldUsage: "input=400 output=500 cache_read=400"}},
	}))
}
