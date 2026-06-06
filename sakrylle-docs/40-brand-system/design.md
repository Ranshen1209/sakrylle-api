---
name: Sakrylle Design System
version: 1.0.0
description: >
  Sakrylle 生态设计系统 — 覆盖 Sakrylle API（Web）、Sakrylle CLI（终端）、
  Sakrylle Studio（桌面）、Sakrylle Web（WebUI）、Sakrylle Chat（Flutter 移动端）、
  Sakrylle Image（SPA）共六个产品。主色为 Monet Purple，强调色为 Logo 樱花粉渐变。

tokens:
  colors:
    primary:
      50:  "#f8f6fc"
      100: "#f0ecf8"
      200: "#e2daf2"
      300: "#cfc2e8"
      400: "#b5a3d9"
      500: "#9181bd"   # Monet Purple 主色
      600: "#7b6aab"
      700: "#6b5b95"
      800: "#584b7a"
      900: "#4a3f66"
      950: "#2d2640"

    sakura:         # 樱花粉 — 来自 Logo coral→hot-pink 渐变
      coral:  "#ffab91"   # Logo 渐变起点（左/上）
      500:    "#ec6a9c"   # 建议 accent 500，居中插值
      pink:   "#f06292"   # Logo 渐变终点（右/下）

    neutral:        # Slate 中性色
      50:  "#f8fafc"
      100: "#f1f5f9"
      200: "#e2e8f0"
      300: "#cbd5e1"
      400: "#94a3b8"
      500: "#64748b"
      600: "#475569"
      700: "#334155"
      800: "#1e293b"
      900: "#0f172a"
      950: "#020617"

    semantic:
      success:  "#10b981"   # emerald-500
      warning:  "#f59e0b"   # amber-500
      error:    "#ef4444"   # red-500
      info:     "#9181bd"   # primary-500，与主色统一

    on-colors:
      on-primary:    "#ffffff"   # 白字 on primary-500 (#9181bd) — 对比度约 3.8:1 (AA Large 通过；body text 对比度不确定，见下方说明)
      on-primary-dark: "#f8f6fc" # 深色模式
      background:    "#f8fafc"   # neutral-50，浅色背景
      background-dark: "#020617" # neutral-950，深色背景
      foreground:    "#0f172a"   # neutral-900，正文前景
      foreground-dark: "#f1f5f9" # neutral-100，深色正文

  typography:
    h1:
      fontFamily: "system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', sans-serif"
      fontSize: "2.25rem"    # 36px (text-4xl)
      fontWeight: "700"
      lineHeight: "1.2"
    h2:
      fontFamily: "inherit"
      fontSize: "1.875rem"   # 30px (text-3xl)
      fontWeight: "700"
      lineHeight: "1.25"
    h3:
      fontFamily: "inherit"
      fontSize: "1.25rem"    # 20px (text-xl / text-lg)
      fontWeight: "600"
      lineHeight: "1.4"
    body:
      fontFamily: "inherit"
      fontSize: "0.875rem"   # 14px (text-sm)
      fontWeight: "400"
      lineHeight: "1.6"
    caption:
      fontFamily: "inherit"
      fontSize: "0.75rem"    # 12px (text-xs)
      fontWeight: "400"
      lineHeight: "1.5"
    code:
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace"
      fontSize: "0.875rem"   # 14px
      fontWeight: "400"
      lineHeight: "1.6"

  rounded:
    sm:   "0.5rem"    # rounded-lg (8px)
    md:   "0.75rem"   # rounded-xl (12px)
    lg:   "1rem"      # rounded-2xl (16px)
    full: "9999px"    # rounded-full

  spacing:
    xs: "0.5rem"    # 8px
    sm: "0.75rem"   # 12px
    md: "1rem"      # 16px
    lg: "1.5rem"    # 24px
    xl: "2rem"      # 32px

  components:
    button-primary:
      background: "linear-gradient(to right, {colors.primary.500}, {colors.primary.600})"
      color: "{colors.on-colors.on-primary}"
      borderRadius: "{rounded.md}"
      padding: "0.625rem 1rem"       # py-2.5 px-4
      fontSize: "{typography.body.fontSize}"
      fontWeight: "500"
      shadow: "0 4px 6px -1px rgba(145,129,189,0.25)"
      hoverBackground: "linear-gradient(to right, {colors.primary.600}, {colors.primary.700})"
      focusRing: "0 0 0 3px rgba(145,129,189,0.3)"
      disabledOpacity: "0.5"

    button-secondary:
      background: "#ffffff"
      backgroundDark: "{colors.neutral.800}"
      color: "{colors.neutral.700}"
      colorDark: "{colors.neutral.200}"
      border: "1px solid {colors.neutral.200}"
      borderRadius: "{rounded.md}"
      padding: "0.625rem 1rem"
      fontSize: "{typography.body.fontSize}"
      fontWeight: "500"
      hoverBackground: "{colors.neutral.50}"

    button-ghost:
      background: "transparent"
      color: "{colors.neutral.600}"
      borderRadius: "{rounded.md}"
      padding: "0.625rem 1rem"
      fontSize: "{typography.body.fontSize}"
      hoverBackground: "{colors.neutral.100}"

    button-danger:
      background: "linear-gradient(to right, #ef4444, #dc2626)"
      color: "#ffffff"
      borderRadius: "{rounded.md}"
      padding: "0.625rem 1rem"
      fontSize: "{typography.body.fontSize}"
      fontWeight: "500"

    card:
      background: "#ffffff"
      backgroundDark: "rgba(30,41,59,0.5)"    # neutral-800/50
      border: "1px solid {colors.neutral.100}"
      borderDark: "1px solid rgba(51,65,85,0.5)"
      borderRadius: "{rounded.lg}"
      shadow: "0 1px 3px rgba(0,0,0,0.04), 0 1px 2px rgba(0,0,0,0.06)"
      hoverShadow: "0 10px 40px rgba(0,0,0,0.08)"
      padding: "{spacing.lg}"

    input:
      background: "#ffffff"
      backgroundDark: "{colors.neutral.800}"
      border: "1px solid {colors.neutral.200}"
      borderRadius: "{rounded.md}"
      padding: "0.625rem 1rem"
      fontSize: "{typography.body.fontSize}"
      focusBorder: "{colors.primary.500}"
      focusRing: "0 0 0 3px rgba(145,129,189,0.3)"
      errorBorder: "{colors.semantic.error}"

    badge:
      borderRadius: "{rounded.full}"
      padding: "0.125rem 0.625rem"
      fontSize: "{typography.caption.fontSize}"
      fontWeight: "500"
      variants:
        primary:
          background: "{colors.primary.100}"
          color: "{colors.primary.700}"
        success:
          background: "#d1fae5"
          color: "#065f46"
        warning:
          background: "#fef3c7"
          color: "#92400e"
        danger:
          background: "#fee2e2"
          color: "#991b1b"

    modal:
      overlayBackground: "rgba(0,0,0,0.5)"
      backdropBlur: "4px"
      background: "#ffffff"
      backgroundDark: "{colors.neutral.800}"
      borderRadius: "{rounded.lg}"
      shadow: "0 25px 50px -12px rgba(0,0,0,0.25)"
      padding: "{spacing.lg}"

    toast:
      background: "#ffffff"
      backgroundDark: "{colors.neutral.800}"
      borderRadius: "{rounded.md}"
      shadow: "0 10px 15px -3px rgba(0,0,0,0.1)"
      borderLeft: "4px solid"

    sidebar:
      background: "#ffffff"
      backgroundDark: "{colors.neutral.900}"
      border: "1px solid {colors.neutral.200}"
      width: "16rem"    # 256px (w-64)
      linkActive:
        background: "{colors.primary.50}"
        color: "{colors.primary.600}"

    progress-bar:
      background: "linear-gradient(to right, {colors.primary.500}, {colors.primary.400})"
      trackBackground: "{colors.neutral.200}"
      height: "0.5rem"
      borderRadius: "{rounded.full}"
