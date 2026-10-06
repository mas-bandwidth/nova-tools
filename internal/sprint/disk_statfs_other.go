//go:build !linux && !darwin

package sprint

// StatVolume answers ErrNoStatfs on a system this tool cannot ask for a volume's figures:
// the beat carries no reading there.
func StatVolume(string) (DiskStat, error) { return DiskStat{}, ErrNoStatfs }
