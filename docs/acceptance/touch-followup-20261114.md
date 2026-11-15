# Touch 咨询接收、撤回及逐条跟进验收

标识：`TOUCH-EXEC-20261114`。工单：[HUI-4662](https://linear.app/huigoo/issue/HUI-4662)，相关共享壳 [HUI-2628](https://linear.app/huigoo/issue/HUI-2628)。
草稿交付：[leads-engine#69](https://github.com/0xclaw-labs/leads-engine/pull/69)。本文件后的交付提交仅收录验收文档，不改变已受测的应用代码。

## 用户可完成的任务

顾客主动请求门店联系后，只填写本次联系所需资料，称呼可以留空。服务许可与营销许可分别记录。Touch 能区分本地已收、正在同步和接收方已真实入库；撤回沿原来源与原事件传播，不撤销同一联系人的其它合法来源许可。

商家从原 Touch 工作台的一条已接收线索进入 CRM 的真实详情，处理后返回该条咨询的原门店。来源引用、ACK 与手机号都不能冒作 lead ID；完整来源元组先解析唯一真实 CRM 租户和 lead。SSO 后重新核当前身份、成员、记录范围及原来源许可，才消费短时一次性 h。原详情返回目标未登记时明确说明并保留详情重试，不伪造可用地址。

## 最终代码和工程检查

Leads 代码候选：`44defd4d1526b88f5f75e2810117f969350f3f49`；完整树：`782348f2d7258a14310a2eea4eff58c62ff1cf6f`。

| 检查 | 命令与观察 |
| --- | --- |
| Go 全套 | `go test -p 1 ./... -count=1`，成功，57.408 秒 |
| Web 全套 | `npm test`，71 个文件、531 项通过 |
| TypeScript | `npx tsc --noEmit --incremental false`，成功 |
| 生产构建 | `npm run build`，成功 |
| 候选稳定性 | 开始至结束 HEAD 和受跟踪文件保持不变；2026-11-14 14:35:32–14:36:29 UTC |

更早全量曾指出新增技术接续路由未登记到 census；已补 `/touch-follow-up/[h]` 的 recovery 映射，保持原业务视图口径，不隐藏该路由来通过清单检查。普通登录范围发现无新物理页面。

## 正常浏览器与原记录

独立审查模型为 `gpt-6.1-terra`。原 Touch 与 Leads 数据库、媒体和既有撤回记录保留，没有 reseed 或改业务表凑成功。

| 原记录与路径 | 观察结果 | 证据范围 |
| --- | --- | --- |
| 原咨询 `sub_8b61d688bc2a6cff45614417` | 顾客页面空称呼提交；真实双应用接收 stored ACK；原页面撤回后沿原事件得到 revoked ACK；刷新同编号及电话回查一致 | 工程 Notify relay 与身份；原记录继续撤回 |
| 新咨询 `sub_6127191218bedd77553449d0` | 正常 C6 新建、仅本次服务同意，真实 forwarder/接收/ACK；从 Touch 原行进入 Leads | 不是用旧已撤回记录改造正例 |
| 原详情 `lead_2dc908169b6dcad6cf128d56b17f3841` | 原 shell code+h → 正常 SSO → resume → 真实 timeline 200，显示 CRM 已接收、没有营销许可 | 不伪造 lead ID 或注入会话 |
| 同详情返回 | 工程目标禁用时真实 503，保留详情；仅恢复工程登记后点同按钮，回到原店 `sto_9f6fd1a6618e423416579859509843bc` | 正式 Touch 目标仍未登记，不能据此声称正式 SSO 上线 |
| 未知/重放 h | 未知 h 安全回首页，经真实 memberships/whoami 进入可用范围；已消费 h 重访 410，未再次进入详情 | 原句柄、不另造成功记录 |
| Leads 跨标签页换账号 | 正常 B 跟进 SSO 后，旧 Leads 标签重新 whoami，当前主体确认为工程 B，再读原 timeline | A/B 均有原 CRM 工程成员权限；同数据合法，不把此例写成无权用户隔离正例 |
| 原 Touch `/admin` 跨标签页换账号 | 最终Touch9be7071在原/admin退出B登录A，旧线索页和旧后台页均自动whoami200并确认A主体；最后两仓原条目→原详情→原店再次通过 | 无cookie/storage注入；此前68be/2e的同路径红例保留，不拿工作区另一登录入口替代 |

Touch 的两次原路径阻断先后修复并复验：旧生产登记快照遗漏上游已登记 Leads；Next middleware 把反代内部 localhost 当作 landing 目标。两端现以经校验的 canonical HTTPS host 生成固定 landing，并保留原 code+h。工程测试传输采用仅转发原 Host/Origin/真实响应和 cookies 的 CONNECT+TLS 代理，不重写业务 Location，不注入登录 cookie。

## 对抗审查修复

- 消费 `leads_sso_reset`，清上一账号保存范围及缓存，废除旧的 whoami 和导航结果。成功业务接续随后保存服务器核准的 CRM 租户。
- 复用原 Identity 解析增加当前成员范围发现。无已选 tenant 也能进入；单范围仍由原 whoami 确认，多范围按名称选择，不取默认第一项。成员或授权权威故障保持 503，不能冒充空列表。
- 账号代次变化通知同源其它标签页。旧业务子树先失效，再按当前 cookie 发现和核验范围。旧跟进、原店返回、生态导航和接续结果不能在换账号后继续跳转。
- 原完整来源、单次 h、当前成员/代理授权、当前来源许可、撤回终态和原 ACK 水位均保持；安全返回不重新授权咨询。

## 部署与仍待正式验收的条件

配套 Touch 迁移 `0138_follow_up_navigation_contexts.sql` 和 Leads `0032_follow_up_navigation_contexts.sql`，沿原增量迁移。导航上下文只保存引用，没有联系人副本；TTL 认证失效已实现，本轮没有新建后台物理清理任务。

按两仓 `deploy/touch-followup-navigation.env.example` 配置可信服务签名、原 receiver 服务身份、租户映射、canonical cookie 与原 Identity exchange。合同详见 [咨询接收及撤回](../integration/touch-leads-contract.md)、[逐条跟进与返回](../integration/touch-followup-navigation.md)、[当前工作范围发现](../integration/session-memberships.md)。

工程 Identity、Notify relay、TLS 代理和工程成员不能替代正式平台注册、真实账号权限与正式通知投递。上游已登记 Leads，但正式 Touch 返回目标仍缺失。PR 保持草稿；工单保留审查，未据工程测试标为 Done。整店正式验收仍由 HUI-4666 和 HUI-3531 收口。

多租户选择、部分撤权/并发变化和真机平台接收没有取得完整浏览器正例；工程隔离测试与静态审查的覆盖不能替代这些证据。审查完成后进程已退出，原数据库、媒体和业务记录保留。

跨仓完整缺陷历史及原路径复验见 [Touch 验收记录](https://github.com/0xclaw-labs/touch-engine/blob/codex/touch-user-jobs-20261114/docs/acceptance/touch-user-jobs-20261114.md)。
