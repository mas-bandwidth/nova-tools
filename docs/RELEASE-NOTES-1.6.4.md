The stable tools release beside nova-sprint v1.6.4, on the owner's direct approval, carrying one change: an uncatalogued model provider (paperinstruments, the Ultramarine model) runs from opencode's custom provider block.

Install: download the assets for your platform from this release, then run `nova-sprint adopt v1.6.4 --source <nova-tools checkout at v1.6.4> --sprint-release <the nova-sprint v1.6.4 release downloaded> --inventory <file> --reason <text>` (see docs/SPEC-RELEASE.md).

## What changed in v1.6.4

- nova-config's provider kind carries `base_url` (https only), `key_env` (the NAME of the key's environment variable, `^[A-Z][A-Z0-9_]*$`) and `npm_adapter`, with migration 0046_provider_custom; all empty is a catalogued provider. (#5998)
- `member.Packet` carries `provider_base_url`, `provider_key_env` and `provider_npm_adapter`; the member passes them to `nova-swarm native`, which writes the job's opencode.json custom provider block (`npm`, `options.baseURL`, `options.apiKey` as `{env:NAME}`, never a key value) and opens no read proxy over the provider's own base. (#5998)
