package reconcile

import "context"

// LandPass is the land duty's per-repo pass without taking the lease (a
// unit test on an in-process store; the lease is the library's,
// functional): token is the lease token its claims name, which the test
// writes on lease:land:<repo> itself.
func (d *LandDuty) LandPass(ctx context.Context, repo, token string) ([]LandLine, error) {
	return d.land(ctx, repo, token)
}
