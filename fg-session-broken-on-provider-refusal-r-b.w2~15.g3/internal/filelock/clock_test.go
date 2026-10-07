package filelock

// The test's clock: production takes its time through options.clock (realClock
// by default), so the rig in rig_test.go passes lockStepClock and a bounded
// wait costs no wall time.
