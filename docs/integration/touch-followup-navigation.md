# Touch M11 到原 Leads 详情与原店返回

## 权威与逐条接续

完整来源恒为 `touch-engine / notify / source tenant / submission ref`。后端只从已提交 Notify inbox、原 source scope、有效原提交 form_authorization consent 与原 lead 读取唯一映射，拒绝歧义、待同步与撤回。手机号只可能是原 CRM 联系人合并依据，不能成为本导航的记录解析条件。源租户可以不同于 CRM 租户。

Touch 服务先用原 `LEADS_FOLLOW_UP_*` 通路解析，再请求原 Identity shell 四字段 launch，将 shell 原样生成的随机 h 登记到 Leads。h 只是路由句柄；真实 SSO 后必须重新核实际主体、当前 CRM 成员/代理授权与原记录 ReadRecord，重新取原 Touch receiver 许可并验证同一完整来源。通过后才一次消费 h，返回原 `/leads/{id}`，使用原详情的原 API。失败不改变业务状态、原许可或原水位。

新增 `POST /internal/v1/touch-follow-ups/{resolve,register}` 除原 InternalToken 外，要求 `X-Touch-Follow-Up-Signature` 为 HMAC-SHA256，hex 编码，secret 复用原 follow-up token，消息是 `touch-follow-up/v1\n<exact path>\n<exact JSON bytes>`。请求只含 `source_tenant_id/source_ref/source_revision/principal_id`；register 另含原 shell `handoff_id`、Touch 服务从提交派生的 `origin_store_id`。BFF 注入共享 internal token 不能自行伪造服务主体。固定服务注册、源 FetchBase/专属 receiver service ID/token 均来自部署配置，不接请求中的服务 URL。

`POST /api/v1/touch-follow-ups/{h}/resume` 从 server context 选择映射 CRM 租户，沿原 requireSession 确认当前真实身份；请求租户不能替换映射。成功返回 `{known:true,crm_tenant_id,path}`；h 60 秒一次消费。错误身份/其他来源不返回目标 ID；已使用/过期 410；当前许可不成立 409；当前成员/记录无权 403 或原 404 mask；服务不可核验 503。未知 h 只返回 `{known:false}`，普通 shell SSO 仍进入原首页。

消费后详情只增加不带联系方式的 `touch_follow_up=fuv_<opaque>` 返回句柄。`POST /api/v1/touch-follow-up-visits/{visit}/return` 核实际主体、当前 CRM tenant 与原记录读权，30 分钟有效；请求原 shell `touch-engine / ti-touch-engine-web`。然后沿原 lead_receiver 服务向 Touch 登记新 shell h；Touch 从原 origin 派生源门店并核当前源成员/store read scope。返回原 shell URL 原样交给浏览器。没有登记目标就 503 `return_target_unavailable`，页面明确说明且可重试，不拼目标 URL。

撤回阻断进入跟进；安全返回原店无需重新授予咨询许可，但仍需当前源成员与门店读权。原详情其他只读/跟进行为继续受原 CRM API 权限与许可合同控制。

## 两仓写面与配置

- Leads migration `0032_follow_up_navigation_contexts.sql`；Touch migration `0138_follow_up_navigation_contexts.sql`。均为隔离导航 context 表，保存引用，不保存联系方式；不写原业务/ACK/consent 水位。
- Touch：`LEADS_FOLLOW_UP_BASE_URL=<Leads Go URL>`，`LEADS_FOLLOW_UP_TOKEN=<原 Leads internal service token>`；现有 Touch Identity base/token；现有受信 `lead_receiver` service registry。
- Leads：原 `LEADS_INGEST_TOUCH_ENGINE_FETCH_BASE/TOKEN/SERVICE_ID`，原完整 scoped `LEADS_TENANT_BINDINGS`、Identity base/token、internal token。
- Web：原 BFF `TOUCH_SERVER_URL/TOUCH_INTERNAL_TOKEN`、`LEADS_SERVER_URL/LEADS_INTERNAL_TOKEN`。两端 `SSO_APP_HOST` 必须对应实际登记 canonical host，`IDENTITY_SSO_EXCHANGE_URL` 指向原 Identity exchange；Go 配 `TOUCH_SESSION_COOKIE=touch-engine_session`、`LEADS_SESSION_COOKIE=leads-engine_session`。桥只原样转发 Identity cookies，无新账户/SSO。
- 原 public-ai 生产 manifest 目前登记 `leads-engine / ti-leads-engine-web -> https://leads.modelxing.com/`，没有 Touch launch target。正式返回必须先获上游实际登记；不会向上游登记表添加工程 target。Touch 仅逐字节同步上游已登记快照，不把工程目标当正式资格。

