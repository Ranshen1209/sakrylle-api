---
title: 31 · Sakrylle Web 改造开发计划（open-webui fork）
status: local
scope: product
last_verified: 2026-06-06
---

> **时态说明（2026-06-06）**：本文保留了早期调研/计划中的历史口径。关于 Sakrylle API OIDC Provider 的当前事实，以 [`../../10-platform-identity/current-state.md`](../../10-platform-identity/current-state.md) 为准；`2026-06-03 前关于 OIDC Provider 能力的旧判断`、`2026-06-03 前关于 OIDC 基座状态的旧判断`、`2026-06-03 前关于 Web SSO 后端依赖的旧判断` 等表述仅适用于 2026-06-03 前的历史背景。

# 31 · Sakrylle Web 改造开发计划（open-webui fork）

**文档日期**：2026-06-03
**版本状态**：规划文档（未上线产品），所有生产操作步骤须经审批 gating。
**核心原则**：**尽量用配置而非改码**。3 处必须代码改动（WEBUI_NAME 后缀 bug、APP_NAME 常量、页面 title），其余能用 env 变量 / volume mount 解决的绝不动源码。
**上游仓库**：`open-webui/open-webui`；Fork 目标：`Ranshen1209/sakrylle-web`（fork 仓库尚未创建，需在 Phase 0 操作）
**部署域名**：`chat.sakrylle.com`（已确认 2026-06-03）
**SSO 方式**：open-webui 原生 OIDC 对接 sub2api，issuer = `https://sub.sakrylle.com`（已确认 2026-06-03）；回调路径已按 open-webui `oidc` provider 路由确认为 `https://chat.sakrylle.com/oauth/oidc/login/callback`，需登记到 sub2api redirect 白名单

---

## 关键依赖关系总览

```
Phase 0（调研与保护）
    ↓ 串行
Phase 1（OpenAI 接入 + 最小可用品牌）   ← 不依赖 sub2api OIDC，立即可启动
    ↓ 串行
Phase 2（sub2api OIDC + Sakrylle Web SSO 对接）  ← 依赖 03-oidc-architecture.md Phase 1 完成
    ↓ 串行
Phase 3（完整权限/审计/配置隔离）
    ↓ 串行
Phase 4（测试/发布/回滚）
```

**Phase 1 可以先于 Phase 2 完成并上线**（用密码登录模式运行，OpenAI 接入 Sakrylle API，先正常提供服务）。Phase 2 是 SSO 接入，需等 sub2api OIDC 改造完成。

---

## Phase 0 · 调研与保护

**目标**：在任何代码改动之前，冻结上游快照、确认计划域名、核实 client 注册状态、确认配置方案可行性。

### 任务列表（串行）

- [ ] **P0-1：Fork 上游 + 固定基础分支**
  - 目标：创建 Sakrylle Web fork，固定改造基础版本，避免合并时混入过多 diff
  - 涉及操作：
    - 在 GitHub 创建 `Ranshen1209/sakrylle-web` fork（基于 `open-webui/open-webui` 最新稳定 tag）
    - 创建 `theme/sakrylle` 分支（命名与 relay-pulse fork 保持一致）
    - 记录基础 commit hash，写入 `CLAUDE.md` 的 Companion services 章节
  - 验收标准：fork 存在，`theme/sakrylle` 分支创建，CI 绿（上游 CI 通过）

- [ ] **P0-2：确认生产域名 + Nginx 配置规划**（只读操作，无需变更生产）
  - 目标：生产域名已确认为 `chat.sakrylle.com`（已确认 2026-06-03）；规划 Nginx conf 文件名
  - 涉及文件：服务器 `/opt/stack/nginx/conf.d/`
  - 实施说明：**只读**，ssh 到 `ssh-tokyo` 查看现有 nginx conf，确认命名空间；不在此 Phase 创建任何配置
  - 验收标准：Nginx conf 文件名规划记录在本文档 §附录

