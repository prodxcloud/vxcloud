# vxcloud Python — Changelog

All `vxcloud` releases are thin shims over `vxsdk`. Each version pins the
matching `vxsdk` release exactly.

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
