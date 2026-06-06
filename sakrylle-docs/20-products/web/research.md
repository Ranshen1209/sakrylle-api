---
title: 30 · Sakrylle Web 调研报告（open-webui fork）
status: local
scope: product
last_verified: 2026-06-06
---

> **时态说明（2026-06-06）**：本文保留了早期调研/计划中的历史口径。关于 Sakrylle API OIDC Provider 的当前事实，以 [`../../10-platform-identity/current-state.md`](../../10-platform-identity/current-state.md) 为准；`2026-06-03 前关于 OIDC Provider 能力的旧判断`、`2026-06-03 前关于 OIDC 基座状态的旧判断`、`2026-06-03 前关于 Web SSO 后端依赖的旧判断` 等表述仅适用于 2026-06-03 前的历史背景。

# 30 · Sakrylle Web 调研报告（open-webui fork）

**调研日期**：2026-06-03
**调研范围**：open-webui 现状技术栈、原生 OAuth/OIDC SSO 支持（含 file:line 引用）、OpenAI endpoint 配置、用户与管理员系统、模型权限、数据目录、品牌改造点、与 Sakrylle API 对接的兼容性差距。
**本地仓库路径**：`/Volumes/APFS_HD/Documents/Github/open-webui/`

---

## 1. 调研范围

| 维度 | 内容 |
|---|---|
| 技术栈 | SvelteKit 前端 + FastAPI/Python 后端，单容器打包 |
| 认证机制 | 原生 OAuth 2.0 / OIDC SSO（authlib），Generic OIDC 分支 |
| OpenAI endpoint 接入 | 多 endpoint 分号分隔，支持 Admin UI 动态配置 |
| 用户系统 | 角色（admin/user/pending），OIDC 自动建账，Domain 白名单 |
| 模型权限 | 依赖 API Key 绑定的 group，open-webui 本身无 quota 控制 |
| 数据目录 | `DATA_DIR`，SQLite 默认，支持 PostgreSQL |
| 品牌改造点 | 共 11 处，多数可 volume mount 覆盖，3 处需代码改动 |

---

## 2. 关键结论

1. **open-webui 原生支持完整 Generic OIDC SSO**，通过 `OPENID_PROVIDER_URL` 指向任意 OIDC Provider（authlib 库自动完成 discovery、token、userinfo 全流程）。  
2. **历史判断（2026-06-03 前）：当时认为 sub2api 尚未提供完整 OIDC Provider 能力**：`/.well-known/oauth-authorization-server`（RFC 8414）已存在，但缺少 `/.well-known/openid-configuration`、`id_token`（JWT）、JWKS，无法直接被 open-webui 的 authlib OIDC client 识别。**OIDC 改造是 Sakrylle Web 接入 SSO 的前置依赖**，详见 `03-sakrylle-api-oidc-architecture.md`。  
3. **WEBUI_NAME 后缀 bug**：`env.py:772-773` 强制给非 `Open WebUI` 的名称追加 `' (Open WebUI)'`，品牌名将变成 `Sakrylle Web (Open WebUI)`；必须修改这两行代码。  
4. **userinfo claim 提取限制**：email/username 提取使用 `user_data.get(claim_name)`（仅一层 flat 查找，`oauth.py:1597`、`oauth.py:1720`）。sub2api `/v1/me` 的 OIDC 分支**已实现（2026-06-04）**：含 `openid` scope 时返回顶层 OIDC `sub`，`email`/`name` 按 scope 裁剪；商业状态留在 scoped account/group 块。**Web 接入期需核实** open-webui 的 flat `user_data.get('email')`/`get('name')` 是否命中顶层 claim（若 sub2api 仍以嵌套 `user.email` 返回则需配置 claim 路径或确认平铺）。  
5. **roles/groups claim 支持 nested dot-path**（`oauth.py:1256`），但对 Sakrylle Web 不相关（不用角色映射）。  
6. **OpenAI endpoint 接入零阻力**：直接填 `OPENAI_API_BASE_URLS=https://api.sakrylle.com/v1`，Admin UI 也可动态修改，无需改代码。  
7. **品牌图片可通过 volume mount 覆盖** `static/static/` 目录，无需重建镜像；但 `constants.ts`、`app.html`、`env.py:772-773` 必须代码改动后重新 build。  