---

# Sakrylle Design System

> 版本 1.0.0 — 覆盖 Sakrylle 全生态六个产品。主色 Monet Purple，强调色樱花粉，货币显示 ￥（存储值不转换）。

## Overview

Sakrylle 设计系统以「莫奈水彩」美学为基调，主色 **Monet Purple** (`#9181bd`) 源自薰衣草-紫藤的低饱和水彩调，传递静谧、专业与创造力。强调色**樱花粉**直接取自品牌 Logo 的 coral→hot-pink 渐变描边（`#ffab91 → #f06292`），少量用于高亮 CTA、徽标和特殊装饰，保持紫色主导地位。

### 品牌标志（Logo）视觉特征

Logo 为**线描风格樱花**（5 瓣，白色花心，线条采用 coral `#ffab91` → hot-pink `#f06292` 渐变描边，约 45° 方向），笔画干净利落，白底/深底均可使用。

- 文件原始路径：`/Users/ariel/Documents/Design/Material/cherry-blossom_15273565.png`
- 生产静态地址：`https://sub.sakrylle.com/static/sakrylle-icon-192.png`

### 适配范围

| 产品 | 平台 | 说明 |
|------|------|------|
| Sakrylle API | Web (Vue3/Vite) | Tailwind CSS，已实施本设计系统 |
| Sakrylle CLI | 终端 (Rust + Node) | 参照 ANSI 色建议章节 |
| Sakrylle Studio | 桌面 (Tauri) | Web 层复用本系统；窗口原生控件独立 |
| Sakrylle Web | Web (SvelteKit) | 继承品牌色后适配 open-webui 组件体系 |
| Sakrylle Chat | 移动/跨端 (Flutter) | 参照 Flutter 色映射章节 |
| Sakrylle Image | Web SPA (Vite/TS) | 复用 API 前端令牌，按需扩展 |

