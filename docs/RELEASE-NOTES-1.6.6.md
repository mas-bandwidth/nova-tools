The stable tools point release after v1.6.5, carrying one fix to the custom provider block backported in v1.6.4 (#5998): a direct provider's model now carries an output limit, so Ultramarine 1 Flash reads stop failing with 400 "max_tokens must be between 1 and 65536". nova-sprint stays at v1.6.5.

Install: download the assets for your platform from this release, then run `nova-update release install --from <release root> --version v1.6.6 --bin <dir>` with the nova-sprint v1.6.5 binaries staged beside them (see docs/SPEC-RELEASE.md).

## What changed in v1.6.6

- Every model declared under a direct (uncatalogued) provider gets `provider.<id>.models.<model>.limit {output, context}` in the job's opencode config. It uses the row's declared limits, or else output 65536 (`DefaultDirectOutputLimit`) and context 0, which is opencode's unknown. Both keys are always written because the pinned opencode (v1.18.20) refuses a limit that is missing either one. Before this fix the harness requested its own output maximum (128000), which a provider capped at 65536 refuses. (#6011)
