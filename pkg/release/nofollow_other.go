//go:build !unix

package release

// oNoFollow is zero where the platform has no O_NOFOLLOW. writeNoFollow still
// refuses a symlink it can see with Lstat, and it does not call os.WriteFile.
const oNoFollow = 0
