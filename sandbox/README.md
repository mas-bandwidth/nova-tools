# nova-sandbox

Give a command the directories it needs for its work. nova-sandbox applies
kernel restrictions with Landlock and seccomp on Linux, or a `sandbox-exec`
profile on macOS. System roots remain readable, with a few documented writable
paths; your flags add grants to that baseline. There is no Windows backend.

```text
nova-sandbox [--read <dir>]... [--write <dir>]... [--net-deny [--net-allow <host:port>]...] [--gpu] [--unix] [--keep-fds] -- <cmd> <args...>
nova-sandbox show [the same flags] [-- <cmd> <args...>]
nova-sandbox check
nova-sandbox probe
nova-sandbox help [<verb>]
```

Paths must be absolute, existing directories. The command itself must be in
a system root or one of your grants. Set its working directory and environment
before launching the tool; both pass through unchanged. Descriptors above
stderr are closed on exec by default; `--keep-fds` preserves them.

On Linux, `--net-deny` requires Landlock and seccomp. It denies IP sockets;
`--net-allow` opens TCP client access to the named port on every address.
On macOS, an allowed localhost port permits TCP and UDP. `show` previews
the host rules without running a command. Linux also restricts Unix-domain sockets
by default, which can prevent logging, agent access or local-service calls.
`--unix` relaxes those restrictions and requires trust in the command's access
to local services. `--gpu` is for macOS only.

The [specification](../docs/SPEC-SANDBOX.md) describes grants, OS limits,
`show`, `check`, `probe` and exit codes. Read those limits before relying on the wall.
The macOS profile is in [darwin.sb.tmpl](darwin.sb.tmpl); the Landlock rules
are in [rules.go](rules.go).

## Build and test

From the repository root, on a build machine:

```sh
build_dir=$(mktemp -d)
go build -o "$build_dir/nova-sandbox" ./sandbox
"$build_dir/nova-sandbox" help
go test ./sandbox                    # parsing, generated rules and refusals
```

[TESTING.md](../docs/TESTING.md) describes functional checks and their prerequisites.
A build or unit-test result alone does not establish that a backend works on
a particular host.
