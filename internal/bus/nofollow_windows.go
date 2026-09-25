//go:build windows

package bus

// oNoFollow is zero on Windows: the platform has no O_NOFOLLOW, and the Lstat before the
// open and the fstat after it carry the rule there.
const oNoFollow = 0

// ONoFollow is oNoFollow for callers outside this package; zero here.
const ONoFollow = oNoFollow
