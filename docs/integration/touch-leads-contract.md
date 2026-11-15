# Touch → Leads v2 接收合同

仅部署登记的 `touch-engine` 来源使用 v2；其他来源继续使用既有
`/internal/v1/lead-records/{ref}` 与 `X-Internal-Token` 协议。

Touch 取件和 ACK 的地址均来自 `LEADS_INGEST_TOUCH_ENGINE_FETCH_BASE`，绝不采用事件 URL。
登记 `LEADS_INGEST_TOUCH_ENGINE_FETCH_SERVICE_ID` 以及该 ID 的
`LEADS_INGEST_TOUCH_ENGINE_FETCH_TOKEN`。Touch 部署需在 `TOUCH_SERVICE_TOKENS`
登记同一个 ID 的专属令牌，并在 `TOUCH_SERVICE_ROLES` 明确登记 `lead_receiver`。
这与 BFF 内部令牌、商家 session 各自独立；缺服务身份时 Leads 接收配置门拒绝开放。

取件与 ACK 共同使用 `X-Service-ID`、`X-Service-Token`、`X-Leads-Tenant`。
租户头携带原来源租户，接收事务沿现有 source tenant → target tenant 映射，
既有 inbox / revocation 保持原目标归属。配置映射改变不会搬迁已接收记录。

- 取件：`GET /api/v1/internal/lead-records/{submissionRef}`。除原 ref/源租户核对外，
  必须有正整数 `source_revision`、明确 `consent_revision`、匹配的 notice 与
  `purpose=lead_submission`。活跃记录必须明确授权；撤回必须是零联系字段、零许可的 tombstone。
- ACK：`POST /api/v1/internal/lead-records/{submissionRef}/acknowledgements`。
  正文严格仅 `receipt_id/source_event_id/submission_id/source_revision/receiver_state`；
  `source_event_id` 引用已持久 inbox 的原 profile event ID；状态仅 `stored|revoked`。
- ACK 意图随业务 / inbox 事务写入 `ReceiptJSON.source_ack`，无额外迁移或队列。
  receipt ID 从原来源、源租户、原事件、提交、实际版本、接收状态确定性生成。
  所有摄取成功出口统一在事务 commit 后发送；源端失败返回 503 `source_ack_pending`，
  使用原签名事件重投可恢复同一 receipt，不新建 contact/lead。
- Touch 尚未标记原 outbox forwarded 时真实 ACK 会 409；该事件仍返回可重试 503。
  不生成替代 source event ID，不通过 SQL 把源状态改成成功。
- 旧 accepted inbox 的原事件重投可补 ACK。重放也核当前源授权：源已撤回时先提交
  本地四类许可撤回与水位，再发 `revoked`。本地已撤回而源异常恢复活跃时拒绝 `stored`。
  只有旧 inbox、没有所应用的实际源版本且源已更新为更高活跃版本时，不猜新版本已存储，
  需要该版本原始事件继续投递。
- 只读 receipt 查询仅展示既有本地回执；补 ACK 由原事件的摄取重试触发。

## 自动化合同回归

```sh
source /workspace/scratch/b0e2059d4a63/runtime/touch-env.sh
go -C server test ./...
go -C server test -race ./internal/httpapi ./internal/notifyingest \
  -run 'TestTouch|TestNotifyIngest|TestTenantMap|TestAcknowledgement|TestLegacyRegistered' -count=1
```

`recordSource` 是明确的 HTTP 工程夹具：Touch 分支验证 v2 路径、三 header、
严格五元 ACK；partner 来源仍验旧协议。夹具不能作为正式 Notify 或真实 Touch 交付证据。

## 可复跑的真实双进程工程联调

`scripts/verify-touch-contract.py` 用 SQLite backup **只读复制** 已有门店数据库，
在同一 exec 网络命名空间构建并启动真实 Touch、真实 Leads。仅 Leads 目标租户通过
原 CLI provision 创建；新咨询与撤回走 Touch 公开 HTTP，后台原 forwarder 发出原事件。
脚本内的 Notify relay 是明确的工程传输替身，不提供虚假的正式 delivery 成功结果。

```sh
source /workspace/scratch/b0e2059d4a63/runtime/touch-env.sh
python3 scripts/verify-touch-contract.py \
  --touch-repo /workspace/scratch/b0e2059d4a63/touch-engine/.worktrees/integration \
  --source-db /tmp/touch-e2e-ZLFKmv/touch.db \
  --original-revoked-ref sub_555c19fad47a379566202232 \
  --output-dir /tmp/leads-touch-contract-unique-run
```

输出目录必须尚不存在。原库应来自正常产品入口、具有已启用的门店咨询表单与 storefront。
脚本在副本创建新的合成联系方式，不输出该手机号；本地生成、登记专用工程服务令牌。
所有 Touch ACK 由真实 Leads 客户端发送。脚本只读查询结果，绝不写 outbox/ACK/同步投影。

验收包含：真实零字段 tombstone；错误服务令牌 401 / 错源租户 404 / 退役路径不可用；
原 suppressed 未发事件不能取得 ACK；真实新提交首次 ACK 的未 forwarded 409、原事件重投恢复；
`crm_received` 只由真实 ACK 出现；公开撤回、原事件重投、旧 submit 不复活；
真实 Touch receipts 与 Leads 单 contact / 单 lead / 零活跃许可的结果核对。

这是复制实例的跨仓 HTTP 工程验证，不恢复原匿名 cookie，不等于独立正常浏览器闭环。
正式环境仍需真实 Notify 服务和定向订阅/签名登记、获准接收方身份、实际源/目标租户及
独立浏览器正常入口复验。未发送即被抑制的旧记录不会被脚本伪造为接收方已确认。