- [ ] **P0-3：核实 `sakrylle-web` client 是否已注册**
  - 目标：确认 sub2api `oauth_clients` 表是否有 `sakrylle-web` 行，以及其 `redirect_uris`
  - 涉及说明：迁移 `148_oauth_v2_sakrylle_seed.sql` 未包含 `sakrylle-web`（见 `30-sakrylle-web-research.md §9`）；需直接查 DB 确认当前状态
  - 实施说明：**只读 SQL**（`SELECT client_id, redirect_uris, allowed_scopes FROM oauth_clients WHERE client_id='sakrylle-web';`）；不在此 Phase 插入
  - 验收标准：给出"已存在/不存在"结论，若存在记录现有 redirect_uris

- [ ] **P0-4：实测 authlib discovery fallback 行为**（「不确定项 U1」验证）
  - 目标：确认 open-webui authlib 在 `OPENID_PROVIDER_URL` 指向 `/.well-known/oauth-authorization-server` 时是否能成功注册（决定 sub2api 是否必须先加 `/.well-known/openid-configuration` 才能 Phase 1 密码模式之后测试 SSO）
  - 实施说明：本地临时启动 open-webui Docker，设 `OPENID_PROVIDER_URL=https://sub.sakrylle.com/.well-known/oauth-authorization-server`，观察启动日志 authlib discovery 请求结果；**不影响生产**
  - 验收标准：记录结论（能/不能）；若不能，则确认 Phase 2 必须先完成 G1（openid-configuration 别名）

- [ ] **P0-5：调研 open-webui Tailwind/CSS 主题变量路径**（「不确定项 U2」）
  - 目标：找到 Monet Purple `#9181bd` 应替换的 CSS 变量 / Tailwind 配置位置
  - 涉及文件：`src/app.css`、Tailwind config（「不确定」路径），`src/lib/components/ui/` 下 CSS 变量声明
  - 验收标准：列出需替换的文件和变量名，估算工作量（代码改动数量）

---

## Phase 1 · OpenAI 接入 + 最小可用品牌（不依赖 sub2api OIDC）

**目标**：Sakrylle Web 能以**密码登录模式**正常运行，接入 Sakrylle API（OpenAI endpoint），品牌最小改造（3 处必须改代码，其余 env + volume）。此 Phase 完成即可对内提供服务，SSO 在 Phase 2 再开。

**依赖项**：Phase 0 全部完成。

### 任务列表

以下任务中，P1-1、P1-2、P1-3 串行（代码改动后一起 build）；P1-4 可并行准备图片资源。

- [ ] **P1-1：修复 WEBUI_NAME 后缀 bug**（必须代码改动，串行第 1 步）
  - 目标：使 `WEBUI_NAME=Sakrylle Web` 能生效，不被追加 `' (Open WebUI)'`
  - 涉及文件：`backend/open_webui/env.py`，第 772–773 行
  - 实施说明：删除以下两行（或将判断条件改为 `!= 'Open WebUI' and != 'Sakrylle Web'`，推荐直接删除）：
    ```python
    # 删除这两行：
    if WEBUI_NAME != 'Open WebUI':
        WEBUI_NAME += ' (Open WebUI)'
    ```
  - 验收标准：启动容器后 API `/api/config` 或 `/api/v1/auths/config` 返回的 `name` 字段值为 `Sakrylle Web`（无后缀）

- [ ] **P1-2：修改前端 APP_NAME 常量**（必须代码改动，串行第 2 步）
  - 目标：前端 SvelteKit 品牌名为 `Sakrylle Web`
  - 涉及文件：`src/lib/constants.ts:4`
  - 实施说明：`export const APP_NAME = 'Sakrylle Web';`
  - 验收标准：build 后页面侧边栏品牌文字显示 `Sakrylle Web`

