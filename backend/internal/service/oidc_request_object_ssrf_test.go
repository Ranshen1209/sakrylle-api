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

func TestOIDCHTTPClient_RejectsRedirectToInternalIP(t *testing.T) {
	// https + 内网 IP：必须命中 CheckRedirect 的 IP 重校验分支。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	client := buildOIDCHTTPClientWithDialGuard(allowLoopbackDial)
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected redirect to internal IP to be rejected")
	}
	if !strings.Contains(err.Error(), "internal host") {
		t.Fatalf("expected internal-host rejection, got: %v", err)
	}
}

func TestOIDCHTTPClient_RejectsHTTPSDowngrade(t *testing.T) {
	// http + 公网 host：必须命中 CheckRedirect 的 scheme 降级分支。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/downgrade", http.StatusFound)
	}))
	defer srv.Close()

	client := buildOIDCHTTPClientWithDialGuard(allowLoopbackDial)
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected https->http downgrade redirect to be rejected")
	}
	if !strings.Contains(err.Error(), "non-https") {
		t.Fatalf("expected scheme-downgrade rejection, got: %v", err)
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