---

## 3. 相关文件路径（均已验证真实存在）

| 文件 | 关键行 | 说明 |
|---|---|---|
| `backend/open_webui/config.py` | 3579–3906 | OIDC/OAuth Generic provider 注册逻辑，所有 `OAUTH_*` / `OPENID_*` ConfigVar |
| `backend/open_webui/config.py` | 3868–3906 | 注册条件：`OAUTH_CLIENT_ID && (OAUTH_CLIENT_SECRET || PKCE S256) && OPENID_PROVIDER_URL`；注册名 `'oidc'` |
| `backend/open_webui/config.py` | 3893–3900 | `oauth.register(name='oidc', server_metadata_url=OPENID_PROVIDER_URL.value, ...)` |
| `backend/open_webui/config.py` | 3903–3906 | `OAUTH_PROVIDERS['oidc'] = {'name': OAUTH_PROVIDER_NAME.value, 'register': ...}` |
| `backend/open_webui/config.py` | 3603–3606 | `OPENID_REDIRECT_URI` ConfigVar（空字符串时由 WEBUI_URL 自动构建） |
| `backend/open_webui/config.py` | 291–417 | `OPENAI_API_BASE_URLS` / `OPENAI_API_KEYS` / `ENABLE_OLLAMA_API` ConfigVar |
| `backend/open_webui/env.py` | 216–261 | `DATA_DIR` / `DATABASE_URL` / `STATIC_DIR` / `FRONTEND_BUILD_DIR` |
| `backend/open_webui/env.py` | 603–712 | `WEBUI_AUTH` / `WEBUI_SECRET_KEY` / OAuth session 配置 |
| `backend/open_webui/env.py` | 771–773 | `WEBUI_NAME` 声明 + **后缀 bug（必须改）** |
| `backend/open_webui/utils/oauth.py` | 1557–1578 | userinfo 拉取逻辑（id_token → userinfo endpoint 回退） |
| `backend/open_webui/utils/oauth.py` | 1564–1565 | 触发 userinfo 回退的条件：email/username claim 不在 id_token |
| `backend/open_webui/utils/oauth.py` | 1596–1597 | `email = user_data.get(email_claim, '')`（仅 flat 查找） |
| `backend/open_webui/utils/oauth.py` | 1718–1720 | `name = user_data.get(username_claim)`（仅 flat 查找） |
| `backend/open_webui/utils/oauth.py` | 1253–1262 | roles claim 支持 nested dot-path（不影响 Sakrylle Web） |
| `backend/open_webui/main.py` | 2804–2826 | `GET /oauth/{provider}/login` + `GET /oauth/{provider}/login/callback` 路由 |
| `src/lib/constants.ts` | 4 | `export const APP_NAME = 'Open WebUI'`（**必须改**） |
| `src/app.html` | 118 | `<title>Open WebUI</title>`（**必须改**） |
| `static/static/` | — | favicon.png/svg、splash.png/.dark.png、logo.png、site.webmanifest（volume mount 可覆盖） |
| `src/lib/components/admin/Settings/Connections.svelte` | 39–76 | Admin UI Connections 面板，绑定 `OPENAI_API_BASE_URLS + OPENAI_API_KEYS` |

---

## 4. 当前实现摘要

### 4.1 技术栈

| 层 | 技术 |
|---|---|
| 前端 | SvelteKit + TypeScript + Tailwind CSS，build 嵌入后端 static 目录 |
| 后端 | Python 3.x + FastAPI + SQLAlchemy（async）+ Alembic |
| 数据库 | SQLite（默认 `DATA_DIR/webui.db`）或 PostgreSQL（`DATABASE_URL` 指定） |
| Cache | Redis（可选，用于 WebSocket 多实例 pub/sub） |
| 认证库 | authlib（OIDC/OAuth 2.0 完整实现） |
| 打包 | Docker，单容器（前端 build 嵌入后端），`Dockerfile` 在项目根 |

### 4.2 原生 OAuth/OIDC SSO 支持