- [ ] **P1-3：修改页面 title**（必须代码改动，串行第 3 步）
  - 目标：浏览器 tab 标题为 `Sakrylle Web`
  - 涉及文件：`src/app.html:118`
  - 实施说明：`<title>Sakrylle Web</title>`
  - 验收标准：浏览器 tab 显示 `Sakrylle Web`

- [ ] **P1-4：准备品牌图片资源**（可并行，准备 volume mount 所需文件）
  - 目标：制作/导出 Sakrylle 樱花 Logo 在 open-webui 各尺寸的版本
  - 品牌规范：线描樱花（5 瓣，白心，coral `#ffab91` → hot-pink `#f06292` 渐变描边），参考 `/Users/ariel/Documents/Design/Material/cherry-blossom_15273565.png`
  - 需要的文件（路径为 volume mount 目标路径 `static/static/`）：

    | 文件名 | 尺寸 | 说明 |
    |---|---|---|
    | `favicon.ico` | 16×16、32×32 多尺寸 ICO | 网站 favicon |
    | `favicon.png` | 32×32 PNG | favicon |
    | `favicon.svg` | — | 矢量 favicon |
    | `favicon-96x96.png` | 96×96 | 高分辨率 |
    | `splash.png` | 160×160+ | 亮色背景首屏 logo |
    | `splash-dark.png` | 160×160+ | 暗色背景首屏 logo（白色/浅色版本） |
    | `logo.png` | 96×96 或更高 | 侧边栏 logo |
    | `web-app-manifest-192x192.png` | 192×192 | PWA 图标 |
    | `web-app-manifest-512x512.png` | 512×512 | PWA 大图标 |
    | `site.webmanifest` | — | 文本文件，修改 `name`/`short_name` 为 `Sakrylle Web` |

  - 验收标准：所有图片文件准备完毕，放入 `static/static/` volume 目录后容器即可引用

- [ ] **P1-5：Docker Compose 配置（隔离 + 接入 Sakrylle API）**
  - 目标：Sakrylle Web 容器独立运行，数据与官方 open-webui 隔离；接入 Sakrylle API
  - 涉及文件：`/opt/stack/docker-compose.yml`（**生产操作，需审批**）、服务器 `/opt/stack/sakrylle-web/` 目录（新建）
  - 实施说明：在 `/opt/stack/` 下创建 `sakrylle-web/` 目录，新建 `.env` 文件（mode 600），compose 配置参考：

    ```yaml
    # /opt/stack/docker-compose.yml 中新增 service：
    sakrylle-web:
      image: ghcr.io/ranshen1209/sakrylle-web:latest   # 或 :theme-sakrylle
      restart: unless-stopped
      volumes:
        - sakrylle-web-data:/app/backend/data
        - /opt/stack/sakrylle-web/static:/app/backend/open_webui/static/static:ro
      env_file:
        - /opt/stack/sakrylle-web/.env
      networks:
        - stack_default

    volumes:
      sakrylle-web-data:
        name: stack_sakrylle-web-data
    ```

    `.env` 内容（mode 600，明文敏感值）：
    ```bash
    WEBUI_SECRET_KEY=<≥32字节随机值>
    WEBUI_AUTH=True
    WEBUI_NAME=Sakrylle Web
    DATA_DIR=/app/backend/data
    WEBUI_URL=https://chat.sakrylle.com
    OPENAI_API_BASE_URLS=https://api.sakrylle.com/v1
    OPENAI_API_KEYS=<sakrylle_web_api_key>
    ENABLE_OLLAMA_API=False
    ENABLE_OPENAI_API=True
    ENABLE_SIGNUP=False
    DEFAULT_USER_ROLE=user
    ENABLE_COMMUNITY_SHARING=False
    WEBUI_ADMIN_EMAIL=admin@sakrylle.com
    WEBUI_ADMIN_PASSWORD=<管理员密码>
    WEBUI_ADMIN_NAME=Sakrylle Admin
    ```

  - **生产 gating**：此任务需审批后执行
  - 验收标准：`docker compose up -d sakrylle-web` 后容器健康，`https://chat.sakrylle.com` 可访问，密码登录可用，`/v1/models` 返回 Sakrylle API 模型列表

