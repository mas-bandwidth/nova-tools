# Security

nova-tools runs commands, reads configuration, delivers messages and installs
software with the caller's permissions. Each tool has its own boundary.
Read its guide before relying on a restriction.

## Boundaries

| Piece | Boundary |
|---|---|
| [bus](../bus/README.md) | Redis holds inboxes. The receiver process, called a hook, reads an inbox onto stdout and records recent presence. A sender supplies its own name. The receiver acknowledges a message after writing it; this does not prove that a person or model read it or completed the work. |
| [config](../config/README.md) | PostgreSQL holds machine settings and programs kept running or scheduled. A write records the supplied actor in history. An actor name is an audit label, not authentication. |
| [sandbox](../sandbox/README.md) | Kernel rules restrict a command's filesystem access and, when requested, network access within the documented OS limits. System roots are readable. The environment and standard streams pass through; descriptors above stderr are closed on exec unless `--keep-fds` preserves them. |
| [secrets](../secrets/README.md) | A seat names a decryption identity with an age key. sops decrypts its file, and `exec --only` passes selected values to the command's environment. The command and its children can use or disclose those values. Recipient keys, including any recovery key, control who can decrypt the file. |
| [reserve](../reserve/README.md) | File locks coordinate cooperating callers. They do not impose CPU, memory or process limits on a command. |
| [cairn](../cairn/README.md) | Saved session records and notes are plain files protected by filesystem permissions. A receipt reads a record back; search and note checks do not establish its truth or detect all edits. Publish policy is a recorded label; the tool sends nothing. |
| [fleet](../fleet/README.md) | Ansible installs binaries and supervised commands on the selected hosts. Inventory, program arguments, variables and release sources are trusted inputs. |

Restrict access to the Redis and PostgreSQL services and to the files and keys
these tools use. Message bodies are external data, even when their sender name
is familiar. Installing a hook does not make its messages instructions to run.

The sandbox's network limits differ by OS. Read the [sandbox guide](../sandbox/README.md)
for the grants and checks on your machine. The wall does not clear the environment
or limit CPU and memory use. A granted directory can contain private data; a
read grant allows the command to read that data.

`--keep-fds` preserves descriptors above stderr, including access through an open
file or socket. On Linux, `--unix` permits Unix-domain socket creation. Use it
only when the command may contact the local services those sockets expose.

The install play checks downloaded assets against the selected release's
`SHA256SUMS`. This detects a mismatch with that file; it is not an independent
signature of the release or protection against a compromised release account.

## What to report

Report a tool violating its documented boundary, disclosing secret values,
modifying an unintended path, corrupting stored data or reporting success when
an operation failed. An inaccurate security claim in a guide is also worth
reporting. A missing feature or a documented limit can be discussed as an
ordinary issue without including sensitive details.

Include the tool and version or commit, operating system, expected behavior and
what you observed. Remove secret values and private records from the report.

## Reporting

Email <glenn@mas-bandwidth.com>. Do not put sensitive findings in public issues
or pull requests. If no acknowledgment arrives after about a week, email
<rowan@mas-bandwidth.com> with `SECURITY` in the subject.

This project publishes no encrypted intake key. Email is not an anonymous or
authenticated reporting channel; send only what you are comfortable disclosing
through it. If a report needs another channel, describe that need without the
sensitive details. Any new channel or key is also published in this file; check
its [history](https://github.com/mas-bandwidth/nova-tools/commits/main/docs/SECURITY.md)
before using one offered by email. Repository history does not protect against
a compromised repository account.

We aim to acknowledge reports within a few days and ask before publishing a
reporter's name in release notes. Please withhold a working bypass while a fix
is being prepared. If neither reporting address answers, further disclosure is
your decision; this policy does not require indefinite silence.

## Updates

Watch the [releases](https://github.com/mas-bandwidth/nova-tools/releases) for
fixes and affected versions. Installed binaries do not update themselves.
Rebuild from a verified checkout or explicitly install a selected release with
the [fleet install play](../fleet/README.md#install-a-release).
