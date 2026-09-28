package life

import "os"

// cpuRead is the cpu line of /proc/stat: user nice system idle iowait irq
// softirq steal; busy is everything but idle and iowait.
func cpuRead() cpuSample {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuSample{}
	}
	return parseProcStat(string(b))
}
