# nova-sandbox

nova-sandbox runs one command inside a wall the kernel enforces. You name the directories the command may read and
write; everything else on disk is denied to it. macOS uses a `sandbox-exec` profile, Linux uses Landlock.

```
nova-sandbox [--read <dir>]... [--write <dir>]... [--net-deny [--net-allow <host:port>]...] [--gpu] -- <cmd> <args...>
nova-sandbox check
nova-sandbox probe
```

The flags, what the wall does and does not guarantee on each OS, the output of `check` and `probe`, and the exit
codes are in [docs/SPEC-SANDBOX.md](../docs/SPEC-SANDBOX.md). The macOS profile, with the measured reason for each
grant, is `darwin.sb.tmpl`; the Landlock ruleset is `rules.go`.
