---
title: Sakrylle Image 现状调研报告
status: local
scope: product
last_verified: 2026-06-06
---

# Sakrylle Image 现状调研报告

> 文档编号: 50 | 状态: 调研完成 | 日期: 2026-06-03
>
> 关联文档: 见 [51-sakrylle-image-oidc-upgrade-plan.md](./51-sakrylle-image-oidc-upgrade-plan.md)（升级方案）、[03-sakrylle-api-oidc-architecture.md](./03-sakrylle-api-oidc-architecture.md)（IdP OIDC 架构）

---

## 1. 调研范围

- 仓库: `/Volumes/APFS_HD/Documents/Github/gpt_image_playground`（本地，Sakrylle Image 源码）
- 关联 IdP: `/Volumes/APFS_HD/Documents/Github/sub2api`（Sakrylle API，OAuth2 provider 侧）
- 目标: 厘清当前技术栈、OAuth2 客户端实现（file:line）、token 管理、API 调用模式、生产部署配置与品牌现状，识别与 OIDC 目标的差距。
- **安全约束**: Sakrylle Image（image.sakrylle.com）与 Sakrylle API（sub.sakrylle.com）均已上线生产，本文档纯为只读调研，不含任何改动指令。

---

## 2. 关键结论

| 维度 | 结论 |
|---|---|
| 技术栈 | React 19 + TypeScript + Vite 6 + Tailwind 3 + Zustand 5，纯静态 SPA，无服务端 |
| OAuth2 状态 | Authorization Code + PKCE (S256) 完整落地，refresh rotation + reuse detection 到位 |
| OIDC 状态 | **客户端尚未接入**：无 `openid` scope、无 nonce、无 id_token 解析、无 UserInfo 规范调用。注：sub2api **服务端 OIDC 已于 2026-06-04 完整实现**（含 `openid` scope 时返回可验签 id_token、`/v1/me` 返回 `sub`），缺口现仅在 Image 客户端侧 |
| 身份获取方式 | 客户端当前完全依赖私有端点 `/v1/me`，读 `user_id`（数字）/ `username`（字符串）；服务端现已可在含 `openid` scope 时返回顶层 OIDC `sub`，待客户端采用 |
| 运行时注入缺口 | `VITE_SAKRYLLE_OAUTH_BASE` / `VITE_SAKRYLLE_OAUTH_CLIENT_ID` 未声明于 `vite-env.d.ts`，未被 `inject-api-url.sh` 注入，换 OAuth 端点或 client_id 须重建镜像 |
| 品牌状态 | 完整 Sakrylle 化：莫奈紫主题 `#9181bd`、樱花 logo、Liquid Glass UI |
| 部署平台 | Docker nginx:alpine 二阶段构建（生产），Vercel 已禁用（`deploymentEnabled: false`），Cloudflare Workers 备用但未启用 |

---

## 3. 相关文件路径

### 核心认证与 API 文件

