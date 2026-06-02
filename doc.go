// Package vxsdk is the official Go SDK for the vxcloud platform.
//
// It provides a typed, ergonomic client over the FastAPI control plane
// at api.vxcloud.io (Infinity) and per-tenant nodes (e.g. node1.vxcloud.io).
//
// The SDK is preview / research quality. It is additive to existing tooling —
// vxcli, the API gateway, and the FastAPI backend are not modified by this
// package. External Go services may import it via:
//
//	go get github.com/prodxcloud/vxcloud@latest
//
// # Basic usage
//
//	c, err := vxsdk.New(ctx,
//	    vxsdk.WithAPIKey("xc_dev_..."),
//	    vxsdk.WithUsername("joelwembo"),
//	)
//	if err != nil { return err }
//
//	pipelines, err := c.CICD().Pipelines().List(ctx)
//
// # Auth precedence
//
// If WithAPIKey is provided, the SDK exchanges it for a JWT on first call
// and caches both. Subsequent requests carry both Authorization: Bearer and
// X-API-Key headers — the same pattern vxcli uses today, which keeps the SDK
// compatible with all existing FastAPI auth dependencies without requiring
// any backend change.
package vxsdk
