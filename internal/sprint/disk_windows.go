package sprint

import "errors"

// statDisk is not measured on Windows: a beat carries no disk reading and the
// tick raises no disk watermark.
func statDisk(string) (free, total, ifree, itotal uint64, volume string, err error) {
	return 0, 0, 0, 0, "", errors.New("the volume's disk is not measured on Windows")
}