---

## Colors

### 主色 — Monet Purple

低饱和水彩薰衣草调，整体和谐柔和，不刺眼。

| Token | 十六进制 | 用途 |
|-------|---------|------|
| `primary-50`  | `#f8f6fc` | 浅色激活背景（侧边栏激活项、选中标签） |
| `primary-100` | `#f0ecf8` | hover 状态背景、徽章背景 |
| `primary-200` | `#e2daf2` | 边框强调、分割线 |
| `primary-300` | `#cfc2e8` | 禁用态前景、占位符 |
| `primary-400` | `#b5a3d9` | 图标、次要文本 |
| `primary-500` | `#9181bd` | **基础主色** — 按钮、链接、焦点环、进度条 |
| `primary-600` | `#7b6aab` | 主色按钮渐变终点、hover 加深 |
| `primary-700` | `#6b5b95` | 高对比场景文字（dark mode 标题） |
| `primary-800` | `#584b7a` | 深色叠加 |
| `primary-900` | `#4a3f66` | 深色文字 |
| `primary-950` | `#2d2640` | 最深强调色 |

主渐变：`linear-gradient(135deg, #9181bd 0%, #7b6aab 100%)`

### 强调色 — Sakura Pink（樱花粉）

来自 Logo coral→hot-pink 渐变，仅用于**Logo、高亮 CTA、特殊装饰**，保持紫色为系统主色。

| Token | 十六进制 | 说明 |
|-------|---------|------|
| `sakura-coral` | `#ffab91` | Logo 渐变起点，柔和珊瑚 |
| `sakura-500`   | `#ec6a9c` | 建议 accent 中点，适合按钮/图标强调 |
| `sakura-pink`  | `#f06292` | Logo 渐变终点，热粉色 |

Logo 渐变参考：`linear-gradient(45deg, #ffab91 0%, #f06292 100%)`

