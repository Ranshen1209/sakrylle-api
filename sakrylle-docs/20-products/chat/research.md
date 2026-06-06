---
title: 40 — Sakrylle Chat 调研报告（kelivo 现状分析）
status: local
scope: product
last_verified: 2026-06-06
---

# 40 — Sakrylle Chat 调研报告（kelivo 现状分析）

> 文档类型：只读调研  
> 调研日期：2026-06-03  
> 上游仓库：kelivo（本地路径 `/Volumes/APFS_HD/Documents/Github/kelivo`）  
> 产品定位：Sakrylle Chat = kelivo fork，Flutter 跨平台 LLM 聊天客户端

---

## 1. 调研范围

- Flutter 技术栈与依赖版本
- 应用标识符（applicationId / bundleId）与平台覆盖
- Provider（模型提供商）注册机制与 API endpoint 配置
- 认证机制（含 OAuth/OIDC 现状）
- 本地存储策略（配置 + 对话历史）
- 品牌点（应用名、窗口标题、图标、主题调色板）
- iOS 扩展与 App Group 风险
- 与 Sakrylle 生态的整合差距

---

## 2. 关键结论

| 结论 | 状态 |
|------|------|
| kelivo 是功能完整的多平台聊天客户端，支持 OpenAI / Claude / Gemini 三类协议 | 确认 |
| 完全无账号体系，纯本地 API Key，无任何 OAuth/OIDC 基础设施 | 确认 |
| API Key 以明文 JSON 存储于 shared_preferences，无 flutter_secure_storage | 确认 |
| applicationId/bundleId 均为 `com.psyche.kelivo`（5 个平台需逐一替换） | 确认 |
| 现有 purple 调色板 primary `#5D5698` 与 Sakrylle Monet purple `#9181bd` 不一致 | 确认 |
| 无预置 Sakrylle API provider，首次启动用户看不到 Sakrylle | 确认 |
| iOS 有 GenerationActivityExtension（WidgetKit），bundle id 硬编码 `psyche.kelivo.*` | 确认 |
| 余额查询端点默认 `/credits`，Sakrylle 的是 `/api/v1/user/balance` | 确认 |

---

## 3. 相关文件路径（含行号）

### 3.1 项目根与依赖声明

| 文件 | 说明 |
|------|------|
| `pubspec.yaml:1` | 应用名 `Kelivo`，版本 `1.1.15+52` |
| `pubspec.yaml:21` | Dart SDK 约束 `^3.8.1` |
| `pubspec.yaml:46` | `provider: ^6.0.5`（状态管理） |
| `pubspec.yaml:49` | `shared_preferences: ^2.2.3`（配置持久化） |
| `pubspec.yaml:61-62` | `hive: ^2.2.3` + `hive_flutter: ^1.1.0`（对话历史） |
| `pubspec.yaml:58` | `jose: ^0.3.4`（仅用于 Google Vertex AI SA JWT 签名） |
| `pubspec.yaml:53` | `http: ^1.5.0` |
| `pubspec.yaml:55` | `dio: ^5.9.0` |

### 3.2 核心 Provider 配置

| 文件:行 | 内容 |
|---------|------|
| `lib/core/providers/settings_provider.dart:54-68` | `_builtInProviderKeysInOrder` 内置 provider 列表（13 个，无 Sakrylle） |
| `lib/core/providers/settings_provider.dart:69-71` | `_builtInProviderKeys` Set（同步更新） |
| `lib/core/providers/settings_provider.dart:4107` | `enum ProviderKind { openai, google, claude }` |
| `lib/core/providers/settings_provider.dart:4114` | `class ProviderConfig` |
| `lib/core/providers/settings_provider.dart:4389` | `classify()` — key → ProviderKind 映射逻辑 |
| `lib/core/providers/settings_provider.dart:4404-4434` | `_defaultBase()` — key → base URL 映射（无 sakrylle 分支） |
| `lib/core/providers/settings_provider.dart:4436` | `defaultsFor()` — 工厂方法，创建带默认值的 ProviderConfig |
| `lib/core/providers/settings_provider.dart:4625-4635` | `_defaultBalanceApiPath()` — 余额 API 路径（默认 `/credits`） |
| `lib/core/providers/settings_provider.dart:4638-4651` | `_defaultBalanceResultPath()` — 余额结果 JSON path |
| `lib/core/providers/settings_provider.dart:4653-4661` | `_defaultBalanceEnabled()` — 是否默认启用余额显示 |

### 3.3 Provider 配置 UI

| 文件:行 | 内容 |
|---------|------|
| `lib/features/provider/pages/provider_detail_page.dart:128-145` | `isUserAdded()` — 判断是否显示删除按钮的固定集合（无 Sakrylle API） |

