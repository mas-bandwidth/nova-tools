package friend

import "github.com/mas-bandwidth/nova-tools/internal/sprint"

// MeasureWorkingVolume is the disk reading a friend's beat records for the
// volume of her working directory, and the largest directories under aiRoot.
// The daemon's beat loop does not call it: the tick reads the fields a beat
// would write (sprint.VolumeReading.Fields) and does not stat the host itself.
func MeasureWorkingVolume(path, aiRoot string) (sprint.VolumeReading, []string, error) {
	rd, err := sprint.MeasureVolume(path)
	if err != nil {
		return sprint.VolumeReading{}, nil, err
	}
	dirs, scanErr := sprint.ScanLargest(aiRoot, sprint.DiskScanBound)
	return rd, dirs, scanErr
}
