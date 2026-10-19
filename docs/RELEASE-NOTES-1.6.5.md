The stable tools release beside nova-sprint v1.6.5, carrying one change: an opt-in local GPU capability for the sandbox, so a walled job on a Mac bench can render with Metal.

Install: download the assets for your platform from this release, then run `nova-sprint adopt v1.6.5 --source <nova-tools checkout at v1.6.5> --sprint-release <the nova-sprint v1.6.5 release downloaded> --inventory <file> --reason <text>` (see docs/SPEC-RELEASE.md).

## What changed in v1.6.5

- `nova-sandbox --gpu metal` appends the measured Metal minimum (`MetalGrants` in pkg/sandbox/gpu.go): iokit-open for AGXDeviceUserClient and IOSurfaceRootUserClient, and mach-lookup for the shader compiler (MTLCompilerService) and the hardware video encoder and decoder. Never a blanket iokit-open or mach-lookup. The default, `--gpu none`, writes the same profile as v1.6.4. (#6005)
- `nova-swarm member --gpu <none|metal>` and `nova-swarm native --gpu` carry the opt-in to each child's wall; the member's operator sets it on its loop record. A nova-swarm older than v1.6.5 refuses the flag, so adopt first, then set it. (#6005)
