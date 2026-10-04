# public-dashboard

The public sprint dashboard served by Caddy from files on the web machine, so
the dashboard server (the machine that runs `where --json`) sees one request per
second whatever the viewer count. The play is `fleet/public-dashboard.yml`;
`fleet/inventory.public-dashboard.example.yml` shows the inventory shape.

    ansible-playbook -i <your inventory> fleet/public-dashboard.yml --check --diff | cat
    ansible-playbook -i <your inventory> fleet/public-dashboard.yml | cat

## What it does

| piece | where | what |
|---|---|---|
| puller | `/usr/local/libexec/nova-dashboard-pull`, `nova-dashboard-pull.service` | **STOPGAP until nova-sprint dashboard publish** (card dashboard-publish-verb). Fetches `<upstream>/api/sprint` once per second into `site/api/sprint.json` (and a `.gz` sidecar), `landings.json` every 60 s (kept only when the upstream serves it; the Studio server.py of 2026-10-04 answers 404, so the page hides its landings panel, as it did behind the proxy), and the page and its assets whenever the snapshot's `build` changes; each file is written to `tmp/` and renamed into place. A failed fetch keeps the last file, so the page shows the last snapshot as stale. |
| site | `{{ public_dashboard_dir }}/site` | owned by the puller's system user; served read-only by Caddy |
| Caddyfile | `/etc/caddy/Caddyfile` | `file_server` with `precompressed gzip` for each of `public_dashboard_sites`; no `reverse_proxy`. `/api/sprint` is rewritten to `api/sprint.json`. |

Cache-Control: `public, max-age=1` on `/`, `/index.html`, `/api/sprint` and
`/landings.json`; `public, max-age=31536000, immutable` on a versioned asset
(`?v=<build>`, as the page links `app.js` and the logo); `public_dashboard_asset_max_age`
on the rest. `/events` is answered 404 here, so the page polls; `/healthz`
answers `ok`.

The puller is started and the site filled before the Caddyfile changes, so the
switch from a proxying configuration never serves an empty directory. The
Caddyfile is validated (`caddy validate`) before it is written.

## Variables

| variable | default | |
|---|---|---|
| `public_dashboard_upstream` | required | the dashboard server, `http://<host>:<port>` |
| `public_dashboard_sites` | required | Caddy site addresses that serve the page |
| `public_dashboard_dir` | `/var/www/sprint` | holds `site/` and `tmp/` |
| `public_dashboard_user` | `nova-dashboard` | the puller's system user |
| `public_dashboard_landings_every` | `60` | seconds between `landings.json` fetches |
| `public_dashboard_asset_max_age` | `3600` | Cache-Control max-age on unversioned assets |