| 文件 | 作用 | 关键行 |
|---|---|---|
| `src/lib/sakrylleAuth.ts` | OAuth PKCE 完整流程：beginLogin / handleCallback / refresh / logout | :10-16（常量），:25-41（SakrylleAuthToken 接口），:229（beginLogin），:248（handleCallback），:390（refreshIfNeeded），:399（forceRefreshToken），:317（revokeToken）|
| `src/lib/sakrylleAccount.ts` | 平台 API 封装：authedFetch / fetchBalance / fetchMe / fetchModels；SakrylleMePayload 接口 | :1-11（base URL），:38-53（SakrylleMePayload），:80（dedupedForceRefresh） |
| `src/lib/oauthFallback.ts` | OAuth Bearer fallback（无 apiKey 时调图像/responses API） | `resolveBearerToken` / `canUseOAuthForProfile` |
| `src/lib/groupSelection.ts` | 多 group 支持，per-mode group token 路由 | `getGroupAccessToken` / `ensureSelectedGroupId` / `fetchResponsesApiGroups` |
| `src/lib/runtimeEnv.ts` | 运行时 env 读取（trim wrapper） | 仅做 `.trim()`，无占位符解析逻辑 |
| `src/vite-env.d.ts` | TypeScript 构建期 env 类型声明 | :6-17（**未声明** `VITE_SAKRYLLE_OAUTH_BASE` / `VITE_SAKRYLLE_OAUTH_CLIENT_ID`，仅声明了 `VITE_DEFAULT_API_URL` / `VITE_SAKRYLLE_PLATFORM_API` 等 6 项）|
| `src/main.tsx` | React 入口 + `/oauth/callback` 路径检测 | `pathname === '/oauth/callback'` 触发 `handleCallback`，finally 调 `history.replaceState` 回首页 |
| `src/components/Header.tsx` | 余额显示 / 登录按钮 / 60s 轮询 / storage 事件多 tab 同步 | :15（`SAKRYLLE_PURCHASE_URL = 'https://sub.sakrylle.com/purchase'`），:163（GitHub 链接），:170（favicon） |
| `src/lib/openaiCompatibleImageApi.ts` | 图像 API 调用核心 | 调用 `resolveBearerToken()` 注入 Bearer |
| `src/lib/apiProfiles.ts` | API base URL 配置 | `DEFAULT_BASE_URL` fallback = `https://api.sakrylle.com/v1` |

### 部署与构建文件

| 文件 | 作用 | 关键行 / 说明 |
|---|---|---|
| `deploy/Dockerfile` | 二阶段构建（node:20-alpine build → nginx:alpine prod） | :6-10（5 个 `ENV` 占位符：`VITE_DEFAULT_API_URL`、`VITE_API_PROXY_AVAILABLE`、`VITE_API_PROXY_LOCKED`、`VITE_DOCKER_DEPLOYMENT`、`VITE_DOCKER_LEGACY_API_URL_USED`）。**不含** `VITE_SAKRYLLE_OAUTH_BASE` / `VITE_SAKRYLLE_OAUTH_CLIENT_ID` |
| `deploy/inject-api-url.sh` | 容器启动时 sed 替换占位符 | :21-25（仅替换上述 5 项占位符，**无** `OAUTH_BASE` / `CLIENT_ID` 替换行） |
| `deploy/nginx.conf` | SPA fallback + 可选 API 代理块 | `ENABLE_API_PROXY=false` 时 sed 删除代理块 |
| `vercel.json` | Vercel 部署配置 | `"deploymentEnabled": false`（生产禁用，不会自动部署到 Vercel） |
| `wrangler.jsonc` | Cloudflare Workers/Pages 配置 | `assets.not_found_handling: "single-page-application"`（SPA fallback），`name: "sakrylle-image-playground"`，`compatibility_date: "2026-05-07"`。生产未使用 |

### 测试文件

| 文件 | 覆盖内容 | 用例数 |
|---|---|---|
| `src/lib/sakrylleAuth.test.ts` | PKCE/state/refresh 轮换/logout 全路径 | 19 个 |
| `src/lib/sakrylleAccount.test.ts` | formatBalance / fetchBalance / 401 dedupe+retry+logout | 9 个 |

---

## 4. 当前实现摘要

### 4.1 技术栈

```
React 19 + TypeScript + Vite 6 + Tailwind 3 + Zustand 5 + i18next + vitest
```

纯前端 SPA，无后端服务。部署为 Docker 镜像（nginx:alpine），构建时在 `Dockerfile` 中用占位符写入 `VITE_*` 变量，运行时 `deploy/inject-api-url.sh` 通过 `sed` 替换为容器 env 变量。多平台支持 linux/amd64+arm64。

### 4.2 OAuth2 客户端实现（file:line 精确引用）

**认证常量定义（`src/lib/sakrylleAuth.ts:10-16`）**:

```typescript
// :10
const OAUTH_BASE = readRuntimeEnv(import.meta.env.VITE_SAKRYLLE_OAUTH_BASE) || 'https://sub.sakrylle.com'
// :11
const CLIENT_ID = readRuntimeEnv(import.meta.env.VITE_SAKRYLLE_OAUTH_CLIENT_ID) || 'sakrylle-image-playground'
// :16 — v2 canonical scopes，不含 openid
const SCOPE = 'profile:read account:read account:balance:read models:read images:create responses:create offline_access'
```