open-webui 原生内置完整 OAuth 2.0 / OIDC SSO（`authlib`）。以下四个 provider 通过专用环境变量配置：Google、Microsoft、GitHub、**Generic OIDC**（`OAUTH_CLIENT_ID` + `OPENID_PROVIDER_URL`）。

**Generic OIDC 注册逻辑**（`config.py:3868-3906`）：

- 条件：`OAUTH_CLIENT_ID` 且（`OAUTH_CLIENT_SECRET` 或 `OAUTH_CODE_CHALLENGE_METHOD=S256`）且 `OPENID_PROVIDER_URL` 均非空时注册
- 用 `server_metadata_url=OPENID_PROVIDER_URL` 调用 authlib OIDC 发现，authlib 会自动从该 URL 拉取 `/.well-known/openid-configuration`（或 `/.well-known/oauth-authorization-server`）
- 注册名为 `'oidc'`，因此回调路由为 `GET /oauth/oidc/login/callback`（`main.py:2809`）

**authlib discovery URL 查找顺序**（「不确定」：尚未实测 authlib 是否按顺序尝试两个 well-known URL）：authlib 文档描述 `server_metadata_url` 直接请求该 URL，若 sub2api 已在该路径返回合法元数据则可对接。sub2api 目前暴露的是 `/.well-known/oauth-authorization-server`，不是 `/.well-known/openid-configuration`；若 `OPENID_PROVIDER_URL` 直接指向前者，authlib 能否成功拉取**需要实测确认**。

**关键 SSO 子功能**：

| 功能 | 环境变量 | 默认值 |
|---|---|---|
| 首次 SSO 自动建账 | `ENABLE_OAUTH_SIGNUP` | `True` |
| 新账号默认角色 | `DEFAULT_USER_ROLE` | `pending`（注意！） |
| 关闭密码登录 | `ENABLE_LOGIN_FORM` | `True` |
| 单 provider 自动跳转 | `OAUTH_AUTO_REDIRECT` | `False` |
| 同邮箱合并账号 | `OAUTH_MERGE_ACCOUNTS_BY_EMAIL` | `False` |
| Domain 白名单 | `OAUTH_ALLOWED_DOMAINS` | 无限制 |
| Backchannel logout | `ENABLE_OAUTH_BACKCHANNEL_LOGOUT` | — |
| PKCE S256（省 secret） | `OAUTH_CODE_CHALLENGE_METHOD=S256` | — |

**回调地址**：由 `WEBUI_URL` + `/oauth/oidc/login/callback` 构建，即 `https://chat.sakrylle.com/oauth/oidc/login/callback`（域名已确认 2026-06-03）。也可通过 `OPENID_REDIRECT_URI` 显式指定（`config.py:3603`）。

### 4.3 OpenAI endpoint 配置

两种配置方式（均指向 `https://api.sakrylle.com/v1`）：

**环境变量（首次启动推荐）**：
```
OPENAI_API_BASE_URLS=https://api.sakrylle.com/v1
OPENAI_API_KEYS=sk-<sakrylle_user_api_key>
```

**多 group / 多 key（分号分隔）**：
```
OPENAI_API_BASE_URLS=https://api.sakrylle.com/v1;https://api.sakrylle.com/v1
OPENAI_API_KEYS=<claude_group_key>;<gpt_group_key>
```

**Admin UI 动态配置**：管理后台 Settings → Connections → OpenAI，实时生效（存入 DB ConfigVar，不需重启），代码位置 `src/lib/components/admin/Settings/Connections.svelte:39-76`。

`ENABLE_OLLAMA_API=False` 可关闭 Ollama，避免侧边栏出现无关入口。

注意：`api.sakrylle.com` 是 Nginx 纯 reverse proxy（`sakrylle-api.conf`），指向 sub2api `/v1/*`，open-webui 直接访问该 URL 即可。

### 4.4 用户与管理员系统

- 用户角色：`admin` / `user` / `pending`（`DEFAULT_USER_ROLE` 默认 `pending`，OIDC 新用户首次登录后需 admin 手动激活，**需改为 `user`**）
- 首个注册用户自动成为 admin（`WEBUI_ADMIN_EMAIL/PASSWORD/NAME` 可预置 admin）
- `ENABLE_SIGNUP=False` 关闭本地密码注册（强制走 Sakrylle SSO）
- `ENABLE_LOGIN_FORM=False` 关闭密码登录入口