- [ ] **P1-6：Nginx 配置（`sakrylle-web.conf`）**（生产操作，需审批）
  - 目标：`chat.sakrylle.com` 通过 Nginx 反代到 `sakrylle-web` 容器
  - 涉及文件：`/opt/stack/nginx/conf.d/sakrylle-web.conf`（新建）
  - 实施说明：
    ```nginx
    server {
        listen 8443 ssl;
        server_name chat.sakrylle.com;
        # SSL 沿用 /opt/stack/certs/live/sakrylle.com/ 通配符证书
        ssl_certificate /opt/stack/certs/live/sakrylle.com/fullchain.pem;
        ssl_certificate_key /opt/stack/certs/live/sakrylle.com/privkey.pem;

        location / {
            proxy_pass http://sakrylle-web:8080;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-Proto https;
            # WebSocket 支持（open-webui 需要）
            proxy_http_version 1.1;
            proxy_set_header Upgrade $http_upgrade;
            proxy_set_header Connection "upgrade";
        }
    }
    ```
  - 注意：`sakrylle-web` 容器默认端口为 `8080`（open-webui 默认），需确认（「不确定」：如与其他容器端口冲突需改）
  - **生产 gating**：此任务需审批后执行
  - 验收标准：`curl -sS https://chat.sakrylle.com/health` 返回正常

- [ ] **P1-7：DNS 记录（`chat.sakrylle.com`）**（生产操作，需审批）
  - 目标：`chat.sakrylle.com` A 记录指向 `64.83.47.108`
  - 实施说明：沿用 Cloudflare DNS API（`/opt/stack/secrets/cloudflare.ini`），格式参见 `CLAUDE.md §Cloudflare DNS`；**DNS-only（不代理）**，与其他 sakrylle.com 子域名一致
  - **生产 gating**：此任务需审批后执行
  - 验收标准：`dig chat.sakrylle.com A` 返回 `64.83.47.108`

- [ ] **P1-8：GHA 构建流程（`.github/workflows/build-image.yml`）**
  - 目标：Push `theme/sakrylle` 分支触发构建，产出 `ghcr.io/ranshen1209/sakrylle-web:latest`（及 `:theme-sakrylle`）
  - 涉及文件：`Ranshen1209/sakrylle-web` 仓库中的 `.github/workflows/build-image.yml`（从上游或 sub2api 仓库参考模板复制）
  - 实施说明：image 名称小写（GHCR 要求），`sakrylle-web` tag 策略与 sub2api `purple` tag 一致
  - 验收标准：Push 后 GHA 构建成功，`ghcr.io/ranshen1209/sakrylle-web:latest` 可 `docker pull`

---

## Phase 2 · sub2api OIDC 对接（SSO 接入）

**目标**：Sakrylle Web 通过 OIDC SSO 实现单点登录，用户用 Sakrylle API 账号登录 open-webui。
**依赖项**：
1. Phase 1 全部完成（Sakrylle Web 已运行）
2. `03-sakrylle-api-oidc-architecture.md` Phase 1 全部完成（sub2api 已实现 `/.well-known/openid-configuration` + `id_token` + 平铺 userinfo endpoint）

**不依赖 Phase 2 才能解锁的内容**：密码登录模式在 Phase 1 已可用，Phase 2 只是叠加 SSO。

### 任务列表

