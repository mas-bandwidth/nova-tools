package main

// Box is the machine's facts serve and status print, read at the moment of the call and
// never cached (rules 8 and 10): the one-minute load average, the free and total memory
// in bytes, and the live GPU wired-memory cap in MiB where the platform has one. A nil
// function, or ok false, is a fact this machine does not report; a test supplies its own.
type Box struct {
	Load1    func() (float64, bool)
	Mem      func() (free, total uint64, ok bool)
	WiredCap func() (mib uint64, ok bool)
}

// LocalBox is this machine's box.
func LocalBox() Box { return localBox() }
