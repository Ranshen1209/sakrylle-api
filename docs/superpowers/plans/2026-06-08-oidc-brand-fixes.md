# OIDC 基座与品牌化修复 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复代码审查产出的全部 32 条 finding（1 High + 8 Medium + ~12 Low/Info），覆盖 Sakrylle API 后端 OIDC 基座与前端品牌化。

**Architecture:** 按 6 个主题组（A SSRF / B Token 端点 / C Logout / D 密钥服务 / E Pairwise+Claims+Discovery / F 品牌+文档）分组提交，每组自洽可独立回滚。组 A 先做，因组 E 的 request_uri 实现依赖 A 的安全 HTTP 客户端。全部在 `theme/monet-purple` 分支直接提交，暂不开 PR；触生产动作仅标注、不在仓库执行。

**Tech Stack:** Go 1.23 / Gin / ent ORM / golang-jwt v5；前端 Vue 3 + Vite + Tailwind；测试用 Go 标准 `testing`，并发项带 `-race`。

**设计依据:** `docs/superpowers/specs/2026-06-08-oidc-brand-fixes-design.md`

**全局约定（每个 commit 前都适用）:**
- 工作目录：仓库根 `/Users/cervine/Documents/Sakrylle/Sakrylle API`
- 后端构建：`cd backend && go build ./...`
- 后端测试：`cd backend && go test ./internal/service/... ./internal/handler/...`
- 提交信息用中文 + 英文混合，结尾带 `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`
- 不改 access_token(`sk_oauth_`) 形态与网关计费路径

---

## 组 A · SSRF + 出站 HTTP 加固

### Task A1：为 OIDC 出站拉取构造 SSRF-safe HTTP 客户端

**Files:**
- Modify: `backend/internal/service/oidc_request_object.go:34-39`（`init()` 中 `DefaultHTTPClient` 构造）
- Test: `backend/internal/service/oidc_request_object_ssrf_test.go`（新建）

复用同包已有的 `safeDialContext`（`channel_monitor_ssrf.go:114`）与 `isPrivateOrLoopbackHost`。改造点是：(1) `Transport.DialContext = safeDialContext`；(2) 加 `CheckRedirect` 每跳重校验 scheme + IP 段、拒绝降级、限跳数 3。

- [ ] **Step 1: 写失败测试**

新建 `backend/internal/service/oidc_request_object_ssrf_test.go`：

```go
package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// buildOIDCHTTPClient is the helper the production init() must use (Task A1 Step 3).
func TestOIDCHTTPClient_RejectsRedirectToInternal(t *testing.T) {
	// Upstream returns a redirect to a link-local metadata address.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer srv.Close()

	client := buildOIDCHTTPClient()
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("expected redirect to 169.254.169.254 to be rejected, got status %d", resp.StatusCode)
	}
	if !strings.Contains(err.Error(), "redirect") && !strings.Contains(err.Error(), "SSRF") && !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected SSRF/redirect rejection, got: %v", err)
	}
}

func TestOIDCHTTPClient_RejectsHTTPSDowngrade(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/downgrade", http.StatusFound)
	}))
	defer srv.Close()

	client := buildOIDCHTTPClient()
	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("expected https->http downgrade redirect to be rejected")
	}
}

func TestOIDCHTTPClient_AllowsNormalResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := buildOIDCHTTPClient()
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("normal request should succeed, got: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}
```

> 注：`httptest.NewServer` 监听 127.0.0.1。`safeDialContext` 会拒绝 loopback——所以「正常响应」用例必须让客户端能连到测试服务器。解决办法见 Step 3：`buildOIDCHTTPClient` 接受一个可选的「拨号校验开关」，测试用一个允许 loopback 的变体。改为下方 Step 1b 的最终测试形态。

- [ ] **Step 1b: 用可注入校验器重写测试**

把上面三个用例的 `buildOIDCHTTPClient()` 改为 `buildOIDCHTTPClientWithDialGuard(dialGuard)`，其中正常用例传入「允许 loopback」的 guard，SSRF 用例传入生产 guard。最终测试：

```go
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
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd backend && go test ./internal/service/ -run TestOIDCHTTPClient -v`
Expected: 编译失败（`buildOIDCHTTPClientWithDialGuard` undefined）。

- [ ] **Step 3: 实现安全客户端构造**

替换 `oidc_request_object.go:34-39` 的 `init()`：

```go
func init() {
	DefaultHTTPClient = buildOIDCHTTPClient()
}

// buildOIDCHTTPClient builds the HTTP client used for request_uri /
// sector_identifier_uri fetches, with SSRF protection at the dial layer and
// per-hop redirect re-validation.
func buildOIDCHTTPClient() *http.Client {
	return buildOIDCHTTPClientWithDialGuard(safeDialContext)
}

// buildOIDCHTTPClientWithDialGuard allows tests to inject a dial guard that
// permits loopback (so httptest servers are reachable) while still exercising
// the redirect re-validation path.
func buildOIDCHTTPClientWithDialGuard(dial func(ctx context.Context, network, address string) (net.Conn, error)) *http.Client {
	return &http.Client{
		Timeout: requestURITimeout,
		Transport: &http.Transport{
			DialContext: dial,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("oidc fetch: too many redirects (%d)", len(via))
			}
			if req.URL.Scheme != "https" {
				return fmt.Errorf("oidc fetch: redirect to non-https scheme %q rejected", req.URL.Scheme)
			}
			// Re-validate the redirect target host at the IP level.
			blocked, err := isPrivateOrLoopbackHost(req.Context(), req.URL.Hostname())
			if err != nil {
				return fmt.Errorf("oidc fetch: redirect host resolution failed: %w", err)
			}
			if blocked {
				return fmt.Errorf("oidc fetch: redirect to internal host %q blocked", req.URL.Hostname())
			}
			return nil
		},
	}
}
```

在 import 块补 `"context"`、`"net"`（`fmt` 已在）。

> 说明：`CheckRedirect` 里对测试用的 loopback 重定向也会拒（因为 `isPrivateOrLoopbackHost` 拒 loopback）——这正是 `RejectsRedirectToInternal` 想验证的。`AllowsNormalResponse` 无重定向，CheckRedirect 不触发，仅 DialContext 用 `allowLoopbackDial` 放行。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd backend && go test ./internal/service/ -run TestOIDCHTTPClient -v`
Expected: PASS（3 个用例）。再跑 `cd backend && go build ./...` 确认编译。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/service/oidc_request_object.go backend/internal/service/oidc_request_object_ssrf_test.go
git commit -m "fix(oidc): harden outbound HTTP client against SSRF (#1)

Wire safeDialContext into DefaultHTTPClient and add CheckRedirect that
re-validates scheme + IP range per hop, rejecting https->http downgrade and
redirects to internal/metadata addresses (169.254.169.254 etc). Closes the
request_uri / sector_identifier_uri SSRF + DNS-rebinding gap.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task A2：sector_identifier_uri / request_uri 拉取走安全客户端 + per-request 超时

**Files:**
- Modify: `backend/internal/service/oidc_request_object.go:503-508`（`FetchRequestURI` 的 `context.Background()`）

`FetchSectorIdentifierURI` 已用 `DefaultHTTPClient`（A1 后即安全）+ `context.WithTimeout`，无需改。`FetchRequestURI` 用的是 `context.Background()`（Info 项隐患），改为带超时的 context。

- [ ] **Step 1: 修改 FetchRequestURI 的 context**

把 `oidc_request_object.go:503-508` 的 `http.NewRequestWithContext(context.Background(), ...)` 改为：

```go
	ctx, cancel := context.WithTimeout(context.Background(), requestURITimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		rawURI,
		nil,
	)
```

- [ ] **Step 2: 构建验证**

Run: `cd backend && go build ./... && go test ./internal/service/ -run 'FetchRequestURI|TestOIDCHTTPClient' -v`
Expected: 编译通过，现有 request_uri 测试 + A1 测试 PASS。

- [ ] **Step 3: 提交**

```bash
git add backend/internal/service/oidc_request_object.go
git commit -m "fix(oidc): add per-request timeout to request_uri fetch (#A-info)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## 组 B · Token 端点正确性

### Task B1：introspect 加跨 client 受众守卫（#7 Medium）

**Files:**
- Modify: `backend/internal/service/oauth_provider_service.go:574-577`（`IntrospectToken`，加载 meta 后）
- Test: `backend/internal/service/oauth_provider_service_test.go`（追加用例）

`IntrospectToken(ctx, clientID, token)` 已拿到调用方 `clientID`，加载 `meta` 后未比对 `meta.ClientID`。加一行守卫，与 revoke 一致。

- [ ] **Step 1: 写失败测试**

在 `oauth_provider_service_test.go` 末尾追加（沿用文件内现有 stub 构造方式；若现有测试用 `newTestProviderService()` 之类 helper，复用之）：

```go
func TestIntrospectToken_RejectsCrossClient(t *testing.T) {
	svc, seed := newIntrospectTestService(t) // helper: see Step 3 note
	// seed a token owned by client "client-a"
	tok := seed("client-a", 42, []string{"openid"})

	// client-b introspects client-a's token -> must be inactive
	resp, err := svc.IntrospectToken(context.Background(), "client-b", tok)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Active {
		t.Fatal("expected active:false for cross-client introspection")
	}

	// owning client introspects -> active
	resp2, err := svc.IntrospectToken(context.Background(), "client-a", tok)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp2.Active {
		t.Fatal("expected active:true for same-client introspection")
	}
}
```

