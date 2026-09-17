## CI wall

Glenn 2026-09-17 (#888): the gate watches CI runtime and pit-stops when it is over two
minutes. It records each run's wall, `updated_at` minus `created_at` from the runs API it
already reads, for the branch tip and each PR it enqueues; a wall over 120 s writes one line
to `<queue>/REDS` of the form `CI-WALL <run id> <wall s> long-pole=<job name> <job wall s>`
naming the job with the largest wall from the jobs API the gate already fetches (`-` when
unknown), and the gate's status line carries `STOP: ci-wall`, while a wall under 120 s
changes nothing.
