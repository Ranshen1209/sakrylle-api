---
title: 41 — Sakrylle Chat 改造开发计划
status: local
scope: product
last_verified: 2026-06-06
---

# 41 — Sakrylle Chat 改造开发计划

> 文档类型：可执行开发计划  
> 基准：kelivo 1.1.15+52  
> 目标产品：Sakrylle Chat（com.sakrylle.chat）  
> 关联调研：见 40-sakrylle-chat-research.md  
> 关联 OIDC 后端：见 03-sakrylle-api-oidc-architecture.md  
> 安全提示：Sakrylle API + Sakrylle Image 已在生产，任何涉及生产后端的步骤须经审批后执行

---

## 概览

Sakrylle Chat 是 kelivo 的 fork，定位为接入 Sakrylle 生态的跨平台 LLM 聊天客户端，支持 Android / iOS / macOS / Windows / Linux 五个平台（**不发布 Flutter Web，仅移动 / 桌面**，已确认 2026-06-03）。改造分 4 个阶段：

| Phase | 目标 | 预估工期 |
|-------|------|----------|
| Phase 0 | 隔离保护：fork 初始化 + 配置隔离（applicationId/bundleId）| 0.5 天 |
| Phase 1 | 最小可用：预置 Sakrylle provider + 品牌基础（名称/颜色） | 1 天 |
| Phase 2 | 品牌完整 + 存储安全：图标/主题/API Key 加固 | 1 天 |
| Phase 3 | OAuth/OIDC 登录：PKCE Auth Code Flow + 自定义 scheme | 2–3 天 |
| Phase 4 | 测试/发布/回滚 | 1 天 |

**总工期估算：5.5–6.5 天**（并行执行部分任务可缩短至 4–5 天）

---

## 认证策略决策：API Key 起步 vs OAuth/OIDC 先行

### 方案 A：纯 API Key 起步（推荐初期路线）

**优势**：
- 无需改动 Sakrylle API 后端
- flutter_web_auth_2 + 自定义 scheme 零依赖
- 与 kelivo 一致的使用体验，用户已熟悉
- Phase 1–2 即可发布可用版本

**劣势**：
- 用户需手动复制粘贴 API Key
- 无单点登录体验
- API Key 以明文存于 shared_preferences（无 secure_storage）

**适用场景**：Sakrylle API 的 OIDC 层（见 03 文档）尚在规划阶段，先上线基础功能，OIDC 作为 Phase 3 增量。

### 方案 B：OAuth/OIDC Authorization Code + PKCE（完整方案，已确认为最终认证模型 2026-06-03）

**确认参数（2026-06-03）**：public client `client_id=sakrylle-chat`；回调 scheme = `sakrylle-chat://oauth/callback`（实现期核实 migration 148 是否已预置）；scope = `openid profile email` + `/v1`；issuer = `https://sub.sakrylle.com`；id_token 签名 RS256 + ES256；`email:read` 对第一方默认授予，登录后可直接拿到 email。


**优势**：
- 单点登录，用户无需复制粘贴 API Key
- access_token 短 TTL（24h），refresh_token 支持无感续期
- 与 Sakrylle Image 等其他客户端体验一致

**劣势**：
- 需 Sakrylle API 后端完成 OIDC 基础层（id_token、JWKS、redirect_uri 允许自定义 scheme）
- 需为每个平台（iOS/Android/macOS/Windows/Linux）注册自定义 URL scheme
- 工程量 2–3 天，且强依赖后端进度

**取舍建议（已确认 2026-06-03）**：最终认证模型为方案 B（OAuth/OIDC Auth Code + PKCE）。落地节奏：Phase 1–2 以 API Key 模式先上线，Phase 3 在 OIDC 后端完成后追加 OAuth 登录功能。两种方式可在 UI 中并存（"使用账号登录"按钮 + "手动填写 API Key"入口）。

---

## Phase 0 — Fork 初始化与配置隔离

**目标**：建立独立的 `sakrylle-chat` Flutter 项目，与 kelivo 完全沙盒隔离，无冲突共存。