### 4.5 模型权限与 quota 控制

open-webui **本身没有 API quota 控制**：用户在 open-webui 中发消息，实际 API 请求走绑定的 Sakrylle API Key，计费和 quota 全在 sub2api 侧发生。

- **共享 key 模式**：所有 open-webui 用户共用一个 Sakrylle API Key，合并计费（无法区分用户用量）
- **per-user key 模式**：OIDC 登录后用户在 open-webui 的 Settings 填入自己的 Sakrylle API Key，各自计费，隔离最佳
- 模型列表来自 `/v1/models` 聚合，由绑定的 key 所属 group 决定可见范围

### 4.6 数据目录与配置路径

| 路径 | 用途 |
|---|---|
| `DATA_DIR`（默认 `/app/backend/data`） | SQLite DB、上传文件、向量 DB、审计日志 |
| `DATA_DIR/webui.db` | SQLite 数据库（可用 `DATABASE_URL` 换为 PostgreSQL） |
| `backend/open_webui/static/` | 后端 serve 的静态文件（favicon、logo、splash、custom.css 等）|
| `STATIC_DIR` | 静态文件目录路径 env var，默认 `OPEN_WEBUI_DIR/static`（`env.py:240`） |
| `DATA_DIR/audit.log` | 审计日志（`env.py:999`） |
| volume `open-webui:/app/backend/data` | Docker Compose 默认 volume 名，Sakrylle Web **必须改名** |

---

## 5. 差距分析

| # | 维度 | 现状 | Sakrylle Web 需求 | 处理方式 |
|---|---|---|---|---|
| D1 | OIDC discovery | ✅ **已实现（2026-06-04）**：`/.well-known/openid-configuration` 已挂载并 serving（与 `/.well-known/oauth-authorization-server` 并存） | open-webui authlib 需要拉取 OIDC 发现文档 | 服务端就绪（`03` G1）；Web 侧 `OPENID_PROVIDER_URL` 指向 sub.sakrylle.com 即可发现 |
| D2 | `id_token` | ✅ **已实现（2026-06-04）**：含 `openid` scope 时签发可验签 id_token（RS256/ES256 按 client `signing_algorithm` 选择，JWKS 发布双算法公钥） | authlib OIDC flow 期望 `id_token`（先从 token 拿，缺则回落 userinfo endpoint） | 服务端就绪（`03` G4）；Web 侧直接消费 id_token |
| D3 | userinfo 平铺格式 | `/v1/me` OIDC 分支已实现：含 `openid` 时返回顶层 `sub`，`email`/`name` 按 scope 裁剪 | open-webui email/username claim 仅做 `user_data.get(claim_name)` 一层查找（`oauth.py:1597`、`oauth.py:1720`） | 服务端就绪（`03` G7）；**Web 接入期核实** flat `get('email')`/`get('name')` 是否命中顶层 claim，否则配置 `OAUTH_EMAIL_CLAIM`/`OAUTH_USERNAME_CLAIM` |
| D4 | WEBUI_NAME 后缀 bug | `env.py:772-773` 强制追加 `' (Open WebUI)'` | 品牌名应为 `Sakrylle Web` | **修改 `env.py:772-773`**（改判断条件或删除追加逻辑） |
| D5 | 前端品牌 | `constants.ts:4` APP_NAME = `'Open WebUI'`；`app.html:118` `<title>Open WebUI</title>` | 改为 `Sakrylle Web` | **修改两处，重新 build 前端** |
| D6 | 品牌图片 | `static/static/` 下均为 Open WebUI 品牌图片 | 替换为 Sakrylle 樱花 logo | **volume mount 覆盖**（无需重建镜像）或 fork build 时替换 |
| D7 | PWA manifest | `site.webmanifest` 中 `name/short_name = 'Open WebUI'` | 改为 `Sakrylle Web` | 随品牌图片一起替换（volume mount 或 fork） |
| D8 | Volume 命名 | Docker Compose 默认 volume 名 `open-webui` | 与官方镜像隔离，Sakrylle Web 专用 | **改为 `sakrylle-web`** 或设 `DATA_DIR=/data/sakrylle-web` |
| D9 | 默认用户角色 | `DEFAULT_USER_ROLE` 默认 `pending` | OIDC SSO 登录后用户需能直接使用 | **设 `DEFAULT_USER_ROLE=user`** |
| D10 | scope=openid | sub2api `canonicalScopes`（`backend/internal/service/oauth_scopes.go`）尚不含 `openid`/`profile`/`email` | authlib OIDC flow 需要 `scope=openid` | **sub2api 侧注册 `openid`/`profile`/`email` 为规范 scope**（见 `03` G5）|

