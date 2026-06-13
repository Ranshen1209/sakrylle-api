package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"html"
	"net/http"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// OAuthDeviceHandler implements RFC 8628 Device Authorization Grant.
//
// Endpoint layout (see docs/OAUTH_V2_DESIGN.md §12.6 / §12.7 / §12.8):
//
//	POST /oauth/device/code           — public, form-encoded; mints a device+user code pair
//	GET  /oauth/device                — public; renders the verification page
//	POST /api/v1/oauth/device/approve — JWT-protected; consents the typed user_code
//	POST /api/v1/oauth/device/deny    — JWT-protected; rejects the typed user_code
//	POST /oauth/token (grant=device_code) — public, form-encoded; CLI polling endpoint
//
// The token-endpoint device-code branch lives on OAuthProviderHandler so the
// existing /oauth/token route + grant-type dispatcher stay in one place.
type OAuthDeviceHandler struct {
	provider *service.OAuthProviderService
}

// NewOAuthDeviceHandler is the wire constructor.
func NewOAuthDeviceHandler(provider *service.OAuthProviderService) *OAuthDeviceHandler {
	return &OAuthDeviceHandler{provider: provider}
}

// DeviceAuthorize implements POST /oauth/device/code (§12.7).
//
// Form body:
//
//	client_id    required
//	scope        optional (defaults to client.default_scopes)
//	group_id     optional (client-level precheck only)
//	device_id    optional install identifier
//	device_name  optional human label
//
// Response body (always Content-Type: application/json, Cache-Control: no-store):
//
//	{
//	  "device_code": "...",
//	  "user_code":   "SKRY-XXXX-XXXXX",
//	  "verification_uri":           "https://sub.sakrylle.com/oauth/device",
//	  "verification_uri_complete":  "https://sub.sakrylle.com/oauth/device?user_code=...",
//	  "expires_in": 600,
//	  "interval":   5
//	}
func (h *OAuthDeviceHandler) DeviceAuthorize(c *gin.Context) {
	if !h.provider.IsEnabled(c.Request.Context()) {
		writeOAuthError(c, http.StatusForbidden, "temporarily_unavailable", "oauth provider disabled")
		return
	}
	if !requireFormContentType(c) {
		return
	}
	if err := c.Request.ParseForm(); err != nil {
		writeOAuthError(c, http.StatusBadRequest, "invalid_request", "form parse failed")
		return
	}

	req := &service.DeviceCodeRequest{
		ClientID:         strings.TrimSpace(c.Request.PostFormValue("client_id")),
		Scopes:           service.ParseScopes(c.Request.PostFormValue("scope")),
		RequestedGroupID: parseOptionalInt64Form(c, "group_id"),
		DeviceID:         optionalStringForm(c, "device_id"),
		DeviceName:       optionalStringForm(c, "device_name"),
		CreatedIP:        optionalStringPtr(c.ClientIP()),
		CreatedUserAgent: optionalStringPtr(c.Request.UserAgent()),
	}
	issued, err := h.provider.CreateDeviceCode(c.Request.Context(), req)
	if err != nil {
		writeOAuthError(c, httpStatusForOAuthError(err), oauthErrorReason(err), infraerrors.Message(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.JSON(http.StatusOK, gin.H{
		"device_code":               issued.DeviceCode,
		"user_code":                 issued.UserCode,
		"verification_uri":          issued.VerificationURI,
		"verification_uri_complete": issued.VerificationURIComplete,
		"expires_in":                issued.ExpiresIn,
		"interval":                  issued.Interval,
	})
}

// DeviceVerificationPage implements GET /oauth/device (§12.8).
//
// Renders an HTML page where the user types or confirms the user_code. The
// page reads the user's JWT from localStorage and POSTs to
// /api/v1/oauth/device/approve | /deny, mirroring the consent page pattern.
//
// Required headers per §12.8:
//
//	Referrer-Policy: no-referrer
//	Cache-Control:   no-store, must-revalidate
//	Content-Security-Policy: frame-ancestors 'none'
func (h *OAuthDeviceHandler) DeviceVerificationPage(c *gin.Context) {
	if !h.provider.IsEnabled(c.Request.Context()) {
		renderOAuthInlineError(c, http.StatusForbidden, "oauth provider is currently disabled")
		return
	}

	prefilled := strings.TrimSpace(c.Query("user_code"))
	csrfToken, err := generateDeviceCSRFToken()
	if err != nil {
		renderOAuthInlineError(c, http.StatusInternalServerError, "csrf token generation failed")
		return
	}
	csrfHash := hashCSRFToken(csrfToken)

	// Bind the CSRF cookie host-only so cross-host scripts cannot read or
	// overwrite it. Path is /oauth/device so it doesn't leak to the
	// frontend SPA cookie jar. Short TTL (10 min) matches the device-code
	// expiry; we re-issue per page load anyway.
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(
		"sakrylle_oauth_device_csrf",
		csrfHash,
		600,        // maxAge seconds
		"/oauth/",  // path
		"",         // domain (host-only)
		isHTTPS(c), // secure
		true,       // httpOnly
	)

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-store, must-revalidate")
	c.Header("Pragma", "no-cache")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Content-Security-Policy", "frame-ancestors 'none'")
	c.Header("X-Frame-Options", "DENY")
	c.String(http.StatusOK, deviceVerificationHTML(prefilled, csrfToken, middleware.GetNonceFromContext(c)))
}

// DeviceApprove implements POST /api/v1/oauth/device/approve (§12.8).
//
// Body:
//
//	{
//	  "user_code":    "SKRY-XXXX-XXXXX",
//	  "group_id":     7,                  // optional, overrides device-code requested group
//	  "csrf_token":   "<from cookie>"     // matched against sakrylle_oauth_device_csrf
//	}
//
// Returns:
//
//	{ "approved": true, "client_name": "Sakrylle CLI" }
//
// On invalid user_code or expired/denied/consumed code we return 400 with
// `invalid_request` and `error_description` "user_code did not match an
// active device authorization" — identical for every miss reason so timing
// and shape don't reveal near misses.
func (h *OAuthDeviceHandler) DeviceApprove(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var body deviceApproveRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": err.Error()})
		return
	}
	if !verifyDeviceCSRF(c, body.CSRFToken) {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid_request", "error_description": "csrf token mismatch"})
		return
	}

	if err := h.provider.ApproveDeviceCode(
		c.Request.Context(),
		subject.UserID,
		body.UserCode,
		ptrInt64(body.GroupID),
	); err != nil {
		// Render miss as opaque 400 invalid_request to avoid revealing
		// near-miss state. Other errors (group not allowed, server) keep
		// their natural OAuth mapping.
		if errors.Is(err, service.ErrOAuthDeviceUserCodeMismatch) ||
			errors.Is(err, service.ErrOAuthDeviceCodeNotFound) ||
			errors.Is(err, service.ErrOAuthDeviceCodeExpired) ||
			errors.Is(err, service.ErrOAuthDeviceCodeAccessDenied) ||
			errors.Is(err, service.ErrOAuthDeviceCodeAlreadyConsumed) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":             "invalid_request",
				"error_description": "user_code did not match an active device authorization",
			})
			return
		}
		c.JSON(httpStatusForOAuthError(err), gin.H{
			"error":             oauthErrorReason(err),
			"error_description": infraerrors.Message(err),
		})
		return
	}

	// Look up display fields for the success response so the page can show
	// "You authorized X". We don't need to pass userID here because the
	// approve already verified the user can access the row; we re-look up
	// metadata for display only.
	meta, _ := h.provider.GetDeviceCodeMetadata(c.Request.Context(), body.UserCode, subject.UserID)
	clientName := ""
	if meta != nil {
		clientName = meta.ClientName
	}
	c.JSON(http.StatusOK, gin.H{
		"approved":    true,
		"client_name": clientName,
	})
}

