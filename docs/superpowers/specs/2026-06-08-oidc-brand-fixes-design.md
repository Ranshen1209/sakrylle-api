# Sakrylle API · OIDC 基座与品牌化修复设计

- 状态：设计待复核
- 日期：2026-06-08
- 分支：`theme/monet-purple`（直接提交，暂不开 PR）
- 范围：修复代码审查产出的全部 finding（1 High + 8 Medium + ~12 Low/Info + 2 待确认）
- 适用仓库：仅 Sakrylle API（`sub2api` fork）的后端 Go OIDC/OAuth provider 与前端 Vue 品牌化。其余 5 个产品（CLI/Studio/Web/Chat/Image）为外部 fork，不在本仓库，不在本设计范围。

> 安全约束：Sakrylle API（`sub.sakrylle.com`）为线上生产 OIDC 服务。本设计**不含**直接改生产配置的指令；所有触及生产 `.env` / `oauth_clients` / `settings` / 签名密钥 / 容器重启的动作，由维护者按 `CLAUDE.md` 审批流自行执行，本文仅标注「需审批」并列出只读核对项。

---

## 1. 背景与目标

代码审查（6 维度对抗式 workflow，46 agent，逐条对源码验证）在 Sakrylle API 仓库内确认了 32 条 finding。OIDC 基座整体稳固：加解密核心、token 流安全、id_token claims fail-closed 护栏均已验证正确。缺陷集中在三处：

1. 出站 HTTP 拉取器（`request_uri` / `sector_identifier_uri`）缺 SSRF 与重定向控制；
2. 多个 OIDC 表面宣告了未兑现的能力（签名 UserInfo 缺 iss/aud、backchannel logout 缺 sid/jti 且过度广播、introspection 无受众限定、discovery 宣告未实现的参数）；
3. 一条文档不变量不准确（声称有独立 `OIDC_KEY_ENCRYPTION_KEY`，实际复用 TOTP 密钥）。

品牌化整体良好，仅少量表层偏差。

**目标**：在 `theme/monet-purple` 分支按 6 个主题组修复全部 finding；High/Medium 项配套测试；触生产动作仅标注与列只读核对项，不在本仓库执行。

---

## 2. 关键决策（已与维护者确认 2026-06-08）

| # | 决策点 | 选定方案 |
|---|---|---|
| 范围 | 修复覆盖档位 | **全部都修**（含 Low/Info/文档订正） |
| #4 | KEK 不一致 | **只订正文档**，承认 OIDC 签名密钥复用 TOTP 加密密钥 |
| #20 | confidential client 的 PKCE | **对所有 client 强制 PKCE S256**（合并前置：只读核对生产无 `pkce_required=false` 的 client） |
| 交付 | 落地与交付方式 | **直接在 `theme/monet-purple` 分支提交，暂不开 PR**；触生产由维护者按审批流执行 |
| #6 | pairwise sub 拉取失败 | **fail-closed**：sector_identifier_uri 拉取/校验失败则拒绝授权，不降级 |
| #11/#12 | discovery 虚假宣告 | **补齐实现，保持 supported=true**（真实实现 request_uri 拉取与 claims 约束） |
| 测试 | 配套测试范围 | **仅 High/Medium 补测试**；表层/文档项靠现有回归 |
| #19 | 主按钮对比度修复范围 | **仅改 consent 页** approve 按钮；前端 `.btn-primary` 保持现状（已知设计取舍，非回归） |

---

## 3. 修复分组与执行顺序

按主题分 6 组提交（非按严重度），因多个 finding 共享同一底层修复；分主题让每个 commit 自洽、可独立回滚。执行顺序 **A → B → C → D → E → F**（A 先做，因 E 的 request_uri 实现依赖 A 的安全 HTTP 客户端）。

