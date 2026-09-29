//go:build !(linux || darwin || freebsd || netbsd || openbsd)

package cairn

// noFollow is zero where the platform has no such open flag; the handle is
// still compared with the name after the open.
const noFollow = 0