### 3.4 API 请求构建

| 文件:行 | 内容 |
|---------|------|
| `lib/core/services/api/chat_api_service.dart:703-706` | `Authorization: Bearer <apiKey>` 头注入 |

### 3.5 本地存储路径

| 文件:行 | 内容 |
|---------|------|
| `lib/utils/app_directories.dart:18-28` | `getAppDataDirectory()` — 平台路径策略 |
| `lib/core/services/chat/chat_service.dart:51-78` | Hive 初始化，boxes: `conversations`/`messages`/`tool_events_v1` |

### 3.6 品牌相关

| 文件:行 | 内容 |
|---------|------|
| `lib/main.dart:104` | `DesktopWindowController.initializeAndShow(title: 'Kelivo')` |
| `lib/main.dart:356` | `MaterialApp` title `'Kelivo'` |
| `lib/theme/palettes.dart:234-298` | `purple` 调色板（zhName `暮紫韵`，primary light `#5D5698`） |
| `flutter_launcher_icons.yaml:4` | `image_path: assets/app_icon_2.png` |

### 3.7 平台身份标识符

| 文件:行 | 内容 |
|---------|------|
| `android/app/build.gradle.kts:11,27` | `namespace = "com.psyche.kelivo"` / `applicationId = "com.psyche.kelivo"` |
| `android/app/src/main/AndroidManifest.xml:12` | `android:label="Kelivo"` |
| `ios/Runner.xcodeproj/project.pbxproj:689` | `PRODUCT_BUNDLE_IDENTIFIER = psyche.kelivo`（主 target） |
| `ios/Runner.xcodeproj/project.pbxproj:554,582,610` | `PRODUCT_BUNDLE_IDENTIFIER = psyche.kelivo.GenerationActivityExtension` |
| `ios/Runner.xcodeproj/project.pbxproj:709,729,747` | `PRODUCT_BUNDLE_IDENTIFIER = psyche.kelivo.RunnerTests` |
| `ios/Runner/Info.plist:35-36` | 后台任务 ID `psyche.kelivo.background-generation.*` |
| `ios/Runner/AppDelegate.swift:7-8` | 后台任务 ID 字面量 |
| `macos/Runner/Configs/AppInfo.xcconfig` | `PRODUCT_BUNDLE_IDENTIFIER = com.psyche.kelivo` / `PRODUCT_NAME = kelivo` |
| `windows/runner/Runner.rc:92-98` | CompanyName / ProductName / OriginalFilename = `kelivo` |
| `linux/CMakeLists.txt:10` | `APPLICATION_ID "com.psyche.kelivo"` |
| `ios/GenerationActivityExtension/Info.plist` | `CFBundleDisplayName = Kelivo`（扩展显示名） |

---

## 4. 当前实现摘要

### 4.1 Flutter 技术栈

- Flutter 1.1.15（pubspec versionName），Dart SDK `^3.8.1`
- 状态管理：`provider ^6.0.5`（`ChangeNotifier` 风格），多个 `ChangeNotifierProvider` 在 `main.dart` 中组合挂载
- 持久化：
  - `shared_preferences ^2.2.3`：存储全部 provider 配置（含 API Key）+ 所有 UI 设置，键名 `provider_configs_v1`
  - `hive ^2.2.3`：存储对话历史（Conversation / ChatMessage / ToolEvent），初始化于 `AppDirectories.getAppDataDirectory()` 返回的平台目录下
- HTTP：`http ^1.5.0`（非流式请求）、`dio ^5.9.0`（部分场景）
- 多平台：Android / iOS / macOS / Windows / Linux / Web（`flutter web` 支持）
- 国际化：`flutter_localizations` + `intl`，中文 / 英文双语

### 4.2 应用类型

完整 LLM 聊天客户端，主要功能：
- 多 Provider 支持（OpenAI 兼容 / Anthropic Claude / Google Gemini）
- 多会话 / 对话管理（Hive 持久化）
- 模型选择、系统提示（System Prompt / Assistants）
- 工具调用（Tool Use / MCP）
- 图像输入、文件上传
- 代理设置（HTTP/SOCKS5 proxy per provider）
- 主题系统（9 套调色板 + dynamic_color）
- 桌面托盘（macOS / Windows / Linux）
- iOS WidgetKit GenerationActivityExtension（后台流式生成）

### 4.3 Provider 与 Endpoint 配置机制

#### 内置 Provider 列表（`settings_provider.dart:54-68`）

现有 13 个内置 provider：OpenAI、SiliconFlow、Gemini、OpenRouter、KelivoIN、Tensdaq、DeepSeek、AIhubmix、Aliyun、Zhipu AI、Claude、Grok、ByteDance。

**无 Sakrylle API 条目。**