- [ ] **P2-1：在 sub2api 注册 `sakrylle-web` OAuth client**（⚠ 生产操作，需审批）
  - 目标：`oauth_clients` 表中新增 `sakrylle-web` 机密 client
  - 涉及说明：`sakrylle-web` 是 **机密 client**（有后端 FastAPI，可安全存储 `client_secret`）；需要一个安全随机的 `client_secret`（bcrypt hash 存 DB，明文存 `/opt/stack/sakrylle-web/.env`）
  - 实施说明（**仅在非生产环境验证 SQL 后再上生产**）：
    ```sql
    INSERT INTO oauth_clients (
        client_id,
        name,
        client_type,
        app_type,
        redirect_uris,
        allowed_scopes,
        default_scopes,
        allowed_origins,
        pkce_required,
        device_flow_enabled,
        client_secret_hash,
        access_token_ttl_seconds,
        refresh_token_ttl_seconds
    ) VALUES (
        'sakrylle-web',
        'Sakrylle Web',
        'confidential',
        'web',
        '["https://chat.sakrylle.com/oauth/oidc/login/callback"]'::jsonb,
        '["profile:read","account:read","models:read","chat.completions:create","responses:create","messages:create","usage:read","offline_access","openid","profile","email"]'::jsonb,
        '["profile:read","account:read","models:read","chat.completions:create","responses:create","messages:create","usage:read","offline_access","openid","profile","email"]'::jsonb,
        '["https://chat.sakrylle.com"]'::jsonb,
        TRUE,
        FALSE,
        '<bcrypt hash of client_secret>',
        86400,
        2592000
    ) ON CONFLICT (client_id) DO NOTHING;
    ```
  - 注意：`redirect_uris` 中的回调路径 `/oauth/oidc/login/callback` 是 open-webui 的标准路由（`main.py:2809`，provider 名为 `oidc`，所以路径是 `/oauth/oidc/login/callback`）
  - **开发环境同步添加 `http://localhost:3000/oauth/oidc/login/callback`**（如需本地调试）
  - **生产 gating**：此任务需审批后执行
  - 验收标准：`SELECT client_id, redirect_uris FROM oauth_clients WHERE client_id='sakrylle-web';` 返回正确行

- [ ] **P2-2：确认 sub2api 平铺 userinfo 字段名（与 open-webui claim 映射对齐）**
  - 目标：确认 sub2api OIDC userinfo 端点（`/v1/me` OIDC 分支，见 `03` G7）返回的 JSON 字段名，确保与 open-webui 的 `OAUTH_EMAIL_CLAIM=email` 和 `OAUTH_USERNAME_CLAIM=name` 对应
  - 实施说明：只读调用 `GET /v1/me` 携带 OIDC scope 的 access_token，观察返回结构；标准 OIDC userinfo 应返回：`sub`（user.id）、`email`、`email_verified`、`name`（需 profile scope）、`picture`（可选）。`email:read` 对第一方 client（Web/Chat）默认授予，登录后可直接拿到 email（已确认 2026-06-03），无需用户额外勾选 email scope
  - id_token 签名算法：RS256 + ES256（已确认 2026-06-03）；issuer = `https://sub.sakrylle.com`
  - 验收标准：能通过 `OAUTH_EMAIL_CLAIM=email`、`OAUTH_USERNAME_CLAIM=name` 无误拿到用户 email 和 display name

- [ ] **P2-3：在 `.env` 中填入 OIDC SSO 配置**
  - 目标：更新 `/opt/stack/sakrylle-web/.env`，启用 OIDC SSO
  - 涉及文件：`/opt/stack/sakrylle-web/.env`（mode 600）
  - 新增/修改的 env 变量（追加到现有文件）：
    ```bash
    # OIDC SSO（Phase 2 启用）
    OAUTH_CLIENT_ID=sakrylle-web
    OAUTH_CLIENT_SECRET=<client_secret 明文，仅存于此文件>
    OPENID_PROVIDER_URL=https://sub.sakrylle.com/.well-known/openid-configuration
    OAUTH_PROVIDER_NAME=Sakrylle API
    OAUTH_SCOPES=openid email profile models:read offline_access
    OAUTH_TOKEN_ENDPOINT_AUTH_METHOD=client_secret_post
    OAUTH_EMAIL_CLAIM=email
    OAUTH_USERNAME_CLAIM=name
    ENABLE_OAUTH_SIGNUP=True
    ENABLE_LOGIN_FORM=False
    OAUTH_AUTO_REDIRECT=True
    OAUTH_MERGE_ACCOUNTS_BY_EMAIL=True
    ENABLE_SIGNUP=False
    ```
  - 部署方式：`docker compose restart sakrylle-web`（修改 env 后重启即可，不需重新 build）
  - 验收标准：重启后访问 `https://chat.sakrylle.com`，能看到 `Sakrylle API` 的 SSO 按钮并成功跳转