| 组 | finding | 共享核心改动 |
|---|---|---|
| A. SSRF + 出站 HTTP 加固 | #1(High)、#2、#12 的 request_uri 拉取、Info(超时) | `safeDialContext` 接入 `DefaultHTTPClient`：重定向重校验 + 拨号层 IP 黑名单 |
| B. Token 端点正确性 | #7、#3、#10、#20 | introspect 受众守卫、UserInfo iss/aud、PKCE 强制 |
| C. Logout 合规 | #8、#9、#13 | 贯通 sid、限定 backchannel 接收方、加 jti |
| D. 密钥服务健壮性 | #5、#14、#15、#16 | 轮换/清理加锁、panic 恢复、指针去重、死路径修正 |
| E. Pairwise + Claims/Discovery | #6、#11、#12 | pairwise fail-closed、ApplyClaimsConstraints 接线、request_uri 真实拉取 |
| F. 品牌 + 文档订正 | #17、#18、#19、#21、#4、Info | 前端小修 + consent 页 + 文档订正 + denylist→allowlist |

---
## 4. 组 A · SSRF + 出站 HTTP 加固

唯一的 High，且是组 E `request_uri` 实现的安全前提。

**现状**：`oidc_request_object.go:32` 的 `DefaultHTTPClient` 仅设 `Timeout`。`FetchRequestURI`、`FetchSectorIdentifierURI` 只校验初始 URL 的 scheme/host；Go 默认客户端随后跟随最多 10 次重定向且不重校验，拨号层从不检查解析出的真实 IP。攻击链：allowlisted host 返回 `302 Location: http://169.254.169.254/...` → 服务器跟随 → 云元数据 / 内网 SSRF；DNS rebinding 同样绕过字符串级 host 校验。

**复用既有能力**：`channel_monitor_ssrf.go:114` 已有包内私有 `safeDialContext`（真实 dial 前校验目标 IP、防 DNS rebinding）。OIDC 文件在同一 `service` 包，直接调用，不导出/不挪动，改动面最小。

**改动**：
1. 重构 `DefaultHTTPClient`（`oidc_request_object.go:36`）：
   - `Transport.DialContext = safeDialContext`（拨号层拒绝 127/0.0.0.0/10/172.16-31/192.168/169.254/::1 等内网与 metadata 段）
   - `CheckRedirect`：每跳重新校验 scheme(https-only) + IP 段；拒绝 https→http 降级；超过合理跳数（3）拒绝
   - 保留 `Timeout`，并为每次请求加 `context.WithTimeout`（消除 Info 项的 `context.Background()` 隐患）
2. `FetchSectorIdentifierURI`（#2）改走同一安全客户端。**不额外加 host 允许列表**——IP 黑名单在拨号层已足够，且 sector_identifier_uri 由 operator 经 SQL 配置、非客户端可注册，加 host 列表与 OIDC §8.1 语义冲突。
3. 两处读 body 前用 `io.LimitReader`(64KB) 封顶，确认 timeout 在读 body 阶段生效。

**测试（High，必配）**：重定向到 `http://169.254.169.254/` 被拒；https→http 降级被拒；拨号层命中内网 IP 被拒；正常外部 https 通过；超大 body 被截断。

---

## 5. 组 B · Token 端点正确性

四个独立小修，集中在 token/userinfo 路径。

**#7 introspect 越权（Medium）** — `oauth_provider_service.go:558-603`
`IntrospectToken` 拿到调用方 `clientID` 却不与 token 归属的 `meta.ClientID` 比对，任何 confidential client 能内省别家 token。revoke 路径(`:1456`)已有此检查。
**改动**：加载 meta 后加守卫 `if meta.ClientID != clientID { return &IntrospectionResponse{Active:false}, nil }`，与 revoke 一致。

**#3 签名 UserInfo JWT 缺 iss/aud（Medium）** — `oidc_userinfo_jwt.go:37-50`
只发 `sub/iat/exp`；因所有 client 共享签名密钥，A 的 UserInfo JWT 对 B 字节级有效（跨 client 重放）。
**改动**：`BuildUserInfoJWTClaims` 加 `issuer`、`clientID` 参数，补 `iss`(OP issuer) 与 `aud`([]string{clientID})。调用方 `oauth_provider_handler.go:1093` 处 `meta.ClientID` 与 `h.discoveryIssuer(c)` 已在作用域内。

