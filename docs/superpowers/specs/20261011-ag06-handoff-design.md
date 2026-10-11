# AG06-HANDOFF 设计：Campaign 送 Matrix 与 Motion（HUI-2391 / HUI-2747）

日期：2026-10-11　分支：`codex/20261011-ag06-handoff`　基线：`origin/main` @ `0d86be4`

## 范围

本轮只交付获客侧（leads-engine）能在本仓内闭合的切片，不跨到 AG10（public-ai matrixdraft）、AG09（Motion 工程）、AG01（公共注册/SSO/资产）的实现。

- HUI-2391 切片 A：源端 HTTP 客户端对接 AG10 持久草稿 API；源端持久记录草稿引用（draft_id、状态、published/charged 恒 false、plan/item/external 为 null）；BFF 路由 `POST/GET /api/v1/campaigns/{id}/matrix-drafts`；受 `FEATURE_MATRIX_HANDOFF` 控制，默认 off。
- HUI-2747 切片 A：把 `campaignmotion.Prepare` 的 nil 接收端改为可观测的计数端口；端口被调用即失败关闭（AssertZero 门禁保存路径）；`model_calls` 视图当前为常量 0（硬编码），E3 接线后改取端口计数。

## 不做（留给后续）

- Web 页面（CampaignMotionPanel 同类的草稿面板）：未做，记入 remaining。
- 真实 Motion 变体工厂、RefVerifier、StudioPort、render、账务：归 AG09 / HUI-2737 SDK 钉版，本仓不实现。
- 外发、审批、排期、扣费、账号：不做。

## 决定与理由（协调者自裁，无人类可问）

1. **基线选 `origin/main` 新建独立树，不续接旧树。** 旧树 `hui-2391-r3`（dfe46b6）、`ext/hui-2747-campaign-motion`（ad478e8）、`claude/hui-2747-campaign-creative-factory`（b58ea17）的提交都已在 origin/main（已逐一 `merge-base --is-ancestor` 核实），是 9/29–10/4 的历史作者，不是在途树。根 checkout（local main 368eaa0，落后 origin/main）与 `20261011-ag06-leads-integrator`（AG06 父级集成树，有他人 WIP）均不触碰。
2. **AG10 客户端放新包 `server/internal/matrixdraftclient`，不改 `matrixhandoff` 的内存 fixture。** 现有 `matrixhandoff` 测试锁定的是夹具语义（`draft_recorded`、`published=false` 等），混入真实 HTTP 会破坏这些断言。新包单独承担真实 HTTP，旧包保留为纯夹具回归。
3. **source_app 固定为 `leads`，content_type 固定为 `copy`。** 依据 AG10 `allowedType`（`leads`→`copy`）。客户端传其它 content_type 一律 422 `wrong_type`，服务端不替客户端猜。图片/视频不走此路由（图片归 `product-image`，视频归 Motion）。
4. **return_target 由服务端生成**：`{app:"leads", path:"/campaigns/<campaign_id>"}`，不接受客户端传 path，避免开放重定向与路径注入。路径需满足 AG10 `validReturnPath`（以 `/` 开头，非 `//`，无反斜杠、无 `://`、无控制字符）。
5. **tenant 来自会话成员，不来自请求体。** 发给 AG10 的 `X-Matrix-Tenant` 与 body `tenant_id` 都取 `c.Member.TenantID`。AG10 的 `X-Matrix-Tenant` 仍是 E3 临时信任，只允许 loopback 使用，本轮不跨 loopback 暴露。
6. **幂等与恢复**：Leads 侧以 `(tenant_id, campaign_id, revision, asset_hash)` 唯一约束持久化引用。每次 POST 都先经 AG10，不做本地短路：服务端决定重放、撤权与许可过期，本地短路会绕过这些判定（实施中已改正，见「实施偏差」）。AG10 成功后才写本地行；同键已存在时 `SaveMatrixDraftRef` 返回既有行且不覆盖。AG10 超时而本地未写入时，同字段重放得到同一 draft_id。已有本地行时若 AG10 不可达，GET 列表仍可读到既有引用，用于恢复。
7. **错误映射**：AG10 `grant_unknown|grant_expired|grant_revoked|license_expired` → 403 原码透传；`wrong_type|old_version|hash_conflict|idempotency_conflict` → 409；`bad_request` → 422；`store_failed|grant_lookup_failed` 与传输失败 → 503 `matrix_unavailable`（可重试，不写本地行）。Leads 主链路不受影响。
8. **未知指标/计划/条目一律 null。** AG10 当前 Draft 不返回 matrix plan/item/external post，本地字段保持 null，不写 0，不写成已发布。
9. **FEATURE_MATRIX_HANDOFF 默认 off**，关闭时路由不挂载（404），与 `FEATURE_CAMPAIGN_MOTION` 同模式。开启但 `MATRIX_DRAFT_BASE_URL` 缺失 → 503 `config_gate_matrix`，失败关闭。
10. **2747 的零调用观测**：`Prepare` 接收一个计数端口。端口的 `WriteCreativeProject` 若被调用即返回错误并计数；`creative_project_writes` 取自端口计数、`model_calls` 视图当前硬编码 0（E3 接线后改取端口计数），期望均为 0。不再用 nil 端口证明零调用。