- [ ] **P2-4：SSO 登录全流程验证**
  - 目标：确认完整 OIDC 登录流程无误
  - 验收检查清单：
    - [ ] 访问 `https://chat.sakrylle.com` → 自动跳转 `https://sub.sakrylle.com/oauth/authorize`（OAUTH_AUTO_REDIRECT=True）
    - [ ] sub2api consent 页显示 `Sakrylle Web` 名称和请求的 scopes
    - [ ] 用户授权后回跳 `https://chat.sakrylle.com/oauth/oidc/login/callback`
    - [ ] open-webui 成功建账（角色为 `user`，非 `pending`）
    - [ ] 模型列表显示 Sakrylle API 的模型
    - [ ] 能正常发送消息（chat completions 请求到 `api.sakrylle.com/v1`）
    - [ ] sub2api `usage_logs` 表有记录（计费正常）

---

## Phase 3 · 完整权限 / 审计 / 配置隔离

**目标**：生产级配置隔离、主题色改造、per-user API key 模式（可选）、OIDC session 管理、权限收紧。
**依赖项**：Phase 2 完成。

### 任务列表（可部分并行）

- [ ] **P3-1：Monet Purple 主题色改造**（串行，依赖 P0-5 调研结论）
  - 目标：UI 主色替换为 Monet Purple `#9181bd`，强调色 Cherry-blossom pink `#ec6a9c`，中性色 slate
  - 涉及文件：「不确定」（待 P0-5 调研确认）—— 预期在 `src/app.css`、Tailwind config、或 CSS 变量声明文件
  - 品牌规范：主渐变 `linear-gradient(135deg, #9181bd 0%, #7b6aab 100%)`；樱花粉仅用于 logo/高亮/CTA；中性色 slate 50–950
  - 验收标准：Admin UI 和用户界面主色视觉检验通过（截图对比）

- [ ] **P3-2：per-user API key 模式（可选，并行）**
  - 目标：引导 OIDC 登录后的用户在 Settings 填入自己的 Sakrylle API Key，实现各自计费隔离
  - 实施说明：open-webui 原生支持 per-user API key（Settings → Account → API Keys）；只需在 open-webui Admin UI 开放此功能，并在文档/欢迎页中引导用户；**无需代码改动**
  - 如需全局共享 key + per-user 隔离（按用户 ID 分配 Sakrylle API Key）：「不确定」：open-webui 是否支持 OIDC 登录后自动为用户创建对应 Sakrylle API Key；此特性可能需要 webhook 或自定义脚本，工作量待评估
  - 验收标准：用户能在 Settings 看到 API Key 配置项，填入后模型调用走该 key 计费

- [ ] **P3-3：OIDC session 管理 + backchannel logout**（可选，并行）
  - 目标：用户在 sub2api 撤销授权后，open-webui 侧 session 同步失效
  - 涉及 env 变量：`ENABLE_OAUTH_BACKCHANNEL_LOGOUT=True`（需 sub2api 侧实现 backchannel logout 端点，详见 `03`）
  - 实施说明：暂时标记为可选，优先级低于核心功能
  - 验收标准：在 sub2api `https://sub.sakrylle.com/api/v1/oauth/authorized-apps` 撤销 `sakrylle-web` 授权后，刷新 open-webui 页面需重新登录