**#10 签名 UserInfo JWT 硬编码 email_verified=false（Low）** — `oidc_userinfo_jwt.go:48`
同函数加 `emailVerified bool` 参数，handler 传 `user.EmailVerified`，与 id_token / 纯 JSON UserInfo 对齐。与 #3 同改。

**#20 对所有 client 强制 PKCE（决策已定）** — `oauth_provider_service.go:648` 及 authorize/token 两端
现状 PKCE 被 `if client.PKCERequired` 门控，confidential client 可关。
**改动**：
- authorize 端：对所有 client 要求合法 `code_challenge` + `code_challenge_method=S256`，缺失即 `invalid_request`。
- token 端：对所有 client 走 `verifyPKCES256`，不再看 `PKCERequired`。
- `LookupClient` 中「PKCERequired=false 且无 secret 才拒」逻辑相应收紧。
- **合并前置（维护者执行，需审批·只读）**：核对生产 `oauth_clients` 无 `pkce_required=false` 的 client：`SELECT client_id FROM oauth_clients WHERE pkce_required=false;`（应为空）。
- 文档同步把「所有 client 强制 PKCE S256」从声称变为真实成立。

**测试（#7/#3/#20 必配；#10 顺带）**：introspect 跨 client→`active:false`、同 client→正常；UserInfo JWT 含正确 iss/aud，B 的 client 校验 A 的 token 应失败；confidential client 无 code_verifier→authorize/token 均被拒、带正确 S256→通过。

---

## 6. 组 C · Logout 合规

**#8 logout_token 缺 sid（Medium）** — `oauth_provider_handler.go:1663`
恒传 `BuildLogoutToken(..., "", ...)`。id_token 已带 `sid`(`oidc_id_token.go:157`)，`verifyLogoutIDToken` 解析了 hint 但只透传 `sub`。
**改动**：`verifyLogoutIDToken` 同时返回 `sid`，沿 `Logout → dispatchBackchannelLogout → BuildLogoutToken` 透传。拿不到 sid 时该次不带 sid，但保留 discovery 的 `backchannel_logout_session_supported=true`（已具备透传能力）。

**#9 backchannel 广播给所有 RP（Medium）** — `oauth_provider_handler.go:1648-1713`
对每个配了 `backchannel_logout_uri` 的 client 都 POST，不论用户是否在那登录过——过度通知 + 泄露登出行为。
**改动**：接收方限定为对该 subject 有活跃 grant 的 client（用 `ListGrantsByUser` 查出 client_id 集合，与「配了 backchannel uri」取交集再广播）。pairwise client 发其专属 `sub` 而非 public sub（一并修）。

**#13 logout_token 缺 jti（Low）** — `oidc_backchannel_logout.go:33-42`
OIDC Back-Channel Logout §2.4 要求 jti 防重放。用现有 `GenerateOpaqueToken` 生成唯一 jti 写入。

**测试（#8/#9 必配）**：logout 后 logout_token 带正确 sid；只有有 grant 的 client 收到；无关 client 不收到。

---

## 7. 组 D · 密钥服务健壮性

无安全漏洞，但有可用性风险（验签失败、调度静默停摆）。

**#5 previous-kids 列表无锁 RMW 竞争（Medium）** — `oidc_key_service.go:668-737`
`cleanupExpiredKeysFor` 对共享 `oidc_signing_previous_kids` 列表做 Get→filter→Put 时只为内存切片持 `s.mu`，不覆盖 store 写；轮换与清理是独立 goroutine，交错会丢掉刚轮换的 kid → JWKS 提前移除 → 该 kid 签的 token 验签失败。
**改动**：让 `cleanupExpiredKeysFor` 整个 store RMW 都在 `s.mu`（或专用 mutex）下。

