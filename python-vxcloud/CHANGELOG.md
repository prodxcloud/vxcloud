# vxcloud Python — Changelog

All `vxcloud` releases are thin shims over `vxsdk`. Each version pins the
matching `vxsdk` release exactly. Versioning is **CalVer** (`YYYY.M.D`) to
stay aligned with the vxnode fleet release tags (e.g. `v2026.6.10-1`); the
0.1.x preview line predates this switch.

## 2026.8.14

- Re-pinned to `vxsdk==2026.8.14` (base + `[async]` extra). No code changes.
- README gains a **SalesShift** section with runnable examples (prospect pool,
  reveal quota, pool → lead → contact, email, opportunities, tasks, social
  distribution, billing) and the matching `vxcli salesshift …` commands.
- Cross-language install table, Links block, and author attribution.

## 2026.8.13

- Re-pinned to `vxsdk==2026.8.13` (base + `[async]` extra), which brings the
  leads / prospect pool, `Sandboxes`, and the full SalesShift platform surface
  (billing, social, opportunities, tasks, campaigns) through the alias.
- No shim-side API changes: `import vxcloud` still re-exports every public name
  from `vxsdk`, and `import vxcloud_async` forwards to `vxsdk_async`.

## 2026.6.10

- Adopt CalVer (`YYYY.M.D`) — version now matches the vxnode fleet release
  date, in lock-step with `vxsdk 2026.6.10`.
- Re-pinned to `vxsdk==2026.6.10` (base + `[async]` extra).

## 0.1.1

- Docs only — no code or API changes; still a pure re-export of `vxsdk 0.1.0`.
- Rewrote the PyPI landing page (README): badges, tagline, quick start,
  capability map, async + error-handling sections.
- Clearer package summary describing what the SDK does.

## 0.1.0 — initial

- Brand-name alias package over `vxsdk 0.1.0`.
- Re-exports `Client`, `VxCloud`, `vxcloud`, all `Vx*` errors, all resource
  classes, and the module-level `load_from_vxcli` helper.
- Optional `async` extra re-exports `vxsdk_async` as `vxcloud_async`.
