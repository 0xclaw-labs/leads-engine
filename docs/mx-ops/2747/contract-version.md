# HUI-2747 — C0 合同采用记录

- 合同：`mx-ops-c0/v1`
- 发布 commit：`6ffc90ca`（0xclaw-labs/public-ai，`docs/contracts/mx-ops-c0/v1/`）
- 上一版快照：`45b32f4b`（G00 errata 只影响 `config-command-response` 消费方；本仓不消费该对象，仅记录 superseding）
- 取用日期：2026-10-19
- 校验：37 artifact digest / 56 vectors 由 G01 与 Lead 复核；本仓只消费下列对象。

## 采用对象与字段

| 对象 | 采用字段 | 用途 |
|---|---|---|
| `execution_policy` | `template`/`time_window`/`dispatch`/`compute_permission`/`cost_bearing`/`service_level`/`enabled`/`policy_version` | 活动制作执行偏好与费用承担（只展示交集，不做服务端政策判断） |
| `job_queue_view` | 全部公开字段（phase/stage/wait_reason/jobs_ahead/waiting_jobs_ahead/running_jobs/可空 ETA/next_eligible_at/policy_version/capacity/borrowed/as_of）；`internal_debug` 仅内部 | 活动卡/制作任务回流真实等待与 ETA |
| `execution_grant` | `cost_bearing`/`price_ref`/`entitlement`/`validity`（只读） | 费用归因展示；不建钱包、不预扣 |
| `error_taxonomy` | `class`/`code`/`retryable`（映射文案） | 失败/等待文案 |

未采用：`model_offering`/`compute_pool`/`config_command`/`config_command_response`。

## 使用方式与边界

- TS 类型副本：`web/src/lib/mx-ops/contract.ts`（从 `6ffc90ca` 原样拷贝，头部标注 sha）。
- fixture：`web/tests/fixtures/mx-ops/`（合同正例）。
- 读取规则：未知=`null`，`0` 是有效测量值；ETA 半未知/倒置拒绝；公开投影删除 `internal_debug`。
- 服务端交接：`internal/campaignmotion` 仅新增 optional `execution_preference`（命名模板，无数字 priority；伪造 priority/requested_priority 直接 `ErrInvalid`）；不复制政策判断，不调用模型，不写创作工程。
- 本仓**不新增**合同字段/枚举；无 additive 需求。
- 真实 RefVerifier/StudioPort/账务观测/队列投影：`UNKNOWN`，由 HUI-3836 窗口验收。
