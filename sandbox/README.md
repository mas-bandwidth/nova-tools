# nova-sandbox

nova-sandbox runs one command inside a wall the kernel enforces. You name the directories the command may read and
write; everything else on disk is denied to it. macOS uses a `sandbox-exec` profile, Linux uses Landlock and a seccomp
filter. Under `--net-deny` the network is closed: on Linux no TCP, UDP or raw socket can be made.

```
nova-sandbox [--read <dir>]... [--write <dir>]... [--net-deny [--net-allow <host:port>]...] [--gpu] [--unix] [--keep-fds] -- <cmd> <args...>
nova-sandbox show [the same flags] [-- <cmd> <args...>]
nova-sandbox check
nova-sandbox probe
```

The flags, what the wall does and does not guarantee on each OS, the output of `check` and `probe`, and the exit
codes are in [docs/SPEC-SANDBOX.md](../docs/SPEC-SANDBOX.md). The macOS profile, with the measured reason for each
grant, is `darwin.sb.tmpl`; the Landlock ruleset is `rules.go`, the seccomp program `bpf.go`. `nova-sandbox show` prints all of it for a flag set.
