# AG06-HANDOFF ap-20261011（第二波）设计：HUI-2391 送矩阵回流 + HUI-2747 变体源端形状

执行者：AG06-HANDOFF（实施）。原票：HUI-2391、HUI-2747。
仓：`https://github.com/bianjiefilm/leads-engine`。独立树：`/Volumes/MobileHD/Work/派诺生态/_worktrees/20261011-agg/ag06-handoff`。
分支：`codex/20261011-agg-ag06-handoff`。基线：`origin/main = 9439a27`（PR #55 + #56 合并点，fetch 后实测）。

## 0. Root 裁定引用（2026-10-11，brainstorm 答案已固定，不再向用户提问）

- **R17a**：合法源端选择→BFF 动作→本地真实 matrixdraft 服务（loopback，public-ai main 构建）创建草稿（含 license facts）→同事实回查→来源 plan/item/receipt 落库→回查接口；同键同事实回同草稿、异参 409、过期/撤权/错租户/类型不匹配负例全套；敏感过滤保持；未知统计 null；draft≠已发布/已触达/转化。正式 SSO/Registry 属 AG01-2375→NOT_RUN+解除条件；本地旅程用 identity stand-in（scratchpad，token chmod600）。
- **R17b**：用已获授权 Campaign 事实与品牌版本，接 motionref sdk v0.4.0 consumer-link 契约构造变体交接对象（参考 pie 的 consumerlink 模式，语义按 campaign 调整：多渠道 variant、参数改复用旧媒体）；`Prepare(..., nil)` 必须变成真实 receiver 绑定或诚实拒绝——nil port 可观测（零调用有断言）而非静默；render 执行/原生编辑归 AG09（本批不在）→真实变体成品 NOT_RUN+解除条件；费用/权限语义按 motionref 契约，不在 Leads 造视频编辑器。
- **R17c**：发布来源回流（plan/item/receipt→回原 Campaign 位置）路径完整；跨租户/撤权/过期/异参幂等负例；返回原位置不自动创建发布项。
- **R17d**：`.github/workflows` 加一个 job：`cd server && GOWORK=off go build ./... && go test ./... -count=1`；保持既有 evidence-gate 不动；以本分支全绿为验收。
- **R17e**：2391 选择→送矩阵→回查→回原位（Playwright scratchpad，channel:'chrome'，1440/390 双视口，截图+JSON）；本地真实 matrixdraft+pie 式 stand-in。

**Root 修订范围（撞车对账后）**：main 已含 PR #55（另一会话 AG06-HANDOFF 切片：matrixdraftclient + BFF POST/GET matrix-drafts + 迁移 0029 + store + CountingWriter）与 #56。本波 = #55 自报 remaining + R17 未覆盖项，不得重复实现 #55 已合内容。

## 1. 与 #55 的分工对账表

| 事项 | #55（已合，main 9439a27） | 本波（ap2） |
|---|---|---|
| matrixdraftclient HTTP 客户端 | 已交付（含 live_test 客户端级） | 不重做；live 测试升级为经 BFF 全链（WI-A） |
| BFF POST/GET /campaigns/{id}/matrix-drafts | 已交付（默认 off） | 不重做；新增 GET 回查（receipt）与 POST 来源事件（WI-D） |
| 迁移 0029 matrix_draft_refs | 已交付 | 不动其列；新迁移 0031 只增量（来源钉/指标/事件幂等） |
| store SaveMatrixDraftRef 等 | 已交付 | 不重做；新增事件落库与来源钉存取 |
| 2747 nil port→CountingWriter | 已交付（Prepare 传端口+AssertZero） | 不重做；在其上建变体源端形状（WI-C） |
| Web 源端面板/走查/真实 AG10 全链 | #55 自报 remaining | 本波交付（WI-A/E） |
| 2747 consumer-link 契约形状 | #55 自报 remaining | 本波交付（WI-C） |
| 回流路径+负例 | #55 自报 remaining | 本波交付（WI-B/D） |
| CI go 门 | 未加（实测仅 evidence-gate.yml） | 本波交付（R17d，#55/#56 均未加） |

