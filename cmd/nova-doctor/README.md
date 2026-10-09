# nova-doctor

## What it is

nova-doctor: says what is missing for the nova tools to work, and the one line that fixes each

## Why use it

Find out what is missing for the nova tools to work on this machine.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-doctor@latest
nova-doctor version
```

## First run

The command below is the tool's own example; `TestDoctorRunsEveryRegisteredCheckAndExitsByTheWorst` in `internal/doctor/doctor_test.go` runs `--local` against stand-in checks.

```text
$ nova-doctor --local
```

## Verbs

The reference for its verbs is [docs/SPEC-DOCTOR.md](../../docs/SPEC-DOCTOR.md).

- `run`
- `version`

## Spec

The contract is [docs/SPEC-DOCTOR.md](../../docs/SPEC-DOCTOR.md).
