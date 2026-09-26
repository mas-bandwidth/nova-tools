package reconcile

import "context"

// LandPass is the land duty's per-repo pass without the lease (a unit test
// on an in-process store; the lease is the library's, functional).
func (d *LandDuty) LandPass(ctx context.Context, repo string) ([]LandLine, error) {
	return d.land(ctx, repo)
}