> 注：sakura 色系在 Tailwind 中当前未注册为 `accent`（`tailwind.config.js` 中 `accent` 已被 slate 复用）。扩展时建议新增 `sakura` 键或按需 inline 使用。

### 中性色 — Slate

| Token | 十六进制 | 用途 |
|-------|---------|------|
| `neutral-50`  | `#f8fafc` | 页面背景、表格头背景 |
| `neutral-100` | `#f1f5f9` | hover 背景 |
| `neutral-200` | `#e2e8f0` | 分割线、边框 |
| `neutral-300` | `#cbd5e1` | 禁用边框 |
| `neutral-400` | `#94a3b8` | 占位符文字 |
| `neutral-500` | `#64748b` | 次要文字 |
| `neutral-600` | `#475569` | 正文 |
| `neutral-700` | `#334155` | dark mode 边框 |
| `neutral-800` | `#1e293b` | dark mode 卡片背景 |
| `neutral-900` | `#0f172a` | dark mode 侧边栏背景 |
| `neutral-950` | `#020617` | dark mode 页面背景 |

### 语义色

| 语义 | 十六进制 | Tailwind 对应 | 用途 |
|------|---------|--------------|------|
| success | `#10b981` | emerald-500 | 成功状态、正趋势 |
| warning | `#f59e0b` | amber-500 | 警告、注意 |
| error | `#ef4444` | red-500 | 错误、危险操作 |
| info | `#9181bd` | primary-500 | 提示信息（与主色统一） |

### WCAG 对比度说明