`VITE_SAKRYLLE_OAUTH_BASE` 和 `VITE_SAKRYLLE_OAUTH_CLIENT_ID` 未在 `src/vite-env.d.ts:6-17` 声明，且不在 `deploy/inject-api-url.sh:21-25` 的 sed 替换列表内，因此实际固定为 bundle 内的 fallback 常量，无法运行时覆盖。

**SakrylleAuthToken 接口（`src/lib/sakrylleAuth.ts:25-41`）**:

```typescript
export interface SakrylleAuthToken {
  accessToken: string
  refreshToken?: string
  expiresAt: number
  scope?: string
  refreshTokenExpiresAt?: number  // family-anchored expiry，跨轮换保留
  additionalTokens?: Array<{      // 多 group 支持
    accessToken: string
    expiresAt: number
    scope?: string
    group?: { id: number; name: string }
  }>
  group?: { id: number; name: string }
  // 注意：接口中无 idToken / idTokenClaims 字段
}
```

**登录流程（`sakrylleAuth.ts:229-245`）**:

1. `beginLogin()` (:229): 生成 32 字节 PKCE verifier + SHA-256 challenge + 16 字节 state
2. verifier/state 写 `sessionStorage`（key: `sakrylle-image-playground.pkce-verifier` / `pkce-state`）
3. 302 跳转至 `${OAUTH_BASE}/oauth/authorize?response_type=code&scope=${SCOPE}&code_challenge_method=S256&...`

**回调处理（`sakrylleAuth.ts:248-290`）**:

1. `handleCallback()`: 在 `src/main.tsx` `pathname === '/oauth/callback'` 时触发
2. 校验 state，从 sessionStorage 取 verifier；callback 后立即清除（`:259-260`）
3. `POST ${OAUTH_BASE}/oauth/token`（`:273`），body: `grant_type=authorization_code&code=...&code_verifier=...`
4. 要求 `refresh_token` 存在（`requireRefresh: true`，`:287`）
5. `saveToken()` 持久化至 `localStorage['sakrylle-image-playground.auth']`（`:307`）
6. **不处理 `id_token` 字段**：`payload` 类型为 `OAuthTokenResponse`，无 `id_token` 字段定义，即使 server 未来返回也被静默忽略。

### 4.3 Token 管理

**存储机制**:

| 数据 | 存储位置 | Key |
|---|---|---|
| access_token + refresh_token + expiresAt + scope + group | `localStorage` | `sakrylle-image-playground.auth` |
| PKCE verifier（临时） | `sessionStorage` | `sakrylle-image-playground.pkce-verifier` |
| PKCE state（临时） | `sessionStorage` | `sakrylle-image-playground.pkce-state` |
| theme | `localStorage` | `sakrylle-image-playground.theme` |
| language | `localStorage` | `sakrylle-image-playground.language` |
| 选中的 group（per-mode） | `localStorage` | `sakrylle-image-playground.selected-groups` |
| group id→name 缓存 | `localStorage` | `sakrylle-image-playground.group-names` |
| 任务/生成历史/Agent 对话 | IndexedDB | `db.ts` 管理 |

**刷新策略**:

- `refreshIfNeeded()`（`:390`）: `expiresAt - 60s` 前主动刷新
- `/v1/* 401 invalid_token` 触发 `forceRefreshToken()`（`:399`）+ `dedupedForceRefresh()`（`sakrylleAccount.ts:80`）合并并发请求
- refresh_token family-anchored expiry（`refreshTokenExpiresAt`）跨轮换保留（`:354`）
- 刷新失败 → 自动 `logout()`

**撤销（`sakrylleAuth.ts:317-334`）**:

- `logoutAndRevoke()`（`:338`）: 先 `logout()` 清本地，再 RFC 7009 `POST ${OAUTH_BASE}/oauth/revoke` 携带 refresh_token（fire-and-forget，网络错误不阻塞）
- `logout()`（`:310`）: 仅清 localStorage + sessionStorage，不发 revoke 请求