// DeviceDeny implements POST /api/v1/oauth/device/deny (§12.8).
func (h *OAuthDeviceHandler) DeviceDeny(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var body deviceApproveRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "error_description": err.Error()})
		return
	}
	if !verifyDeviceCSRF(c, body.CSRFToken) {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid_request", "error_description": "csrf token mismatch"})
		return
	}
	if err := h.provider.DenyDeviceCode(c.Request.Context(), subject.UserID, body.UserCode); err != nil {
		// Same opaque-400 strategy as DeviceApprove: don't reveal whether
		// the code existed.
		c.JSON(http.StatusBadRequest, gin.H{
			"error":             "invalid_request",
			"error_description": "user_code did not match an active device authorization",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"denied": true})
}

// HandleDeviceCodeGrant is mounted on OAuthProviderHandler.Token's grant_type
// switch. It lives here in the device handler so the device feature stays
// self-contained.
//
// Body:
//
//	grant_type=urn:ietf:params:oauth:grant-type:device_code
//	device_code=...
//	client_id=...
//	client_secret=... (optional, for confidential clients)
func (h *OAuthDeviceHandler) HandleDeviceCodeGrant(c *gin.Context) {
	clientID, clientSecret := extractClientCredentials(c)
	deviceCode := strings.TrimSpace(c.Request.PostFormValue("device_code"))

	issued, err := h.provider.ExchangeDeviceCode(c.Request.Context(), clientID, clientSecret, deviceCode)
	if err != nil {
		writeOAuthError(c, httpStatusForOAuthError(err), oauthErrorReason(err), infraerrors.Message(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	c.JSON(http.StatusOK, tokenResponseJSON(issued))
}

// ── request body type ───────────────────────────────────────────────────────

type deviceApproveRequest struct {
	UserCode  string `json:"user_code" binding:"required"`
	GroupID   int64  `json:"group_id"`
	CSRFToken string `json:"csrf_token" binding:"required"`
}

// ── helpers ─────────────────────────────────────────────────────────────────

// requireFormContentType enforces §12.6 / §12.7 Content-Type discipline.
// Returns false (and writes the error) if the request is JSON or unspecified.
func requireFormContentType(c *gin.Context) bool {
	ct := strings.ToLower(strings.TrimSpace(c.GetHeader("Content-Type")))
	// Strip charset etc.
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	if ct == "application/x-www-form-urlencoded" {
		return true
	}
	writeOAuthError(c, http.StatusBadRequest, "invalid_request", "Content-Type must be application/x-www-form-urlencoded")
	return false
}

func parseOptionalInt64Form(c *gin.Context, key string) *int64 {
	raw := strings.TrimSpace(c.Request.PostFormValue(key))
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		return nil
	}
	return &v
}

func optionalStringForm(c *gin.Context, key string) *string {
	raw := strings.TrimSpace(c.Request.PostFormValue(key))
	if raw == "" {
		return nil
	}
	return &raw
}

func optionalStringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ptrInt64(v int64) *int64 {
	if v <= 0 {
		return nil
	}
	return &v
}

// generateDeviceCSRFToken returns a fresh 32-byte base64url-no-padding token.
func generateDeviceCSRFToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// hashCSRFToken returns the hex SHA-256 used as the cookie value.
func hashCSRFToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// verifyDeviceCSRF checks that the submitted plaintext token's SHA-256 matches
// the sakrylle_oauth_device_csrf cookie via constant-time compare.
func verifyDeviceCSRF(c *gin.Context, submitted string) bool {
	if strings.TrimSpace(submitted) == "" {
		return false
	}
	cookie, err := c.Cookie("sakrylle_oauth_device_csrf")
	if err != nil || cookie == "" {
		return false
	}
	got := hashCSRFToken(submitted)
	return subtle.ConstantTimeCompare([]byte(got), []byte(cookie)) == 1
}

func isHTTPS(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	if strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https") {
		return true
	}
	return false
}

// ── verification page ───────────────────────────────────────────────────────

// deviceVerificationHTML renders the GET /oauth/device page.
//
// The page bootstraps a small JS module that:
//  1. Reads user_code from input (auto-prefilled from query param).
//  2. Reads JWT from localStorage (same key the SPA uses).
//  3. POSTs to /api/v1/oauth/device/approve (or /deny on second button) with
//     {user_code, csrf_token, group_id?} and Authorization: Bearer <jwt>.
//  4. Surfaces success / error inline.
//
// nonce comes from CSP middleware. csrfToken is the plaintext cookie pair.
func deviceVerificationHTML(prefilledUserCode, csrfToken, nonce string) string {
	bootstrap := mustMarshalDeviceBootstrap(map[string]string{
		"user_code":  prefilledUserCode,
		"csrf_token": csrfToken,
	})
	return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>设备授权 · Sakrylle API</title>
<style>
  :root { color-scheme: light dark; --primary:#9181bd; --primary-dim:#7c6ba8; --bg:#faf9fc; --fg:#1f1b2e; --card:#ffffff; --muted:#6b6481; --border:#e5e1ed; }
  @media (prefers-color-scheme: dark) {
    :root { --bg:#13111c; --fg:#ece9f5; --card:#1c1828; --muted:#a39bbf; --border:#2a2438; }
  }
  body { margin:0; font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif; background:var(--bg); color:var(--fg); display:flex; align-items:center; justify-content:center; min-height:100vh; padding:24px; }
  .card { background:var(--card); border:1px solid var(--border); border-radius:16px; padding:32px; max-width:440px; width:100%; box-shadow:0 8px 32px rgba(145,129,189,0.08); }
  h1 { font-size:20px; margin:0 0 8px; font-weight:600; }
  .lead { color:var(--muted); font-size:14px; margin:0 0 24px; line-height:1.5; }
  label { display:block; font-size:13px; font-weight:600; color:var(--fg); margin-bottom:8px; }
  input[type=text] { width:100%; box-sizing:border-box; padding:14px 16px; font-family:ui-monospace,SFMono-Regular,Menlo,monospace; font-size:18px; letter-spacing:.08em; text-transform:uppercase; border:1px solid var(--border); border-radius:10px; background:var(--bg); color:var(--fg); }
  input[type=text]:focus { outline:2px solid var(--primary); outline-offset:1px; }
  .meta { margin:16px 0 24px; padding:14px; border-radius:10px; background:var(--bg); border:1px solid var(--border); display:none; font-size:13px; color:var(--muted); line-height:1.5; }
  .meta.shown { display:block; }
  .meta strong { color:var(--fg); display:block; margin-bottom:4px; }
  .actions { display:flex; gap:12px; margin-top:8px; }
  button { flex:1; padding:12px 16px; border-radius:10px; font-size:15px; font-weight:600; cursor:pointer; border:none; transition:all .15s; }
  button:disabled { opacity:.5; cursor:not-allowed; }
  .approve { background:var(--primary); color:#fff; }
  .approve:hover:not(:disabled) { background:var(--primary-dim); }
  .deny { background:transparent; color:var(--fg); border:1px solid var(--border); }
  .deny:hover:not(:disabled) { background:var(--border); }
  .status { margin-top:16px; padding:12px; border-radius:8px; font-size:13px; display:none; }
  .status.error { background:rgba(220,38,38,.12); color:#dc2626; display:block; }
  .status.info { background:rgba(145,129,189,.12); color:var(--primary); display:block; }
  .status.success { background:rgba(34,197,94,.12); color:#16a34a; display:block; }
</style>
</head>
<body>
<div class="card">
  <h1>设备授权</h1>
  <p class="lead">在您的 CLI 或设备上看到的代码是什么？输入下面的代码以完成授权。</p>
  <label for="user_code">用户代码</label>
  <input type="text" id="user_code" autocomplete="off" autocapitalize="characters" placeholder="SKRY-XXXX-XXXXX" />
  <div class="meta" id="meta"></div>
  <div class="actions">
    <button class="deny" id="deny" type="button">拒绝</button>
    <button class="approve" id="approve" type="button">授权</button>
  </div>
  <div class="status" id="status"></div>
</div>
<script nonce="` + html.EscapeString(nonce) + `">
(function() {
  var bootstrap = ` + bootstrap + `;
  var $code = document.getElementById("user_code");
  var $approve = document.getElementById("approve");
  var $deny = document.getElementById("deny");
  var $status = document.getElementById("status");
  if (bootstrap.user_code) { $code.value = bootstrap.user_code; }

  function setStatus(kind, message) {
    $status.className = "status " + kind;
    $status.textContent = message;
  }
  function getJWT() {
    try {
      var raw = localStorage.getItem("token") || localStorage.getItem("auth_token") || localStorage.getItem("access_token");
      if (raw && raw.charAt(0) === "{") {
        var parsed = JSON.parse(raw);
        return parsed.token || parsed.access_token || "";
      }
      return raw || "";
    } catch (e) { return ""; }
  }
  function gotoLogin() {
    var next = encodeURIComponent(window.location.pathname + window.location.search);
    window.location.href = "/login?redirect=" + next;
  }
  function submit(decision) {
    var jwt = getJWT();
    if (!jwt) {
      setStatus("info", "需要登录后才能授权，正在跳转…");
      setTimeout(gotoLogin, 600);
      return;
    }
    var code = ($code.value || "").trim().toUpperCase();
    if (!code) {
      setStatus("error", "请输入用户代码");
      return;
    }
    $approve.disabled = true;
    $deny.disabled = true;
    var path = decision === "approve" ? "/api/v1/oauth/device/approve" : "/api/v1/oauth/device/deny";
    var body = { user_code: code, csrf_token: bootstrap.csrf_token };
    fetch(path, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "Authorization": "Bearer " + jwt },
      body: JSON.stringify(body)
    }).then(function(res) {
      return res.json().then(function(data) { return { status: res.status, data: data }; });
    }).then(function(out) {
      if (out.status === 401) { gotoLogin(); return; }
      if (out.status >= 400) {
        setStatus("error", (out.data && out.data.error_description) || "授权失败");
        $approve.disabled = false; $deny.disabled = false;
        return;
      }
      if (decision === "approve") {
        setStatus("success", "已授权" + (out.data && out.data.client_name ? "：" + out.data.client_name : "") + "。您可以关闭此页面，回到设备继续操作。");
      } else {
        setStatus("info", "已拒绝。您可以关闭此页面。");
      }
    }).catch(function(err) {
      setStatus("error", "网络错误：" + err.message);
      $approve.disabled = false; $deny.disabled = false;
    });
  }
  $approve.addEventListener("click", function() { submit("approve"); });
  $deny.addEventListener("click", function() { submit("deny"); });
})();
</script>
</body>
</html>`
}

// mustMarshalDeviceBootstrap is the device-page mirror of mustMarshalConsentForm:
// produces a JSON literal safe to embed inside <script>.
//
// Uses the shared mustMarshalJSONForScript helper so HTML-significant characters
// are encoded as < / > / & — valid JSON AND safe inside <script>.
// (HTML entities like &lt; would NOT be valid here: the browser JS engine reads
// them literally, not as the unescaped character.)
func mustMarshalDeviceBootstrap(v map[string]string) string {
	return mustMarshalJSONForScript(v, "oauth: device bootstrap marshal failed")
}