---

## 6. 品牌改造点详情

| 改造点 | 路径 | 方式 | 说明 |
|---|---|---|---|
| APP_NAME 常量 | `src/lib/constants.ts:4` | **代码改动 + 重新 build** | `'Open WebUI'` → `'Sakrylle Web'` |
| 页面 title | `src/app.html:118` | **代码改动 + 重新 build** | `<title>Open WebUI</title>` → `<title>Sakrylle Web</title>` |
| WEBUI_NAME 后缀 bug | `backend/open_webui/env.py:772-773` | **代码改动 + 重新 build** | 删除追加 `' (Open WebUI)'` 的两行 |
| favicon.png/svg/ico | `static/static/` | volume mount 覆盖 | 替换为 Sakrylle 樱花 favicon |
| splash.png | `static/static/splash.png` | volume mount 覆盖 | 亮色主题首屏 logo |
| splash-dark.png | `static/static/splash-dark.png` | volume mount 覆盖 | 暗色主题首屏 logo |
| logo.png | `static/static/logo.png` | volume mount 覆盖 | 侧边栏 logo |
| site.webmanifest | `static/static/site.webmanifest` | volume mount 覆盖 | PWA 安装名称 `name/short_name` 改为 `Sakrylle Web` |
| web-app-manifest 图标 | `static/static/web-app-manifest-192x192.png` / `…-512x512.png` | volume mount 覆盖 | PWA 图标 |
| 主题颜色 | Tailwind CSS / CSS 变量 | 「不确定」：open-webui 主题颜色配置路径未在本次调研中定位 | Monet Purple `#9181bd` 替换原主色；需进一步调研 `app.css` / Tailwind config |
| ENABLE_COMMUNITY_SHARING | env var | 环境变量 | 设 `False` 隐藏分享到社区按钮（默认 `True`） |

---

## 7. 环境变量全集（Sakrylle Web 配置清单）

### 核心必填

```bash
WEBUI_SECRET_KEY=<≥32字节随机值>          # JWT 签名密钥 (env.py:614)，必填
WEBUI_AUTH=True                            # 启用认证
DATA_DIR=/data/sakrylle-web                # 数据目录隔离
WEBUI_URL=https://chat.sakrylle.com        # 回调 URL 构建基础 (config.py:2488)
```

### 品牌

```bash
WEBUI_NAME=Sakrylle Web                    # 需先修复 env.py:772-773 bug
ENABLE_COMMUNITY_SHARING=False
```

### OpenAI / Sakrylle API 接入

```bash
OPENAI_API_BASE_URLS=https://api.sakrylle.com/v1
OPENAI_API_KEYS=sk-<sakrylle_api_key>      # 建议用管理员 key 或 sakrylle-web 专用 key
ENABLE_OLLAMA_API=False
ENABLE_OPENAI_API=True
```

### OIDC SSO（依赖 sub2api OIDC 改造完成后填入）

