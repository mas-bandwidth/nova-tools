package sprint

import "errors"

// GCVolumeUse is not measured on Windows: gc says volume=- there.
func GCVolumeUse(string) (int, error) {
	return 0, errors.New("the volume's use is not measured on Windows")
}
