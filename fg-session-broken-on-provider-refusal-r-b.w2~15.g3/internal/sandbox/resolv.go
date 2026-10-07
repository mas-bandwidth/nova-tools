//go:build linux

package sandbox

import "path/filepath"

// resolverConfigDirectory is the directory the system resolver's configuration
// file at path resolves to. Empty means there is no extra directory to grant:
// the path is missing, does not resolve, or resolves onto the filesystem root.
//
// It is the path unit of the resolver grant, not a Landlock call: on WSL2 the distro's
// /etc/resolv.conf -> /mnt/wsl/resolv.conf, and /mnt/wsl sits outside every static
// linux read root, so glibc inside the wall had no nameserver. The linux backend
// grants this directory read-only and skip-if-absent. The file carries the linux
// build tag because the linux roots table is the function's only caller: the
// function is built only where the wall it serves is built.
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
