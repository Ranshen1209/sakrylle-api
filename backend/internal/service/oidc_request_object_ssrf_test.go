package service

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// allowLoopbackDial permits 127.0.0.1 so httptest servers are reachable,
// while still exercising the redirect re-validation logic.
func allowLoopbackDial(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

func TestOIDCHTTPClient_RejectsRedirectToInternal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	client := buildOIDCHTTPClientWithDialGuard(allowLoopbackDial)
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected redirect to 169.254.169.254 to be rejected")
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("expected redirect rejection, got: %v", err)
	}
}

func TestOIDCHTTPClient_AllowsNormalResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := buildOIDCHTTPClientWithDialGuard(allowLoopbackDial)
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("normal request should succeed, got: %v", err)
	}
	resp.Body.Close()
}
