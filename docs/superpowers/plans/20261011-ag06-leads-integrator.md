# AG06 Leads 集成人实施计划（HUI-2626 / HUI-2598）

依据：`docs/superpowers/specs/20261011-ag06-leads-integrator-design.md`。基线 `origin/main` `0d86be4`。每步有可验证产出；失败保留首轮结果，不删断言。

执行方式：本环境无子代理派生工具，按 `superpowers:executing-plans` 由本作者逐步执行。独立审查记 NOT_RUN，见 spec D5。

## 步骤

**P0 基线（已完成）**
- `cd server && GOWORK=off go test ./... -count=1` → 全部 26 个含测试包 ok，退出 0。
- `cd web && npm ci && npx vitest run` → 40 文件 267 测试通过。

**P1 本机三段链启动（scratchpad，不入仓）**
- 写 identity stand-in（Node，`127.0.0.1:29310`），实现 `POST /internal/v1/identity/session/resolve`：校验 `X-PilotSeaView-Internal-Token`，把 session_token 映射到 principal（测试值）。
- `GOWORK=off go build -o <scratch>/leads-server ./cmd/leads-server`。
- 用 `provision-tenant` / `provision-member` / `provision-grant` 建租户 A、B 与成员（owner、sales）。
- 启动 Go（`LEADS_HTTP_ADDR=127.0.0.1:29300`，`FEATURE_NOTIFY_INGEST=true`，身份与内部令牌均为本机生成测试值）。
- 启动 BFF（`next dev -p 29330`，`LEADS_SERVER_URL=http://127.0.0.1:29300`，`LEADS_INTERNAL_TOKEN` 同 Go）。
- 验证：`curl 127.0.0.1:29300/healthz` 与 `curl 127.0.0.1:29330/api/whoami`（带 cookie）返回预期。
- 产出：启动日志与 healthz、whoami 真实输出。

**P2 双租户 Touch 来源线索注入（真实 HTTP）**
- 按 `server/internal/notifyingest/envelope.go` 构造 `directed-event/v1`，事件 `lead.authorized_submitted`，`trace_id=lead:<source_ref>`，带 `source_version`、`return_target`，HMAC 为 `X-Notify-Signature: sha256=<hex>`。
- 分别投给租户 A、B 各一条；重复投递同 trace 与版本必须幂等。
- 断言：A 的 Today 出现该线索，B 不出现；重复投递不产生重复记录。
- 产出：请求、响应与计数（不写真实密钥）。

**P3 Today → 共享详情 → 跟进 → 下一步 → 回原位（浏览器）**
- Playwright（scratchpad 安装，系统 Chrome，headless）；cookie 与 `leads_tenant_id` 注入等效工作范围，服务端仍逐请求校验。
- 1440×900 与 390×844 各跑一遍：Today → 打开线索详情 → 记录跟进 → 安排下一步 → 返回原筛选与滚动 → 刷新 → 重登。
- 记录每次点击、输入次数、重复输入、错误与恢复结果。
- 断言：刷新与重登后服务端事实不变；A/B 切换无残留。
- 产出：截图与 JSON 计数。

**P4 失败路径**
- 服务端 500 或网络中断时输入保留且有恢复入口；保存失败不显示成功。
- 产出：对应截图与 JSON。

**P5 缺陷修复（仅当 P2–P4 发现缺陷）**
- 每个缺陷先写失败测试（TDD），再最小修复，再跑 P0 全量。
- 不改 HUI-1682 真实文本，不发送渠道消息。

**P6 HUI-2598 度量（可能 NOT_RUN）**
- 旧基线 `b58ea17`，新版本 `origin/main` HEAD；三个代表任务各 5 轮；记录主动时间、等待时间、步数、输入、误操作、完成与恢复。
- 若本会话无法完整跑完，记 NOT_RUN 与解除条件，不写改善百分比。

**P7 证据包**
- `docs/audits/hui-2626/ap-20261011/README.md`：命令、真实输出摘要、PASS/FAIL/NOT_RUN/UNKNOWN/UNSUPPORTED 逐条、截图位置。

**P8 收口**
- 固定提交（尾行 `Co-Authored-By: Claude Haiku 5.5 <noreply@anthropic.com>`）。
- `git push -u origin codex/20261011-ag06-leads-integrator`；`gh pr create --draft`（描述尾行 `🤖 Generated with [Claude Code](https://claude.com/claude-code)`）。
- 代码审查未派出时，PR 描述与 Linear 写明 NOT_RUN。
- Linear：写前刷新，两票各增量评论，写后回读。不改状态、负责人、依赖、标签、关系，不重开 Done。

## 验证矩阵

| 项 | 命令或方法 | 证据位置 |
| --- | --- | --- |
| Go 全量 | `GOWORK=off go test ./... -count=1` | 本文件 P0 |
| Web 全量 | `npx vitest run` | 本文件 P0 |
| 双租户注入 | 真实 HTTP `POST /internal/v1/notify/ingest` | 证据包 |
| 浏览器主路径 | Playwright + 系统 Chrome | 证据包截图与 JSON |
| 度量 | 人工计时协议 | 证据包（可能 NOT_RUN） |
| 审查 | 非作者 Spec / Standards | NOT_RUN，escalations E1 |
