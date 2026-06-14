# grok-imagine-image-lite 图片端点 400 修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `grok-imagine-image-lite` 能通过 sub2api 的 `/v1/images/generations`,消除本地 `400 "images endpoint requires an image model"`。

**Architecture:** 唯一改动是放开 `backend/internal/service/openai_images.go` 的图片模型前缀白名单 `isOpenAIImageGenerationModel`——从只认 `gpt-image-` 改为前缀集合,新增 `grok-imagine-image`。放行之后的路由/账号选择/上游转发/计费链路全部模型无关,无新增分支。`channel 14 restrict_models=true` + `channel_model_pricing` row 326 是兜底闸,未配置模型仍 503。

**Tech Stack:** Go 1.26.x、testify(`require`)、gin。测试为 `package service` 白盒,可直接调用未导出函数。

**工作目录:** worktree `/Users/cervine/Documents/Sakrylle/Sakrylle API/.claude/worktrees/grok-image-fix`,分支 `fix/grok-image-local-400`(基线 `theme/monet-purple`)。下列相对路径均相对此 worktree 根;`go` 命令在 `backend/` 子目录运行。

---

### Task 1: 放开图片模型前缀白名单(TDD)

**Files:**
- Modify: `backend/internal/service/openai_images.go:457-459`(`isOpenAIImageGenerationModel`)
- Test: `backend/internal/service/openai_images_test.go`(新增一个表驱动测试函数,追加到文件末尾)

- [ ] **Step 1: 写失败的表驱动测试**

在 `backend/internal/service/openai_images_test.go` 末尾追加(`strings` 不需要、`require` 与 `testing` 已在文件 import 中):

```go
func TestValidateOpenAIImagesModel(t *testing.T) {
	cases := []struct {
		name    string
		model   string
		wantErr bool
	}{
		{"grok imagine image lite accepted", "grok-imagine-image-lite", false},
		{"grok imagine image accepted", "grok-imagine-image", false},
		{"grok imagine image pro accepted", "grok-imagine-image-pro", false},
		{"grok imagine image edit accepted", "grok-imagine-image-edit", false},
		{"grok imagine video rejected", "grok-imagine-video", true},
		{"gpt image still accepted", "gpt-image-2", false},
		{"unrelated text model rejected", "gpt-5.4", true},
		{"empty rejected", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOpenAIImagesModel(tc.model)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test ./internal/service/ -run TestValidateOpenAIImagesModel -v`
Expected: FAIL。`grok imagine image *` 四个子用例报 `Received unexpected error: images endpoint requires an image model, got "grok-imagine-image-lite"`(当前函数只认 `gpt-image-` 前缀)。`grok imagine video rejected` / `gpt image still accepted` / `unrelated text model rejected` / `empty rejected` 应已 PASS。

- [ ] **Step 3: 改实现为前缀集合**

把 `backend/internal/service/openai_images.go:457-459` 的函数体替换为:

```go
func isOpenAIImageGenerationModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range []string{"gpt-image-", "grok-imagine-image"} {
		if strings.HasPrefix(m, prefix) {
			return true
		}
	}
	return false
}
```

`validateOpenAIImagesModel`(`:461-470`)不改动。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd backend && go test ./internal/service/ -run TestValidateOpenAIImagesModel -v`
Expected: PASS,全部 8 个子用例通过。

- [ ] **Step 5: 跑整个 service 包确认无回归**

Run: `cd backend && go test ./internal/service/`
Expected: ok（特别是现有 `TestOpenAIGatewayServiceParseOpenAIImagesRequest_RejectsNonImageModel` 中 `gpt-5.4` 仍被拒，断言 `:334` 不变）。

- [ ] **Step 6: Commit**

```bash
cd "/Users/cervine/Documents/Sakrylle/Sakrylle API/.claude/worktrees/grok-image-fix"
git add backend/internal/service/openai_images.go backend/internal/service/openai_images_test.go
git commit -m "fix(images): accept grok-imagine-image* on /v1/images/generations

放开图片模型前缀白名单，新增 grok-imagine-image 前缀（覆盖 lite/pro/edit，
排除 video）。修复 grok-imagine-image-lite 经 sub2api 图片端点返回的
400 \"images endpoint requires an image model\"。链路其余部分模型无关，
restrict_models + channel_model_pricing(row 326) 兜底未配置模型。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: 全后端编译 + vet 验证

