package main

import (
	"context"

	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
)

// serveFrom is serveCtx for a caller that never goes away (a test's step): only the tests
// ask for it, the server itself always has the request's context.
func (a *app) serveFrom(req sprintwire.Request, local bool) sprintwire.Response {
	return a.serveCtx(context.Background(), req, local)
}
