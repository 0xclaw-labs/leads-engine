# MX-OPS handoff — HUI-2747 获客 Campaign 制作交接增量

- Group / Ticket: G08 / HUI-2747（保留 In Progress；不接管 Motion/GoBoost/公共服务）
- Repo: 0xclaw-labs/leads-engine
- Branch: `mx-ops/2747-campaign-handoff-queue`
- Base: `912fcb78`（origin/main）
- Head: `43e4f14`（交付时更新为本分支最新 SHA）
- Worktree: `.worktrees/mx-ops-2747-leads-engine`
- Contract: `mx-ops-c0/v1 @ 6ffc90ca`（`docs/mx-ops/2747/contract-version.md`；无 additive）

## Scope done（逐条对工单 2026-10-18 MX-OPS 增量）

- [x] 最小合法事实交接：`server/internal/campaignmotion` 新增 optional `execution_preference`（仅命名模板 `immediate|night_batch|free_idle|paid_elastic`）；伪造 `priority`/`requested_priority` 直接 `ErrInvalid`；不调用模型、不写创作工程（`ModelCalls/PieceGenerated/PieceSent` 仍恒 0/false）。
- [x] 客户端事实校验：`web/src/lib/campaignMotionQueue.ts` `validateHandoffFacts` 只允许 campaign_goal/target_audience/approved_facts/authorized_asset_ids/CTA/channels/budget_attribution_id/payer/execution_preference/motion；CRM 私密键（chat/contact/phone/leads/customer/name/privacy…）拒绝。
- [x] 队列等待回流：严格读 `job_queue_view`（null≠0、ETA 可空、internal_debug 删除）；区分「等待云端产物」与「等待销售跟进/人工审核」；GPU 忙不阻塞 CRM 文案明确。
- [x] 费用归因：campaign_id/creative_id/variant + model_calls/channel_count；N 渠道参数化 Variant 不产生 N 次生成；只展示，唯一收费归星模 + 公共 Billing（不建钱包）。
- [x] 不自动外发：`autoOutreach:false`/`autoConversion:false`；队列完成只记回活动卡，不触达客户/发布/记录转化。
- [x] 活动截止 vs 夜窗：窗口晚于截止只提示「赶不上」，不自动升级付费/改活动。
- [x] 偏好选择只从 capability `allowed_templates` 取；未提供时不传数字优先级。

## Tests

- `cd web && npx vitest run` => 见最终报告（新增 4 个 mx-ops 测试文件：queue/execution/return/campaign-wait）。
- `cd server && go build ./...` => PASS
- `cd server && go test ./internal/campaignmotion/ ./internal/httpapi/ -count=1` => 见最终报告（新增 `preference_test.go` 3 例）。
- 已知基线：`npx tsc --noEmit` 有 3 个既有测试类型错误（channelInteraction/pageMap），与本票无关。

## Code review

- Standards: 待补。
- Spec: 待补。

## Blocked / UNKNOWN

1. 真实 RefVerifier/StudioPort/账务观测仍未交付 → `UNKNOWN`；本增量只做交接事实与展示，不声称执行授权。
2. 真实 `job_queue_view` 投影/执行策略/权益：未接；用合同 fixture 验证，真实接线 `UNKNOWN`。
3. 真实 Motion 工程/多渠道 Variant/渲染回流：`NOT_RUN`；本票不建 renderer/editor 副本。
4. 真实费用与转化归因：`NOT_RUN`；只展示合同金额/承担方，不写转化回创作工程。
5. `execution_preference` 是否被专业工具真实采用：本票只保证交出的合法命名偏好；工具侧采用 `UNKNOWN`，归 3826/专业工具接线。

## Real-integration status

未在真实 GPU/生产验证（UNKNOWN）。验收：Campaign→工具→星模排队/预约→候选回流、权限拒绝、预算不足、原 job 恢复、无自动外发、费用可追溯；由 HUI-3836 窗口执行。

## Linear receipt

Lead 统一回报；本代理不写 Linear。