## 2. 现状关键事实（9439a27 实测）

- `internal/matrixhandoff`：内存版最小来源/同事实草稿 ref/敏感过滤/未知统计 null（本轮不挂 HTTP，保持）。
- `internal/campaignmotion`：`Prepare` 不调 writer；`CountingWriter` 计数+拒绝（`ErrWriterCalled`）；handler 传端口并 `AssertZero`。
- `internal/matrixdraftclient`：契约字段固定（schema matrix-draft-request/v1、source_app=leads、copy、X-Matrix-Tenant）；`GetReceipt` 已有；错误面 APIError{Status,Code}。
- BFF：`FEATURE_MATRIX_HANDOFF` off 即不挂路由；on 且无 URL→503 config_gate_matrix；每次（含重放）先经 AG10 裁决；拒绝/不可达/存储失败不写本地行。
- AG10（public-ai origin/main `6d1aadea`，≥d8facafc）：`internal/matrixdraft/cmd/matrixdraft`（-db file sqlite -listen 127.0.0.1 -grants file）；403 wrong_tenant/grant_expired/grant_revoked/grant_unknown/license_expired；409 wrong_type/old_version/hash_conflict/idempotency_conflict；receipt=GET /v1/matrix-drafts/{id}/receipt（**无 plan/item/external 字段**——这些 null 由 AG10 侧结构保证）。
- leads 为公仓、public-ai 为私仓；leads CI 为 hosted runner、无跨仓凭据 secret（pie 的 `PUBLIC_AI_SDK_READ_KEY` 模式在 leads 无授权复制）→ **leads server/go.mod 不引入私有 sdk 模块**（见 §4 决策）。
- sdk/go v0.4.0（tag `sdk/go/v0.4.0`→`6065fba6`）契约面：Ref（13 键+ExpiresAt）、Validate（R0/R3/R4/R5/R6/R8/R9 权限矩阵）、CanonicalRefBytes（spec 3.1.2 手写 canonical）、ComputeDigest（sha256 over "motionref.ref.v1\n"+canonical）、BuildConsumerHandoffLink（https 恒可/http 仅 loopback、base 无 query/fragment/userinfo、五存储键 JSON、EncodeReturnToken 手写百分号编码）、param:<path> 三重门（can_parameter_edit+parameter_edit+param 声明）、MaxParamActions=64。

## 3. 工作项与验收

### WI-B：来源钉 + 回流存储层（迁移 0031 + store）
- 0031 增量（不动 0029 已有列）：`matrix_draft_refs` 加 `asset_source_ref TEXT NOT NULL DEFAULT ''`（hash 来源钉：调用方申报的出处引用，token 校验，非验证）、`matrix_reach/matrix_likes/matrix_leads INTEGER`（可空——未知统计恒 null）；新表 `matrix_draft_source_events(id, tenant_id REFERENCES tenants, draft_ref_id, provider_event_id, kind, fingerprint, created_by, created_at, UNIQUE(tenant_id, provider_event_id))`。
- store 方法：`AppendMatrixDraftSourceEvent`（同事件同指纹→幂等返回既有；同事件异指纹→`ErrIdempotencyConflict`；revoked 行拒 result；plan/item/external 只落 token 合法值；指标 NULL 语义保持）；`SetMatrixDraftRefRevoked`；`Get/List` 带新列。
- published/charged 仍 CHECK=0（草稿≠发布≠扣费）。

