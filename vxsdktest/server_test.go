package vxsdktest_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	vxsdk "github.com/prodxcloud/vxcloud"
	vxerrors "github.com/prodxcloud/vxcloud/errors"
	"github.com/prodxcloud/vxcloud/vxsdktest"
)

func TestPipelinesList_HappyPath(t *testing.T) {
	srv := vxsdktest.NewServer()
	defer srv.Close()

	srv.Handle("GET", "/api/v2/cicd/pipelines", vxsdktest.JSON(200, map[string]any{
		"data": []any{
			map[string]any{"id": "p1", "name": "alpha", "provider": "github", "repository_url": "https://x/alpha"},
			map[string]any{"id": "p2", "name": "beta", "provider": "github", "repository_url": "https://x/beta"},
		},
		"count": 2,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := vxsdk.New(ctx,
		vxsdk.WithAPIKey("xc_dev_test1234567890abcd"),
		vxsdk.WithUsername("alice"),
		vxsdk.WithInfinityURL(srv.URL()),
		vxsdk.WithNodeURL(srv.URL()),
	)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	pipelines, err := c.CICD().Pipelines().List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got := len(pipelines); got != 2 {
		t.Fatalf("len=%d want 2", got)
	}
	if pipelines[0].ID != "p1" || pipelines[1].Name != "beta" {
		t.Errorf("unexpected: %+v", pipelines)
	}

	// Auth headers on the wire?
	if got := srv.Calls[0].Headers.Get("X-API-Key"); got == "" {
		t.Error("expected X-API-Key header")
	}
}

func TestAutoRefreshOn401(t *testing.T) {
	srv := vxsdktest.NewServer()
	defer srv.Close()

	// First call: 401. Refresh: 200 with new tokens. Second list call: 200.
	srv.Handle("GET", "/api/v2/cicd/pipelines", vxsdktest.Sequence(
		vxsdktest.JSON(401, map[string]any{"detail": "expired"}),
		vxsdktest.JSON(200, map[string]any{"data": []any{}, "count": 0}),
	))
	srv.Handle("POST", "/api/v1/auth/developer/keys/login", vxsdktest.JSON(200, map[string]any{
		"access":  "fresh-jwt",
		"refresh": "fresh-refresh",
		"user":    map[string]any{"username": "alice"},
	}))

	ctx := context.Background()
	c, err := vxsdk.New(ctx,
		vxsdk.WithAPIKey("xc_dev_test1234567890abcd"),
		vxsdk.WithUsername("alice"),
		vxsdk.WithInfinityURL(srv.URL()),
		vxsdk.WithNodeURL(srv.URL()),
	)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := c.CICD().Pipelines().List(ctx); err != nil {
		t.Fatalf("list (should auto-recover): %v", err)
	}
	// Verify the refresh actually happened.
	var sawLogin bool
	for _, call := range srv.Calls {
		if call.Method == http.MethodPost && call.Path == "/api/v1/auth/developer/keys/login" {
			sawLogin = true
		}
	}
	if !sawLogin {
		t.Errorf("expected a developer/keys/login call to refresh JWT; got calls: %+v", srv.Calls)
	}
}

func TestTypedAuthError(t *testing.T) {
	srv := vxsdktest.NewServer()
	defer srv.Close()
	// 401 from refresh too — SDK gives up.
	srv.Handle("GET", "/api/v2/cicd/pipelines", vxsdktest.JSON(401, map[string]any{"detail": "nope"}))
	srv.Handle("POST", "/api/v1/auth/developer/keys/login", vxsdktest.JSON(401, map[string]any{"detail": "key revoked"}))

	ctx := context.Background()
	c, _ := vxsdk.New(ctx,
		vxsdk.WithAPIKey("xc_dev_test1234567890abcd"),
		vxsdk.WithUsername("alice"),
		vxsdk.WithInfinityURL(srv.URL()),
		vxsdk.WithNodeURL(srv.URL()),
	)
	_, err := c.CICD().Pipelines().List(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	var ae *vxerrors.AuthError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *AuthError, got %T: %v", err, err)
	}
}
