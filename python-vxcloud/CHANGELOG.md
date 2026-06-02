# vxcloud Python — Changelog

All `vxcloud` releases are thin shims over `vxsdk`. Each version pins the
matching `vxsdk` release exactly.

## 0.1.0 — initial

- Brand-name alias package over `vxsdk 0.1.0`.
- Re-exports `Client`, `VxCloud`, `vxcloud`, all `Vx*` errors, all resource
  classes, and the module-level `load_from_vxcli` helper.
- Optional `async` extra re-exports `vxsdk_async` as `vxcloud_async`.