> 以下数据来自设计稿测算，**未经实测工具验证**，请在发布前用 [Contrast Checker](https://webaim.org/resources/contrastchecker/) 确认。

| 组合 | 大约对比度 | AA Small 文字 | AA Large/UI |
|------|----------|--------------|-------------|
| 白字 (`#fff`) on `primary-500` (`#9181bd`) | ~3.8:1 | 不确定（AA 需 4.5:1）| **通过**（3:1） |
| 白字 on `primary-700` (`#6b5b95`) | ~6.2:1 | **通过** | **通过** |
| `neutral-900` on `neutral-50` | ~19:1 | **通过** | **通过** |
| `primary-500` on `neutral-50` | ~4.8:1 | **通过** | **通过** |

结论：**主色按钮（白字 on primary-500）在正文大小（14px/400）下对比度约 3.8:1，未达 AA 4.5:1**。建议重要正文按钮使用 `primary-600`（`#7b6aab`）作为背景或将文字加大至 16px bold（AA Large 降为 3:1 即通过）。`不确定`——待 Axe / Lighthouse 实测确认。

### CLI（终端）ANSI 色建议

CLI 应用无法使用十六进制颜色，建议映射如下（256 色终端）：

| 用途 | ANSI 256 色索引 | 近似效果 | 退化为 16 色 |
|------|----------------|---------|------------|
| 主色（Monet Purple） | 97（浅紫）或 99 | 近似 #9181bd | `\033[35m`（magenta） |
| 强调（樱花粉） | 205（浅粉）或 207 | 近似 #ec6a9c | `\033[95m`（bright magenta） |
| 成功 | 114（emerald） | 对应 #10b981 | `\033[32m`（green） |
| 警告 | 214（amber） | 对应 #f59e0b | `\033[33m`（yellow） |
| 错误 | 203（red） | 对应 #ef4444 | `\033[31m`（red） |
| 次要文字 | 245（灰） | 对应 neutral-500 | `\033[2m`（dim） |

Rust 推荐使用 `owo-colors` 或 `colored` crate；Node 端推荐 `chalk`。

### Flutter（Sakrylle Chat）色映射

```dart
// 主色
const Color sakrylleMonetPurple = Color(0xFF9181BD);
const Color sakrylleMonetPurpleDark = Color(0xFF7B6AAB);
// 强调色
const Color sakrylleSakuraAccent = Color(0xFFEC6A9C);
// 背景（浅色）
const Color sakrylleBackground = Color(0xFFF8FAFC);
// 背景（深色）
const Color sakrylleBackgroundDark = Color(0xFF020617);
// 正文
const Color sakrylleForeground = Color(0xFF0F172A);
```

---

## Typography

字体栈以**系统字体**为优先，中文优先 PingFang SC（macOS/iOS）、Microsoft YaHei（Windows）、Hiragino Sans GB（老版 macOS）。代码字体使用等宽族。

### 字阶规范

| 层级 | 大小 | 权重 | 行高 | 用途 |
|------|------|------|------|------|
| `h1` | 36px / 2.25rem | 700 Bold | 1.2 | 页面主标题（首页英雄区） |
| `h2` | 30px / 1.875rem | 700 Bold | 1.25 | 区块标题 |
| `h3` | 20px / 1.25rem | 600 SemiBold | 1.4 | 卡片标题、弹窗标题 |
| `body` | 14px / 0.875rem | 400 Regular | 1.6 | 正文（默认） |
| `body-large` | 18px / 1.125rem | 400 Regular | 1.6 | 副标题、重要段落 |
| `caption` | 12px / 0.75rem | 400 Regular | 1.5 | 辅助说明、时间戳、标签文字 |
| `code` | 14px / 0.875rem | 400 Regular | 1.6 | 内联代码、代码块 |

### 中文排版要点

- 正文行高建议 1.6（中文较拉丁文需要更大行高）
- 中英混排时优先让系统字体自动调整字号比例，勿强制 `font-size` 差异
- 避免在移动端使用小于 14px 的正文字号（iOS 自动放大会破坏布局）

---

## Layout

### 断点（继承 Tailwind 默认）

| 名称 | 最小宽度 | 用途 |
|------|---------|------|
| `sm` | 640px | 大手机横屏、小平板 |
| `md` | 768px | 平板 |
| `lg` | 1024px | 笔记本 |
| `xl` | 1280px | 桌面 |
| `2xl` | 1536px | 大屏 |

### 网格与间距

系统以 **4px 为基础单位**，间距梯度：

| Token | 值 | 像素 |
|-------|----|-----|
| `spacing-xs` | 0.5rem | 8px |
| `spacing-sm` | 0.75rem | 12px |
| `spacing-md` | 1rem | 16px |
| `spacing-lg` | 1.5rem | 24px |
| `spacing-xl` | 2rem | 32px |

### 页面布局结构

```
┌──────────────────────────────────┐
│  AppSidebar (w-64 / 16rem)      │
│  ┌────────────────────────────┐  │
│  │  SidebarHeader  (h-16)     │  │
│  ├────────────────────────────┤  │
│  │  SidebarNav                │  │
│  │  └─ sidebar-link           │  │
│  │  └─ sidebar-link-active    │  │
│  └────────────────────────────┘  │
├──────────────────────────────────┤
│  MainContent                     │
│  ┌────────────────────────────┐  │
│  │  page-header               │  │
│  │  .page-title / .page-desc  │  │
│  ├────────────────────────────┤  │
│  │  Content Area (grid/flex)  │  │
│  └────────────────────────────┘  │
└──────────────────────────────────┘
```

移动端侧边栏收起为 overlay（`fixed inset-y-0`），主内容满宽。

---

## Elevation & Depth

阴影系统以「玻璃拟态」为核心，层级越高阴影越扩散，紫色发光效果点缀交互高亮。

| 层级 | Token | 值 | 用途 |
|------|-------|-----|------|
| 0 | 无阴影 | — | 内联文本、图标 |
| 1 | `shadow-card` | `0 1px 3px rgba(0,0,0,.04), 0 1px 2px rgba(0,0,0,.06)` | 静止卡片 |
| 2 | `shadow-glass-sm` | `0 4px 16px rgba(0,0,0,.06)` | 玻璃卡片 |
| 3 | `shadow-glass` | `0 8px 32px rgba(0,0,0,.08)` | 悬浮组件、下拉菜单 |
| 4 | `shadow-card-hover` | `0 10px 40px rgba(0,0,0,.08)` | hover 卡片 |
| 5 | `shadow-lg` | `0 10px 15px -3px rgba(0,0,0,.1)` | 模态框、Toast |
| 6 | `shadow-glow` | `0 0 20px rgba(145,129,189,.25)` | 主色发光（交互反馈） |
| 7 | `shadow-glow-lg` | `0 0 40px rgba(145,129,189,.35)` | 英雄区 CTA 发光 |

深色模式下阴影透明度整体降低约 20%（背景本身较暗，阴影对比度足够）。

### 毛玻璃效果（Glass）

```css
/* 标准玻璃卡 */
background: rgba(255,255,255,0.7);
backdrop-filter: blur(24px);
border: 1px solid rgba(255,255,255,0.2);
border-radius: 1rem;
box-shadow: 0 8px 32px rgba(0,0,0,0.08);

/* 深色模式 */
background: rgba(30,41,59,0.7);
border: 1px solid rgba(51,65,85,0.5);
```

---

## Shapes

圆角设计延续「柔和」品牌调性，避免锐角。

| Token | 值 | 像素 | 用途 |
|-------|----|-----|------|
| `rounded-sm` (`rounded-lg`) | 0.5rem | 8px | 小标签、小按钮、代码片段 |
| `rounded-md` (`rounded-xl`) | 0.75rem | 12px | 标准按钮、输入框、下拉菜单、对话框 |
| `rounded-lg` (`rounded-2xl`) | 1rem | 16px | 卡片、模态框、主要容器 |
| `rounded-full` | 9999px | — | 徽章、开关、头像、圆形图标按钮 |
| `rounded-4xl` | 2rem | 32px | 特大圆角（英雄区装饰块） |

> Tailwind 默认 `rounded-xl`（0.75rem）对应本系统的 `rounded-md`；`rounded-2xl`（1rem）对应 `rounded-lg`。命名上存在偏移，使用 class 时以 Tailwind 实际值为准。

---

## Components

### 按钮（Button）

按钮共 5 个语义变体 + 3 个尺寸 + 图标模式。

#### 变体

```html
<!-- Primary：主色渐变，白字，带紫色发光阴影 -->
<button class="btn btn-primary">开始使用</button>

<!-- Secondary：白底，灰边，适合次要操作 -->
<button class="btn btn-secondary">取消</button>

<!-- Ghost：透明底，悬停显示灰色背景 -->
<button class="btn btn-ghost">更多</button>

<!-- Danger：红色渐变，用于破坏性操作 -->
<button class="btn btn-danger">删除</button>

<!-- Success：绿色渐变，用于确认/保存 -->
<button class="btn btn-success">保存</button>
```

#### 尺寸

| 修饰类 | 圆角 | 内边距 | 字号 |
|--------|------|--------|------|
| `btn-sm` | 8px | py-1.5 px-3 | 12px |
| `btn-md` | 12px | py-2 px-4 | 14px（默认） |
| `btn-lg` | 16px | py-3 px-6 | 16px |
| `btn-icon` | 12px | p-2.5 | — |

#### 状态

- **hover**：渐变颜色加深一阶，阴影扩大
- **active**：`scale(0.98)` 缩放反馈
- **focus**：`ring-2 ring-primary-500/50 ring-offset-2`（键盘导航可见）
- **disabled**：`opacity-50 cursor-not-allowed`，无 transform

#### Do's and Don'ts（按钮专项）

- ✅ 一个视觉焦点只放一个 Primary 按钮
- ✅ 破坏性操作使用 `btn-danger`，配合确认对话框
- ❌ 不要在一行内并排超过 3 个带色彩的 Primary/Danger 按钮
- ❌ 不要把 Ghost 用于核心 CTA

---

### 卡片（Card）

```html
<!-- 标准卡片 -->
<div class="card card-hover">
  <div class="card-header">标题</div>
  <div class="card-body">内容</div>
  <div class="card-footer">操作</div>
</div>

<!-- 玻璃卡片（半透明，毛玻璃效果） -->
<div class="glass-card">内容</div>

<!-- 统计卡片 -->
<div class="stat-card">
  <div class="stat-icon stat-icon-primary">图标</div>
  <div>
    <div class="stat-value">¥128.50</div>
    <div class="stat-label">余额</div>
  </div>
</div>
```

> 货币显示统一使用 `￥`（全角日元符号，display-only，存储数值不转换）。

---

### 输入框（Input）

```html
<label class="input-label">API Key</label>
<input class="input" placeholder="sk-..." />
<span class="input-hint">用于调用 /v1/* 接口</span>

<!-- 错误状态 -->
<input class="input input-error" />
<span class="input-error-text">格式无效</span>
```

焦点样式：`border-primary-500 + ring-2 ring-primary-500/30`（兼容键盘和触控）

---

### 徽章（Badge）

```html
<span class="badge badge-primary">Pro</span>
<span class="badge badge-success">运行中</span>
<span class="badge badge-warning">告警</span>
<span class="badge badge-danger">错误</span>
<span class="badge badge-gray">禁用</span>
```

---

### 模态框（Modal）

```html
<div class="modal-overlay">
  <div class="modal-content max-w-lg">
    <div class="modal-header">
      <h3 class="modal-title">确认删除</h3>
    </div>
    <div class="modal-body">内容</div>
    <div class="modal-footer">
      <button class="btn btn-secondary">取消</button>
      <button class="btn btn-danger">删除</button>
    </div>
  </div>
</div>
```

动画：进入 `scale(0.95) → scale(1)` + `opacity 0→1`，250ms ease-out；离开 200ms ease-in。支持 `prefers-reduced-motion`。

---

### Toast 通知

```html
<!-- 信息 Toast（主色左边框） -->
<div class="toast toast-info">
  <p class="font-medium text-gray-900 dark:text-white">提示</p>
  <p class="text-sm text-gray-600 dark:text-gray-300">操作已完成</p>
</div>
```

右上角 `fixed right-4 top-4`，最小宽度 320px，进入动画 `slide-in-right`。

---

### 侧边栏（Sidebar）

```html
<nav class="sidebar">
  <div class="sidebar-header">
    <!-- Logo + 产品名 -->
  </div>
  <div class="sidebar-nav">
    <div class="sidebar-section">
      <p class="sidebar-section-title">主导航</p>
      <a class="sidebar-link sidebar-link-active">仪表盘</a>
      <a class="sidebar-link">API Keys</a>
    </div>
  </div>
</nav>
```

激活项：`bg-primary-50 text-primary-600`（浅色），`bg-primary-900/20 text-primary-400`（深色）。

---

### 进度条（Progress）

```html
<div class="progress">
  <div class="progress-bar" style="width: 65%"></div>
</div>
```

条体：`bg-gradient-to-r from-primary-500 to-primary-400`，高度 8px，圆角 full。

---

### 代码（Code / Code Block）

```html
<!-- 内联代码：紫色前景，浅灰背景 -->
<code class="code">sk-xxx-yyy</code>

<!-- 代码块：深色背景，浅色文字 -->
<pre class="code-block">
  <code>curl https://api.sakrylle.com/v1/models</code>
</pre>
```

---

### 开关（Switch）

```html
<div class="switch switch-active">
  <div class="switch-thumb"></div>
</div>
```

激活色 `primary-500`，Telegram 圆形切换风格，`transition-transform 200ms`。

---

### 深色模式切换

暗色模式通过 `darkMode: 'class'`（Tailwind）+ View Transitions API 实现。切换时以圆形扩散动画（Telegram 风格）过渡，自动跟随系统主题（`prefers-color-scheme`）。相关实现：`frontend/src/style.css:5-16`（view-transition keyframes），`frontend/src/composables/useTheme.ts`（不确定，需确认实际路径）。

---

## Do's and Don'ts

### 颜色

| ✅ Do | ❌ Don't |
|-------|---------|
| 主色 primary-500 用于按钮、链接、焦点环、进度条 | 把 sakura 粉用于大面积背景 |
| sakura 色仅用于 Logo、CTA 特别强调 | 在同一视图混用多个强调色 |
| 深色模式使用 `/20`、`/30` 透明变体降低色彩强度 | 强制使用亮色模式的硬编码颜色值 |
| 货币前缀用 `￥`（display-only） | 修改数据库存储的货币数值（见 Currency policy） |
| 语义色明确传递状态（成功/警告/错误） | 用 primary 色代替 error 传递错误信息 |

### 排版

| ✅ Do | ❌ Don't |
|-------|---------|
| h1 用于页面级唯一大标题 | 同一页面出现多个 h1 |
| 正文最小字号 14px | 移动端正文低于 14px |
| 中英混排保持系统字体栈 | 在 UI 中单独引入 Google Fonts（增加 CLS/性能开销） |

### 间距与形状

| ✅ Do | ❌ Don't |
|-------|---------|
| 卡片圆角使用 `rounded-2xl`（16px） | 在卡片内嵌套使用更大圆角 |
| 行内徽章使用 `rounded-full` | 给徽章加矩形锐角 |
| 按钮 padding 遵循 sm/md/lg 梯度 | 自定义 padding 破坏视觉节律 |

### 无障碍（Accessibility）

| ✅ Do | ❌ Don't |
|-------|---------|
| 使用 `focus:ring-2` 键盘焦点环 | 使用 `outline: none` 移除焦点轮廓 |
| 所有表单控件配对 `<label>` | 用 placeholder 代替 label |
| 动画支持 `prefers-reduced-motion` | 强制所有用户播放过渡动画 |
| 主色按钮使用 `primary-700` 背景提高对比度（正文大小） | 小字正文文字用 primary-500 on white（约 4.8:1，勉强 AA） |

### 产品平台特殊规则

| 平台 | 规则 |
|------|------|
| CLI（终端） | 不输出 raw hex，使用 ANSI 256 色映射；颜色退化方案见 Colors 章节 |
| Flutter Chat | 使用 `MaterialColor` 从 primary-500 推导完整 swatch；不要硬编码 `Colors.purple` |
| Studio（Tauri） | Web 层复用本系统；原生 titlebar/菜单跟随 OS 主题，不覆盖 |
| 所有产品 | localStorage / IndexedDB 前缀统一加产品命名空间（如 `sakrylle-image-playground.*`），避免跨 origin 污染 |

---

## 附录：设计 Token 快速索引

```
主色         primary-500 = #9181bd
主色深       primary-600 = #7b6aab
主渐变       linear-gradient(135deg, #9181bd 0%, #7b6aab 100%)
樱花粉       sakura-500  = #ec6a9c
樱花渐变     linear-gradient(45deg, #ffab91 0%, #f06292 100%)
成功         #10b981
警告         #f59e0b
错误         #ef4444
页面背景     #f8fafc (浅色) / #020617 (深色)
正文         #0f172a (浅色) / #f1f5f9 (深色)
卡片背景     #ffffff (浅色) / #1e293b (深色)
边框         #f1f5f9 (浅色) / rgba(51,65,85,.5) (深色)
```

---

> **Lint 说明**：本文档遵循 `design.md` 规范格式编写（YAML front matter + 指定章节顺序）。当前为**规划阶段文档，未运行 lint**。
> 原因：`@google/design.md` 包需要 Node.js 环境且未安装于本机，规划文档以可读性和工程可执行性为优先，lint 工具验证建议在 CI 阶段集成（`npx @google/design.md lint DESIGN.md`）。