### 4.4 API 调用模式

**Base URL 配置链**:

```
图像 API:  VITE_DEFAULT_API_URL（占位符注入）→ 容器 env DEFAULT_API_URL → fallback https://api.sakrylle.com/v1
平台 API:  VITE_SAKRYLLE_PLATFORM_API → VITE_DEFAULT_API_URL → https://api.sakrylle.com/v1
OAuth 端点: VITE_SAKRYLLE_OAUTH_BASE → https://sub.sakrylle.com（bundle 内固定，无运行时注入）
```

**用户身份获取（`sakrylleAccount.ts:38-53`）**:

```typescript
export interface SakrylleMePayload {
  user_id?: number        // 数字 ID，非 OIDC 标准 sub（字符串）
  username?: string       // 用户名，非 OIDC preferred_username
  display_name?: string
  avatar_url?: string
  locale?: string
  balance?: number        // 余额（scope: account:balance:read）
  currency_display?: 'CNY' | 'USD'
  granted_scopes: string[]
  effective_capabilities: string[]
  current_group?: string
  current_group_id?: number
  allowed_groups?: SakrylleGroup[]
  quota?: unknown
  capabilities?: unknown
  // 无 sub、iss、aud、email、email_verified 等 OIDC 标准 claims
}
```

`GET /v1/me` 是当前唯一身份来源，返回私有 JSON，非 OIDC UserInfo 规范格式。

**图像生成（`src/lib/openaiCompatibleImageApi.ts`）**:

- 调用 `resolveBearerToken()` 注入 `Authorization: Bearer <access_token>`
- `POST /v1/images/generations`、`POST /v1/images/edits`
- 无 API Key 时走 `oauthFallback.ts` 的 OAuth Bearer 路径

### 4.5 生产配置：Vercel / Cloudflare

**Vercel（`vercel.json`）**: `"deploymentEnabled": false`，**已禁用**，不会自动部署到 Vercel。

**Cloudflare Workers（`wrangler.jsonc`）**:

```jsonc
{
  "name": "sakrylle-image-playground",
  "compatibility_date": "2026-05-07",
  "assets": {
    "directory": "./dist",
    "not_found_handling": "single-page-application"
  }
}
```

已配置 SPA fallback，可通过 `wrangler deploy` 部署至 Cloudflare Pages/Workers。当前生产（image.sakrylle.com）**未使用**，仍为 Docker+nginx 方案，保留作备用部署入口。

**当前生产部署**: Docker nginx:alpine 镜像，通过 `/opt/stack/docker-compose.yml` 管理，nginx 反代到 image.sakrylle.com。`ENABLE_API_PROXY=false`，前端直连 `api.sakrylle.com`，强依赖 CORS 正确配置（sub2api 侧须将 `image.sakrylle.com` 列入 CORS allowlist）。

「不确定」: `/opt/stack/docker-compose.yml` 中 image.sakrylle.com 服务使用的具体 Docker 镜像标签，以及是否有 GHA workflow 自动推送构建（未查到对应 `.github/workflows/` 文件）。

### 4.6 品牌现状

**颜色系统（`src/index.css:16-20`）**:

```css
--primary: 253 26% 62%;          /* Monet Purple #9181bd */
--primary-soft: 253 50% 96%;
--sakrylle-purple: 145, 129, 189; /* RGB 值 */
```

**品牌覆盖面**:

| 位置 | 内容 |
|---|---|
| `index.html:6` | theme-color: #9181bd |
| `index.html:7` | meta description: "Sakrylle 图像工坊" |
| `index.html:9` | apple-mobile-web-app-title: "Sakrylle" |
| `src/index.css:43-82` | `.sakrylle-ambient` 莫奈紫光斑背景 |
| `src/index.css:523-640` | Liquid Glass UI 组件（全紫色系） |
| `src/index.css:676-705` | 主题切换（View Transitions API，系统主题自动跟随） |
| `src/components/icons.tsx` | `SakrylleLogo` 组件：紫渐变+樱花 SVG |
| `src/components/Header.tsx:15` | `SAKRYLLE_PURCHASE_URL = 'https://sub.sakrylle.com/purchase'` |
| `src/components/Header.tsx:163` | GitHub 链接指 Ranshen1209 fork |
| `public/manifest.webmanifest` | PWA 名称 "Sakrylle 图像工坊" |
| `tailwind.config.js` | primary 颜色用 CSS HSL 变量，blue-* 已替换为莫奈紫 hex |
| `src/locales/zh.json + en.json` | `header.appName` 等品牌字符串 |