```bash
# 前置条件：sub2api 已实现 /.well-known/openid-configuration + id_token + 平铺 userinfo
OAUTH_CLIENT_ID=sakrylle-web
OAUTH_CLIENT_SECRET=<sakrylle-web client_secret>    # 机密 client，有后端存储
OPENID_PROVIDER_URL=https://sub.sakrylle.com/.well-known/openid-configuration
OAUTH_PROVIDER_NAME=Sakrylle API
OAUTH_SCOPES=openid email profile models:read
OAUTH_EMAIL_CLAIM=email                    # 对应 OIDC 标准平铺 userinfo 的 'email'
OAUTH_USERNAME_CLAIM=name                  # 对应 OIDC 标准平铺 userinfo 的 'name'
ENABLE_OAUTH_SIGNUP=True
DEFAULT_USER_ROLE=user                     # 避免 pending 需手动激活
ENABLE_LOGIN_FORM=False                    # 关闭密码登录，强制走 Sakrylle SSO
OAUTH_AUTO_REDIRECT=True                   # 单 provider 时自动跳转
OAUTH_MERGE_ACCOUNTS_BY_EMAIL=True
ENABLE_SIGNUP=False                        # 关闭本地密码注册
```

### 可选

```bash
DATABASE_URL=postgresql+asyncpg://...      # 替换 SQLite，生产环境推荐
REDIS_URL=redis://...                      # 多实例 WebSocket 同步
WEBUI_ADMIN_EMAIL=admin@sakrylle.com       # 首次启动自动创建管理员
WEBUI_ADMIN_PASSWORD=<管理员密码>
WEBUI_ADMIN_NAME=Sakrylle Admin
```

---

## 8. 不确定项

| 编号 | 问题 | 影响 | 建议 |
|---|---|---|---|
| U1 | authlib `server_metadata_url` 指向 `/.well-known/oauth-authorization-server` 时是否能成功拉取（authlib 是否只接受 `/.well-known/openid-configuration` 路径名）| 若 authlib 只接受后者，则 sub2api 必须先实现 openid-configuration 别名才能对接 | **实测**：在本地临时运行 open-webui，设 `OPENID_PROVIDER_URL` 指向 sub2api 的 RFC 8414 端点，观察 authlib 日志 |
| U2 | open-webui 主题色配置路径（Tailwind config / CSS 变量）在哪个文件，Monet Purple 应替换哪些变量 | 影响品牌视觉改造工作量 | 进一步调研 `src/app.css`、Tailwind config、`src/lib/components/ui/` 中的 CSS 变量 |
| U3 | sub2api `/.well-known/oauth-authorization-server` 的 `userinfo_endpoint` 已指向 `issuer + "/v1/me"`（`oauth_provider_handler.go:633`），authlib 能否用 opaque access_token 调用 `/v1/me` 拿到 userinfo | 决定是否必须先完成 id_token 才能对接，还是可以先用 userinfo endpoint 回退路径 | 实测 authlib OIDC 回退行为 |
| U4 | ✅ 已解决（2026-06-03）：Sakrylle Web 生产域名确认为 `chat.sakrylle.com`（非 `web.sakrylle.com`） | 影响 redirect_uri 白名单注册和 Nginx 配置 | 用户确认；实现期把 `chat.sakrylle.com/oauth/oidc/login/callback` 登记进 sub2api redirect 白名单 |

---

## 9. 后续问题

1. **sub2api OIDC 改造的优先级**：Sakrylle Web 的 SSO 接入完全依赖 sub2api 实现 `/.well-known/openid-configuration` + `id_token` + 平铺 userinfo；若 sub2api OIDC 工期较长，应先以密码登录模式运行 Sakrylle Web（`ENABLE_LOGIN_FORM=True`，手动建账）。
2. **`sakrylle-web` client 是否已在 sub2api `oauth_clients` 表注册**：迁移 148 (`148_oauth_v2_sakrylle_seed.sql`) 未含 `sakrylle-web`，需在 Phase 0 补充。
3. **per-user API key 模式 vs 共享 key 模式**：两者计费语义完全不同，需在开发计划中明确选择，影响 Sakrylle Web 的 UI 改造范围（是否需要引导用户填入个人 key）。
4. **Sakrylle Web 是否需要 offline_access / refresh_token**：open-webui 有后端（FastAPI），可以机密 client 存储 refresh_token（server-side），是比纯 SPA 更安全的模式，见 `docs/OAUTH_V2_DESIGN.md:297-304`。

---

*交叉引用：`02-sakrylle-api-oauth-current-state.md`、`03-sakrylle-api-oidc-architecture.md`、`05-configuration-isolation-standard.md`*
