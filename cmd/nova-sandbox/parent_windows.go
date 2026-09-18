//go:build windows

package main

// parentExecutable is W12: identity on windows is a Job Object and process-image question,
// NEVER a POSIX one.
//
// The internal verb's third guard -- the child asks the OS for its parent's executable -- is
// QueryFullProcessImageNameW on a handle opened to the parent pid, compared with this
// binary's own image path. There is no proc_pidpath, no inode and no device number here, and
// a check written against any of those is a check that compiles and answers nothing. The
// body is in runwin_windows.go, beside IsProcessInJob, so that the two halves of the
// identity question -- the image and the job -- are read in one place.
//
// The first two guards are unchanged: the 16 raw bytes on an inherited pipe, and the three
// constant-time copies. What changes on windows is only HOW the pipe is inherited --
// bInheritHandles and the STARTUPINFOEX handle list, not fd 3.
//
// NOT MEASURED. Like the rest of the windows half, this has not run on a Windows machine.
func parentExecutable(pid int) (string, error) { return parentExecutableWindows(pid) }
