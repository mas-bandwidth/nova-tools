package sandbox

import "path/filepath"

// resolverConfigDirectory is the directory the system resolver's configuration
// file at path resolves to. Empty means there is no extra directory to grant:
// the path is missing, does not resolve, or resolves onto the filesystem root.
//
// It is the path unit of #1737, not a Landlock call: on WSL2 (measured
// 2026-09-19, kernel 6.18.33.2) /etc/resolv.conf -> /mnt/wsl/resolv.conf, and
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