## 实施偏差（如实记录）

- TDD 顺序：`CountingWriter.AssertZero` 的测试与实现在同一条命令中追加，RED 未单独观测。`Prepare` 的 nil 端口和计数端口测试先于实现写入并通过。
- 本地短路：初版设计写了「命中即返回既有行、不再调 AG10」，实现与测试都发现它会绕过撤权判定，已删除，并把测试改为「撤权后重放 403 且行不变」。
- 旧的内存 `matrixhandoff` 包与其夹具测试未改动，仍是纯夹具回归。
- 实时 AG10 首次运行时 sqlite 初始化较慢，进程在 LISTEN 之前曾停滞数秒，属环境观察，不是产品缺陷。

## 实时证据（非生产，loopback）

- AG10 来源：`public-ai` commit `a4b352f4`，用 `git archive` 导出到 scratchpad 后构建，未改动 AG10 工作树。
- 启动：`matrixdraft -db <file sqlite> -grants <测试 grant 文件> -listen 127.0.0.1:18733`。
- 客户端实测：`MATRIX_DRAFT_LIVE_URL=http://127.0.0.1:18733 go test ./internal/matrixdraftclient -run TestLiveAG10Loopback -v` 通过：创建 201、重放 200 同 id、receipt、同键异资产 409 idempotency_conflict、撤权 403 grant_revoked、跨租户 403 wrong_tenant。published 与 charged 均为 false。
- 未做：经 BFF 对真实 AG10 的端到端（需要为测试租户预置 grant，并启动带会话的 leads 服务），记 NOT_RUN。

## 升级给 AG00（escalations）

- E1：AG10 草稿 API 的真实调用方身份仍是 `X-Matrix-Tenant` 临时头（E3），需 AG01/2375 供给 SSO/服务凭证后替换。
- E2：AG10 当前 Draft 不返回 matrix plan/item/external post；来源侧的 plan/item/receipt 完整回写需 AG10 或 AG01 提供字段后再钉版。
- E3：2747 的真实 Motion 接收、RefVerifier、StudioPort、render 与账务观测需要 AG09 与 HUI-2737 SDK 版本钉定，本轮不实现，需 AG00 裁定由谁接收端口。
- E4：独立 Spec / Standards 审查需要非作者子代理。本会话没有派发子代理的工具，记 NOT_RUN，需 AG00 另派。
- E5：AG10 PR #289 为 draft，未合入；本仓对其 HTTP 契约的钉定以 commit `a4b352f4` 为准。
- E6：资产真实性未由本仓核验。`asset_id`、`asset_hash` 由调用方提供，AG10 只检查授权（grant）与许可（license）。资产归属与哈希的真实来源需 AG01 资产服务供给，本轮未做。
- E7：源端 Web 草稿面板未实现（设计范围之外的 UI，列入 remaining）。
- E8：2747 的真实 Motion 接收端、AG09 变体工厂、RefVerifier、render 与账务观测未实现（见范围与 E3）。