**包标识与遥测（已确认 2026-06-03）**：包标识统一 `com.sakrylle.*`，Chat = `com.sakrylle.chat`（Android applicationId + iOS / macOS bundle id）；遥测默认关闭。

### 任务列表

- [ ] **0-1：Fork 并建立维护分支**（串行，最先执行）
  - 目标：在 GitHub fork kelivo 到 `Ranshen1209/sakrylle-chat`，创建工作分支 `theme/sakrylle`
  - 实施说明：
    - `git clone` + `git remote add upstream <kelivo-origin>`
    - 设置 upstream 同步策略（同 sub2api 的 `theme/monet-purple` 做法）
  - 验收标准：本地可 `flutter pub get` + `flutter build apk --debug` 通过

- [ ] **0-2：修改 Android applicationId**（可与 0-3 并行）
  - 目标：`com.psyche.kelivo` → `com.sakrylle.chat`
  - 涉及文件：
    - `android/app/build.gradle.kts:11` — `namespace`
    - `android/app/build.gradle.kts:27` — `applicationId`
    - `android/app/src/main/AndroidManifest.xml:12` — `android:label`（同步改为 `Sakrylle Chat`）
  - 验收标准：`flutter build apk --debug` 生成包名 `com.sakrylle.chat`；`aapt dump badging` 确认

- [ ] **0-3：修改 iOS bundle id**（可与 0-2 并行）
  - 目标：`psyche.kelivo` → `com.sakrylle.chat`；扩展 → `com.sakrylle.chat.GenerationActivityExtension`
  - 涉及文件：
    - `ios/Runner.xcodeproj/project.pbxproj:689,883,909` — 主 Runner target（3 处 `psyche.kelivo`）
    - `ios/Runner.xcodeproj/project.pbxproj:554,582,610` — GenerationActivityExtension（3 处）
    - `ios/Runner.xcodeproj/project.pbxproj:709,729,747` — RunnerTests（3 处）
    - `ios/Runner/Info.plist:35-36` — 后台任务 ID（`psyche.kelivo.background-generation.*` → `com.sakrylle.chat.background-generation.*`）
    - `ios/Runner/AppDelegate.swift:7-8` — 同上常量
    - `ios/GenerationActivityExtension/Info.plist` — `CFBundleDisplayName = Kelivo` → `Sakrylle Chat`
  - 实施说明：建议用 Xcode 的 `Rename` 功能批量替换，或用 `sed -i` 脚本，替换后全量 grep `psyche.kelivo` 确认清零
  - 风险：漏改任一处后台任务 ID 会导致 iOS 后台生成静默失效
  - 验收标准：`flutter build ios --debug --no-codesign` 通过；grep `psyche.kelivo` 返回空

- [ ] **0-4：修改 macOS bundle id**（可与 0-2/0-3 并行）
  - 目标：`com.psyche.kelivo` → `com.sakrylle.chat`
  - 涉及文件：
    - `macos/Runner/Configs/AppInfo.xcconfig` — `PRODUCT_BUNDLE_IDENTIFIER` + `PRODUCT_NAME`（改为 `Sakrylle Chat`）
    - `macos/Runner/Configs/Release.xcconfig` / `Debug.xcconfig` — 不确定是否硬编码，需核查
  - 验收标准：`flutter build macos --debug` 通过

- [ ] **0-5：修改 Windows 产品标识**（可与 0-2/0-3 并行）
  - 目标：`kelivo` → `Sakrylle Chat`
  - 涉及文件：
    - `windows/runner/Runner.rc:92-98` — CompanyName（`com.sakrylle`）、FileDescription、InternalName、OriginalFilename（`sakrylle_chat.exe`）、ProductName（`Sakrylle Chat`）
  - 验收标准：`flutter build windows --debug` 通过

- [ ] **0-6：修改 Linux APPLICATION_ID**（可与 0-2/0-3 并行）
  - 目标：`com.psyche.kelivo` → `com.sakrylle.chat`
  - 涉及文件：
    - `linux/CMakeLists.txt:10` — `set(APPLICATION_ID "com.sakrylle.chat")`
  - 验收标准：`flutter build linux --debug` 通过（若有 Linux 构建环境）