---

## 5. 差距分析

### 5.1 OIDC 缺口（全量，**均为 Image 客户端侧**；服务端 OIDC 已于 2026-06-04 就绪）

| # | 缺口 | 严重程度 | 影响 |
|---|---|---|---|
| C1 | 无 `openid` scope | 高 | `SCOPE` 常量（sakrylleAuth.ts:16）不含 `openid`，未触发 server 颁发 id_token（server 现已支持，待客户端加 scope） |
| C2 | 无 nonce | 高 | `beginLogin()` 不生成 nonce，无法防 id_token 重放 |
| C3 | 无 id_token 字段定义与解析 | 高 | `SakrylleAuthToken` 接口无 `idToken` 字段；`handleCallback` 不读取 id_token |
| C4 | 身份信息依赖私有端点 | 中 | `SakrylleMePayload` 无 `sub`（OIDC 主键），user_id 是数字而非 OIDC 字符串 sub |
| C5 | 无 Discovery 使用 | 低 | 端点硬编码，不符合 OIDC RP 规范，但功能不受影响 |

### 5.2 运行时注入缺口

| 缺口 | 影响 | 位置 |
|---|---|---|
| `VITE_SAKRYLLE_OAUTH_BASE` 无 Dockerfile 占位符 | 更换 OAuth 端点须重建镜像 | `deploy/Dockerfile:6-10`（无相关 ENV 行）、`deploy/inject-api-url.sh:21-25`（无相关 sed 行）|
| `VITE_SAKRYLLE_OAUTH_CLIENT_ID` 无 Dockerfile 占位符 | 更换 client_id 须重建镜像 | 同上 |
| `vite-env.d.ts` 未声明两变量 | TypeScript 无构建期类型检查，`import.meta.env.VITE_SAKRYLLE_OAUTH_BASE` 类型为 `any` | `src/vite-env.d.ts:6-17` |

### 5.3 安全注意事项（既有，非新增）

| 项 | 说明 |
|---|---|
| refresh_token 存 localStorage | XSS 可读。SPA 架构固有风险，OIDC 升级不会改变也不会恶化。文档 `docs/OAUTH_V2_INTEGRATION.md §11` 已注明 |
| id_token 签名验证 | SPA 无法安全做 RS256 验证（需 BFF 或引入 jose 库）。对当前用途（读用户名/sub）风险可接受，需文档明确 |

---

## 6. 开发任务拆分

> 详细可执行任务清单见 [51-sakrylle-image-oidc-upgrade-plan.md](./51-sakrylle-image-oidc-upgrade-plan.md)

高层任务组（本文仅列出，执行见 51）：

1. **前置 P0**：补齐运行时注入机制（OAUTH_BASE / CLIENT_ID 占位符）
2. **Phase 1**：server 端 OIDC 实现（依赖 sub2api 侧，见 [03](./03-sakrylle-api-oidc-architecture.md)）
3. **Phase 2**：client 端加 `openid` scope + nonce + id_token 解析
4. **Phase 3**：从 id_token claims 获取身份信息，降低对 `/v1/me` 的依赖
5. **Phase 4**：测试覆盖扩展 + 文档更新

---

## 7. 优先级

