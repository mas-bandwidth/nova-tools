# Setting nova up

## When something is wrong

### setup-nova-doctor-r-r2.w1

Run `nova-doctor run --local` first, whatever is wrong. It prints one `DOCTOR <check> ok|warn|fail`
line per dependency with what it saw and, when it is not ok, the one line that fixes it; it
exits 0 when nothing failed, 1 on a warn under `--strict`, 2 on a fail. `--check <name>` runs
one check and `--json` prints the same results as one object. The contract is
[SPEC-DOCTOR.md](SPEC-DOCTOR.md).
