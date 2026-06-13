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
		// OIDC: forward the nonce parsed on the GET /authorize so the consent
		// page's POST to /begin persists it into the transaction; without this
		// the browser flow drops nonce and every id_token lacks the claim.
		"nonce": req.Nonce,
	})

	return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>授权 ` + html.EscapeString(clientName) + ` · Sakrylle API</title>
<style>
  :root { color-scheme: light dark; --primary:#9181bd; --primary-dim:#7b6aab; --bg:#faf9fc; --fg:#1f1b2e; --card:#ffffff; --muted:#6b6481; --border:#e5e1ed; }
  @media (prefers-color-scheme: dark) {
    :root { --bg:#13111c; --fg:#ece9f5; --card:#1c1828; --muted:#a39bbf; --border:#2a2438; }
  }
  body { margin:0; font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif; background:var(--bg); color:var(--fg); display:flex; align-items:center; justify-content:center; min-height:100dvh; padding:24px; box-sizing:border-box; }
  .card { background:var(--card); border:1px solid var(--border); border-radius:16px; padding:24px; max-width:800px; width:100%; box-shadow:0 8px 32px rgba(145,129,189,0.08); }
  @media (min-width:480px) { .card { padding:32px; } }
  @media (min-width:768px) { .card { padding:40px 48px; } }
  h1 { font-size:20px; margin:0 0 8px; font-weight:600; }
  .lead { color:var(--muted); font-size:14px; margin:0 0 24px; line-height:1.5; }
  .client { font-weight:600; color:var(--primary); }
  ul.scopes { list-style:none; padding:0; margin:0 0 24px; border:1px solid var(--border); border-radius:10px; overflow:hidden; }
  ul.scopes li { padding:12px 16px; border-bottom:1px solid var(--border); font-size:14px; }
  ul.scopes li:last-child { border-bottom:none; }
  .scope-name { display:none; }
  .scope-desc { display:block; color:var(--fg); font-size:14px; }
  .actions { display:flex; gap:12px; max-width:480px; margin-left:auto; margin-right:auto; }
  button { flex:1; padding:12px 16px; border-radius:10px; font-size:15px; font-weight:600; cursor:pointer; border:none; transition:all .15s; }
  button:disabled { opacity:.5; cursor:not-allowed; }
  .approve { background:#6b5b95; color:#fff; }
  .approve:hover:not(:disabled) { background:#584b7a; }
  .deny { background:transparent; color:var(--fg); border:1px solid var(--border); }
  .deny:hover:not(:disabled) { background:var(--border); }
  .status { margin-top:16px; padding:12px; border-radius:8px; font-size:13px; display:none; }
  .status.error { background:rgba(220,38,38,.12); color:#dc2626; display:block; }
  .status.info { background:rgba(145,129,189,.12); color:var(--primary); display:block; }
  .group-wrap { margin-bottom:24px; display:none; }
  .group-wrap > .group-title { display:block; font-size:13px; color:var(--muted); margin-bottom:8px; font-weight:500; }
  .group-list { display:grid; grid-template-columns:1fr; gap:8px; }
  @media (min-width:480px) { .group-list { grid-template-columns:repeat(2, 1fr); } }
  @media (min-width:640px) { .group-list { grid-template-columns:repeat(3, 1fr); } }
  @media (min-width:768px) { .group-list { grid-template-columns:repeat(4, 1fr); } }
  .group-list.single { grid-template-columns:1fr; max-width:200px; }
  .group-item { display:flex; align-items:center; gap:10px; padding:10px 12px; border-radius:8px; border:1px solid var(--border); background:var(--card); cursor:pointer; transition:border-color .15s; min-width:0; }
  .group-item:has(input:checked) { border-color:var(--primary); background:rgba(145,129,189,.06); }
  .group-item input[type=checkbox] { accent-color:var(--primary); width:16px; height:16px; cursor:pointer; }
  .group-item .g-name { font-size:14px; color:var(--fg); flex:1; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .group-item .g-badge { font-size:11px; padding:2px 8px; border-radius:4px; background:rgba(145,129,189,.12); color:var(--primary); flex-shrink:0; }
</style>
</head>
<body>
<div class="card">
  <h1><img src="` + sakrylleLogoDataURI + `" width="28" height="28" style="vertical-align:middle;margin-right:8px">授权请求</h1>
  <p class="lead">应用 <span class="client">` + html.EscapeString(clientName) + `</span> 请求访问您的 Sakrylle API 账户。</p>
  <ul class="scopes">` + scopeListHTML + `</ul>
  <div class="group-wrap" id="image-group-wrap">
    <span class="group-title">Image API 分组 / Image API Groups</span>
    <div class="group-list" id="image-group-list"></div>
  </div>
  <div class="group-wrap" id="responses-group-wrap">
    <span class="group-title">Responses API 分组 / Responses API Groups</span>
    <div class="group-list" id="responses-group-list"></div>
  </div>
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

  // Detect device name from User-Agent
  function getDeviceName() {
    var ua = navigator.userAgent;
    if (/iPhone/.test(ua)) return "iPhone";
    if (/iPad/.test(ua)) return "iPad";
    if (/Android/.test(ua)) {
      if (/Mobile/.test(ua)) return "Android 手机";
      return "Android 平板";
    }
    if (/Macintosh/.test(ua)) return "Mac";
    if (/Windows/.test(ua)) return "Windows PC";
    if (/Linux/.test(ua)) return "Linux";
    return ""; // empty = backend will show "未命名设备"
  }

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

  // Build a group <label> via DOM APIs (createElement + textContent) instead of
  // innerHTML string concatenation, so admin-configured group names/badges can
  // never inject markup. Defense in depth on top of the nonce-based CSP.
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
    var payload = Object.assign({}, beginPayload);
    payload.device_name = getDeviceName();
    fetch("/api/v1/oauth/authorize/begin", {
      method: "POST",
      headers: { "Content-Type": "application/json", "Authorization": "Bearer " + jwt },
      body: JSON.stringify(payload)
    }).then(function(res) {
      return res.json().then(function(data) { return { status: res.status, data: data }; });
    }).then(function(out) {
      if (out.status === 401) { gotoLogin(); return; }
      if (out.status >= 400) {
        setStatus("error", (out.data && out.data.error_description) || "无法开始授权流程");
        return;
      }
      tx = { transaction_id: out.data.transaction_id, csrf_token: out.data.csrf_token };
      // Populate group selectors from allowed_groups returned by /begin.
      // Split into two categories: Image API groups and Responses API groups.
      var groups = (out.data.allowed_groups && Array.isArray(out.data.allowed_groups)) ? out.data.allowed_groups : [];
      var imageGroups = [];
      var responsesGroups = [];
      for (var i = 0; i < groups.length; i++) {
        var g = groups[i];
        if (g.allow_image_generation) {
          imageGroups.push(g);
        } else {
          responsesGroups.push(g);
        }
      }

      // Render Image API groups
      var $imageWrap = document.getElementById("image-group-wrap");
      var $imageList = document.getElementById("image-group-list");
      if (imageGroups.length > 0) {
        $imageList.innerHTML = "";
        if (imageGroups.length === 1) {
          $imageList.className = "group-list single";
        }
        for (var i = 0; i < imageGroups.length; i++) {
          var g = imageGroups[i];
          var id = g.id || g;
          var name = g.name || String(id);
          $imageList.appendChild(buildGroupItem(id, name, "Image"));
        }
        $imageWrap.style.display = "block";
      }

      // Render Responses API groups
      var $responsesWrap = document.getElementById("responses-group-wrap");
      var $responsesList = document.getElementById("responses-group-list");
      if (responsesGroups.length > 0) {
        $responsesList.innerHTML = "";
        if (responsesGroups.length === 1) {
          $responsesList.className = "group-list single";
        }
        for (var i = 0; i < responsesGroups.length; i++) {
          var g = responsesGroups[i];
          var id = g.id || g;
          var name = g.name || String(id);
          var mult = Number(g.rate_multiplier);
          var badge = (isFinite(mult) && mult > 0) ? mult + "x" : "";
          $responsesList.appendChild(buildGroupItem(id, name, badge));
        }
        $responsesWrap.style.display = "block";
      }

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
    var checks = document.querySelectorAll('input[name="group_ids"]:checked');
    if (checks.length > 0) {
      var ids = [];
      for (var i = 0; i < checks.length; i++) { ids.push(parseInt(checks[i].value, 10)); }
      body.group_ids = ids;
      body.group_id = ids[0];
    }
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
		// OIDC standard scopes
		"openid":   "使用您的账户登录（OpenID Connect）/ Sign in with your account (OpenID Connect)",
		"profile":  "查看您的基本资料（用户名）/ Read your basic profile (username)",
		"email":    "查看您的邮箱地址 / Read your email address",
		// legacy scopes (kept for backward compat)
		"image_generation": "调用图像生成 API（gpt-image-2 等）/ Call image generation API",
		"balance:read":     "读取您的账户余额 / Read your balance",
		// v2 canonical scopes
		"profile:read":            "读取您的用户名和头像 / Read your username and avatar",
		"email:read":              "读取您的邮箱地址 / Read your email address",
		"account:read":            "读取账户信息和分组 / Read account info and groups",
		"account:balance:read":    "读取余额 / Read your balance",
		"models:read":             "查看可用模型列表 / View available models",
		"chat.completions:create": "发送对话请求 / Send chat requests",
		"responses:create":        "发送 Responses API 请求 / Send Responses API requests",
		"messages:create":         "发送 Messages API 请求 / Send Messages API requests",
		"images:create":           "生成和编辑图片 / Generate and edit images",
		"usage:read":              "查看使用记录 / View usage records",
		"offline_access":          "保持登录状态 / Stay signed in",
	}
	var b strings.Builder
	for _, s := range scopes {
		desc, ok := descs[s]
		if !ok {
			desc = "（未知权限）"
		}
		_, _ = b.WriteString(`<li><span class="scope-name">`)
		_, _ = b.WriteString(html.EscapeString(s))
		_, _ = b.WriteString(`</span><span class="scope-desc">`)
		_, _ = b.WriteString(html.EscapeString(desc))
		_, _ = b.WriteString(`</span></li>`)
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
