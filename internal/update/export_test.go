package update

import (
	"context"
	"net/http"
	"os"
	"time"
)

// Latest reads only the declared endpoint, without credentials or persistent cache (tests).
func Latest(ctx context.Context, e Entry, timeout time.Duration, client *http.Client) Read {
	return latestIn(ctx, nil, e, timeout, client)
}

// Installed is the test seam for reading installed tool versions.
func Installed(ctx context.Context, e Entry, timeout time.Duration, report bool) Read {
	return installed(ctx, e, timeout, report, Environment{}.runProcess)
}

// writeSnapshot commits with os.Rename, the atomic commit operation (tests).
func writeSnapshot(path string, s *snapshot) error {
	return writeSnapshotWith(path, s, os.Rename)
}