### WI-D：BFF 回查 + 来源事件端点（含负例全套）
- `GET /api/v1/campaigns/{id}/matrix-drafts/{draftID}/receipt`：同事实回查——经 `matrixdraftclient.GetReceipt` 打**真实 AG10**，返回 live receipt + 本地行视图（plan/item/external null 保持）；AG10 不可达→503 matrix_unavailable（如实，不回退本地行）；跨租户→404。
- `POST /api/v1/campaigns/{id}/matrix-drafts/{draftID}/source-events`（FEATURE_MATRIX_HANDOFF 同门）：会话鉴权（真实矩阵 webhook 机器鉴权属 AG01 链→NOT_RUN+解除条件）；body 严格 decode（DisallowUnknownFields，phone/email 等私域键→400）；kind∈result/revoke/replay/edit/withdraw；负例：跨租户 404、未知草稿 404、未知 kind 422、同事件异参 409 idempotency_conflict、revoked 后 result 403 grant_revoked、返回视图 published=false 且**不创建任何发布项**（测试断言唯一写入=事件行+引用行更新）。
- `asset_source_ref` 加入创建 body（可选，token 校验），落库并在视图中回显（来源钉）。

### WI-A：经 BFF 对真实 AG10 的全链 live 测试（本地证据，env 门）
- 新 `server/internal/httpapi/matrix_draft_live_test.go`：`MATRIX_DRAFT_LIVE_URL` 未设即 skip（CI 零影响）。设好时：harness（带会话 fake identity + 真 sqlite 文件）+ 真实 AG10 进程 + 预置 grant 文件（tnt_live_a/grant_live_1=active）。
- 旅程：POST 创建 201（draft_id 非 0、published=false、charged=false、plan/item/external=null）→ 同事实重放 200（同 draft_id）→ GET 列表 1 行 → GET receipt 回查（同事实）→ 负例：异参同键 409 idempotency_conflict（改 asset_hash）、过期许可 403 license_expired（license_expires_at 为过去）、撤权 grant 403 grant_revoked（重放后撤权同样拒绝）、未知 grant 403 grant_unknown、类型不匹配 422 wrong_type；每负例后本地行不变化。
- 本地运行业：public-ai origin/main 构建产物放 scratchpad；transcript 存证据包。

### WI-C：2747 变体源端形状（motionref v0.4.0 契约钉死）
- 新 `server/internal/campaignmotion/variantref.go` + 测试：
  - Ref 线材（13 键+ExpiresAt）、R0-R9 校验子集（与本波用到的构造路径全量等价：R0 view 必需；R3 flattened 禁 open/param/creative/export；R4 param 需 open+rank≥1；R8 动作词表+param 声明格式+去重+预算；R9 三重门）、CanonicalRefBytes、ComputeDigest（域 `motionref.ref.v1\n`）、五存储键 consumer link（https 恒可/http 仅 loopback、base 无 query/fragment/userinfo、EncodeReturnToken 同字母表）。
  - **与 sdk v0.4.0 字节级一致由 golden vectors 钉死**：用真实 sdk（本地 module cache，GOPRIVATE）生成固定 Ref 的 digest 与 consumer link，golden 值+生成命令写入测试注释。不引入私有模块依赖（leads 公仓 hosted CI 无跨仓凭据——UNSUPPORTED 决策+解除条件：leads 获授权配置 `PUBLIC_AI_SDK_READ_KEY` 式 secret 后切换为模块直引）。
  - campaign 语义：每渠道一 Ref（variant_id=`var_`+sha256(campaign\x1fchannel)[:16] hex）；owner.tenant_id=会话租户；owner.account_id=调用方供给（缺→诚实拒绝 ErrNoMotionReceiver）；origin_app=leads；origin_context_ref 指回原活动（`leads/campaigns/<id>/motion-handoffs#<hash8>`，返回目标）；权限=param 改复用旧媒体最小诚实矩阵：view+open+parameter_edit、editable_level=structured、creative/export/pay 恒 false（原生编辑归 AG09）；param 路径声明=授权素材复用槽（`media.asset_<n>`，值域=authorized_asset_ids，复用映射留在 Leads 侧文档，不伪造新媒体）。
  - 拒绝面：无 motion 工程（project/revision 空）→拒绝不伪造；外族 run（variant_binding.owner_tenant_id≠会话租户）→403 foreign_tenant；错租户（motion.campaign_id≠路径 id）→既有 motion 错误；过期（expires_at 已过）→422 variant_expired；digest 与内容不绑定→拒绝出形状。
