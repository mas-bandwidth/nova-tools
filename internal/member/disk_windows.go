package member

import (
	"errors"
	"time"
)

// measureDisk is not measured on Windows: a beat carries no disk reading.
func measureDisk(string, time.Time) (diskReading, error) {
	return diskReading{}, errors.New("the volume's disk is not measured on Windows")
}
