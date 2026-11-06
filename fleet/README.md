# Fleet

The `fleet` directory holds two Ansible plays. They are one example of running
the tools on many machines; use them as they are, change them, or use your own.
`nova-tools-install.yml` installs a release of the tools. `services.yml` keeps
programs running, or runs them on a schedule, on each machine.

## Before you start

You need Ansible on the machine you run the plays from, and SSH to the others.
One way to get it, in a throwaway directory:

```sh
work=$(mktemp -d)
python3 -m venv "$work/venv"
"$work/venv/bin/pip" install -q ansible-core
export PATH="$work/venv/bin:$PATH"
git clone -q https://github.com/mas-bandwidth/nova-tools "$work/nova-tools"
cd "$work/nova-tools"
```

For Windows hosts, also run
`ansible-galaxy collection install -r fleet/requirements.yml`.

The plays read ordinary Ansible inventory. A **service** is one program a
machine keeps running or runs every N seconds. `services.yml` reads each host's
services from a list named `nova_services`. For example, this inventory has one
host, the local machine, with one service:

```sh
cat > "$work/inventory.yml" <<'EOF'
all:
  hosts:
    localhost:
      ansible_connection: local
      nova_home: /tmp/fleet-example-home
      nova_services:
        - name: hello
          argv: [sh, -c, "echo hello; sleep 60"]
EOF
```

`nova_home` is the login's home by default. This example points it at a
throwaway directory so nothing real is touched. `nova-config inventory` prints
inventory in this shape from a database; see [Config](../config/README.md).

## Run the services play

Run it in check mode first. Check mode reports what the play would do and
changes nothing:

```sh
ansible-playbook -i "$work/inventory.yml" fleet/services.yml --check -e nova_service_only=hello
```

To apply it, drop `--check`. Each row becomes a systemd user unit on Linux
(with a timer when the row has `every`), or a launchd job on macOS. A row's
`argv` runs as written; when its first word is one of the tools, the play puts
the tools' bin directory in front of it. A row may also set `env`, `log`
(default `~/.local/state/nova/services/<name>.log`), `every` (seconds),
`enabled` (default true), and `seat` and `keys` to run it under
`nova-secrets exec --only <keys>`.

The play prints these lines:

| Line | Meaning |
|---|---|
| `RESTART <name> on <host>: <why>` | The unit was restarted, because its file changed or `nova_service_restart` named it. Check mode says `WOULD-RESTART`. |
| `WOULD-RETIRE <name> on <host> (<file>)` | Check mode only: a unit the play would stop and remove. |
| `NOTE <host> <file>: not written by this play, ...` | A unit of the same shape that the play leaves alone. |
| `SERVICES host=... place=... records=... enabled=... written=... retired=...` | The **receipt**: one summary line per host at the end of the run. |

In the receipt, `place` is the unit directory, `records` the rows the run
handled, `enabled` how many of them are enabled, `written` the unit files it
changed, and `retired` the units it removed. A run limited with
`nova_service_only` adds `only=<names>`. Check mode adds `(check: nothing changed)`.

**Warning: a full run removes units.** The play marks every unit it writes.
On each host, a run without `nova_service_only` stops and removes every marked
unit that no row names. Always read the `WOULD-RETIRE` lines of a check run
before you apply. `nova_service_only=a,b` limits a run to those rows and
removes nothing. A host whose `nova_services` list is empty, but which still
has units the play wrote, is refused rather than emptied; pass
`-e nova_service_retire_all=true` when you mean to remove them all.

| Refusal | What to do |
|---|---|
| `<host> has no nova_services: ...` | Give the host a `nova_services` list. An empty list is valid. |
| `<host> has no service rows, and this run would retire <n> unit(s): <files>. ...` | Check the inventory. If the empty list is meant, pass `-e nova_service_retire_all=true`. |
| `nova_service_only names <x>, which no service record of this run's machines is ...` | Fix the name. Nothing was written. `nova_service_restart` is refused the same way. |
| `<host>: service <name> runs <program>, which is not on its unit PATH ...` (macOS) | Give `argv` an absolute path. |
| `<host> wrote the unit file of <name> and did not restart it ...` (Linux) | Run the play again, or restart the unit by hand as the line says. |

## Install a release

`nova-tools-install.yml` downloads the tools from a GitHub release, checks each
file against the release's `SHA256SUMS`, and installs it into the bin directory
(default `~/.local/bin`). A binary that already matches is left alone. The
release must ship the tools you name: v2.0.0 is the first release with all six.
With your own inventory and hosts:

```sh
ansible-playbook -i "$work/inventory.yml" fleet/nova-tools-install.yml \
  -e nova_tools_version=v2.0.0 -e nova_tools_bin_dir=/tmp/fleet-example-bin --check
ansible-playbook -i "$work/inventory.yml" fleet/nova-tools-install.yml \
  -e nova_tools_version=v2.0.0 -e nova_tools_bin_dir=/tmp/fleet-example-bin
```

Check mode still reads `SHA256SUMS` from the release. Release files are named
`<tool>_<tag>_<os>_<arch>`, with `.exe` on Windows, for Linux amd64 and arm64,
macOS arm64 and Windows amd64.

| Variable | Default | Effect |
|---|---|---|
| `nova_tools_version` | none; required | The release tag. |
| `nova_tools_names` | all six tools | The tools to install. |
| `nova_tools_bin_dir` | `~/.local/bin` | Where the tools go on Linux and macOS. |
| `nova_tools_release_url` | this repository's releases | Where the files are downloaded from. |
| `nova_tools_restart`, `nova_tools_restart_commands` | false, empty | Commands to run once when a binary changed. |

| Refusal | What to do |
|---|---|
| `nova-tools-install: name the release (-e nova_tools_version=vX.Y.Z), ...` | Pass a tag such as `v2.0.0`. |
| `nova-tools-install: no release asset for <os>-<arch>` | The release has no build for this platform. Build from source instead. |
| `nova-tools-install: <file> is not listed in <tag>'s SHA256SUMS` | That release does not ship the tool. Pick a later tag, or shorten `nova_tools_names`. |
| A download error on `SHA256SUMS` | The tag does not exist at `nova_tools_release_url`. Check the tag. |

Other defaults are in [group_vars/all.yml](group_vars/all.yml) and
the [install role's defaults](roles/nova-tools-install/defaults/main.yml).
Set them as host variables or with `-e`. A `group_vars/all.yml` beside your
inventory does not override them, because Ansible reads the play's own file
later.

## Clean up

```sh
cd / && rm -rf "$work" /tmp/fleet-example-home /tmp/fleet-example-bin
```
