package land_test

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
)

type landTestFixture struct {
	t      *testing.T
	ctx    context.Context
	client *redis.Client
	sprint string
	repo   string
	base   string
	gen    int64
	lease  string
}