#### URL 映射逻辑（`_defaultBase`，`settings_provider.dart:4404-4434`）

通过 `key.toLowerCase()` 的关键词匹配决定 base URL。未匹配到任何关键词时 fallback 到 `https://api.openai.com/v1`。Sakrylle 需新增如下分支：

```dart
if (k.contains('sakrylle')) return 'https://api.sakrylle.com/v1';
```

#### ProviderKind 分类（`classify`，`settings_provider.dart:4389-4402`）

- 关键词含 `gemini` / `google` → `ProviderKind.google`
- 关键词含 `claude` / `anthropic` → `ProviderKind.claude`
- 其余（含 openai / 第三方 OpenAI 兼容网关）→ `ProviderKind.openai`

Sakrylle API 暴露 OpenAI 兼容 `/v1/chat/completions`，应走 `ProviderKind.openai` 分支。

#### 余额查询（`settings_provider.dart:4625-4661`）

| 字段 | 默认值 | Sakrylle 实际端点 |
|------|--------|------------------|
| `balanceApiPath` | `/credits` | `/api/v1/user/balance` |
| `balanceResultPath` | `data.total_usage` | `data.balance`（不确定，需与后端实测确认） |
| `balanceEnabled` | `false` | 建议 `true` |

### 4.4 认证机制

**现状：完全无认证体系。**

- 用户在 ProviderDetailPage 中手动填写 API Base URL / API Key / Chat Path，存于 `shared_preferences` 的 `provider_configs_v1` 键中（明文 JSON）
- 无 `flutter_secure_storage` 依赖（未在 pubspec.yaml 中找到）
- 无 `flutter_web_auth_2` / `flutter_appauth` 依赖
- iOS `Info.plist` 无 `CFBundleURLSchemes`（无自定义 URL scheme）
- Android `AndroidManifest.xml` 无 `intent-filter data android:scheme` 节点
- `jose ^0.3.4` 仅用于 `lib/core/services/api/google_service_account_auth.dart:5`（Google Vertex AI 服务账号 JWT 签名），与用户认证无关

**OAuth/OIDC 支持需从零构建。**

### 4.5 本地存储路径

| 平台 | Hive（对话历史） | SharedPreferences（配置）|
|------|-----------------|--------------------------|
| macOS | `~/Library/Application Support/com.psyche.kelivo/` | `~/Library/Preferences/com.psyche.kelivo.plist` |
| iOS | `~/Documents/`（沙盒内） | 标准 NSUserDefaults |
| Android | `/data/user/0/com.psyche.kelivo/files/` | `/data/data/com.psyche.kelivo/shared_prefs/` |
| Windows | `%APPDATA%/com.psyche.kelivo/` | Roaming 下 |
| Linux | `~/.local/share/com.psyche.kelivo/` | XDG_CONFIG_HOME 下 |

Hive boxes（3 个）：`conversations.hive`、`messages.hive`、`tool_events_v1.hive`。子目录：`/upload`、`/images`、`/avatars`、`/cache/avatars`。

**数据迁移风险**：改 applicationId/bundleId 后，上述路径全部变更，已安装用户的对话历史与配置将丢失。需实现迁移路径（见开发计划 §Phase 0）。

### 4.6 品牌点

| 品牌点 | 当前值 | 目标值 |
|--------|--------|--------|
| pubspec name | `Kelivo` | `sakrylle_chat`（Dart package 名，snake_case） |
| 窗口标题（`main.dart:104`） | `'Kelivo'` | `'Sakrylle Chat'` |
| MaterialApp title（`main.dart:356`） | `'Kelivo'` | `'Sakrylle Chat'` |
| Android label | `Kelivo` | `Sakrylle Chat` |
| iOS CFBundleDisplayName | `Kelivo` | `Sakrylle Chat` |
| macOS PRODUCT_NAME | `kelivo` | `Sakrylle Chat` |
| Windows ProductName | `kelivo` | `Sakrylle Chat` |
| 主题 purple（`palettes.dart:240`） | `#5D5698`（暮紫韵） | `#9181bd`（Monet Purple 500） |
| 应用图标（`flutter_launcher_icons.yaml:4`） | `assets/app_icon_2.png` | Sakrylle 樱花线描图标 |

### 4.7 iOS 扩展注意事项

iOS 有 `GenerationActivityExtension`（WidgetKit，bundle id `psyche.kelivo.GenerationActivityExtension`），与主 app bundle 共享前缀 `psyche.kelivo`。后台任务 ID 也含 `psyche.kelivo` 前缀，分布在：

- `ios/Runner/Info.plist:35-36`
- `ios/Runner/AppDelegate.swift:7-8`