> Step 3 备注：若文件中已无可直接复用的 introspect stub helper，则改用现有 `oauth_provider_oidc_test.go` / `oauth_provider_handler_test.go` 里 introspect 相关测试的构造方式（grep `IntrospectToken` 找现成 stub），把上面两段断言并入。关键断言不变：跨 client → `Active==false`，同 client → `Active==true`。

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/service/ -run TestIntrospectToken_RejectsCrossClient -v`
Expected: FAIL（跨 client 仍返回 active:true）或编译失败（helper 待补）。

- [ ] **Step 3: 实现守卫**

在 `oauth_provider_service.go` 的 `IntrospectToken`，紧接 `meta` 加载成功之后（现 `:577` 之后、`:579` issuer 之前）插入：

```go
	// Audience scoping: a client may only introspect tokens it owns.
	// Mirrors the revoke path's cross-client check; without this any
	// confidential client could introspect any other client's token.
	if meta.ClientID != clientID {
		return &IntrospectionResponse{Active: false}, nil
	}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd backend && go test ./internal/service/ -run TestIntrospectToken -v && go build ./...`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/service/oauth_provider_service.go backend/internal/service/oauth_provider_service_test.go
git commit -m "fix(oidc): scope token introspection to owning client (#7)

IntrospectToken now compares the caller client_id against the token's
meta.ClientID and returns active:false on mismatch, matching the revoke
path. Prevents any confidential client from introspecting another
client's token.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task B2：签名 UserInfo JWT 补 iss/aud + 真实 email_verified（#3 Medium / #10 Low）

**Files:**
- Modify: `backend/internal/service/oidc_userinfo_jwt.go:27-51`（`BuildUserInfoJWTClaims` 签名与实现）
- Modify: `backend/internal/handler/oauth_provider_handler.go:1093`（调用点）
- Test: `backend/internal/service/oidc_userinfo_jwt_test.go`（新建）

- [ ] **Step 1: 写失败测试**

新建 `backend/internal/service/oidc_userinfo_jwt_test.go`：

```go
package service

import (
	"testing"
	"time"
)

