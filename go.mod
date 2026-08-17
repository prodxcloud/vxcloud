module github.com/prodxcloud/vxcloud

go 1.22

// Release versions encode the CalVer date in the MINOR field as YYYYMMDD —
// release 2026.8.17 is tagged v0.20260817.0. A v-prefixed CalVer tag cannot be
// used directly: Go reads v2026.8.17 as major version 2026 and rejects it
// unless the module path ends in /v2026. See README.md#install.
//
// v0.2.0 is NOT deleted — sum.golang.org has recorded it permanently, and
// removing a published tag breaks anyone who pinned it rather than helping
// them. Retracting is the supported way to stop offering it.

retract v0.2.0 // superseded by v0.20260817.0 — same release, CalVer-encoded