- [ ] **0-7：修改 pubspec.yaml 包名与描述**
  - 涉及文件：`pubspec.yaml:1-2`
    ```yaml
    name: sakrylle_chat
    description: "Sakrylle Chat — AI gateway chat client by Sakrylle API"
    ```
  - 验收标准：`flutter pub get` 通过；无 import 因 package name 变更而报错（注：Dart package 名仅影响内部 `package:kelivo/` 引用，需全库替换）
  - 实施说明：用 `grep -rn "package:kelivo/"` 找出所有内部引用，批量替换为 `package:sakrylle_chat/`

---

## Phase 1 — 最小可用：预置 Sakrylle provider + 品牌基础

**目标**：用户首次启动 Sakrylle Chat 即可看到 Sakrylle API provider，填入 API Key 即可使用；应用名和主色体现 Sakrylle 品牌。

### 任务列表

- [ ] **1-1：新增 Sakrylle API 内置 provider**（串行，依赖 Phase 0 完成）
  - 目标：在内置 provider 列表首位添加 `'Sakrylle API'`，提供完整默认配置
  - 涉及文件：`lib/core/providers/settings_provider.dart`
  - 实施说明（需同步修改 3 处）：

    **a) `_builtInProviderKeysInOrder`（line 54）**：在列表首位添加：
    ```dart
    static const List<String> _builtInProviderKeysInOrder = [
      'Sakrylle API',  // 新增，放在首位
      'OpenAI',
      // ... 其余不变
    ];
    ```

    **b) `_defaultBase()`（line 4404）**：在函数体最前面添加：
    ```dart
    if (k.contains('sakrylle')) return 'https://api.sakrylle.com/v1';
    ```

    **c) `_defaultBalanceApiPath()`（line 4625）**：
    ```dart
    if (k.contains('sakrylle')) return '/api/v1/user/balance';
    ```

    **d) `_defaultBalanceResultPath()`（line 4638）**：
    ```dart
    if (k.contains('sakrylle')) return 'data.balance';
    ```
    「不确定」：`data.balance` 是 Sakrylle `/api/v1/user/balance` 的实际字段路径，需与后端接口实测确认，见 CLAUDE.md `GET /v1/account/balance`

    **e) `_defaultBalanceEnabled()`（line 4653）**：
    ```dart
    return k.contains('sakrylle') || k.contains('aihubmix') || ...;
    ```

    **f) `defaultsFor()` 中为 Sakrylle 添加专属 case**（在 `case ProviderKind.openai:` 分支最前面，参考 KelivoIN special-case 模式，`line 4504-4554`）：
    ```dart
    if (lowerKey.contains('sakrylle')) {
      return ProviderConfig(
        id: key,
        enabled: true,  // 首次启动即启用
        name: displayName ?? 'Sakrylle API',
        apiKey: '',
        baseUrl: _defaultBase(key),
        providerType: ProviderKind.openai,
        chatPath: null,  // 使用默认 /chat/completions
        useResponseApi: false,
        models: const [
          'claude-opus-4-8',
          'claude-opus-4-7',
          'claude-sonnet-4-6',
          'claude-haiku-4-5-20251001',
          'gpt-5.5',
          'gpt-5.4',
          'gpt-5.4-mini',
          'deepseek-v4-pro',
          'deepseek-v4-flash',
        ],
        modelOverrides: const {},
        proxyEnabled: false,
        proxyHost: '',
        proxyPort: '8080',
        proxyUsername: '',
        proxyPassword: '',
        multiKeyEnabled: false,
        apiKeys: const [],
        keyManagement: const KeyManagementConfig(),
        aihubmixAppCodeEnabled: false,
        balanceEnabled: true,
        balanceApiPath: _defaultBalanceApiPath(key),
        balanceResultPath: _defaultBalanceResultPath(key),
        claudePromptCachingEnabled: false,
      );
    }
    ```
    注意：预置模型列表需与 `channel_model_pricing` 实际配置保持同步；以上为当前已知模型（2026-06-03），后续新增模型需同步更新

  - 验收标准：首次启动 Sakrylle Chat，provider 列表首位显示 `Sakrylle API`；填入有效 API Key 后可成功发送消息

