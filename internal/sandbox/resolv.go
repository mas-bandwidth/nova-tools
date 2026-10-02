package sandbox

import "path/filepath"

// resolverConfigDirectory is the directory the system resolver's configuration
// file at path resolves to. Empty means there is no extra directory to grant:
// the path is missing, does not resolve, or resolves onto the filesystem root.
//
// The resolver path may be a symlink outside the static Linux read roots, so its
// resolved directory is granted read-only to keep name resolution available.
// /mnt/wsl sits outside every static linux read root, so glibc inside the wall
// had no nameserver. The linux backend grants this directory read-only and
// skip-if-absent. The function lives here, without a linux build tag, so a
// WSL2 fixture can prove the grant on Darwin.
func resolverConfigDirectory(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	dir := filepath.Dir(resolved)
	if dir == "" || dir == "/" || dir == "." {
		return ""
	}
	return dir
}