**Files:** 无改动，仅验证。

- [ ] **Step 1: 编译整个后端**

Run: `cd backend && go build ./...`
Expected: 无输出、退出码 0（确认改动不破坏编译）。

- [ ] **Step 2: vet service 包**

Run: `cd backend && go vet ./internal/service/`
Expected: 无输出、退出码 0。

- [ ] **Step 3:（若本机装了 golangci-lint）lint 改动文件**

Run: `cd backend && golangci-lint run --max-same-issues=0 ./internal/service/ 2>/dev/null || echo "golangci-lint 未安装，跳过（CI 会跑）"`
Expected: 无新增 finding，或打印跳过提示。CI 在 PR 上会强制跑（见根 CLAUDE.md「CI green gotchas」：用 `--max-same-issues=0` 避免截断）。

无需 commit（本任务不改文件）。

---

### Task 3: 部署 + 东京端到端验证（需用户授权部署）

> ⚠️ 本任务改动会进生产镜像。合并/推送/部署前必须由用户确认（参考 superpowers:finishing-a-development-branch 决定合并方式）。单元测试无法覆盖「放行后整条链路真能出图 + 计费」，必须实打验证。

**Files:** 无代码改动。

- [ ] **Step 1: 合并到部署分支并推送（用户授权后）**

```bash
cd "/Users/cervine/Documents/Sakrylle/Sakrylle API"   # 主工作区
git checkout theme/monet-purple
git merge --no-ff fix/grok-image-local-400
git push origin theme/monet-purple
```
Expected: GHA 触发构建 `ghcr.io/ranshen1209/sakrylle-api:purple`。

- [ ] **Step 2: 东京拉取新镜像并重启**

```bash
ssh ssh-tokyo 'docker pull ghcr.io/ranshen1209/sakrylle-api:purple && cd /opt/stack && docker compose up -d sub2api'
curl -sS https://sub.sakrylle.com/health
```
Expected: 新镜像拉到（代码改动 → 镜像变化 → `up -d` 会重建容器，非 no-op），`{"status":"ok"}`。

- [ ] **Step 3: 实打图片端点（用有效 group 22 key）**

```bash
curl -sS -o /dev/null -w 'HTTP %{http_code}\n' \
  -H "Authorization: Bearer <group22-key>" -H "Content-Type: application/json" \
  https://api.sakrylle.com/v1/images/generations \
  -d '{"model":"grok-imagine-image-lite","prompt":"a red apple","n":1}'
```
Expected: **HTTP 200**（不再 400）。再去掉 `-o /dev/null` 看 body，应有 `data[].b64_json` 或 `data[].url`。

- [ ] **Step 4: 核对计费触发**

在东京库查最近一条该模型的账本记录，确认按 row 326（`billing_mode=image`、`per_request=0.02`）× group 22 `rate_multiplier` 计费，未走 token/零费路径：

```bash
ssh ssh-tokyo "docker exec sub2api-postgres psql -U sub2api sub2api -c \"SELECT model_name, channel_id, group_id, prompt_tokens, completion_tokens, cost, created_at FROM usage_logs WHERE model_name LIKE 'grok-imagine-image%' ORDER BY created_at DESC LIMIT 3;\""
```
Expected: 有记录、`cost` 非 0（= 0.02 × group 22 multiplier；注意 group 22 multiplier=0.001 → cost 极小但应 >0）。若 `usage_logs` 表名/列名不符，先 `\d` 查实际账本表结构再调整查询。

---

## 风险与回滚

- **回滚**：单点函数改动，`git revert` 该 commit 即可；或部署回滚到上一镜像 tag。
- **capability 分类风险**：`classifyOpenAIImagesCapability` 对非 `gpt-image-` 前缀归类 `Native`，若 channel 14 账号选不中会回退 basic——Step 3 若 200 但报「无可用账号」类错误，需查 `account.SupportsOpenAIImageCapability` 对该账号的判定（openai+apikey 应满足）。
- **未配置模型**：前缀放宽不会误开——`restrict_models=true` 对没有定价行的 `grok-imagine-image-pro`/`-edit` 仍 503。