- [ ] **1-2：将 Sakrylle API 加入 `isUserAdded()` 固定集合**（可与 1-1 并行）
  - 目标：防止用户误删内置 Sakrylle provider
  - 涉及文件：`lib/features/provider/pages/provider_detail_page.dart:128-145`
  - 实施说明：在 `const fixed = {...}` 中添加 `'Sakrylle API'`
  - 验收标准：在 Sakrylle API provider 详情页，不显示删除按钮

- [ ] **1-3：修改应用名称品牌字符串**（可与 1-1 并行）
  - 目标：所有用户可见的应用名替换为 `Sakrylle Chat`
  - 涉及文件：
    - `lib/main.dart:104` — `title: 'Sakrylle Chat'`
    - `lib/main.dart:356` — `title: 'Sakrylle Chat'`
    - `ios/Runner/Info.plist` — `CFBundleDisplayName`（需确认是否直接写值，还是通过 xcconfig）
  - 验收标准：iOS / macOS 任务切换器、Android 启动器、Windows 任务栏均显示 `Sakrylle Chat`

- [ ] **1-4：新增 Sakrylle Monet Purple 主题调色板**（串行，依赖 Phase 0）
  - 目标：添加 `#9181bd` 为主色的 Material 3 ColorScheme，并设为新安装的默认主题
  - 涉及文件：`lib/theme/palettes.dart`
  - 实施说明：

    新增调色板常量（参考 `purple` 调色板模式，`palettes.dart:234-298`），名称建议 `monetPurple`，zhName `莫奈紫`，enName `Monet Purple`：

    ```dart
    static const ThemePalette monetPurple = ThemePalette(
      id: monetPurpleId,
      zhName: '莫奈紫',
      enName: 'Monet Purple',
      light: ColorScheme(
        brightness: Brightness.light,
        primary: Color(0xFF9181BD),        // Monet Purple 500
        onPrimary: Color(0xFFFFFFFF),
        primaryContainer: Color(0xFFE2DAF2), // Monet Purple 200
        onPrimaryContainer: Color(0xFF2D2640), // Monet Purple 950
        secondary: Color(0xFFEC6A9C),       // 樱花粉 accent 500（少量用于高亮）
        onSecondary: Color(0xFFFFFFFF),
        secondaryContainer: Color(0xFFFFEBF4),
        onSecondaryContainer: Color(0xFF3D0020),
        // ... surface / error 等参考 slate 中性色
        surface: Color(0xFFF8F6FC),         // Monet Purple 50
        onSurface: Color(0xFF1C1B1F),
        // ... 其余字段按 Material 3 tonalPalette 规范生成
      ),
      dark: ColorScheme(
        brightness: Brightness.dark,
        primary: Color(0xFFCFC2E8),         // Monet Purple 300（深色模式提亮）
        onPrimary: Color(0xFF2D2640),
        primaryContainer: Color(0xFF6B5B95), // Monet Purple 700
        onPrimaryContainer: Color(0xFFE2DAF2),
        // ...
      ),
    );
    ```

    完整 Material 3 ColorScheme 建议使用 [Material Theme Builder](https://m3.material.io/theme-builder) 以 `#9181bd` 作为 seed color 生成，导出 Dart 代码后替换上述占位值。

    同时在 palettes 列表中注册 `monetPurple`，并在 `settings_provider.dart` 的主题默认值处设为新安装默认（或在首次启动 onboarding 流程中预选）。

  - 验收标准：主题选择列表显示 `莫奈紫 / Monet Purple`；选中后主色渲染为 `#9181bd` 附近的 Material 3 tone；深色模式正确翻转

---

## Phase 2 — 品牌完整 + 存储安全

**目标**：替换应用图标、完善多平台品牌，并加固 API Key 存储安全性。

### 任务列表

- [ ] **2-1：替换应用图标**（串行，依赖 Phase 0）
  - 目标：全平台使用 Sakrylle 樱花线描图标（coral→hot-pink 渐变描边，5 瓣白心）
  - 源文件：`/Users/ariel/Documents/Design/Material/cherry-blossom_15273565.png`（需确认分辨率是否满足各平台要求）
  - 涉及文件：
    - `flutter_launcher_icons.yaml:4` — `image_path: assets/app_icon_2.png`（替换为新图标路径）
    - `assets/app_icon.png`、`assets/app_icon_2.png`、`assets/app_icon_foreground.png`、`assets/icon_mac.png`（替换资源文件）
  - 实施说明：
    1. 将源图标导出为 1024×1024 PNG（无透明背景 + 圆角处理，满足 Android Adaptive Icon / iOS 规范）
    2. 更新 `flutter_launcher_icons.yaml` 中的路径
    3. 运行 `flutter pub run flutter_launcher_icons`
    4. Android Adaptive Icon 前景（`app_icon_foreground.png`）可用樱花图标，背景色用 Monet Purple 900 `#4a3f66`
  - 验收标准：Android / iOS / macOS 启动器均显示 Sakrylle 图标；iOS App Store 预览图标正确

- [ ] **2-2：迁移 API Key 存储至 flutter_secure_storage**（串行，可独立执行）
  - 目标：将 `provider_configs_v1` 中的 `apiKey` 字段迁移至 `flutter_secure_storage`，其余配置仍留在 `shared_preferences`
  - 依赖：在 `pubspec.yaml` 中添加 `flutter_secure_storage: ^9.2.4`（或最新稳定版）
  - 涉及文件：
    - `pubspec.yaml` — 添加依赖
    - `lib/core/providers/settings_provider.dart` — 修改 `setProviderConfig` / `getProviderConfig` 的 apiKey 读写逻辑
    - Android：`android/app/build.gradle.kts` — 确认 `minSdk >= 21`（flutter_secure_storage Android 要求）
    - iOS：`ios/Runner/Info.plist` — 不需要额外配置（使用 Keychain）
  - 实施说明：
    1. `SecureStorage` 单例，key 格式：`sakrylle_chat.apikey.<providerId>`
    2. `setProviderConfig` 时：apiKey 写入 SecureStorage，其余字段写入 SharedPreferences（JSON 中 apiKey 留空字符串或 null）
    3. `getProviderConfig` 时：从 SharedPreferences 读取结构，再从 SecureStorage 补填 apiKey
    4. 迁移逻辑：首次启动时检测 SharedPreferences 中的 `provider_configs_v1` 是否含非空 apiKey，若有则搬移至 SecureStorage 后清除 SharedPreferences 中的明文
  - 验收标准：API Key 不再出现在 SharedPreferences 的 JSON dump 中；多 key 轮转（`ApiKeyConfig`）同样迁移；Web 平台 `flutter_secure_storage` 使用 localStorage 加密，需单独测试
  - 风险：Web 平台 `flutter_secure_storage` 实现非真正加密（localStorage + XOR），接受此风险或 Web 平台单独处理

- [ ] **2-3：SiliconFlow fallback key 审查**（可并行）
  - 目标：确认 `lib/secrets/fallback.dart` 中无真实密钥
  - 实施说明：读取文件，确认内容仅为 `'sk-xxxx'` 占位符；若为真实密钥需立即吊销并替换为空字符串或环境变量注入
  - 验收标准：文件中无真实 API Key；在 `.gitignore` 或 git history 中无意外提交的真实密钥

---

## Phase 3 — OAuth/OIDC 登录（PKCE Authorization Code Flow）

**目标**：实现用户通过 Sakrylle 账号登录 Sakrylle Chat，自动获取 access_token 用作 Sakrylle API 的 Bearer 凭证，无需手动复制 API Key。

**前置依赖（后端）**：
- Sakrylle API OIDC 基础层完成（参见 03-sakrylle-api-oidc-architecture.md Phase 1）
- `oauth_clients` 表中注册 `sakrylle-chat` client（见下文 §9 客户端注册规范）
- `/oauth/authorize` 的 `redirect_uri` 验证支持自定义 scheme（「不确定」需确认 `backend/internal/service/oauth_service.go`）

**此 Phase 仅在后端前置依赖完成后启动。**

### 任务列表

- [ ] **3-1：添加 OAuth 依赖**（串行，Phase 3 起点）
  - 目标：引入 PKCE 授权流程所需依赖
  - 涉及文件：`pubspec.yaml`
  - 添加：
    ```yaml
    flutter_web_auth_2: ^4.0.0   # 浏览器跳转 + 自定义 scheme 回调
    crypto: ^3.0.3               # 已有，PKCE code_verifier SHA-256
    ```
  - 验收标准：`flutter pub get` 通过；无依赖冲突

- [ ] **3-2：注册自定义 URL Scheme（各平台）**（可与 3-1 并行，依赖 3-1 完成后测试）
  - 目标：各平台注册 `sakrylle-chat://oauth/callback`（已确认 2026-06-03）使 OAuth 回调能唤起 App
  - 涉及文件：

    **Android**（`android/app/src/main/AndroidManifest.xml`）在主 Activity 内添加：
    ```xml
    <intent-filter android:label="Sakrylle Chat OAuth">
      <action android:name="android.intent.action.VIEW" />
      <category android:name="android.intent.category.DEFAULT" />
      <category android:name="android.intent.category.BROWSABLE" />
      <data android:scheme="sakrylle-chat" android:host="oauth" android:pathPrefix="/callback" />
    </intent-filter>
    ```

    **iOS**（`ios/Runner/Info.plist`）添加：
    ```xml
    <key>CFBundleURLTypes</key>
    <array>
      <dict>
        <key>CFBundleURLName</key>
        <string>com.sakrylle.chat.oauth</string>
        <key>CFBundleURLSchemes</key>
        <array>
          <string>sakrylle-chat</string>
        </array>
      </dict>
    </array>
    ```

    **macOS**（`macos/Runner/Info.plist`）同 iOS 格式添加 `CFBundleURLTypes`

    **Windows / Linux**：`flutter_web_auth_2` 在桌面端使用 loopback 重定向（`http://127.0.0.1:<随机端口>/callback`），无需注册 scheme，但需在 `oauth_clients.redirect_uris` 白名单中添加 loopback 前缀匹配（「不确定」：sub2api 是否支持 loopback URI 前缀模式而非精确匹配，见 RFC 8252 §8.3）

  - 验收标准：Android/iOS/macOS 上浏览器完成授权后能正确回调唤起 App

- [ ] **3-3：实现 PKCE 授权流程核心逻辑**（串行，依赖 3-1、3-2）
  - 目标：新建 `lib/core/services/auth/sakrylle_oauth_service.dart`，封装完整 PKCE Auth Code Flow
  - 实施说明：

    ```dart
    // issuer = https://sub.sakrylle.com；scheme = sakrylle-chat://oauth/callback；
    // scope = openid profile email + /v1；public client；id_token 签名 RS256 + ES256（均已确认 2026-06-03）
    class SakrylleOAuthService {
      static const _authBaseUrl = 'https://sub.sakrylle.com';
      static const _clientId = 'sakrylle-chat';
      static const _redirectUri = 'sakrylle-chat://oauth/callback';
      static const _scopes = 'openid profile email /v1';

      /// 生成 PKCE code_verifier（43-128 字符随机字符串）和 code_challenge（S256）
      ({String verifier, String challenge}) _generatePkce();

      /// 构建 /oauth/authorize URL
      Uri _buildAuthUrl({required String codeChallenge, required String state, String? nonce});

      /// 发起授权：唤起浏览器 → 等待回调 → 解析 code
      Future<String> authorize();

      /// 用 code 换取 token（POST /oauth/token）
      Future<OAuthTokens> exchangeCode(String code, String verifier);

      /// 用 refresh_token 静默续期
      Future<OAuthTokens> refreshTokens(String refreshToken);

      /// 撤销 token（POST /oauth/token/revoke，RFC 7009）
      Future<void> revokeToken(String token);
    }
    ```

    - `flutter_web_auth_2` 的 `WebAuth.authenticate(url, callbackUrlScheme)` 处理跨平台浏览器弹出 + 回调捕获
    - `code_verifier`：用 `crypto` 包生成 32 字节随机数，base64url 编码
    - `code_challenge`：`base64url(sha256(ascii(verifier)))`
    - token 存储：access_token 写入 `flutter_secure_storage`（key: `sakrylle_chat.oauth.access_token`）；refresh_token 同（key: `sakrylle_chat.oauth.refresh_token`）；token 有效期存 SharedPreferences

  - 涉及文件（新建）：
    - `lib/core/services/auth/sakrylle_oauth_service.dart`
    - `lib/core/models/oauth_tokens.dart`（值对象）
  - 验收标准：完整 PKCE flow 跑通（模拟 sub2api 本地或 staging 环境）；access_token 成功写入 SecureStorage

- [ ] **3-4：将 OAuth access_token 注入 Sakrylle provider**（串行，依赖 3-3）
  - 目标：登录成功后，将 access_token 自动写入 `Sakrylle API` provider 的 apiKey 字段（SecureStorage），无需用户手动填写
  - 实施说明：
    - 在 `SakrylleOAuthService.authorize()` 完成后，调用 `settingsProvider.setProviderApiKey('Sakrylle API', accessToken)`
    - `ChatApiService` 已通过 `Authorization: Bearer <apiKey>` 发送请求（`chat_api_service.dart:704`），无需改动请求层
    - token 过期时（401 响应），触发静默 refresh 并重写 apiKey
  - 验收标准：登录后无需手动填写 API Key，即可直接发送消息到 Sakrylle API；token 过期后自动续期

- [ ] **3-5：OAuth 登录 UI 入口**（串行，依赖 3-3、3-4）
  - 目标：在 Provider 配置页和应用首屏提供"使用 Sakrylle 账号登录"按钮
  - 实施说明：
    - 在 Sakrylle API provider 详情页（`provider_detail_page.dart`）的 API Key 输入框上方添加"使用 Sakrylle 账号登录"按钮，点击后触发 `SakrylleOAuthService.authorize()`
    - 登录成功后 API Key 输入框自动填入（`[通过账号登录，自动管理]` 提示），可选显示已登录用户名/邮箱
    - 提供"退出登录"按钮（触发 `revokeToken` + 清除 SecureStorage 中的 token + 清空 apiKey）
  - 验收标准：完整登录→使用→退出流程在 iOS/Android/macOS 三个平台验证通过

- [ ] **3-6：在 sub2api 侧注册 sakrylle-chat OAuth client**（需额外审批，触及生产数据库）
  - 目标：在 `oauth_clients` 表中确认/插入 `sakrylle-chat` public client 配置
  - 实施说明（生产操作，需审批后在 `ssh ssh-tokyo` 上执行）：**先核实 migration `148` 是否已预置 `sakrylle-chat`（含 `sakrylle-chat://oauth/callback` 回调），已确认 2026-06-03 回调 scheme 为此值**；若 148 已种则只需校验，无需重复 INSERT。下方 SQL 为未预置时的兜底：
    ```sql
    INSERT INTO oauth_clients (
      client_id, name, client_secret_hash,
      redirect_uris, allowed_scopes,
      pkce_required, default_group_id,
      access_token_ttl_seconds, refresh_token_ttl_seconds,
      disabled
    ) VALUES (
      'sakrylle-chat',
      'Sakrylle Chat',
      '',  -- 公共 client，无 secret（已确认 2026-06-03）
      '["sakrylle-chat://oauth/callback", "http://127.0.0.1"]',
      '["openid", "profile", "email", "models:read", "balance:read"]',  -- openid profile email + /v1（email:read 对第一方默认授予）
      true,  -- 强制 PKCE（已确认 2026-06-03）
      2,     -- 默认 group_id = Claude-Kiro (group 2) 或按策略确认
      86400,    -- access token 24h
      2592000,  -- refresh token 30d
      false
    );
    ```
  - 验收标准：`SELECT * FROM oauth_clients WHERE client_id = 'sakrylle-chat';` 返回正确行（回调含 `sakrylle-chat://oauth/callback`）；`/oauth/authorize?client_id=sakrylle-chat&...` 返回同意页面

---

## Phase 4 — 测试、发布、回滚

### 任务列表

- [ ] **4-1：多平台构建验证**（并行执行各平台）
  - Android：`flutter build appbundle --release`
  - iOS：`flutter build ipa --export-method=app-store` + XCode Archive 验证
  - macOS：`flutter build macos --release`
  - Windows：`flutter build windows --release`
  - （**不发布 Flutter Web，已确认 2026-06-03**，故不构建 web target）
  - 验收标准：五个平台（Android / iOS / macOS / Windows / Linux）release 构建通过；bundle id 均为 `com.sakrylle.chat`；应用名均显示 `Sakrylle Chat`

- [ ] **4-2：功能冒烟测试**
  - [ ] 手动填写 Sakrylle API Key → 发送消息 → 正确响应
  - [ ] 余额显示（若 Phase 1 `balanceEnabled = true`）
  - [ ] 模型列表显示预置的 9 个模型
  - [ ] Monet Purple 主题渲染正确
  - [ ] iOS 生成进度 Live Activity / 灵动岛（GenerationActivityExtension）正常（**保留 kelivo 此功能，已确认 2026-06-03**）
  - [ ] (Phase 3) OAuth 登录 → 自动填充 API Key → 发消息 → token 续期 → 退出登录

- [ ] **4-3：回滚方案**
  - git tag 策略：每个 Phase 完成后打一个 tag（`v0.x.0-phase<N>`），确保可回滚到任意 Phase 节点
  - sub2api 侧 `oauth_clients` 如需回滚：`UPDATE oauth_clients SET disabled = true WHERE client_id = 'sakrylle-chat';`（软删除，不破坏现有 token）

---

## 多平台注意事项汇总

| 平台 | 主要改动文件 | 特殊注意点 |
|------|-------------|-----------|
| Android | `build.gradle.kts`, `AndroidManifest.xml` | OAuth intent-filter 需 `android:autoVerify="true"` 配合 Digital Asset Links（若使用 https scheme）；自定义 scheme 无需验证 |
| iOS | `project.pbxproj`（多处）, `Info.plist`, `AppDelegate.swift` | GenerationActivityExtension bundle id 必须同步更新；background task ID 字面量 2 处；`flutter_secure_storage` 使用 Keychain，需在 Xcode Capabilities 中启用 Keychain Sharing（「不确定」是否必须） |
| macOS | `AppInfo.xcconfig`, `Info.plist` | OAuth scheme 同 iOS；macOS Sandbox 需在 Entitlements 中添加 `com.apple.security.network.client`（访问 sub.sakrylle.com）|
| Windows | `Runner.rc` | `flutter_web_auth_2` 桌面端使用 loopback，需确认防火墙规则不拦截本地端口 |
| Linux | `CMakeLists.txt` | 同 Windows，loopback 方案；部分发行版需 `xdg-open` 可用 |
| ~~Web~~ | — | **不发布 Flutter Web（已确认 2026-06-03，仅移动 / 桌面）**；Web 端浏览器聊天由 Sakrylle Web（open-webui，`chat.sakrylle.com`）承担，与本 Flutter 客户端无关 |

---

## 附录：Sakrylle Chat 品牌配置隔离规范

根据 05-configuration-isolation-standard.md，Sakrylle Chat 的存储路径在 fork 后变更为：

| 平台 | 路径（fork 后） |
|------|----------------|
| macOS（Hive） | `~/Library/Application Support/com.sakrylle.chat/` |
| macOS（SharedPreferences） | `~/Library/Preferences/com.sakrylle.chat.plist` |
| iOS（Hive） | `~/Documents/`（沙盒，bundle id 隔离） |
| Android（Hive） | `/data/user/0/com.sakrylle.chat/files/` |
| Android（SharedPreferences） | `/data/data/com.sakrylle.chat/shared_prefs/` |
| Windows | `%APPDATA%/com.sakrylle.chat/` |
| Linux | `~/.local/share/com.sakrylle.chat/` |

路径隔离保证 Sakrylle Chat 与已安装 kelivo 完全沙盒独立，无数据冲突。

---

*参见* 40-sakrylle-chat-research.md（调研详情）  
*参见* 03-sakrylle-api-oidc-architecture.md（OIDC 后端规划）  
*参见* 05-configuration-isolation-standard.md（配置隔离规范）