**#14 panic 恢复注册在循环外（Low）** — `oidc_key_rotation.go:105-170`
`recover()` 在 goroutine 入口注册一次、在 `for` 外——循环体一次 panic 即 goroutine 永久退出，轮换/清理静默停摆。`rotationIntervalHours`/`gracePeriodSeconds` 调 `GetValue` 缺 nil-guard。
**改动**：每轮工作包进带自身 `recover` 的内层函数，外层 recover 作兜底；补 nil-guard。

**#15 指针翻转失败致 kid 重复（Low）** — `oidc_key_service.go:525-532`
`appendPreviousKID` 成功后若「写 current 指针」失败，旧 kid 同时为 current 与 previous，重启后 JWKS 出现两次（同密钥，无安全影响）。
**改动**：在 `EnsureKey` load 时把 current kid 从 previous 集合去重（改动小、对已有脏数据自愈；不做全事务化）。

**#16 死的非对称 request-object 路径 iss 矛盾（Low）** — `oidc_request_object.go:346-364`
`ParseWithClaims` 传 `jwt.WithIssuer(issuer)` 却又要求 `iss==client_id`，互斥 → 拒绝一切合规 token；两处 `ParseWithClaims` 均未设 `jwt.WithValidMethods`。该路径将在组 E 的 request_uri 实现里被真正接线，故此处修正为前置。
**改动**：去掉 `WithIssuer(issuer)`（按 §6.1 用 client_id），两分支都加 `WithValidMethods`，补 RS/ES/PS 测试。

**测试（#5 必配；#16 接线后必配）**：并发轮换+清理不丢 kid（带 `-race`）；非对称验签用例。

---
## 8. 组 E · Pairwise + Claims / Discovery

让 discovery 宣告的能力真正成立（依赖组 A 的安全 HTTP 客户端）。

**#6 pairwise sub fail-closed（决策已定）** — `oidc_pairwise.go:109-124`
现状拉取失败时回退到哈希原始 URI 字符串，与成功路径（哈希 redirect host 列表）基准不同 → sub 漂移、RP 侧账号关联损坏。
**改动**：`ResolvePairwiseSub` 在 `FetchSectorIdentifierURI` 出错（含 subset 校验失败）时直接返回错误，由 authorize 流程拒绝授权，删除 raw-URI 回退分支。错误信息区分「网络失败」与「subset 校验拒绝」便于运维定位。fail-closed 后 sector 拉取稳定性直接影响 pairwise client 登录——组 A 已为该拉取加安全客户端 + 超时。

**#11 ApplyClaimsConstraints 接线（Low）** — `oidc_claims_enforcement.go:20` + authorize 流程
现 `claims` 参数解析存进 `req.Claims`(`handler:161`) 但 `ApplyClaimsConstraints` 零生产调用。
**改动**：在签发 id_token 与返回 UserInfo 时调用 `ApplyClaimsConstraints`，**必须放在 `assertNoForbiddenClaims` 之后**——claims 参数只能删减/约束 claim，绝不可借此注入 forbidden claim（保住 fail-closed 不变量）。订正函数误导性的「not supported」doc 注释。

**#12 request_uri 真实拉取（Low→需实现）** — `oauth_provider_handler.go:169-175` + `oidc_request_object.go:464`
现在 `request_uri` 只打日志、是 no-op。
**改动**：authorize 校验阶段调用 `FetchRequestURI`（走组 A 安全客户端 + host 允许列表 + 64KB 限制），取回 Request Object 后复用组 D #16 修正后的非对称验签路径，校验 `iss==client_id`、`aud` 含 issuer。discovery 的 `request_uri_parameter_supported=true` 由此真实成立。订正 `:854` 处「not supported」注释。

