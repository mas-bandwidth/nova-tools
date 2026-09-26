package life

import "os/exec"

// cpuRead asks top for one CPU usage line ("CPU usage: 13.22% user, 37.47%
// sys, 49.31% idle"); busy is 100 minus idle. One short-lived process per
// call; the beat calls it once a second.
func cpuRead() cpuSample {
	out, err := exec.Command("/usr/bin/top", "-l", "1", "-n", "0", "-s", "0").Output()
	if err != nil {
		return cpuSample{}
	}
	return parseTopCPU(string(out))
}