| 优先级 | 任务 | 理由 |
|---|---|---|
| P0 | 补齐 OAUTH_BASE / CLIENT_ID 运行时注入 | 无论是否 OIDC，当前无法不重建镜像换端点/client_id，运维风险高 |
| P1 | server 端 OIDC 实现（sub2api 侧）— ✅ **已完成 2026-06-04** | 服务端已在含 `openid` scope 时返回 id_token；本项不再阻塞，客户端可直接接入 |
| P2 | client 端加 openid scope + nonce + id_token 解析 | 依赖 P1；两侧必须同步发布 |
| P3 | 从 id_token claims 替代 `/v1/me` 获取身份 | 减少网络请求，更符合标准；依赖 P2 |
| P4 | 测试覆盖扩展（id_token 路径、nonce 验证） | 依赖 P2 |

---

## 8. 风险

| 风险 | 概率 | 影响 | 缓解措施 |
|---|---|---|---|
| server 未返回 id_token，client 静默降级 | 低（服务端已于 2026-06-04 实现 id_token 签发） | 低（不报错，行为退回当前状态） | handleCallback 中检测 id_token 存在性并记录 console.warn |
| client 先加 `openid` scope 而 server 不识别 | 中（server 当前 NormalizeScopes 丢弃 openid） | 高（授权请求因未知 scope 被拒，登录中断） | 必须 server 端 scope 注册与 client 同步发布，不可单侧先上 |
| id_token RS256 签名无法在 SPA 客户端验证 | 确定 | 中（仅读 payload，无密码学保证） | 文档明确说明；对安全敏感场景须用 BFF；当前读用户名风险可接受 |
| OAUTH_BASE 占位符变更影响生产镜像构建 | 低（值不变，仅加注入机制） | 低 | 新增占位符与现有 fallback 并存，fallback 值不变，行为一致 |

---

## 9. 验收标准

调研阶段验收（均已满足）：

- [x] 所有关键文件 path:line 均可在本地仓库验证
- [x] OAuth2 流程完整描述，覆盖 beginLogin → handleCallback → refresh → revoke
- [x] `SakrylleAuthToken` 接口现状已记录（无 idToken 字段）
- [x] `SakrylleMePayload` 接口现状已记录（无 sub 字段）
- [x] 所有 OIDC 缺口（C1-C5）已识别并量化
- [x] 运行时注入缺口已定位至具体文件行（Dockerfile:6-10，inject-api-url.sh:21-25，vite-env.d.ts:6-17）
- [x] 品牌现状覆盖所有已知 surface
- [x] 安全注意事项已记录（不要求本调研解决）

---

## 10. 后续问题

1. **「不确定」** `/opt/stack/docker-compose.yml` 中 image.sakrylle.com 服务（如存在）使用哪个 Docker 镜像标签？是否有 GHA workflow 自动推送？需登服务器核查 `docker-compose.yml` 对应服务的 `image:` 与 `environment:` 节。

2. **「不确定」** 生产 `docker-compose.yml` 中是否已通过 `environment:` 显式注入 `DEFAULT_API_URL` 等容器 env？还是全部依赖 bundle 内 fallback？影响评估占位符补齐后的行为一致性。

3. **「已更新 2026-06-04」** sub2api `/oauth/token` **现已在授权 scope 含 `openid` 时返回可验签 `id_token`**（RS256/ES256 按 client `signing_algorithm` 选择）；`/v1/me` 在含 `openid` 时返回顶层 `sub`。原调研记录的「服务端不返回 id_token」状态已被服务端 OIDC 实现取代。Image 客户端加 `openid` scope 后即可消费 id_token（缺口现仅在客户端侧）。详见 [03-sakrylle-api-oidc-architecture.md](./03-sakrylle-api-oidc-architecture.md)。

4. **「不确定」** `email:read` scope 是否已在 sub2api 端完整实现？`docs/OAUTH_V2_INTEGRATION.md §6` 列出但注明 "Not granted to first-party clients by default"。

5. **「不确定」** `src/index.html` 的 `<link rel="icon">` 是否已指向 Sakrylle 樱花 favicon？与 `/opt/stack/nginx/static/` 版本一致性未核查。

6. **「不确定」** `wrangler.jsonc` Cloudflare Workers 部署在开发/测试环境是否被使用？若作备用生产方案，VITE_* 占位符注入机制（nginx sed 方案）在 Workers 静态资产环境下不适用，需另行设计环境变量注入策略。
