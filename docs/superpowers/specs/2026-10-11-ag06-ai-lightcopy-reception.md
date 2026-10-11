# AG06-AI 2026-10-11：HUI-1682 轻文案真实调用路径 + HUI-1688 接待 journey 锁定

日期：2026-10-11。原票：HUI-1682（FEAT-0183）、HUI-1688（FEAT-0189）。
基线：leads-engine `origin/main` = `0d86be4780c808505545fbb491569933f74522b2`（PR #53 合入）。
独立树：`/Volumes/MobileHD/Work/派诺生态/_worktrees/20261011-agg/ag06-ai`，分支 `codex/20261011-agg-ag06-ai`。
Root 裁定引用：R15a（分层交付①②③④）、R15b（真实模型 E2E 判定与契约需求）、R15c（账务诚实）、R15d（人工权真实生效）、R15e（专业交接边界）。brainstorm 由 Root 作答，本文件即裁定记录。

## 现状（0d86be4 实测）

- `internal/lightcopy/decide.go`：纯决策包。`Generate` 在 `!model.FromLiveModel || 空` 时拒绝（`model_unavailable`），占位正文拒绝。`ModelOutput.FromLiveModel` 本仓从不置真。
- `internal/httpapi/handlers_light_copy.go`：`handleLightCapability` 硬编码 `model_ready=false`；`handleLightGenerate` 固定传 `lightcopy.ModelOutput{}`，永远 409 `model_unavailable`。
- `internal/platformtask/`：`Client.Phrase` 是唯一平台文本接缝——hold 0.01 CNY、轮询、SUCCEEDED 记 1 分（`recorded`），其余 `not_completed`。`Phraser` 接口只回 `TaskID/CostCents/BillingVerdict`，不回文本；`taskSummary` 不解析 `result_json`。
- `internal/httpapi/server.go`：三键（`PLATFORM_TASK_BASE_URL/TOKEN/ACCOUNT_ID`）齐时装配 `ReceptionPhraser`。
- `internal/reception/engine.go` + `handlers_reception.go`：FAQ/事实/assist/approve/takeover/release/close/epoch 迟到负例已有实现与测试。
- 审计 preflight（HUI-2295）：public-ai 无统一 text source provider；#53 本地 Task 预占 0.01 后 FAILED（无模型上游）、`billing_verdict=not_completed`。

## 本轮做什么（R15a 分层交付）

### ① model_ready 从硬编码改为 config/env 驱动

- `Server` 增加 `LightCopyPhraser`（`platformtask.TextGenerator` 接口）。`Open()` 在三键齐时与 `ReceptionPhraser` 同源装配。
- `handleLightCapability` 的 `model_ready = LightCopyPhraser != nil`。配置在→true；否则 false 并保持诚实拒绝。响应附 `provider` 字段说明接缝身份，`live_charge` 恒 0，note 说明：生成提交计量任务，失败/未完成仍拒绝且不写正文。
- 能力存在 ≠ 上游确有文本模型：`model_ready=true` 只表示"接缝已配置"；真实产出仍以任务 `recorded` + 非空文本为准。

### ② Generate 接 platformtask 真实调用路径

- `platformtask` 增加 `Generate(ctx, GenerateInput) GenerateResult`：沿 `Phrase` 既有模式（hold 0.01、提交、轮询、SUCCEEDED 记 1 分），并解析 GET `result_json` 中的 `{"text": ...}`。capability 默认 `text.generate`，可由 `PLATFORM_TASK_TEXT_CAPABILITY` 覆盖；`PLATFORM_TASK_TEXT_MODEL` 可选。
- 账务语义沿既有：`recorded`（1 分）或 `not_completed`；UNKNOWN/not_completed 不洗白；recorded 但文本为空/不可解析 → 回执如实 `recorded`（真实扣费）+ 409 `model_output_unusable`，正文不写。
- prompt 只含 `lightcopy.Project` 允许清单事实（title/activity_ref/price_cents）+ kind + 语言。联系人全文/销售备注/画像永不外发。
- 生成成功：`Draft.Generated=true`、`Origin=model`、`LiveCharge=result.CostCents`、版本+1、需人工 Confirm；不自动发送（lightcopy 无 send 路由，保持 404）。
- `light_copy_drafts` 的 `sent/published/marketing_permitted` 恒 0 不变量保持；`generated/live_charge` 仅在真实 recorded 任务产出非空文本时可为真。

### ③ 贯穿断言补强（并入④的测试）

reply_id / session / epoch / 资料授权（notice_version、purpose、allow_contact）/ version 贯穿从提案到显式发送的断言。

### ④ 20 问 journey + 负例（stand-in provider）

- stand-in platform-task：httptest 脚本化 正常 / 慢速（超 client 超时）/ 5xx 错误 / 卡死 RUNNING（ctx 超时）四种行为，同一服务器按 capability+idempotency 分派。
- 20 个访客问题跑完整链：FAQ 带来源回答、未知澄清、写入意图拒绝、价格事实（有效/过期/缺失/超时）、注入、撤回知识、模型故障降级、双租户隔离迟到负例、assist→approve→send、takeover 抬 epoch 旧回复不得发、release 交回、close 后拒发、人工稿不被后台覆盖。
- 1682 侧同一 stand-in 跑 text.generate 成功/慢/错/卡四态 + 占位正文拒绝 + 记账行标注测试。

## R15b 真实模型 E2E 判定

普查命令与证据见 `docs/audits/hui-1682+hui-1688/ap-20261011/`。结论（详见 evidence README）：
- leads 侧既有接缝只有 `reception.phrase` 计量，不回文本；`text.generate` 消费端本轮补齐，但 public-ai main 无统一 text source provider，`reception.phrase` 无文本产出上游（#53 本地实证 FAILED）。
- 工作区凭证普查：无 `PLATFORM_TASK_*` 凭证文件（仅 env.example 注释）；`MODELXING_API_KEY` 属 ModelXing 视频/他道，非 platform-task 文本凭证；GoBoost `.env` 属 GoBoost 车道。均不构成"既有接缝+受控凭据可达真实文本"。
- 故全部真实模型测项 NOT_RUN + 解除条件；输出 text source provider 契约需求文档，交 Root 安排 public-ai 车道。本仓不改 public-ai。

## 不做什么（R15c/R15d/R15e 与窗口红线）

- 不改渠道消息发送；`replies/send` 语义不动（只是显式发送路径沿用）。
- quote/hold/charge/refund 沿 platformtask 既有语义；不新增钱包/账本表。
- 不在 CRM 造专业编辑器；crafthandoff 边界与「真实模型未完成」覆盖保留原样（本轮真实模型 NOT_RUN）。
- 不碰 web 侧 Today/workbench/详情页；本轮 web 无最小接线需求（capability 响应向后兼容加字段）。
- 不删树、不 force-push、不合并 PR、不改他人 worktree（含 `_worktrees/20261011-ag06-*` 与 `20261011-agg` 下他人树；构建时 `GOWORK=off` 规避他人 go.work）。

## 验收口径

- 新增单测全绿；26 包 `go test ./...` 不回退（基线逐字记录在证据包）。
- journey 测试覆盖 R15a④ 列表并断言 R15a③ 贯穿字段。
- 账务断言：stand-in 路径的回执行标注测试用途；`not_completed` 不出现 cost_cents>0；`recorded` 必须 task_id 非空。
- 真实模型项：NOT_RUN + 解除条件 = public-ai 统一 text source provider 上线且 `PLATFORM_TASK_*` 三键 + `PLATFORM_TASK_TEXT_CAPABILITY` 指向该 provider capability。