- HTTP 挂接：motion POST 增可选 `variant_binding`（owner_account_id 必填、owner_tenant_id 可选须等于会话租户、expires_at 可选 RFC3339）；提供时逐渠道构建变体 ref，document.Variants 增量键（variant_id/digest/origin_context_ref，全 id 事实）入 export（exportKey 白名单同步）；不提供→维持 #55 行为（如实"未绑定"）。**不出链 HTTP 端点、不为真实流量签发链接**（pie 先例；正向腿 NOT_RUN，解除条件=AG09 接收端+真实 Motion 工程就绪）。
- CountingWriter 断言保持：变体形状构建零 creative-project 写入（AssertZero 仍过）。

### WI-E：Web 源端面板 + 浏览器走查
- `web/src/components/MatrixDraftPanel.tsx` + `web/src/lib/matrixDraft.ts`（presenter：null 保持、无"已发布/已送出/已扣费"措辞），挂载于活动所在位置 `(shell)/leads/[id]/page.tsx`（CampaignMotionPanel 旁，单行挂载——面板必须可达，详情页仅此一行动	diff，Root 修订范围明确要求 Web 源端面板）。
- 走查（真实栈）：identity stand-in（:29310，token chmod600 scratchpad）+ 真实 AG10 matrixdraft（file sqlite+grant 文件，127.0.0.1）+ 真实 leads-server（:29300，FEATURE_MATRIX_HANDOFF=on、MATRIX_DRAFT_BASE_URL 指向真 AG10）+ next BFF（:29330）；种 touch_campaign 线索为活动位。
- Playwright scratchpad，channel:'chrome'，1440 与 390 双视口：打开活动位→面板填表（asset/hash/revision/license/brand/payer）→送矩阵（201，面板显示 draft_id、published=false、plan/item/external="—"）→回查（receipt 拉真实 AG10，同事实回显）→回原位（return_path=/campaigns/<id> 即本页，无自动创建发布项）。截图+JSON 存证据包。

### R17d：CI go 门
- 实测 `.github/workflows/` 仅 evidence-gate.yml（#55/#56 均未加）→ 新增 `go-gate.yml`：pull_request（paths server/**）+ push 保护；`cd server && GOWORK=off go build ./... && GOWORK=off go test ./... -count=1`；hosted ubuntu；无私有依赖故无需凭据。evidence-gate 不动。

## 4. 明确不做 / NOT_RUN / UNSUPPORTED

- 真实变体成品/render 执行/原生编辑：AG09→**NOT_RUN**。解除条件：AG09 接收端与真实 Motion 工程就绪，用本波形状直接出链验收。
- 正式 SSO/Registry、矩阵 webhook 机器鉴权：AG01-2375→**NOT_RUN**（本地旅程 identity stand-in）。解除条件：AG01 交付后把 stand-in 换成真实 identity/webhook。
- sdk 模块直引：**UNSUPPORTED（环境）**——leads 公仓 hosted CI 无跨仓私仓凭据；以 golden vectors 钉死 v0.4.0 契约。解除条件：leads 仓获授权配置跨仓只读 secret 后切直引并复跑字节一致性测试。
- 真实矩阵发布/触达/转化/扣费：本波恒 NOT 声明；published/charged 列 CHECK=0。
- 不碰：lightcopy/platformtask/reception、Today/workbench 其余部分、`.github/workflows/evidence-gate.yml`、public-ai/pie 任何工作树（只读/只构建）。

## 5. 纪律

每工作项一 commit；先红后绿；`GOWORK=off`；凭据/令牌只入 scratchpad（chmod600）不入仓不入日志；全量回归逐字记录；证据包 `docs/audits/hui-2391+hui-2747/ap-20261011/`。