## 工程 Identity 与正常入口浏览器 runner

`scripts/touch-followup-identity-fixture.mjs CONFIG.json` 是明确的 loopback 工程 Identity 替身，不写应用数据库，不自动建成员、线索或许可。真实 Go/Web 服务必须分别启动；Notify 沿原可信工程 relay 或实际已获准通路。复用原库时不得 reseed 原记录/清撤回/改 ACK。正例由正常 C6 新咨询经原 relay 取得真实 stored ACK；已有两个撤回记录必须继续撤回。初始两端成员可在获准工程前提下用原 `provision-member` CLI 配置相同 principal；source→target 映射保持实际登记。

配置文件放 0700 目录，文件 0600，不纳入仓库：

```json
{
  "port": 18591,
  "apps": {
    "touch-engine": {"token":"<dedicated identity app token>","host":"touch-followup.modelxing.com","target_id":"ti-touch-engine-web","launch_url":"https://touch-followup.modelxing.com/"},
    "leads-engine": {"token":"<dedicated identity app token>","host":"leads.modelxing.com","target_id":"ti-leads-engine-web","launch_url":"https://leads.modelxing.com/"}
  },
  "users": [{"principal_id":"<existing shared test principal>","email":"<engineering login>","password":"<engineering password>","enabled":true,"tenants":{"touch-engine":["<actual source tenant>"],"leads-engine":["<actual mapped CRM tenant>"]}}],
  "disabled_targets_file":"<optional local JSON array path>",
  "revocations_file":"<optional local JSON principal array path>"
}
```

fixture shell 严格四字段，目标是上述显式工程登记，仅签原 code+h；exchange 校 canonical Host/Origin、audience、主体、TTL和一次消费，签发各 app 原 canonical cookie。受信 app token、密码不打印。可向 disabled_targets_file 写入 `"ti-touch-engine-web"` 验证返回明确阻断，再清空重试；revocations_file 撤回主体，真实 session/launch 路径即时拒绝。目录仅测试，不是正式平台注册证明。

浏览器 runner 只将上述两 canonical HTTPS origin 的网络传输代理到已运行本地 Next，保留浏览器原URL与 Host/Origin/真实cookie；所有业务与 SSO 响应来自真实服务。不开内部 API 后门，不伪造存储/许可/成功响应。先启动两 Go、两 Next、fixture 与原 relay；把两端 Go Identity base与两Web exchange设为fixture loopback URL，配置真实专属 app token及canonical cookie。原库保持原路径由独立审查者操作；本实现者未改原库。

```sh
source /workspace/scratch/b0e2059d4a63/runtime/touch-env.sh
node scripts/touch-followup-identity-fixture.mjs /private/fixture.json
node scripts/verify-touch-followup-browser.mjs /private/browser.json
```

browser.json（不含 service token；登录凭据仍放本地0600文件）：

```json
{"touch_host":"touch-followup.modelxing.com","leads_host":"leads.modelxing.com","touch_web":"http://127.0.0.1:18340","leads_web":"http://127.0.0.1:18330","email":"<engineering login>","password":"<engineering password>","source_ref":"<original normal C6 new ref with real stored ACK>","expected_lead_id":"<same full tuple's actual lead id>","origin_store_id":"<source submission's original store>","origin_store_name":"<actual store heading>","output_dir":"/private/m11-evidence","playwright_module":"/absolute/existing/playwright/index.mjs","expect_return_blocked":false}
```

runner 从 `/work/leads` 正常登录，点指定原行的“进入获客详情”，等待浏览器原 timeline API 200，核真实记录URL与opaque visit，点“返回原门店”，核原店URL与原店标题；输出三张截图和脱敏 method/host/path/status 证据。`expect_return_blocked=true` 专测未登记返回，不声称正例。未运行 runner 的语法检查只能算工程准备；独立 Terra 必须实际沿原正常入口验收。

运行补充配置样例分别在两仓 `deploy/touch-followup-navigation.env.example`，在原环境后加载，保留原 service token/成员/映射。Touch 的 `touch_sso_reset` 已接原 workspace-tenant 机制：新 SSO 清上一个账号的 tenant memory、作废在途 tenant 确认；已核验业务 continuation 再保存本次服务器返回的源租户。普通非业务 h 回到原 `/`，不新建 `/work` 工作台。

本轮独立浏览器发现上述 Playwright route 方式不会截获所有真实重定向，实际验收改用保留原域和响应的 CONNECT+TLS 传输代理。不能只运行该 runner 的语法检查或一次 route 请求就宣称 SSO 闭环通过。最终候选、真实原记录、工程与正式证据边界见 [验收记录](../acceptance/touch-followup-20261114.md)。