## 单次咨询撤回范围与历史许可归属

Touch 的四类许可现在按 `(目标租户, contact, Touch来源提交键, channel)` 精确撤回。
来源提交键是 `touch-notify/v1/` 加 `['touch-engine','notify',源租户,原始ref]`
规范 JSON 编码的 SHA256；原始 ref 继续留在原 inbox、source_refs 和 ACK。
保留前缀中的 `/` 不符合现有 Notify 原始 source_ref 的字符校验，因此其他登记来源不能
通过提交一个原始 ref 来命名这条内部 Touch 键。现有 consent 唯一键及 contact merge/undo
规则继续适用，无迁移。另一份 Touch 咨询、partner 许可与真正的全局停止营销各自保留。

历史裸 ref 行只有在先前持久化的 accepted inbox 能唯一证明同一 app/ns/源租户，
且 source_refs 的权威快照完整匹配时，才会认领为该 Touch 来源键。认领保留原 consent ID、
营销事实、告知版本及已有撤回标记。同 ref 已有两份来源证据、来源身份不完整或权威快照
不匹配时，不推断哪一行归这份咨询，也不通过全联系人 UPDATE 代替归属处理。

- `consent_scope_conflict`（503）：已接受的原来源观察到真实 tombstone，但历史许可无法
  唯一归属。持久原来源撤回水位和待处理 receipt 后返回，不改歧义许可、不发 revoked ACK。
- `source_scope_conflict`：已有 source_refs 快照缺少或不匹配 app/ns/源租户。新活跃提交
  返回 409；撤回或原 accepted 重放返回 503 / pending，不能使用另一来源的 lead/contact。
- pending receipt 标明 `scope_error` 与 `source_ack_pending`，新撤回事实使用
  `ingestion=revocation_pending`。只读 receipt 不会将待处理说成许可已撤回。
- 已有 inbox 的异常来源冲突若不能安全使用原 watermark 键，观察到的单调
  `source_tombstone_revision` 只保存在该来源已存在的 receipt 中，既不改另一来源水位，
  也不允许旧 submit 重新授权。日志只给命名空间摘要、生成的 contact/source-row/inbox ID，
  不记录手机号、令牌或许可原文。

现有 source_refs 与 inbox/revocation 的旧目标租户/app/rawRef 键不能同时容纳两个
来源租户、同 app 同 rawRef 的两份 lead。本修复明确拒绝这种异常冲突；不会默默合并，
也不会让来源 B 的拒绝事件占用来源 A 未来合法撤回的旧键。尚未被本地接受的 B 由其源端
持久 outbox/tombstone 保留待处理意图，不新增本地业务/inbox/水位、不 ACK。Touch 正常
submission ID 本身是全局随机引用；这里的冲突应由来源归属处置，不能改原事件引用凑成功。

操作员应通过原事件的签名 receipt 查询、具备权限的 contact consents 只读列表，及日志中的
source-row/inbox ID 定位原来源证据。审查原 inbox 的 app/ns/source_tenant、source_refs 权威
快照、合并历史和许可 ID；没有足够证据时保持 pending。历史共享行可能已经不可逆地合并
两份授权，必须另行取得授权、可审计的数据归属处置；不要直接清除 revoked_at、改原来源
引用、改 Touch ACK/outbox 状态或把不可恢复的历史冲突标为处理完成。归属问题确实解决后，
重投原签名事件，由原合同再核权威、收敛和 ACK。

## 本轮独立验收记录（TOUCH-EXEC-20261114）

代码候选 `404b9f4ff2b0b5e86771338d1566127befd4ad31` 已由独立
`gpt-6.1-terra` 审查；Touch 对端为 `47421ea352f70d362551f2c76b40e0e6c86df4a2`。
这组记录对应本地真实进程与持久化，不能替代正式 Notify、正式账号与部署验收。

| 检查 | 观察到的结果 |
| --- | --- |
| 正常浏览器入口 | 原门店首页 → 隐私与授权 → 请门店联系我；称呼留空、有效合成电话、仅服务许可 |
| 原提交接收 | `sub_8b61d688bc2a6cff45614417`；初始待同步，原 forwarder 经工程 relay 投递；首次 503，原事件重投后真实 stored ACK |
| 原提交撤回 | 同页面撤回后先显示接收方待确认；原撤回事件重投后真实 revoked ACK；刷新后凭同一编号和电话回查一致 |
| 持久化结果 | Touch stored revision 1 / revoked revision 2；Leads 一条 contact、一条 lead、零活跃许可 |
| 历史未发送记录 | `sub_555c19fad47a379566202232` 无接收凭据，未伪造成功 |
| 独立对抗回归 | 不同 Touch ref/partner 许可隔离、历史唯一认领与歧义 pending、异来源冲突不占合法来源水位、旧重放不复活、原全局停止营销操作 |
| root 全量工程回归 | Go 全套通过；前端 66 文件 / 515 项通过；TypeScript 无错误；Next 生产构建成功 |

本次浏览器使用原数据库，未 seed 或修改业务表凑状态。工程 relay 只转发本次原事件；
接收方目标租户通过原 CLI 创建，服务身份/租户映射为工程登记。审查没有对所有历史
异常来源实际制造浏览器数据，这部分结论来自隔离 SQLite/HTTP 测试。

部署接续应先完成两端服务身份、原租户映射和 Notify 定向订阅/签名登记，
确认 Touch 提供上述 v2 取件与 ACK 合同，再启用接收方配置并重投原待处理事件。
不可用旧协议替代本来源的 v2 ACK，也不可清除历史撤回水位。正式链路仍待同一发布
候选的端到端验收；HUI-4662 仅进入审查，不据工程证据标为 Done。
