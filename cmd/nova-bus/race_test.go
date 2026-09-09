//go:build race

package main

// raceEnabled is how the timing test knows it must not run. The race detector multiplies
// every one of these operations by something between five and twenty, so a wall-clock bound
// asserted under it would measure the instrumentation. There is no way to ask this at run
// time, so it is asked at build time, which is the whole reason this file exists.
const raceEnabled = true
