package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// oauthConsentHTML renders the consent page shown by GET /oauth/authorize.
//
// Inline JS reads the JWT from localStorage (where the SPA stores it after
// login), POSTs to /api/v1/oauth/authorize/begin to open a server-side
// authorize transaction (FIX A1 / §10.3), then POSTs to
// /api/v1/oauth/authorize/approve with ONLY {transaction_id, csrf_token,
// decision, group_id?} — never client_id / redirect_uri / scope. Those
// values live on the server-side transaction row keyed by transaction_id,
// so the JS cannot tamper with the parameters the user consented to.
//
// If the JWT is missing the page bounces to the SPA login flow with a
// post-login `next` pointing back at the current /oauth/authorize URL.
//
// nonce is the per-request CSP nonce from middleware.GetNonceFromContext; the
// inline <script> tag must carry it or the production CSP (script-src 'self'
// 'nonce-...' without 'unsafe-inline') will block both buttons. Pass "" when
// CSP is disabled or the nonce generator failed and middleware fell back to
// 'unsafe-inline' — the empty attribute is harmless in that mode.
//
// Untrusted strings are emitted in two modes:
//   - HTML body (clientName, scope names) → html.EscapeString
//   - JSON literal embedded in <script> (state, code_challenge, redirect_uri)
//     → json.Marshal with HTML-escape ON (default), which turns "<" / ">" / "&"
//     into < / > / &. This is what prevents
//     state="</script><script>alert(1)</script>" from breaking out of the
//     <script> tag and executing attacker JS in the consent page's origin
//     (which holds the user's JWT in localStorage).
func oauthConsentHTML(clientName string, req *service.AuthorizeRequest, nonce string) string {
	scopes := req.Scopes
	if len(scopes) == 0 {
		scopes = []string{"image_generation"}
	}
	scopeListHTML := scopeBulletsHTML(scopes)

	// Begin payload echoes the validated /authorize query so /begin can
	// re-validate, capture the JWT subject, and produce the transaction id +
	// CSRF token. Field names match BeginAuthorizeRequest exactly.
	beginJSON := mustMarshalConsentForm(map[string]string{
		"client_id":             req.ClientID,
		"redirect_uri":          req.RedirectURI,
		"response_type":         req.ResponseType,
		"scope":                 strings.Join(req.Scopes, " "),
		"state":                 req.State,
		"code_challenge":        req.CodeChallenge,
		"code_challenge_method": req.CodeChallengeMethod,
	})

	return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>授权 ` + html.EscapeString(clientName) + ` · Sakrylle API</title>
<style>
  :root { color-scheme: light dark; --primary:#9181bd; --primary-dim:#7c6ba8; --bg:#faf9fc; --fg:#1f1b2e; --card:#ffffff; --muted:#6b6481; --border:#e5e1ed; }
  @media (prefers-color-scheme: dark) {
    :root { --bg:#13111c; --fg:#ece9f5; --card:#1c1828; --muted:#a39bbf; --border:#2a2438; }
  }
  body { margin:0; font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif; background:var(--bg); color:var(--fg); display:flex; align-items:center; justify-content:center; min-height:100vh; padding:24px; }
  .card { background:var(--card); border:1px solid var(--border); border-radius:16px; padding:32px; max-width:420px; width:100%; box-shadow:0 8px 32px rgba(145,129,189,0.08); }
  h1 { font-size:20px; margin:0 0 8px; font-weight:600; }
  .lead { color:var(--muted); font-size:14px; margin:0 0 24px; line-height:1.5; }
  .client { font-weight:600; color:var(--primary); }
  ul.scopes { list-style:none; padding:0; margin:0 0 24px; border:1px solid var(--border); border-radius:10px; overflow:hidden; }
  ul.scopes li { padding:12px 16px; border-bottom:1px solid var(--border); font-size:14px; }
  ul.scopes li:last-child { border-bottom:none; }
  .scope-name { font-family:ui-monospace,SFMono-Regular,Menlo,monospace; font-size:13px; color:var(--primary); }
  .scope-desc { display:block; color:var(--muted); font-size:12px; margin-top:4px; }
  .actions { display:flex; gap:12px; }
  button { flex:1; padding:12px 16px; border-radius:10px; font-size:15px; font-weight:600; cursor:pointer; border:none; transition:all .15s; }
  button:disabled { opacity:.5; cursor:not-allowed; }
  .approve { background:var(--primary); color:#fff; }
  .approve:hover:not(:disabled) { background:var(--primary-dim); }
  .deny { background:transparent; color:var(--fg); border:1px solid var(--border); }
  .deny:hover:not(:disabled) { background:var(--border); }
  .status { margin-top:16px; padding:12px; border-radius:8px; font-size:13px; display:none; }
  .status.error { background:rgba(220,38,38,.12); color:#dc2626; display:block; }
  .status.info { background:rgba(145,129,189,.12); color:var(--primary); display:block; }
</style>
</head>
<body>
<div class="card">
  <h1>授权请求</h1>
  <p class="lead">应用 <span class="client">` + html.EscapeString(clientName) + `</span> 请求访问您的 Sakrylle API 账户。</p>
  <ul class="scopes">` + scopeListHTML + `</ul>
  <div class="actions">
    <button class="deny" id="deny" disabled>拒绝</button>
    <button class="approve" id="approve" disabled>授权</button>
  </div>
  <div class="status" id="status"></div>
</div>
<script nonce="` + html.EscapeString(nonce) + `">
(function() {
  var beginPayload = ` + beginJSON + `;
  var $approve = document.getElementById("approve");
  var $deny = document.getElementById("deny");
  var $status = document.getElementById("status");

  // Server-issued transaction id + CSRF token. Filled in by /begin once the
  // user is confirmed authenticated; held in this closure (NOT localStorage)
  // so XSS on a same-origin page can't grab the CSRF token after the user
  // has navigated away from /oauth/authorize.
  var tx = null;

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
    window.location.href = "/auth/login?next=" + next;
  }

  // Step 1: open the transaction. Runs on page load so the consent UI is
  // backed by a real server-side row before the user clicks anything; if
  // /begin fails (expired client, scope rejected, etc.) we surface the
  // error inline rather than waiting for the approve POST.
  function begin() {
    var jwt = getJWT();
    if (!jwt) {
      setStatus("info", "需要登录后才能授权，正在跳转...");
      setTimeout(gotoLogin, 600);
      return;
    }
    fetch("/api/v1/oauth/authorize/begin", {
      method: "POST",
      headers: { "Content-Type": "application/json", "Authorization": "Bearer " + jwt },
      body: JSON.stringify(beginPayload)
    }).then(function(res) {
      return res.json().then(function(data) { return { status: res.status, data: data }; });
    }).then(function(out) {
      if (out.status === 401) { gotoLogin(); return; }
      if (out.status >= 400) {
        setStatus("error", (out.data && out.data.error_description) || "无法开始授权流程");
        return;
      }
      tx = { transaction_id: out.data.transaction_id, csrf_token: out.data.csrf_token };
      $approve.disabled = false;
      $deny.disabled = false;
    }).catch(function(err) {
      setStatus("error", "网络错误：" + err.message);
    });
  }

  // Step 2: approve or deny against the transaction we just opened.
  // The body carries ONLY the transaction id + csrf token + decision.
  function submit(decision) {
    if (!tx) {
      setStatus("error", "授权流程尚未就绪，请刷新页面重试");
      return;
    }
    var jwt = getJWT();
    if (!jwt) {
      setStatus("info", "登录已过期，正在跳转...");
      setTimeout(gotoLogin, 600);
      return;
    }
    $approve.disabled = true;
    $deny.disabled = true;
    var body = {
      transaction_id: tx.transaction_id,
      csrf_token: tx.csrf_token,
      decision: decision
    };
    fetch("/api/v1/oauth/authorize/approve", {
      method: "POST",
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
      if (out.data && out.data.redirect_to) {
        window.location.href = out.data.redirect_to;
      }
    }).catch(function(err) {
      setStatus("error", "网络错误：" + err.message);
      $approve.disabled = false; $deny.disabled = false;
    });
  }
  $approve.addEventListener("click", function() { submit("approve"); });
  $deny.addEventListener("click", function() { submit("deny"); });
  begin();
})();
</script>
</body>
</html>`
}

// mustMarshalConsentForm serializes a string→string map to a JSON object with
// HTML-significant characters escaped to \uXXXX, suitable for embedding inside
// a <script> block without risk of </script> breakout. encoding/json defaults
// to SetEscapeHTML(true), which is exactly what we need.
//
// Panics on marshal failure. For map[string]string this is unreachable;
// reaching the panic indicates a "should be impossible" bug we want surfaced
// loudly rather than silently degraded.
func mustMarshalConsentForm(form map[string]string) string {
	return mustMarshalJSONForScript(form, "oauth: consent form marshal failed")
}

// mustMarshalJSONForScript serializes v to a JSON literal safe to embed inside
// a <script> block. Uses json.Encoder with SetEscapeHTML(true) so "<", ">",
// and "&" are written as < / > / & — valid JSON AND safe inside
// <script>. The opposite (HTML entities like &lt;) is NOT valid here because
// the browser JS engine reads the script body literally.
//
// Panics on marshal failure with the supplied label; callers pass labels that
// identify the call site so a panic stack tells operators which embed broke.
func mustMarshalJSONForScript(v any, panicLabel string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(true)
	if err := enc.Encode(v); err != nil {
		panic(fmt.Sprintf("%s: %v", panicLabel, err))
	}
	// json.Encoder.Encode appends a trailing newline; trim it so the result
	// embeds cleanly into our template.
	return strings.TrimRight(buf.String(), "\n")
}

// scopeBulletsHTML renders scope <li>s with a friendly description per known scope.
func scopeBulletsHTML(scopes []string) string {
	descs := map[string]string{
		"image_generation": "调用图像生成 API（gpt-image-2 等）",
		"balance:read":     "读取您的账户余额",
		"models:read":      "读取可用模型列表",
	}
	var b strings.Builder
	for _, s := range scopes {
		desc, ok := descs[s]
		if !ok {
			desc = "（未知权限）"
		}
		b.WriteString(`<li><span class="scope-name">`)
		b.WriteString(html.EscapeString(s))
		b.WriteString(`</span><span class="scope-desc">`)
		b.WriteString(html.EscapeString(desc))
		b.WriteString(`</span></li>`)
	}
	return b.String()
}

// oauthInlineErrorHTML is shown when redirecting back to the client would be
// unsafe (bad client_id / bad redirect_uri) and we have to surface the error
// directly to the user.
func oauthInlineErrorHTML(message string) string {
	return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>授权失败 · Sakrylle API</title>
<style>
  body { margin:0; font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif; background:#faf9fc; color:#1f1b2e; display:flex; align-items:center; justify-content:center; min-height:100vh; padding:24px; }
  @media (prefers-color-scheme: dark) { body { background:#13111c; color:#ece9f5; } .card { background:#1c1828 !important; border-color:#2a2438 !important; } }
  .card { background:#fff; border:1px solid #e5e1ed; border-radius:16px; padding:32px; max-width:420px; width:100%; box-shadow:0 8px 32px rgba(145,129,189,0.08); }
  h1 { font-size:20px; margin:0 0 12px; }
  p { color:#6b6481; line-height:1.6; margin:0; font-size:14px; }
  .err { font-family:ui-monospace,Menlo,monospace; font-size:13px; padding:12px; background:rgba(220,38,38,.12); color:#dc2626; border-radius:8px; margin-top:16px; }
</style>
</head>
<body>
<div class="card">
  <h1>授权请求被拒绝</h1>
  <p>该 OAuth 请求未通过校验，请检查 <code>client_id</code> 与 <code>redirect_uri</code> 是否注册正确。</p>
  <div class="err">` + html.EscapeString(message) + `</div>
</div>
</body>
</html>`
}