修改主 bundle id 为 `com.sakrylle.chat` 时，以上所有硬编码引用必须同步更新为 `com.sakrylle.chat.*`，否则 iOS 后台生成功能失效。如暂不需要 WidgetKit 功能，可考虑在 fork 初期完整移除该扩展以降低复杂度。

---

## 5. 差距分析

| 差距 | 影响程度 | 说明 |
|------|----------|------|
| 无 OAuth/OIDC 客户端基础设施 | 高 | 需添加 `flutter_web_auth_2` + PKCE + 自定义 URL scheme，iOS/Android 需平台配置 |
| API Key 明文存储 | 中 | `shared_preferences` 无加密；OAuth access token 有 24h TTL 风险相对可控，长期静态 key 需 `flutter_secure_storage` 加固 |
| 无预置 Sakrylle provider | 高 | 用户无法通过 UI 直接发现 Sakrylle API；需在 3 处同步添加 |
| Monet purple 调色板缺失 | 中 | 现有 purple 主色 `#5D5698` 偏暗，需新增 `#9181bd` 专属调色板 |
| 余额端点不适配 | 低 | `_defaultBalanceApiPath` 默认 `/credits`，需为 Sakrylle 添加专属映射 |
| applicationId/bundleId 占用 | 高 | 5 个平台 + iOS 扩展需逐一修改，否则无法与 kelivo 共存，且无法上架 App Store |
| `isUserAdded()` 固定集合未含 Sakrylle | 低 | 会显示删除按钮，需同步 `provider_detail_page.dart:129-144` |
| i18n 品牌字符串硬编码 | 低 | `android:label`、`CFBundleDisplayName` 等分散在多个平台文件 |
| iOS 扩展 bundle id 耦合 | 中 | 需同步更新，或移除扩展 |
| Hive 对话历史无加密 | 低 | 企业合规场景需 `hive_flutter` 加密 box，消费者场景暂可接受 |

---

## 6. 当前实现摘要（补充：OAuth 回调 scheme 方案可行性评估）

Sakrylle sub2api OAuth 端点 (`/oauth/authorize`) 已支持 `redirect_uri` 精确白名单（见 `oauth_clients.redirect_uris` jsonb 字段）。自定义 scheme `com.sakrylle.chat://oauth/callback` 理论上可作为合法 redirect_uri 注册。

需在 sub2api 侧的 `oauth_clients` 表中为 `sakrylle-chat` client 添加如下 redirect URI：
- `com.sakrylle.chat://oauth/callback`（移动端自定义 scheme）
- `http://127.0.0.1` 前缀（loopback，桌面端备用，见 RFC 8252）

不确定项：sub2api 的 `redirect_uri` 验证逻辑是否支持自定义 scheme（非 https），需查看 `backend/internal/service/oauth_service.go` 确认（见 03-sakrylle-api-oidc-architecture.md）。

---

## 7. 风险

1. **数据迁移**：改 applicationId 后平台数据路径变更，已安装 kelivo 的用户对话历史丢失（Sakrylle Chat 定位为独立 app，此为预期行为，但需在上线说明中告知）
2. **API Key 明文存储**：OAuth access token 短 TTL（24h）风险相对可控；若用户选择粘贴长期静态 Sakrylle API key，安全级别与原 kelivo 一致（无加密），建议 Phase 2 迁移至 `flutter_secure_storage`
3. **iOS 扩展**：`psyche.kelivo.GenerationActivityExtension` 中后台任务 ID 分散在 Swift 代码和 plist 中，漏改会导致后台功能静默失效
4. **SiliconFlow fallback key**：`lib/secrets/fallback.dart` 包含字符串常量 `'sk-xxxx'`，文件名 `secrets/` 易触发 git 扫描工具误报，fork 后需确认无真实密钥

---

## 8. 不确定项

- sub2api OAuth 端点 `redirect_uri` 验证是否允许自定义 scheme（非 https URI）——需读 `backend/internal/service/oauth_service.go`
- Sakrylle 余额 API `/api/v1/user/balance` 的响应 JSON 结构中余额字段路径（`data.balance` 还是其他路径）——需实测
- `jose` 包是否在 `lib/` 中还有其他用途（除 `google_service_account_auth.dart`）——已知仅见于该文件，但未全量 grep 确认
- macOS / iOS Hive 数据是否有 iCloud Drive 同步风险——取决于沙盒存储类型（Documents vs Application Support）
- Windows `Runner.rc` / Linux `CMakeLists.txt` 中是否还有其他需同步修改的身份引用——已找到主要位置，但未深入 `windows/runner/` 子目录全部文件

---

*参见* 41-sakrylle-chat-development-plan.md（改造计划）  
*参见* 03-sakrylle-api-oidc-architecture.md（OIDC 后端规划）  
*参见* 05-configuration-isolation-standard.md（多产品配置隔离规范）