func TestBuildUserInfoJWTClaims_IncludesIssAndAud(t *testing.T) {
	claims := BuildUserInfoJWTClaims(
		"https://sub.sakrylle.com", "client-a",
		"42", "alice", "alice@example.com", true,
		time.Unix(1_700_000_000, 0), 0,
	)
	if claims["iss"] != "https://sub.sakrylle.com" {
		t.Fatalf("iss = %v, want issuer", claims["iss"])
	}
	aud, ok := claims["aud"].([]string)
	if !ok || len(aud) != 1 || aud[0] != "client-a" {
		t.Fatalf("aud = %v, want [client-a]", claims["aud"])
	}
	if claims["email_verified"] != true {
		t.Fatalf("email_verified = %v, want true", claims["email_verified"])
	}
	if claims["sub"] != "42" {
		t.Fatalf("sub = %v, want 42", claims["sub"])
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/service/ -run TestBuildUserInfoJWTClaims -v`
Expected: 编译失败（参数个数不符）。

- [ ] **Step 3: 改签名与实现**

把 `oidc_userinfo_jwt.go` 的 `BuildUserInfoJWTClaims` 改为：

```go
func BuildUserInfoJWTClaims(
	issuer string,
	clientID string,
	sub string,
	username string,
	email string,
	emailVerified bool,
	now time.Time,
	ttl time.Duration,
) jwt.MapClaims {
	if ttl <= 0 {
		ttl = DefaultUserInfoJWTTTL
	}
	claims := jwt.MapClaims{
		"iss": issuer,
		"aud": []string{clientID},
		"sub": sub,
		"iat": now.Unix(),
		"exp": now.Add(ttl).Unix(),
	}
	if username != "" {
		claims["name"] = username
		claims["preferred_username"] = username
	}
	if email != "" {
		claims["email"] = email
		claims["email_verified"] = emailVerified
	}
	return claims
}
```

同步更新函数 doc 注释，删除「No iss/aud」的旧描述，补「iss = OP issuer, aud = [clientID]」。

- [ ] **Step 4: 改调用点**

`oauth_provider_handler.go:1093` 现为：
`claims := service.BuildUserInfoJWTClaims(sub, username, emailForJWT, time.Now(), 0)`
改为（`meta.ClientID` 与 `h.discoveryIssuer(c)` 在作用域内；`emailVerified` 取已有的用户字段——grep 该函数体内 UserInfo 已用的 `email_verified` 来源，通常是 `user.EmailVerified` 或 meta 上的字段；若不可得则传 `emailVerifiedForJWT` 局部变量，与纯 JSON UserInfo 同源）：

```go
		claims := service.BuildUserInfoJWTClaims(
			h.discoveryIssuer(c), meta.ClientID,
			sub, username, emailForJWT, emailVerifiedForJWT,
			time.Now(), 0,
		)
```

> 实施备注：读 `oauth_provider_handler.go` 1034 附近纯 JSON UserInfo 分支，确认 `email_verified` 的真实来源变量名，令 `emailVerifiedForJWT` 与之一致（同一用户标志，不得再硬编码 false）。

- [ ] **Step 5: 运行确认通过 + 构建**

Run: `cd backend && go test ./internal/service/ -run TestBuildUserInfoJWTClaims -v && go build ./...`
Expected: PASS + 编译通过。

- [ ] **Step 6: 提交**

```bash
git add backend/internal/service/oidc_userinfo_jwt.go backend/internal/service/oidc_userinfo_jwt_test.go backend/internal/handler/oauth_provider_handler.go
git commit -m "fix(oidc): add iss/aud and real email_verified to signed UserInfo JWT (#3,#10)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task B3：对所有 client 强制 PKCE S256（#20）

**Files:**
- Modify: `backend/internal/service/oauth_provider_service.go:648-658`（`ValidateAuthorizeRequest` 的 PKCE 门控）
- Modify: `backend/internal/service/oauth_provider_service.go:1101-1102`（token 交换 PKCE 门控）
- Modify: `backend/internal/service/oauth_provider_service.go:524`（`LookupClient` 收紧逻辑）
- Test: `backend/internal/service/oauth_security_test.go`（追加用例）

- [ ] **Step 1: 写失败测试**

在 `oauth_security_test.go` 追加（沿用文件内现有 client/req 构造 helper；下方用占位 helper 名，Step 3 据现有 helper 调整）：

```go
func TestValidateAuthorizeRequest_PKCERequiredForConfidentialClient(t *testing.T) {
	svc := newSecurityTestService(t)
	// confidential client (secret set) with PKCERequired=false in DB
	registerTestClient(t, svc, &OAuthClient{
		ClientID:         "conf-client",
		ClientSecretHash: "$2a$10$ignoredhashvalueignoredhashvalue00000000000000000",
		RedirectURIs:     []string{"https://rp.example.com/cb"},
		AllowedScopes:    []string{"openid"},
		PKCERequired:     false,
	})
	req := &AuthorizeRequest{
		ResponseType: "code",
		ClientID:     "conf-client",
		RedirectURI:  "https://rp.example.com/cb",
		Scopes:       []string{"openid"},
		State:        "xyz",
		// no code_challenge
	}
	_, err := svc.ValidateAuthorizeRequest(context.Background(), req)
	if !errors.Is(err, ErrOAuthMissingPKCE) {
		t.Fatalf("expected ErrOAuthMissingPKCE for confidential client without PKCE, got: %v", err)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/service/ -run TestValidateAuthorizeRequest_PKCERequired -v`
Expected: FAIL（当前 confidential client 跳过 PKCE，返回 nil err）。

- [ ] **Step 3: authorize 端强制 PKCE**

把 `oauth_provider_service.go:648-658` 的 `if client.PKCERequired { ... }` 整块改为无条件：

```go
	// PKCE S256 is mandatory for ALL clients (public and confidential), per the
	// v2 contract. We no longer gate on client.PKCERequired.
	if strings.TrimSpace(req.CodeChallenge) == "" {
		return nil, ErrOAuthMissingPKCE
	}
	if req.CodeChallengeMethod != "S256" {
		return nil, ErrOAuthUnsupportedChallenge
	}
	if !validatePKCEChallenge(req.CodeChallenge) {
		return nil, ErrOAuthPKCEFormat
	}
```

- [ ] **Step 4: token 端强制 PKCE**

把 `oauth_provider_service.go:1101-1102` 的 `if client.PKCERequired || code.CodeChallenge != "" {` 改为无条件校验：

```go
	// PKCE verification is mandatory for all clients.
	if !verifyPKCES256(code.CodeChallenge, codeVerifier) {
```

（保留其内原有的失败返回分支不变。）

- [ ] **Step 5: 收紧 LookupClient**

读 `oauth_provider_service.go:513-540` `LookupClient`。`:524` 的 `if !client.PKCERequired && client.ClientSecretHash == ""` 是「公共 client 必须开 PKCE」校验。强制全员 PKCE 后，该拒绝条件应改为「任何 client 都视作需要 PKCE」——但因 authorize/token 端已无条件强制，此处最稳妥是**保留**该防御（不放松），仅在注释说明 PKCE 现已全局强制。无需改代码逻辑，仅加注释：

```go
	// Note: PKCE S256 is now enforced unconditionally at authorize/token
	// endpoints regardless of this flag; this guard remains as defense in depth.
	if !client.PKCERequired && client.ClientSecretHash == "" {
```

- [ ] **Step 6: 运行确认通过 + 全量回归**

Run: `cd backend && go test ./internal/service/ ./internal/handler/ && go build ./...`
Expected: 新用例 PASS；若有旧测试依赖「confidential client 可跳过 PKCE」，更新这些测试以提供合法 S256 challenge（这是有意的行为收紧）。

- [ ] **Step 7: 提交**

```bash
git add backend/internal/service/oauth_provider_service.go backend/internal/service/oauth_security_test.go
git commit -m "fix(oidc): enforce PKCE S256 for all clients incl. confidential (#20)

authorize and token endpoints now require a valid S256 code_challenge /
code_verifier for every client, no longer gated on client.PKCERequired.

NOTE (merge prerequisite, ops): before deploying, verify production has no
client with pkce_required=false:
  SELECT client_id FROM oauth_clients WHERE pkce_required=false;
(should be empty).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## 组 C · Logout 合规

### Task C1：logout_token 贯通 sid（#8 Medium）

**Files:**
- Modify: `backend/internal/handler/oauth_provider_handler.go:1236-1312`（`verifyLogoutIDToken` 返回值加 sid）
- Modify: `backend/internal/handler/oauth_provider_handler.go:1145-1225`（`Logout` 接收 sid 并透传）
- Modify: `backend/internal/handler/oauth_provider_handler.go:1632,1663`（`dispatchBackchannelLogout` 加 sid 参数并传给 `BuildLogoutToken`）

现状：`verifyLogoutIDToken` 返回 `(clientID, sub, err)`；`Logout` 调 `dispatchBackchannelLogout(c, clientID, sub)`；`dispatchBackchannelLogout` 恒传 `BuildLogoutToken(..., "", ...)`（sid 为空）。id_token 的 `sid` claim 已存在（`oidc_id_token.go:157`），只是 logout 路径没透传。

- [ ] **Step 1: verifyLogoutIDToken 返回 sid**

把 `verifyLogoutIDToken` 签名从 `(string, string, error)` 改为 `(string, string, string, error)`（clientID, sub, sid, err）。在函数体 `:1310` 处 `sub, _ := claims["sub"].(string)` 之后加：

```go
	sid, _ := claims["sid"].(string)
	return clientID, sub, sid, nil
```

并把该函数内所有早返回的 `return "", "", err`（共约 4 处：`:1280`、`:1285`、`:1290`、`:1306` 等）改为 `return "", "", "", err`。

- [ ] **Step 2: Logout 接收并透传 sid**

`oauth_provider_handler.go:1153-1165`，把：

```go
	var clientID string
	var sub string
	if idTokenHint != "" {
		cid, verifiedSub, err := h.verifyLogoutIDToken(c.Request.Context(), idTokenHint)
		...
		clientID = cid
		sub = verifiedSub
	}
```

改为同时取 sid：

```go
	var clientID string
	var sub string
	var sid string
	if idTokenHint != "" {
		cid, verifiedSub, verifiedSid, err := h.verifyLogoutIDToken(c.Request.Context(), idTokenHint)
		if err != nil {
			slog.Warn("oidc logout: id_token_hint verification failed", "error", err)
			h.renderLogoutErrorPage(c, "invalid_request", "id_token_hint is invalid or expired")
			return
		}
		clientID = cid
		sub = verifiedSub
		sid = verifiedSid
	}
```

把两处 `h.dispatchBackchannelLogout(c, clientID, sub)`（`:1175`、`:1222`）改为 `h.dispatchBackchannelLogout(c, clientID, sub, sid)`。

- [ ] **Step 3: dispatchBackchannelLogout 加 sid 参数**

`:1632` 函数签名 `func (h *OAuthProviderHandler) dispatchBackchannelLogout(c *gin.Context, clientID string, sub string)` 改为追加 `sid string`。`:1663` 的 `BuildLogoutToken(issuer, sub, client.ClientID, "", now, 0)` 改为 `BuildLogoutToken(issuer, sub, client.ClientID, sid, now, 0)`。

- [ ] **Step 4: 构建**

Run: `cd backend && go build ./... && go test ./internal/handler/ -run 'Logout' -v`
Expected: 编译通过；现有 logout 测试 PASS（若签名变更影响测试 helper，同步更新）。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/handler/oauth_provider_handler.go
git commit -m "fix(oidc): propagate sid into back-channel logout_token (#8)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task C2：backchannel logout 限定接收方为有活跃 grant 的 client（#9 Medium）

**Files:**
- Modify: `backend/internal/handler/oauth_provider_handler.go:1648-1657`（`dispatchBackchannelLogout` 的 client 列表过滤）

现状：广播给所有配了 `backchannel_logout_uri` 的 client。改为：与该 subject 有活跃 grant 的 client 集合取交集。`ListGrantsByUser(ctx, userID)` 已存在于 service（`oauth_provider_service.go:311`），但 dispatch 当前只有 `sub`（可能是 pairwise 伪名）。需先拿到数值 userID。

> 实施备注：`dispatchBackchannelLogout` 当前只接收 `sub`（字符串，public client 即数值 userID 字符串，pairwise client 是伪名）。要按 userID 查 grant，需在 `Logout` 中从 verifyLogoutIDToken 已验证的 id_token 里取数值 userID——但 sub 在 pairwise 下不是 userID。最稳妥实现：在 `verifyLogoutIDToken` 解析阶段额外不依赖 sub，而是改为「`Logout` 已知 clientID 后，用 OP 端 session/grant 反查」。鉴于本仓库 grant 按 `(userID, clientID)` 组织，且 dispatch 仅有 sub：**采用 client_id 维度的最小正确过滤**——只广播给（a）发起 logout 的 clientID，加（b）与发起 client 共享同一 `sid` 的 client。这与设计文档「至少限定到发起 client + 共享 sid 的 client」一致，不需要 userID 反查。

- [ ] **Step 1: 改广播范围**

把 `:1648-1657` 的「遍历所有 ListClientsWithBackchannelLogout」改为「只通知发起 client」（最小正确范围，消除对无关 RP 的过度通知与登出行为泄露）：

```go
	// Notify only the initiating client (the RP that triggered logout), not
	// every client with a backchannel_logout_uri. Broadcasting to all RPs
	// over-notifies unrelated parties and leaks the user's logout activity.
	if clientID == "" {
		return
	}
	client, err := h.provider.GetClientByID(c.Request.Context(), clientID)
	if err != nil || client == nil {
		slog.Warn("oidc backchannel logout: initiating client not found", "client_id", clientID, "error", err)
		return
	}
	if client.BackchannelLogoutURI == nil || *client.BackchannelLogoutURI == "" {
		return // initiating client has no backchannel endpoint
	}
	clients := []*service.OAuthClient{client}
```

保留下方 `now := time.Now()` 起的 for 循环不变（现在 `clients` 只含发起 client）。

> 备注：若 `h.provider` 无 `GetClientByID`，grep `func.*GetClient`/`LookupClient` 找现成单 client 查询方法（service 层 `LookupClient` 存在，但 handler 经 `h.provider` 接口暴露的方法名以实际为准）。退路：保留 `ListClientsWithBackchannelLogout` 遍历，但在循环内 `if client.ClientID != clientID { continue }`。

- [ ] **Step 2: 构建 + 测试**

Run: `cd backend && go build ./... && go test ./internal/handler/ -run Logout -v`
Expected: 编译通过；logout 测试 PASS。若有断言「所有 RP 收到广播」的旧测试，更新为「仅发起 client 收到」。

- [ ] **Step 3: 提交**

```bash
git add backend/internal/handler/oauth_provider_handler.go
git commit -m "fix(oidc): limit back-channel logout to the initiating client (#9)

Stop broadcasting logout_token to every client with a backchannel_logout_uri;
only the RP that triggered logout is notified. Eliminates over-notification
and logout-activity leakage to unrelated RPs.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task C3：logout_token 加 jti（#13 Low）

**Files:**
- Modify: `backend/internal/service/oidc_backchannel_logout.go:33-46`（`BuildLogoutToken` 加 jti）

- [ ] **Step 1: 加 jti**

在 `BuildLogoutToken` 的 `claims` map 构造后、`return` 前插入 jti（用现有 `GenerateOpaqueToken`，签名 `func GenerateOpaqueToken(nbytes int) (string, error)`，`oauth_provider_service.go:2266`）：

```go
	if jti, err := GenerateOpaqueToken(16); err == nil {
		claims["jti"] = jti
	} else {
		// crypto/rand failure is extremely rare; fall back to a timestamp-based
		// unique-enough value so the token still carries a jti per spec §2.4.
		claims["jti"] = fmt.Sprintf("jti_%d", now.UnixNano())
	}
```

import 块补 `"fmt"`（若未引）。

- [ ] **Step 2: 测试 + 构建**

在 `oidc_backchannel_logout_test.go`（若不存在则新建）加断言 `claims["jti"]` 非空且两次调用不同。
Run: `cd backend && go test ./internal/service/ -run LogoutToken -v && go build ./...`
Expected: PASS。

- [ ] **Step 3: 提交**

```bash
git add backend/internal/service/oidc_backchannel_logout.go backend/internal/service/oidc_backchannel_logout_test.go
git commit -m "fix(oidc): add jti to logout_token for replay protection (#13)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## 组 D · 密钥服务健壮性

### Task D1：cleanupExpiredKeysFor 全程持锁（#5 Medium）

**Files:**
- Modify: `backend/internal/service/oidc_key_service.go:668-737`（`cleanupExpiredKeysFor`）
- Test: `backend/internal/service/oidc_key_service_test.go`（追加 `-race` 并发用例）

现状：`cleanupExpiredKeysFor` 的 store Get→filter→Put（`:675-719`）不持 `s.mu`，只在 `:722-734` 锁内存 prune。`RotateKey`/`appendPreviousKID` 全程持 `s.mu`。两个独立 goroutine 交错会丢刚轮换的 kid。

- [ ] **Step 1: 写并发失败测试**

在 `oidc_key_service_test.go` 追加（沿用文件内现有 `newTestOIDCKeyService` / store stub 构造方式）：

```go
func TestOIDCKeyService_ConcurrentRotateAndCleanup(t *testing.T) {
	svc := newTestOIDCKeyService(t) // existing helper in this file
	if err := svc.EnsureKey(context.Background()); err != nil {
		t.Fatalf("ensure key: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = svc.RotateKey(context.Background()) }()
		go func() { defer wg.Done(); _, _ = svc.CleanupExpiredKeys(context.Background()) }()
	}
	wg.Wait()
	// JWKS must still load (no panic / no nil key).
	if _, err := svc.JWKS(context.Background()); err != nil {
		t.Fatalf("JWKS after concurrent rotate/cleanup: %v", err)
	}
}
```

import 补 `"sync"`、`"context"`（若未引）。

- [ ] **Step 2: 运行（带 -race）确认数据竞争**

Run: `cd backend && go test ./internal/service/ -run TestOIDCKeyService_ConcurrentRotateAndCleanup -race -count=5 -v`
Expected: DATA RACE 报告（store list 的并发 Get/Put 与 rotation 交错）。

- [ ] **Step 3: 让整个 store RMW 持锁**

把 `cleanupExpiredKeysFor` 改为入口即 `s.mu.Lock(); defer s.mu.Unlock()`，并删掉原 `:722` 内层单独的 `s.mu.Lock()/Unlock()`（因已全程持锁）。即在函数体第一行（`jsonStr, found, err := s.store.Get(...)` 之前）加：

```go
	s.mu.Lock()
	defer s.mu.Unlock()
```

并把 `:722-734` 内存 prune 段的 `s.mu.Lock()` / `s.mu.Unlock()` 两行删除（保留中间的 prune 逻辑）。

> 重要：`CleanupExpiredKeys`（`:629`）调用 `cleanupExpiredKeysFor` 两次（rsa/ec），它本身不持锁，所以 `cleanupExpiredKeysFor` 持锁不会自死锁。但要确认 `RotateKey`/`RotateECKey` 不在持锁时调用 cleanup（它们不调，已核对）。

- [ ] **Step 4: 运行确认无竞争**

Run: `cd backend && go test ./internal/service/ -run 'TestOIDCKeyService_ConcurrentRotateAndCleanup|TestOIDCKeyCleanup|TestOIDCECKeyCleanup' -race -count=5 -v`
Expected: PASS，无 DATA RACE。再跑 `go build ./...`。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/service/oidc_key_service.go backend/internal/service/oidc_key_service_test.go
git commit -m "fix(oidc): hold s.mu across full previous-kids store RMW in cleanup (#5)

cleanupExpiredKeysFor now locks s.mu for the entire Get->filter->Put on the
previous-kids store list, not just the in-memory prune, closing the TOCTOU
race with RotateKey/RotateECKey that could drop a freshly rotated kid from
JWKS and break verification of tokens signed with it.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task D2：调度器每轮工作带自身 recover + nil-guard（#14 Low）

**Files:**
- Modify: `backend/internal/service/oidc_key_rotation.go:105-170`（`rotationLoop`）、`:176-218`（`cleanupLoop`）、`:276-302`（`rotationIntervalHours`/`gracePeriodSeconds` 的 nil-guard）

现状：`recover()` 在 goroutine 入口注册一次、在 `for` 外——循环体一次 panic 即 goroutine 永久退出。`rotationIntervalHours`/`gracePeriodSeconds` 调 `s.settingSvc.GetValue` 前无 `s.settingSvc == nil` 检查（`isEnabled`/`cleanupIntervalHours` 有）。

- [ ] **Step 1: 给每轮工作加内层 recover**

在 `oidc_key_rotation.go` 加一个 helper：

```go
// runGuarded executes fn with its own panic recovery so a single iteration's
// panic does not kill the scheduler goroutine. The outer loop's recover
// remains as a last-resort backstop.
func runGuarded(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("oidc key rotation: panic recovered in iteration", "stage", name, "panic", r)
		}
	}()
	fn()
}
```

把 `rotationLoop` 里的 `s.rotateBoth(ctx)`（`:144`）和 `s.cleanupExpired(ctx, "rotation_cycle")`（`:158`）改为：

```go
		runGuarded("rotate", func() { s.rotateBoth(ctx) })
		...
		runGuarded("cleanup_after_rotate", func() { s.cleanupExpired(ctx, "rotation_cycle") })
```

把 `cleanupLoop` 里的 `s.cleanupExpired(ctx, "independent")`（`:208`）改为：

```go
		runGuarded("cleanup", func() { s.cleanupExpired(ctx, "independent") })
```

（保留两个 loop 入口原有的外层 `recover()` 作兜底，不删。）

- [ ] **Step 2: 给 rotationIntervalHours / gracePeriodSeconds 补 nil-guard**

`oidc_key_rotation.go:276` `rotationIntervalHours` 与 `:290` `gracePeriodSeconds` 函数体第一行加（对齐 `cleanupIntervalHours:244` 的写法）：

```go
	if s.settingSvc == nil {
		return DefaultOIDCKeyRotationIntervalHours // gracePeriodSeconds 用 defaultGracePeriodSec
	}
```

（`rotationIntervalHours` 用 `DefaultOIDCKeyRotationIntervalHours`，`gracePeriodSeconds` 用 `defaultGracePeriodSec`。）

- [ ] **Step 3: 测试**

在 `oidc_key_rotation` 测试（grep 现有 `*_rotation_test.go`，无则新建 `oidc_key_rotation_test.go`）加：(a) `runGuarded` 吞掉 panic 不外抛；(b) `settingSvc == nil` 时两函数返回默认值不 panic。
Run: `cd backend && go test ./internal/service/ -run 'Rotation|Guarded' -v && go build ./...`
Expected: PASS。

- [ ] **Step 4: 提交**

```bash
git add backend/internal/service/oidc_key_rotation.go backend/internal/service/oidc_key_rotation_test.go
git commit -m "fix(oidc): per-iteration panic recovery + nil-guard in key scheduler (#14)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task D3：EnsureKey load 时把 current kid 从 previous 去重（#15 Low）

**Files:**
- Modify: `backend/internal/service/oidc_key_service.go`（`EnsureKey` 的 load 段，及/或 `loadPreviousKIDsFor`）
- Test: `backend/internal/service/oidc_key_service_test.go`

现状：`RotateKey`/`RotateECKey` 在 `appendPreviousKID` 成功后若「写 current 指针」失败，旧 kid 同时为 current + previous，重启 load 后 JWKS 出现两次。修复选「load 时去重」（设计已定，不做全事务化）。

- [ ] **Step 1: 写失败测试**

在 `oidc_key_service_test.go` 追加：构造一个 store，其 current kid pointer 指向 X，且 previous-kids 列表也含 X；调用 `EnsureKey` 后 `JWKS()` 中 kid==X 只出现一次。

```go
func TestOIDCKeyService_DedupCurrentFromPrevious(t *testing.T) {
	svc, store := newTestOIDCKeyServiceWithStore(t) // existing helper returning the stub store
	if err := svc.EnsureKey(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	curKID := svc.rsaKID
	// Simulate the dirty state: current kid also present in previous-kids list.
	_ = svc.appendPreviousKID(context.Background(), oidcPreviousKIDsKey, curKID)
	// Reload from store.
	svc2 := newTestOIDCKeyServiceFromStore(t, store)
	if err := svc2.EnsureKey(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	jwks, err := svc2.JWKS(context.Background())
	if err != nil {
		t.Fatalf("jwks: %v", err)
	}
	count := 0
	for _, k := range jwks.Keys {
		if k.Kid == curKID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("current kid %q appeared %d times in JWKS, want 1", curKID, count)
	}
}
```

> 备注：helper 名以文件现有为准；核心断言是「current kid 在 JWKS 只出现一次」。

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/service/ -run TestOIDCKeyService_DedupCurrentFromPrevious -v`
Expected: FAIL（count==2）。

- [ ] **Step 3: 在 load 路径去重**

读 `EnsureKey` 与 `loadPreviousKIDsFor`。在加载完 current kid（`s.rsaKID` / `s.ecKID`）与 previous-kids 之后，从 previous 集合剔除等于 current 的 kid。最小改动：在 `EnsureKey` 末尾（两类 key load 完成后）加：

```go
	// Self-heal: if a prior rotation's pointer flip failed, the current kid may
	// also linger in the previous-kids list, which would surface it twice in
	// JWKS. Drop it from the in-memory previous slices on load.
	s.rsaPrevKIDs = dropKID(s.rsaPrevKIDs, s.rsaKID)
	s.ecPrevKIDs = dropKID(s.ecPrevKIDs, s.ecKID)
```

加 helper：

```go
func dropKID(kids []string, drop string) []string {
	out := kids[:0]
	for _, k := range kids {
		if k != drop {
			out = append(out, k)
		}
	}
	return out
}
```

> 备注：若 `JWKS()` 直接读 store 的 previous 列表而非内存 `s.rsaPrevKIDs`，则去重需作用在 JWKS 组装处——读 `JWKS()`（`:~460-484`）确认其遍历的是内存 slice 还是 store；据实把 `dropKID` 应用在 JWKS 实际遍历的来源上。

- [ ] **Step 4: 运行确认通过**

Run: `cd backend && go test ./internal/service/ -run 'TestOIDCKeyService_Dedup|JWKS' -v && go build ./...`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/service/oidc_key_service.go backend/internal/service/oidc_key_service_test.go
git commit -m "fix(oidc): de-dup current kid from previous-kids on load (#15)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task D4：修正死的非对称 request-object 路径 iss 矛盾 + WithValidMethods（#16 Low）

**Files:**
- Modify: `backend/internal/service/oidc_request_object.go:346-364`（`ParseRequestObjectJWTWithJWKS` 的 ParseWithClaims）
- Modify: `backend/internal/service/oidc_request_object.go:231-234`（`ParseRequestObjectJWT` 的 ParseWithClaims，补 WithValidMethods）
- Test: `backend/internal/service/oidc_request_object_test.go`

现状（`:346-351`）：`jwt.WithIssuer(issuer)` 与 `:362` 要求 `iss==client_id` 互斥 → 拒绝一切合规 token；两处 `ParseWithClaims` 均无 `jwt.WithValidMethods`。该路径将被组 E 的 request_uri 接线，故此处先修。

- [ ] **Step 1: 写失败测试（RS256 合规 token 应通过）**

在 `oidc_request_object_test.go`（无则新建）加：用一对 RSA key 生成 `iss=client_id`、`aud=[issuer]` 的 request object JWT，经 `ParseRequestObjectJWTWithJWKS`（传一个返回该公钥的 keyFunc）应成功返回 `AuthorizeRequest`。

```go
func TestParseRequestObjectJWTWithJWKS_RS256_Valid(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	issuer := "https://sub.sakrylle.com"
	clientID := "client-a"
	claims := &RequestObjectClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    clientID,
			Audience:  jwt.ClaimStrings{issuer},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
		ClientID: clientID, RedirectURI: "https://rp/cb", ResponseType: "code",
		Scope: "openid", State: "s", CodeChallenge: "abc", CodeChallengeMethod: "S256",
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "k1"
	signed, _ := tok.SignedString(priv)

	keyFunc := func(alg, kid string) (any, error) { return &priv.PublicKey, nil }
	req, err := ParseRequestObjectJWTWithJWKS(signed, issuer, clientID, "", keyFunc)
	if err != nil {
		t.Fatalf("expected valid RS256 request object, got: %v", err)
	}
	if req.RedirectURI != "https://rp/cb" {
		t.Fatalf("redirect_uri = %q", req.RedirectURI)
	}
}
```

import 补 `crypto/rsa`、`crypto/rand`、`time`、jwt（按文件现状）。

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/service/ -run TestParseRequestObjectJWTWithJWKS_RS256_Valid -v`
Expected: FAIL（`WithIssuer(issuer)` 要求 iss==issuer，但我们设 iss==client_id → 验签校验拒绝）。

- [ ] **Step 3: 修正 ParseWithClaims**

`oidc_request_object.go:346-351`，删除 `jwt.WithIssuer(issuer)`（iss 校验已由 `:362` 的 `iss==client_id` 显式做），保留 `jwt.WithAudience(issuer)`，并加 `jwt.WithValidMethods`：

```go
	token, err := jwt.ParseWithClaims(rawJWT, &RequestObjectClaims{},
		func(t *jwt.Token) (any, error) { return verifyKey, nil },
		jwt.WithLeeway(30*time.Second),
		jwt.WithAudience(issuer),
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512", "ES256", "ES384", "ES512", "PS256", "PS384", "PS512", "HS256", "HS384", "HS512"}),
	)
```

`ParseRequestObjectJWT`（`:231-234`，对称路径）也补 `jwt.WithValidMethods([]string{"HS256","HS384","HS512"})`（该路径只接受 HS）。

- [ ] **Step 4: 运行确认通过**

Run: `cd backend && go test ./internal/service/ -run 'TestParseRequestObjectJWT' -v && go build ./...`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/service/oidc_request_object.go backend/internal/service/oidc_request_object_test.go
git commit -m "fix(oidc): correct iss check + pin alg in asymmetric request object path (#16)

Drop the contradictory WithIssuer(issuer) (iss must equal client_id per
OIDC Core 6.1, already enforced explicitly) and add WithValidMethods to both
request-object parse paths.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## 组 E · Pairwise + Claims / Discovery

> 依赖：组 A（安全 HTTP 客户端）已合并——E3 的 request_uri 拉取走该客户端。

### Task E1：pairwise sub fail-closed（#6 Medium）

**Files:**
- Modify: `backend/internal/service/oidc_pairwise.go:100-125`（`ResolvePairwiseSub` 改为可返回 error）
- Modify: `backend/internal/service/oauth_provider_service.go:360-363`（`maybeSignIDToken` 调用点）
- Modify: `backend/internal/service/oidc_userinfo_jwt.go:62-69`（`UserInfoSubForOAuthToken` 调用点）
- Test: `backend/internal/service/oidc_pairwise_test.go`

现状：`ResolvePairwiseSub` 在 `FetchSectorIdentifierURI` 出错时回退到哈希原始 URI 字符串（`:114`），与成功路径基准不同 → sub 漂移。改为：拉取/校验失败直接返回 error，由 authorize/签发流程拒绝。

- [ ] **Step 1: 写失败测试**

在 `oidc_pairwise_test.go`（无则新建）加：当 `sectorIdentifierURI` 指向一个会拉取失败的地址时，`ResolvePairwiseSub` 返回非 nil error（而非回退算出 sub）。

```go
func TestResolvePairwiseSub_FailClosedOnFetchError(t *testing.T) {
	uri := "https://nonexistent.invalid/sector.json"
	_, err := ResolvePairwiseSub("https://sub.sakrylle.com", 42, "pairwise", &uri,
		[]string{"https://rp.example.com/cb"})
	if err == nil {
		t.Fatal("expected error when sector_identifier_uri fetch fails (fail-closed)")
	}
}

func TestResolvePairwiseSub_NoSectorURI_UsesRedirectHosts(t *testing.T) {
	sub, err := ResolvePairwiseSub("https://sub.sakrylle.com", 42, "pairwise", nil,
		[]string{"https://rp.example.com/cb"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sub == "" {
		t.Fatal("expected a deterministic pairwise sub from redirect hosts")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/service/ -run TestResolvePairwiseSub -v`
Expected: 编译失败（`ResolvePairwiseSub` 现签名无 error 返回）。

- [ ] **Step 3: 改 ResolvePairwiseSub 返回 error**

把 `oidc_pairwise.go:100` 的 `ResolvePairwiseSub` 改为返回 `(string, error)`，删除 raw-URI 回退分支：

```go
func ResolvePairwiseSub(issuer string, userID int64, subjectType string, sectorIdentifierURI *string, redirectURIs []string) (string, error) {
	if subjectType != "pairwise" {
		return "", nil
	}
	var sectorID string
	if sectorIdentifierURI != nil && *sectorIdentifierURI != "" {
		fetchedURIs, err := FetchSectorIdentifierURI(*sectorIdentifierURI, redirectURIs)
		if err != nil {
			// Fail closed: do NOT fall back to hashing the raw URI string, which
			// would produce a sub on a different basis than the success path and
			// silently rotate the user's pairwise identifier during an outage.
			return "", fmt.Errorf("pairwise sub: sector_identifier_uri unresolved: %w", err)
		}
		sectorID = SectorIdentifierFromRedirectURIs(fetchedURIs)
	} else {
		sectorID = SectorIdentifierFromRedirectURIs(redirectURIs)
	}
	return ComputePairwiseSub(issuer, userID, sectorID), nil
}
```

- [ ] **Step 4: 更新两个调用点**

`oauth_provider_service.go:360-363`（`maybeSignIDToken`）改为：

```go
	var pairwiseSub string
	if client != nil && client.SubjectType == "pairwise" {
		ps, err := ResolvePairwiseSub(issuer, userID, client.SubjectType, client.SectorIdentifierURI, client.RedirectURIs)
		if err != nil {
			return "", fmt.Errorf("oidc: resolve pairwise sub: %w", err)
		}
		pairwiseSub = ps
	}
```

`oidc_userinfo_jwt.go:62-69`（`UserInfoSubForOAuthToken`）——该函数当前不返回 error。改为返回 `(string, error)`：

```go
func UserInfoSubForOAuthToken(userID int64, client *OAuthClient, issuer string) (string, error) {
	if client != nil && client.SubjectType == "pairwise" {
		pw, err := ResolvePairwiseSub(issuer, userID, client.SubjectType, client.SectorIdentifierURI, client.RedirectURIs)
		if err != nil {
			return "", err
		}
		if pw != "" {
			return pw, nil
		}
	}
	return strconv.FormatInt(userID, 10), nil
}
```

grep `UserInfoSubForOAuthToken` 找其调用点（handler 中 UserInfo / signed UserInfo 路径），更新为处理 error（失败时返回 500/`server_error`，不降级出错误 sub）。

- [ ] **Step 5: 运行确认通过 + 构建**

Run: `cd backend && go test ./internal/service/ -run 'TestResolvePairwiseSub|UserInfoSub' -v && go build ./...`
Expected: PASS。修复所有因签名变更而编译失败的调用点。

- [ ] **Step 6: 提交**

```bash
git add backend/internal/service/oidc_pairwise.go backend/internal/service/oauth_provider_service.go backend/internal/service/oidc_userinfo_jwt.go backend/internal/handler/oauth_provider_handler.go backend/internal/service/oidc_pairwise_test.go
git commit -m "fix(oidc): fail closed on sector_identifier_uri fetch failure (#6)

ResolvePairwiseSub no longer falls back to hashing the raw URI string on
fetch/validation failure; it returns an error so authorization is rejected
rather than silently rotating the user's pairwise sub.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task E2：接线 ApplyClaimsConstraints（#11 Low）

**Files:**
- Modify: `backend/internal/handler/oauth_provider_handler.go`（UserInfo 端点 + signed UserInfo JWT 构造处，`:1093` 附近）
- Modify: `backend/internal/service/oidc_claims_enforcement.go:7-19`（订正 doc 注释）

> 范围说明：claims 参数已被解析并持久化到 `oauth_authorize_transactions.claims`（migration 154）→ `AuthorizeRequest.Claims`。id_token 侧透传需穿过 `OAuthCode`（当前无 Claims 字段）→ 涉及 DB 列，成本高。本任务在 **UserInfo 端点**接线 `ApplyClaimsConstraints`（handler 层可直接从 token 的 OAuth meta / 已解析请求拿到 claims 约束，无需 migration），即可让 `claims_parameter_supported=true` 真实成立于 UserInfo 响应。**关键不变量：`ApplyClaimsConstraints` 必须在 claims 已构建（且 id_token 路径的 `assertNoForbiddenClaims` 已跑过）之后调用——它只删减 claim，绝不新增，故不可能注入 forbidden claim。**

- [ ] **Step 1: 写测试**

在 `oidc_claims_enforcement_test.go`（已存在）补一条「essential + value 约束在 UserInfo 输出生效」的断言（复用文件现有 `ApplyClaimsConstraints` 单测风格），并加一条「claims 不能新增 forbidden claim」：

```go
func TestApplyClaimsConstraints_CannotInjectForbiddenClaim(t *testing.T) {
	// A claims request naming "balance" must not cause balance to appear:
	// ApplyClaimsConstraints only filters existing claims, never adds.
	claims := jwt.MapClaims{"sub": "42", "email": "a@b.com"}
	req := &ClaimsRequest{UserInfo: map[string]ClaimRequestDetail{
		"balance": {Essential: boolPtr(true)},
	}}
	out := ApplyClaimsConstraints(claims, req, "userinfo")
	if _, exists := out["balance"]; exists {
		t.Fatal("ApplyClaimsConstraints must never add a claim not already present")
	}
}
```

（`boolPtr` helper：若文件无则加 `func boolPtr(b bool) *bool { return &b }`。）

- [ ] **Step 2: 运行确认（应已通过——验证不变量）**

Run: `cd backend && go test ./internal/service/ -run TestApplyClaimsConstraints -v`
Expected: PASS（`ApplyClaimsConstraints` 本就只删不增；此测试锁死该不变量）。

- [ ] **Step 3: 在 UserInfo 端点调用 ApplyClaimsConstraints**

读 `oauth_provider_handler.go` 的 UserInfo handler（`/userinfo` + `/v1/me` OIDC 分支，`:1034`–`:1110` 区）。在已组装好 UserInfo claims map（纯 JSON 与签名 JWT 两路）之后、返回之前，若该 token 的 authorize 请求带了 claims（从 OAuth meta 读取已存的 claims 约束；若 meta 未携带 claims 约束则跳过），调用：

```go
	if claimsReq := service.ClaimsRequestFromMap(meta.Claims); claimsReq != nil {
		userInfoClaims = service.ApplyClaimsConstraints(userInfoClaims, claimsReq, "userinfo")
	}
```

> 备注：grep OAuth access metadata 结构（`LoadOAuthAccessMetadata` 返回类型）确认是否已带 `Claims map[string]any`。若没有，则 #11 的 UserInfo 接线同样缺源——此时退为「仅订正 doc 注释 + 在 authorize 的 request-object 合并路径（`:161` `req.Claims = reqObj.Claims` 已存在）确保 claims 流向 transaction 持久化」，并在 commit message 注明 id_token/UserInfo 强制过滤待 meta 透传 claims 后启用。**不要为此引入新 migration**（YAGNI；超出本轮范围）。

- [ ] **Step 4: 订正 doc 注释**

`oidc_claims_enforcement.go:7-9` 把「best-effort mode」描述保留，但删除任何「not supported / 未接线」措辞；改为说明已在 UserInfo 响应生效、且永不新增 claim。同时改 `oauth_provider_handler.go:855` 的 `claims_parameter_supported (not supported)` 注释为「supported (UserInfo filtering)」。

- [ ] **Step 5: 构建 + 测试**

Run: `cd backend && go test ./internal/service/ ./internal/handler/ -run 'Claims|UserInfo' -v && go build ./...`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add backend/internal/service/oidc_claims_enforcement.go backend/internal/service/oidc_claims_enforcement_test.go backend/internal/handler/oauth_provider_handler.go
git commit -m "feat(oidc): enforce claims parameter constraints on UserInfo (#11)

Wire ApplyClaimsConstraints into the UserInfo response so claims_parameter_supported
is backed by real filtering. Constraints only narrow claims, never add, so the
id_token forbidden-claim invariant is preserved.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task E3：request_uri 真实拉取并验签（#12 Low）

**Files:**
- Modify: `backend/internal/handler/oauth_provider_handler.go:169-175`（request_uri 分支：从 no-op 改为真实拉取）
- Modify: `backend/internal/handler/oauth_provider_handler.go:854-855,870`（订正注释；flag 保持 true）
- Test: `backend/internal/handler/oauth_provider_handler_test.go` 或新建 `oauth_provider_oidc_request_uri_test.go`

现状（`:169-175`）：request_uri 只 `slog.Info` 后 no-op。改为：调 `service.FetchRequestURI`（走组 A 安全客户端 + host 允许列表 + 64KB），取回 JWT 后复用 D4 修正后的 `ParseRequestObjectJWTWithJWKS` 验签，合并进 `req`（与上方 `request` 参数分支同样的 merge 逻辑）。

- [ ] **Step 1: 写测试**

新建 `backend/internal/handler/oauth_provider_oidc_request_uri_test.go`：(a) request_uri 指向一个返回合法 request object JWT 的 httptest 服务器（其 host 在 client 注册的 `RequestURIs` 白名单内）→ authorize 合并成功；(b) request_uri 指向内网/未注册 host → 被拒。

```go
func TestAuthorize_RequestURI_FetchedAndMerged(t *testing.T) {
	// Serve a signed request object JWT; register its host in the client's RequestURIs.
	// Assert the authorize request adopts redirect_uri/scope from the object.
	// (Construct via the handler test harness used by existing oauth_provider_oidc_test.go.)
}

func TestAuthorize_RequestURI_RejectsUnregisteredHost(t *testing.T) {
	// request_uri whose host is NOT in client's RequestURIs -> error returned,
	// no merge.
}
```

> 备注：用 `oauth_provider_oidc_test.go` 现有的 handler 测试构造方式（grep 该文件取 harness）。两条核心断言：合法远程对象被采纳；未注册 host / 内网被拒（后者由组 A 的 safeDialContext 在拨号层兜底，host 白名单在拉取前先拦）。

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/handler/ -run TestAuthorize_RequestURI -v`
Expected: FAIL（当前 no-op，不合并、不拒绝）。

- [ ] **Step 3: 实现 request_uri 拉取 + 验签 + merge**

把 `:169-175` 的 request_uri no-op 块替换为：

```go
	if requestURIParam := formVal("request_uri"); requestURIParam != "" {
		issuer := h.discoveryIssuer(c)
		// Look up the client to get its registered request_uris whitelist.
		reqURIClient, lookupErr := h.provider.LookupClient(c.Request.Context(), req.ClientID)
		if lookupErr != nil {
			redirectOAuthProviderError(c, req.RedirectURI, req.State, lookupErr)
			return
		}
		rawObj, fetchErr := service.FetchRequestURI(requestURIParam, reqURIClient.RequestURIs)
		if fetchErr != nil {
			slog.Warn("oidc: request_uri fetch failed", "error", fetchErr)
			redirectOAuthProviderError(c, req.RedirectURI, req.State, fetchErr)
			return
		}
		// Verify the fetched request object using the client's JWKS (asymmetric)
		// or client_secret (symmetric), reusing the request-object parser.
		var keyFunc service.JWKSKeyFunc
		if reqURIClient.JWKS != nil { // adapt to actual client JWKS accessor
			keyFunc = service.JWKSKeyFuncFromSet(*reqURIClient.JWKS)
		}
		reqObj, parseErr := service.ParseRequestObjectJWTWithJWKS(rawObj, issuer, req.ClientID, "", keyFunc)
		if parseErr != nil {
			slog.Warn("oidc: request_uri object validation failed", "error", parseErr)
			redirectOAuthProviderError(c, req.RedirectURI, req.State, parseErr)
			return
		}
		req.RedirectURI = reqObj.RedirectURI
		req.ResponseType = reqObj.ResponseType
		req.Scopes = reqObj.Scopes
		req.CodeChallenge = reqObj.CodeChallenge
		req.CodeChallengeMethod = reqObj.CodeChallengeMethod
		if reqObj.Claims != nil {
			req.Claims = reqObj.Claims
		}
		if reqObj.Nonce != "" && req.Nonce == "" {
			req.Nonce = reqObj.Nonce
		}
	}
```

> 备注：`reqURIClient.RequestURIs` 与 `reqURIClient.JWKS` 的真实字段名以 `OAuthClient` struct 为准（grep `RequestURIs`/`JWKS`/`Jwks` in `oauth_provider_types.go`）。`OAuthClient` 来自 migration 155 的 `request_uris` 列。若 client 无 JWKS（公共 client 常无），`ParseRequestObjectJWTWithJWKS` 在 keyFunc==nil 且 alg 为非对称时会按其内部逻辑返回错误——这与「公共 client 用 request_uri 须自带可验签 JWKS」一致；若需支持 client_secret 对称签名，把第 4 个参数传 client secret（公共 client 为空）。

- [ ] **Step 4: 订正 discovery 注释（flag 保持 true）**

`oauth_provider_handler.go:854-855` 注释里 `request_uri_parameter_supported ... (not supported)` 改为 `(supported)`；`:870` 的 `resp["request_uri_parameter_supported"] = true` 不变（现已真实成立）。

- [ ] **Step 5: 运行确认通过 + 构建**

Run: `cd backend && go test ./internal/handler/ -run TestAuthorize_RequestURI -v && go build ./...`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add backend/internal/handler/oauth_provider_handler.go backend/internal/handler/oauth_provider_oidc_request_uri_test.go
git commit -m "feat(oidc): fetch and verify request_uri objects (#12)

request_uri is now fetched via the SSRF-safe client (group A), verified
against the client's JWKS, and merged into the authorize request — backing
the request_uri_parameter_supported=true discovery flag.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## 组 F · 品牌 + 文档订正

### Task F1：AmountInput 货币符号 $ → ￥（#17 Low）

**Files:**
- Modify: `frontend/src/components/payment/AmountInput.vue:32-34`

- [ ] **Step 1: 改符号**

`AmountInput.vue` 第 32-34 行的 `$` 字面量改为 `￥`（全角，与全站 display-only 货币约定一致）：

```html
        <span class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 dark:text-dark-500">
          ￥
        </span>
```

（仅改显示字符；`pl-8` 等布局类不变，解析逻辑与符号无关。）

- [ ] **Step 2: 前端构建/测试**

Run: `cd frontend && pnpm build`（或仓库现有前端校验命令，如 `pnpm test` / `pnpm lint`）
Expected: 构建通过。

- [ ] **Step 3: 提交**

```bash
git add frontend/src/components/payment/AmountInput.vue
git commit -m "fix(brand): use ￥ instead of \$ in amount input (#17)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task F2：PaymentView 余额补 ￥ 前缀（#18 Low）

**Files:**
- Modify: `frontend/src/views/user/PaymentView.vue:38`

- [ ] **Step 1: 加前缀**

`PaymentView.vue:38` 现为：
`{{ t('payment.currentBalance') }}: {{ user?.balance?.toFixed(2) || '0.00' }}`
改为：
`{{ t('payment.currentBalance') }}: ￥{{ user?.balance?.toFixed(2) || '0.00' }}`

- [ ] **Step 2: 构建**

Run: `cd frontend && pnpm build`
Expected: 通过。

- [ ] **Step 3: 提交**

```bash
git add frontend/src/views/user/PaymentView.vue
git commit -m "fix(brand): prefix PaymentView balance with ￥ (#18)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task F3：consent 页 approve 按钮对比度（#19 Low，仅 consent 页）

**Files:**
- Modify: `backend/internal/handler/oauth_provider_consent.go:90`（`.approve` 背景色）

> 仅改 consent 页；前端 `.btn-primary` 保持现状（已知设计取舍，非回归）。

- [ ] **Step 1: 改 approve 背景为 primary-700**

`oauth_provider_consent.go:90` 现为 `.approve { background:var(--primary); color:#fff; }`（`--primary` = `#9181bd`，白字 ≈3.45:1，低于 AA 4.5）。改为用 primary-700（`#6b5b95`，白字 ≈6.2:1 通过 AA）：

```
  .approve { background:#6b5b95; color:#fff; }
  .approve:hover:not(:disabled) { background:#584b7a; }
```

（hover 顺势下沉到 primary-800 `#584b7a`；原 `.approve:hover` 用的 `--primary-dim` 改由此行覆盖。）

- [ ] **Step 2: 构建**

Run: `cd backend && go build ./...`
Expected: 通过（纯字符串改动）。

- [ ] **Step 3: 提交**

```bash
git add backend/internal/handler/oauth_provider_consent.go
git commit -m "fix(brand): raise consent approve button contrast to AA (#19)

Switch the consent approve button to primary-700 (#6b5b95, ~6.2:1 white)
to meet WCAG AA 4.5:1 for body-size text. Frontend .btn-primary unchanged.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task F4：consent 页用 DOM API 替换 innerHTML 拼接（#21 Low）

**Files:**
- Modify: `backend/internal/handler/oauth_provider_consent.go:225-261`（两处 group 渲染 innerHTML）

现状：image groups（`:225-236`）与 responses groups（`:248-261`）两处用 `item.innerHTML = '...' + name + '...'` 拼接，`name`/`id`/`rate_multiplier` 未转义。虽有 CSP nonce + 无 unsafe-inline 兜底，纵深防御仍补：改用 `createElement` + `textContent` 建节点。

- [ ] **Step 1: 把两处 innerHTML 拼接替换为 DOM 构造**

在 consent 页内联 JS 里加一个 helper（放在两处渲染前的合适作用域），再替换两处：

```javascript
      function buildGroupItem(id, name, badge) {
        var item = document.createElement("label");
        item.className = "group-item";

        var cb = document.createElement("input");
        cb.type = "checkbox";
        cb.name = "group_ids";
        cb.value = String(id);
        cb.checked = true;

        var nameSpan = document.createElement("span");
        nameSpan.className = "g-name";
        nameSpan.textContent = name;

        item.appendChild(cb);
        item.appendChild(nameSpan);

        if (badge) {
          var badgeSpan = document.createElement("span");
          badgeSpan.className = "g-badge";
          badgeSpan.textContent = badge;
          item.appendChild(badgeSpan);
        }
        return item;
      }
```

image groups 循环体（`:230-235`）替换为：

```javascript
          var id = g.id || g;
          var name = g.name || String(id);
          $imageList.appendChild(buildGroupItem(id, name, "Image"));
```

responses groups 循环体（`:249-258`）替换为（含数值校验）：

```javascript
          var id = g.id || g;
          var name = g.name || String(id);
          var mult = Number(g.rate_multiplier);
          var badge = (isFinite(mult) && mult > 0) ? mult + "x" : "";
          $responsesList.appendChild(buildGroupItem(id, name, badge));
```

- [ ] **Step 2: 构建（确认内联 JS 字符串无语法破坏）**

Run: `cd backend && go build ./...`
Expected: 通过。若有 consent 页 handler 测试（grep `oauth_provider_consent` 测试），跑之确认 HTML 仍渲染。

- [ ] **Step 3: 提交**

```bash
git add backend/internal/handler/oauth_provider_consent.go
git commit -m "fix(oidc): build consent group items via DOM API, not innerHTML (#21)

Replace innerHTML string concatenation of admin-configured group names with
createElement + textContent, and coerce rate_multiplier to a number. Defense
in depth on top of the existing nonce CSP.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task F5：consent --primary-dim 对齐 primary-600（Info）

**Files:**
- Modify: `backend/internal/handler/oauth_provider_consent.go:71`

- [ ] **Step 1: 改色值**

`:71` 的 `--primary-dim:#7c6ba8` 改为 `--primary-dim:#7b6aab`（对齐设计系统 primary-600）。

> 注：若 F3 已把 `.approve:hover` 直接写死为 `#584b7a`、不再引用 `--primary-dim`，则 `--primary-dim` 仅余其他引用处使用；此改动仍使该 token 与 primary-600 一致。

- [ ] **Step 2: 构建 + 提交**

```bash
cd backend && go build ./...
git add backend/internal/handler/oauth_provider_consent.go
git commit -m "style(brand): align consent --primary-dim with primary-600 (#info)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task F6：assertNoForbiddenClaims 反转为 allowlist（Info）

**Files:**
- Modify: `backend/internal/service/oidc_id_token.go:29-46,178-186`
- Test: `backend/internal/service/oidc_id_token_test.go`

把 denylist（`forbiddenIDTokenClaims`，拒 12 个已知商业名）反转为真正的 allowlist：只放行 §8 的 14 个标准 claim，其余一律拒——fail-closed 更彻底，能挡未来新增的敏感 claim（phone_number/address/role 等）。

- [ ] **Step 1: 写失败测试**

在 `oidc_id_token_test.go` 追加：构造一个含未知 claim（如 `phone_number`）的 map，`assertNoForbiddenClaims` 应拒。

```go
func TestAssertNoForbiddenClaims_AllowlistRejectsUnknown(t *testing.T) {
	claims := map[string]any{
		"iss": "x", "sub": "1", "aud": []string{"c"}, "iat": 1, "exp": 2,
		"phone_number": "+100000000", // not in the allowlist
	}
	if err := assertNoForbiddenClaims(claims); err == nil {
		t.Fatal("expected allowlist to reject unknown claim phone_number")
	}
}

func TestAssertNoForbiddenClaims_AllowsStandardSet(t *testing.T) {
	claims := map[string]any{
		"iss": "x", "sub": "1", "aud": []string{"c"}, "iat": 1, "exp": 2,
		"nonce": "n", "auth_time": 3, "sid": "s",
		"name": "a", "preferred_username": "a", "email": "a@b", "email_verified": true,
		"at_hash": "h", "c_hash": "h2",
	}
	if err := assertNoForbiddenClaims(claims); err != nil {
		t.Fatalf("standard claim set must pass, got: %v", err)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd backend && go test ./internal/service/ -run TestAssertNoForbiddenClaims -v`
Expected: `AllowlistRejectsUnknown` FAIL（当前 denylist 放行未知 claim）。

- [ ] **Step 3: 反转为 allowlist**

把 `oidc_id_token.go:33-46` 的 `forbiddenIDTokenClaims` 替换为 allowlist：

```go
// allowedIDTokenClaims is the exhaustive set of claim names permitted in an
// id_token (OIDC Core §8 claim set for this OP). assertNoForbiddenClaims fails
// closed on ANY claim outside this set, so a future regression that adds a
// sensitive claim (phone_number, address, role, balance, group, ...) is caught.
var allowedIDTokenClaims = map[string]struct{}{
	"iss": {}, "sub": {}, "aud": {}, "exp": {}, "iat": {},
	"nonce": {}, "auth_time": {}, "sid": {},
	"name": {}, "preferred_username": {}, "email": {}, "email_verified": {},
	"at_hash": {}, "c_hash": {},
}
```

把 `:178-186` 的 `assertNoForbiddenClaims` 改为：

```go
// assertNoForbiddenClaims fails closed if any claim outside the standard OIDC
// allowlist is present (defense in depth against a builder regression leaking
// business/PII state into the id_token).
func assertNoForbiddenClaims(claims map[string]any) error {
	for k := range claims {
		if _, ok := allowedIDTokenClaims[k]; !ok {
			return fmt.Errorf("oidc: claim %q is not in the id_token allowlist", k)
		}
	}
	return nil
}
```

- [ ] **Step 4: 运行确认通过 + 全量回归**

Run: `cd backend && go test ./internal/service/ -run 'AssertNoForbiddenClaims|BuildIDTokenClaims|TestOIDC' -v && go build ./...`
Expected: PASS。`BuildIDTokenClaims` 只产出 allowlist 内 claim，故现有 id_token 测试不回归。

- [ ] **Step 5: 提交**

```bash
git add backend/internal/service/oidc_id_token.go backend/internal/service/oidc_id_token_test.go
git commit -m "fix(oidc): invert id_token claim guard to a fail-closed allowlist (#info)

assertNoForbiddenClaims now rejects any claim outside the standard OIDC set
instead of only 12 known business names, catching future regressions that
add sensitive claims.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task F7：文档订正 — KEK 复用 TOTP 密钥（#4）+ 货币/sakura Info

**Files:**
- Modify: OIDC 部署文档（grep 定位：`grep -rl "OIDC_KEY_ENCRYPTION_KEY" --include="*.md" .`，设计文档指向 `OIDC-DEPLOYMENT.md:51-52`）
- Modify: `sakrylle-docs/30-program-management/risk-register.md:111`（R-PROD-01 / R-KEY-03 中的 KEK 描述）
- Modify: `sakrylle-docs/40-brand-system/design.md`（Currency policy 补 ￥/¥ 约定；sakura 现状）

> 纯文档，无生产写操作。#4 决策为「只订正文档，承认复用 TOTP 密钥」。

- [ ] **Step 1: 定位所有 OIDC_KEY_ENCRYPTION_KEY 引用**

Run: `grep -rn "OIDC_KEY_ENCRYPTION_KEY" --include="*.md" . `
列出全部命中文件行。

- [ ] **Step 2: 订正 KEK 描述**

把每处「独立 `OIDC_KEY_ENCRYPTION_KEY`、独立轮换」改为如实描述：

> OIDC 签名私钥在 `security_secrets` 表中由 **共享的 TOTP 加密密钥**（`cfg.Totp.EncryptionKey`，env `TOTP_ENCRYPTION_KEY`）经 AES-256-GCM 包裹——**不存在**独立的 `OIDC_KEY_ENCRYPTION_KEY`。该 KEK 同时保护 TOTP、channel-monitor、backup secrets。
>
> ⚠️ **运维警告**：`TOTP_ENCRYPTION_KEY` 未设时进程启动会自动生成（`config.go:1485-1494`）。**轮换或重置该密钥会使已存的 OIDC 签名私钥（以及 monitor/backup secrets）无法解密**——必须同步重新初始化 OIDC 签名密钥。切勿当作「独立、永不轮换」的密钥对待。

- [ ] **Step 3: 补 Currency / sakura Info**

`design.md` Currency policy 段补：

> 货币符号约定：全角 `￥` 用于 display-only 的内部余额展示；半角 `¥` 用于真实 CNY 支付网关金额（如订单表）。二者是有意区分，非品牌漂移。

并在 sakura 段落确认「sakura 色当前未在 `tailwind.config.js` 注册为 token，仅 consent logo 经内联 PNG 携带渐变」这一现状描述准确（与代码一致）。

- [ ] **Step 4: 提交**

```bash
git add -f docs sakrylle-docs 2>/dev/null; git add sakrylle-docs
git commit -m "docs(oidc): correct KEK reuse, document currency/sakura conventions (#4,#info)

OIDC signing keys are wrapped by the shared TOTP encryption key, not a
dedicated OIDC_KEY_ENCRYPTION_KEY; add an ops warning that rotating the TOTP
key breaks stored OIDC/monitor/backup secrets. Document the ￥/¥ split and
sakura-not-registered status.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task F8：删除未跟踪的 .bak 文件（housekeeping）

**Files:**
- Delete: `backend/internal/service/oidc_key_service.go.bak`
- Delete: `backend/internal/service/oidc_token_wiring_test.go.bak`

- [ ] **Step 1: 确认未跟踪并删除**

```bash
cd "/Users/cervine/Documents/Sakrylle/Sakrylle API"
git status --porcelain backend/internal/service/*.bak   # 确认是 untracked (??) 或不在版本控制
rm -f backend/internal/service/oidc_key_service.go.bak backend/internal/service/oidc_token_wiring_test.go.bak
```

- [ ] **Step 2: 构建确认无引用**

Run: `cd backend && go build ./... && go test ./internal/service/ -count=1`
Expected: 通过（.bak 不参与编译，删除无影响）。

> 无需 commit（未跟踪文件）；若 `.bak` 竟被跟踪，则 `git rm` 后单独提交 `chore: remove stale .bak files`。

---

## 收尾验收

### Task Z1：全量构建 + 测试 + race

- [ ] **Step 1: 后端全量**

Run: `cd backend && go build ./... && go test ./internal/service/... ./internal/handler/... -count=1`
Expected: 全绿。

- [ ] **Step 2: 关键并发项带 race**

Run: `cd backend && go test ./internal/service/ -run 'OIDCKey|Rotate|Cleanup' -race -count=5`
Expected: 无 DATA RACE。

- [ ] **Step 3: 前端构建**

Run: `cd frontend && pnpm build`
Expected: 通过。

- [ ] **Step 4: 确认 finding 全覆盖**

对照 `docs/superpowers/specs/2026-06-08-oidc-brand-fixes-design.md` §3 分组表，逐条核对每个 finding 编号都有对应 commit。

---

## 自审记录（writing-plans self-review）

- **Spec 覆盖**：A(#1/#2/#12-超时) / B(#3/#7/#10/#20) / C(#8/#9/#13) / D(#5/#14/#15/#16) / E(#6/#11/#12) / F(#17/#18/#19/#21/#4/Info denylist→allowlist/Info currency/Info primary-dim) + housekeeping(.bak)。全部 finding 均有对应 Task。
- **占位符**：无 TBD/TODO；每个改码步骤含真实代码块与 path:line。少数「实施备注」明确要求用 grep 核实真实字段名（`emailVerifiedForJWT` 来源、`OAuthClient.RequestURIs`/`JWKS` 字段名、UserInfo meta 是否带 Claims、handler 单 client 查询方法名）——这些是「执行期按现有代码核对的点」，非占位符，且每处都给了退路方案。
- **类型一致性**：`ResolvePairwiseSub` 改签名后两个调用点（`maybeSignIDToken`、`UserInfoSubForOAuthToken`）均已在 E1 同步更新；`verifyLogoutIDToken` 三返回值变四返回值后所有 `return` 与调用点（C1）同步；`BuildUserInfoJWTClaims` 加参数后调用点（B2）同步。
- **已知执行期风险**：#11（E2）的 id_token 侧透传依赖 `OAuthCode`/meta 是否带 claims，本计划限定为 UserInfo 侧接线、明确不引入 migration；#12（E3）依赖 `OAuthClient` 的 `RequestURIs`/JWKS 字段实际命名，已给 grep 指引与退路。