- [ ] **P3-4：关闭 open-webui 社区分享等对外功能**
  - 目标：收紧 open-webui 默认开放的外部功能，符合 Sakrylle 私有部署定位
  - 涉及 env 变量（追加到 `.env`）：
    ```bash
    ENABLE_COMMUNITY_SHARING=False
    ENABLE_MODEL_FILTER=False          # 可选：强制只显示 Sakrylle API 模型
    SHOW_ADMIN_DETAILS=False           # 向非 admin 用户隐藏管理员详情
    ```
  - 验收标准：UI 中不出现 Community/分享到社区相关入口

- [ ] **P3-5：开发环境 client 注册（本地调试用）**
  - 目标：在 sub2api 注册 `sakrylle-web-dev` client，含 localhost 回调，供本地开发使用
  - 实施说明：`redirect_uris` 含 `http://localhost:3000/oauth/oidc/login/callback`；仅在 staging/开发环境执行，**不上生产**
  - 验收标准：本地 open-webui 开发环境能完成 OIDC 登录

---

## Phase 4 · 测试 / 发布 / 回滚

**目标**：上线前全面验证，建立回滚路径，更新运维文档。

### 任务列表（串行）

- [ ] **P4-1：集成测试清单验证**
  - 验收检查清单：
    - [ ] 密码登录模式（作为 SSO 降级方案）：admin 账号密码登录可用
    - [ ] OIDC SSO：完整 flow（见 P2-4 检查清单）
    - [ ] 模型列表：显示 Sakrylle API 配置的模型（Claude / GPT / DeepSeek 按 group key 显示）
    - [ ] Chat completions：多轮对话正常，流式 SSE 正常
    - [ ] Image generation（若配置了 GPT-Image group key）：`/v1/images/generations` 可用
    - [ ] sub2api `usage_logs`：每次对话有计费记录
    - [ ] 余额不足：sub2api 返回 402/insufficient_quota 时 open-webui 界面正确展示错误
    - [ ] Volume mount 品牌图片：favicon、splash、logo 均显示 Sakrylle 樱花
    - [ ] WEBUI_NAME：页面显示 `Sakrylle Web`（无后缀）
    - [ ] 主题色：Monet Purple 显示正确
    - [ ] PWA 安装：`site.webmanifest` name 显示 `Sakrylle Web`
    - [ ] WebSocket：实时流式输出正常（Nginx 配置 `Upgrade: websocket` 正确）
    - [ ] 数据隔离：volume `stack_sakrylle-web-data` 与其他容器无共享

- [ ] **P4-2：回滚方案记录**
  - 目标：明确 Sakrylle Web 回滚路径（因其为新服务，回滚 = 停服，不影响任何现有生产服务）
  - 实施说明：
    - 停服：`docker compose stop sakrylle-web`
    - 保留数据：volume `stack_sakrylle-web-data` 保留，待修复后重启
    - sub2api OIDC 回滚：`sakrylle-web` client 只需 `UPDATE oauth_clients SET disabled=true WHERE client_id='sakrylle-web'`（不删除，保留 grant 记录）；open-webui 侧删除 `OPENID_*` env 变量后重启即退回密码登录模式
  - 验收标准：回滚步骤经过模拟测试，耗时估算 < 5 分钟

- [ ] **P4-3：更新运维文档**（不创建 markdown 文件，更新 `CLAUDE.md`）
  - 目标：在 `CLAUDE.md §Companion services` 章节补充 Sakrylle Web 运维信息
  - 内容：
    - 部署命令：`docker compose pull sakrylle-web && docker compose up -d sakrylle-web`
    - 日志：`docker compose logs --tail=50 sakrylle-web`
    - env 位置：`/opt/stack/sakrylle-web/.env`（mode 600）
    - volume：`stack_sakrylle-web-data`
    - OIDC client：`sakrylle-web`（机密 client，refresh token TTL 30 天）
  - 验收标准：`CLAUDE.md` 更新完成，运维信息准确

