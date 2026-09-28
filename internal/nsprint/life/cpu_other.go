//go:build !darwin && !linux

package life

// cpuRead cannot measure here; the beat's cpu cell stays empty.
func cpuRead() cpuSample { return cpuSample{} }
