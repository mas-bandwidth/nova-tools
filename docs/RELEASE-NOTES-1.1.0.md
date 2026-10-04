# Nova Tools 1.1.0

## Upgrading

Run `nova-redis fn load` against every store after you install 1.1.0. The
`nova_sprint` Redis Function library in this release is smaller, so its digest
differs from the one a store holds. Until the load, `nova-redis fn check`
reports the store `STALE` and names the same command as its remedy.
