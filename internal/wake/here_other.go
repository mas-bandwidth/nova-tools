//go:build !linux && !darwin

package wake

// A platform with neither reading prints load=- and procs=-, which is a reading
// rather than a refusal.
func loadAverage() ([3]float64, bool) { return [3]float64{}, false }

func processCount() (int, bool) { return 0, false }