**测试（#6 + request_uri 验签必配）**：pairwise sector 拉取失败→authorize 被拒、不产漂移 sub；claims essential/value/values 约束生效、经 claims 注入 balance 仍被 `assertNoForbiddenClaims` 挡；request_uri 合法远程对象通过、指向内网被组 A 拦、签名/iss 错被拒。

---

## 9. 组 F · 品牌 + 文档订正

**前端（frontend/）**
- **#17** `AmountInput.vue:32-34` 硬编码 `$` → 改 ￥（或按所选支付币种驱动）。
- **#18** `PaymentView.vue:38` 余额无符号 → 加 ￥ 前缀，与全站一致。
- **#19（仅 consent 页，决策已定）** consent 页 `.approve`(`oauth_provider_consent.go:90`) 白字 on `var(--primary)` ≈3.45:1，低于 AA 4.5 → 改用 primary-700 级（#6b5b95）。**前端 `.btn-primary` 保持现状**（已知设计取舍，非回归，不动全站视觉）。

**consent 页（oauth_provider_consent.go）**
- **#21** group 名 `innerHTML` 拼接(`:232-258`) → 改用 `textContent`/`createElement` 建节点；`id`/`rate_multiplier` 数值校验。虽有 CSP nonce + 无 unsafe-inline 兜底，纵深防御仍补。
- **Info** `--primary-dim`(`:71`) `#7c6ba8` → `#7b6aab`（对齐 primary-600）。

**文档订正**
- **#4 KEK（决策：只订正文档）** — 改 `OIDC-DEPLOYMENT.md:51-52`、`risk-register.md:111`：明确 OIDC 签名密钥由共享 TOTP 加密密钥(`cfg.Totp.EncryptionKey`)包裹，不存在独立 `OIDC_KEY_ENCRYPTION_KEY`；加运维警告：**轮换/重置 TOTP 密钥会使已存 OIDC 签名密钥（及 monitor/backup secrets）无法解密**，需同步重新初始化。
- **Info** Currency policy 文档记录 ￥(全角,内部余额) vs ¥(半角,真实 CNY 网关) 的有意约定；sakura 未注册 Tailwind 的现状如实标注。
- **Info（denylist→allowlist，决策：全修）** 把 `assertNoForbiddenClaims` 从「拒 12 个已知商业名」反转为真正 allowlist（只放行 §8 那 14 个 claim，其余一律拒），fail-closed 更彻底。属代码改动，配测试。

**housekeeping**：删除两个未跟踪的 `.bak`（`oidc_key_service.go.bak`、`oidc_token_wiring_test.go.bak`）。

---

## 10. 测试与验收

- 框架：沿用现有 `backend/internal/service/oidc_*_test.go`、`backend/internal/handler/oauth_provider_*_test.go` 风格；并发项带 `-race`。
- 范围：组 A/B/C/D/E 的 High/Medium 项 + #6 + request_uri 验签 + denylist→allowlist 反转配套测试；组 F 表层/文档项靠现有回归。
- 每组改完跑 `go build ./...` 与相关包 `go test`；前端改动跑现有前端测试。
- 验收：全部 finding 关闭；现有测试零回归；id_token claims fail-closed 不变量保持；access_token(`sk_oauth_`) 形态与网关计费路径零改动。

## 11. 触生产动作（维护者执行，需审批）

| 动作 | 触发组 | 性质 |
|---|---|---|
| 只读核对 `oauth_clients` 无 `pkce_required=false` | B(#20) | 只读 SQL |
| 部署含全部修复的新构建 | 全部 | 容器更新 |
| 文档订正后无需生产写操作（#4 仅改文档） | F | 无 |

> 全部代码改动落在本仓库 `theme/monet-purple` 分支；本设计不在仓库内执行任何生产写操作。

## 12. 回滚

各组为独立主题 commit，可单独 `git revert`。功能性改动（A 安全客户端、B PKCE 强制、E fail-closed/接线）均为收紧或叠加，不改 access_token 形态与计费路径；最坏情况按组回退即可恢复。
