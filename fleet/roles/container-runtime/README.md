# container-runtime

Rootless, daemonless podman for the user a runner runs as, so the functional
tier can run in a container (`infra/functional-image/README.md`). The play is
`fleet/container-runtime.yml`; `fleet/inventory.container-runtime.example.ini`
shows the inventory shape, with placeholder hosts.

    ansible-playbook -i <your inventory> fleet/container-runtime.yml --check --diff | cat
    ansible-playbook -i <your inventory> fleet/container-runtime.yml | cat

Run it from a control node, through a pipe: ansible needs blocking stdio. The
play connects as the runner user and uses `sudo` for the tasks that change the
host. The role acts for an ordinary user only: before its first change it stops
when the runner user is root or has a uid below `container_runtime_min_uid`, so
a connection made as root (or `become` set in `ansible.cfg`, which gathers the
facts as root) names the runner with `container_runtime_user` instead.

## What it does

| step | how |
|---|---|
| podman | the distribution's `podman` with its rootless helpers (`uidmap`, `passt`, `crun`, `conmon`, `netavark`, `aardvark-dns`, `catatonit`, `dbus-user-session`), through apt |
| subordinate ids | a `/etc/subuid` and `/etc/subgid` row for the user when it has none; an existing row is never rewritten and one smaller than 65536 ids stops the play |
| linger | `loginctl enable-linger`, so the user's runtime directory and systemd manager exist with no login session |
| user namespaces | `user.max_user_namespaces` is read and must be above zero; the role does not change kernel settings |
| cgroup v2 delegation | the controllers `cpu`, `memory` and `pids` must be delegated to the user's systemd manager; a drop-in is written only where they are not, and the play stops until the manager restarts, because restarting it ends the user's containers |
| probe | see below |

`--check --diff` names the packages that would be installed and shows the
subuid, subgid, linger and delegation changes as diffs, and changes nothing. A
second real run reports `changed=0`.

## The probe

The last tasks run trivial containers as the runner user with the flags a
functional run uses: `--network none --ipc private --pids-limit --memory --cpus
--read-only --tmpfs --timeout`. podman warns and goes on when a limit is
unsupported, so the container reads its own cgroup back and the play fails when

- `pids.max`, `memory.max` or `cpu.max` is not the value asked for,
- the network is more than loopback,
- the root filesystem is writable or `/tmp` is not, or
- a container that sleeps past `--timeout` is not gone within
  `container_runtime_probe_grace` seconds, or one of the probe containers is
  left behind.

The probe image is the base of the functional image; a class test
(`TestFunctionalImageRuntimeAndReadmeAgree`) holds the two equal.

## Variables

| variable | default | |
|---|---|---|
| `container_runtime_user` | the connecting user | the runner user |
| `container_runtime_min_uid` | `1000` | the lowest uid the role acts for; root is refused whatever it is set to |
| `container_runtime_packages` | the list above | packages to install |
| `container_runtime_install_recommends` | `true` | apt recommends; the rootless helpers are recommended by `podman` |
| `container_runtime_subid_start`, `container_runtime_subid_count` | `100000`, `65536` | the range given when the user has no row |
| `container_runtime_controllers` | `cpu`, `memory`, `pids` | controllers a limit needs delegated |
| `container_runtime_probe_image` | the base image of the functional image, by digest | image the probe runs |
| `container_runtime_probe_pids`, `_memory_bytes`, `_cpus`, `_timeout` | `128`, `268435456`, `2`, `60` | the probe's limits |
| `container_runtime_probe_kill_after`, `_grace` | `3`, `20` | the bound the timeout probe uses, and the seconds it may take to take effect |

Debian-family Linux with cgroups v2 only (apt, and the unified hierarchy the
limits rely on).
