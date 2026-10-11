# AG06-HANDOFF ap-20261011 证据包（HUI-2391 获客内容送矩阵 + HUI-2747 Campaign→Motion 工厂源端）

执行者：AG06-HANDOFF（接任实施者；前任 I8 静默中断，本包由接任者完成）。
分支：`codex/20261011-agg-ag06-handoff`。基线：origin/main=9439a27（#55+#56 合并点），对账后 merge origin/main（2179b3f，#57）为 merge ad0e184。
设计/计划：`docs/superpowers/specs/20261011-ag06-handoff-ap2-design.md`、`docs/superpowers/plans/20261011-ag06-handoff-ap2.md`。

## 提交台账（本波，接任者）

| commit | 工作项 | 内容 |
|---|---|---|
| ad0e184 | 对账 | merge origin/main（2179b3f，#57 platformtask 增量），零冲突 |
| 0cb3159 | WI-D（切片2，前任 RED 遗存续完） | BFF receipt 读穿 + source-events 落地 + asset_source_ref + store GetMatrixDraftRefByID |
| （WI-C commit） | WI-C（切片4） | campaignmotion/variantref.go：motionref sdk v0.4.0 golden 钉死 + variant_binding 挂接 |
| （WI-A commit） | WI-A（切片3） | matrix_draft_live_test.go 真实 AG10 全链（env 门）+ transcript |
| （WI-E code/证据 commit） | WI-E（切片5） | Web 面板+presenter+走查截图/JSON/transcript |
| （R17d commit） | 切片6 | .github/workflows/go-gate.yml（实测无既有 go 门，evidence-gate 不动） |
| （收口 commit） | 切片7 | 证据包 README+回归记录+spec/plan 接手记录 |

## 逐项裁定（PASS/FAIL/NOT_RUN/UNKNOWN/UNSUPPORTED）

| 项 | 裁定 | 证据 |
|---|---|---|
| 2391 选择→BFF→真 AG10 建草稿（含 license facts） | **PASS** | wi-a-live-transcript.txt（201 draft_id 非 0、published/charged=false、plan/item/external=null） |
| 同事实回查（receipt 读穿真 AG10，null 保持） | **PASS** | 同上 + 单测 TestMatrixDraftReceiptReadsLiveServiceAndKeepsUnknownsNull |
| 同键同事实回同草稿 | **PASS** | live：同事实重放 200 同 draft_id；单测幂等重放 |
| 异参同键 409 | **PASS** | live：brand 异→409 idempotency_conflict；hash 异（同版本）→409 hash_conflict（真实服务代码，设计预估 idempotency_conflict 的偏差如实记录——两者皆 409 冲突族） |
| 过期/撤权/错租户/类型不匹配负例 | **PASS** | live：license_expired 403 / grant_revoked 403 / grant_unknown 403 / wrong_type 422（本地门）；错租户=BFF 会话租户不可跨（单测 404）；每负例本地行不变 |
| 来源 plan/item/receipt→回查接口+回原 Campaign 位置 | **PASS** | source-events 落地+幂等+异指纹 409+revoked 后 result 403（单测）；面板 return_path 事实+回原位链接；走查 JSON backAt=/leads/<lead> 本页 |
| 返回原位置不自动创建发布项 | **PASS** | 单测断言唯一写入=事件行+引用行更新；列表行数不变 |
| 敏感过滤保持 | **PASS** | campaignmotion 既有 scrub 面全绿；source-events 严格 decode 私域键 400（单测） |
| 未知统计 null | **PASS** | 迁移 0031 指标列可空；presenter'—'；走查 unknownsAreDash=true |
| 2747 consumer-link 契约形状（sdk v0.4.0） | **PASS** | variantref golden：digest/link/canonical 与真 sdk（tag 6065fba6）字节一致（生成命令入测试注释） |
| 多渠道 variant + 参数改复用旧媒体 + 返回目标 | **PASS** | variantref_test + httpapi variant_binding 测试（每渠道一 ref、param:media.asset_<n> 复用槽、origin_context_ref 指回活动） |
| 外族 run/错租户/过期拒绝 | **PASS** | ErrForeignTenant→403 foreign_tenant、错 campaign→既有 motion 错误、ErrVariantExpired→422 variant_expired、无收件方 422 motion_receiver（单测+httpapi） |
| CountingWriter 之上真实绑定、nil port 保持可观测 | **PASS** | Prepare 仍零 creative 写入（AssertZero 全绿）；#55 的 CountingWriter 语义未动 |
| 经 BFF 全链走查（R17e） | **PASS** | wi-e-walkthrough-transcript.txt + 8 张截图 + walkthrough.json（1440/390，chrome） |
| CI go 门（R17d） | **PASS**（本地模拟命令全绿；runner 实跑见 PR checks） | go-gate.yml；回归-server/web.txt |
| 正式 SSO/Registry、矩阵 webhook 机器鉴权 | **NOT_RUN** | AG01-2375 链；本地旅程 identity stand-in。解除条件：AG01 交付后替换 stand-in |
| 真实变体成品/render 执行/原生编辑/出链端点 | **NOT_RUN** | AG09 接收端未就绪；本波只出形状不出链（无 HTTP 签发端点，CountingWriter 断言零写入）。解除条件：AG09 接收端+真实 Motion 工程，用本波形状直接出链验收 |
| sdk 模块直引 | **UNSUPPORTED（环境）** | leads 公仓 hosted CI 无跨仓私仓凭据；golden vectors 钉死 v0.4.0。解除条件：获授权配置跨仓只读 secret 后切直引复跑字节一致性 |
| 真实矩阵发布/触达/转化/扣费 | **NOT 声明（恒）** | published/charged CHECK=0；面板恒显示 false+'—' |

## 命令与运行记录

- server：`GOWORK=off go build ./... && GOWORK=off go test ./... -count=1` → 27 包全 ok（regression-server.txt 逐字）
- web：`./node_modules/.bin/vitest run` → 42 文件 278 测试全绿（regression-web.txt）
- live：`MATRIX_DRAFT_LIVE_BIN=<ag10> go test ./internal/httpapi/ -run TestMatrixDraftLiveFullChain -v` → PASS（AG10=public-ai origin/main ebd537cd 构建，bin sha256 在 transcript）
- 走查：`node walkthrough.mjs lead_535e…` → 双视口 PASS（transcript 含栈与端口）

## 事故记录（如实）

- 接任者在清理本票栈时 `pkill -f "leads-server$"` 误杀了前任 I8 scratchpad（/private/tmp/claude-501/.../ag06-continue/）遗留的 29300 leads-server 进程。该栈属本票死掉前任的遗留（其 scratchpad 树未动），接任者以 29301 重建了自己的全栈；另一进程 leads-server-old 未受影响。无其他会话工作受损。

## 与 #55 分工对账（复盘）

#55 已交付且本波未重做：matrixdraftclient（含 GetReceipt）、BFF POST/GET matrix-drafts、迁移 0029、store SaveMatrixDraftRef、2747 CountingWriter+Prepare nil port 可观测。
本波增量：BFF GET receipt 读穿 + POST source-events、迁移 0031（来源钉/指标 null/事件幂等）、variantref 源端形状（v0.4.0 golden）、Web 面板+走查、真 AG10 全链 live 测试、CI go 门。