---

## 配置-改码决策矩阵（决策依据汇总）

| 改造项 | 方式 | 理由 |
|---|---|---|
| OpenAI endpoint 接入 Sakrylle API | **纯 env 变量** | open-webui 原生支持多 endpoint 配置，Admin UI 可动态更新 |
| OIDC SSO 对接 | **纯 env 变量** | open-webui 原生 Generic OIDC，无需改代码 |
| `DEFAULT_USER_ROLE`、`ENABLE_LOGIN_FORM`、`ENABLE_SIGNUP` 等 | **纯 env 变量** | open-webui ConfigVar 系统支持 env 覆盖 |
| 品牌图片（favicon、splash、logo、manifest） | **volume mount 覆盖** | open-webui `STATIC_DIR` 支持外挂；不需重新 build 镜像 |
| WEBUI_NAME 后缀 bug（`env.py:772-773`） | **必须代码改动** | env 变量无法绕过此 hardcode 逻辑 |
| 前端 APP_NAME（`constants.ts:4`） | **必须代码改动** | 编译时常量，env 无法覆盖 |
| 页面 title（`app.html:118`） | **必须代码改动** | 静态 HTML，不读取任何 env 或运行时变量 |
| Monet Purple 主题色 | **代码改动（可能）** | 「不确定」：待 P0-5 调研后确认；若是 CSS 变量可能也可 volume mount |
| Docker volume 命名隔离 | **compose 配置** | volume name 在 compose YAML 中指定，非代码改动 |
| sub2api `sakrylle-web` client 注册 | **直接 SQL**（生产需审批） | 与其他 client 相同方式；不修改 sub2api 代码 |

---

## §附录：已确认的引用路径

以下路径均已在本次调研中验证真实存在（本地 open-webui 仓库路径 `/Volumes/APFS_HD/Documents/Github/open-webui/`，sub2api 路径 `/Volumes/APFS_HD/Documents/Github/sub2api/`）：

| 引用 | 确认路径 |
|---|---|
| sub2api OAuth routes | `backend/internal/server/routes/oauth.go:57` |
| sub2api discovery metadata handler | `backend/internal/handler/oauth_provider_handler.go:625-647` |
| sub2api userinfo_endpoint | `backend/internal/handler/oauth_provider_handler.go:633` → `issuer + "/v1/me"` |
| sub2api /v1/me 嵌套结构 | `backend/internal/handler/oauth_provider_account_handler.go:161-222` |
| open-webui OIDC provider 注册 | `backend/open_webui/config.py:3868-3906` |
| open-webui callback 路由 | `backend/open_webui/main.py:2809` → `/oauth/{provider}/login/callback` |
| open-webui userinfo email 提取（flat only） | `backend/open_webui/utils/oauth.py:1596-1597` |
| open-webui userinfo username 提取（flat only）| `backend/open_webui/utils/oauth.py:1718-1720` |
| WEBUI_NAME 后缀 bug | `backend/open_webui/env.py:771-773` |
| 前端 APP_NAME | `src/lib/constants.ts:4` |
| 页面 title | `src/app.html:118` |
| sakrylle-web client 部署域名 | `https://chat.sakrylle.com`（已确认 2026-06-03）；OIDC 回调用 open-webui 标准路由 `/oauth/oidc/login/callback`，实现期核实并登记到 sub2api redirect 白名单 |
| sub2api migration 148（无 sakrylle-web） | `backend/migrations/148_oauth_v2_sakrylle_seed.sql` |

---

*交叉引用：`02-sakrylle-api-oauth-current-state.md`、`03-sakrylle-api-oidc-architecture.md`、`05-configuration-isolation-standard.md`、`30-sakrylle-web-research.md`*
