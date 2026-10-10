# nova-delete

## What it is

nova-delete: move a literal path to quarantine instead of deleting it

## Why use it

Safely remove files by moving them to a dated quarantine folder instead of using recursive forced removal.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-delete@latest
nova-delete version
```

## Spec

[docs/SPEC-DELETE.md](../../docs/SPEC-DELETE.md)

## Verbs

See [docs/CLI.md#nova-delete](../../docs/CLI.md#nova-delete)

## First run

```sh
$ NOVA_DELETE_ROOTS=/tmp nova-delete /tmp/to_delete.txt
MOVED /tmp/to_delete.txt -> /tmp/.quarantine-20240101/to_delete.txt.150405.12345

$ nova-delete sweep --older-than 7d
SWEPT /tmp/.quarantine-20231201
```

## See also

- [docs/CLI.md](../../docs/CLI.md#nova-delete) - Full CLI reference
