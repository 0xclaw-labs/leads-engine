#!/usr/bin/env node
// Normal merchant entry -> exact source row -> original CRM detail -> original
// store. Only loopback transport for registered HTTPS engineering hosts is
// proxied; API bodies/verdicts and SSO cookies are never fabricated here.
import {readFileSync,mkdirSync,writeFileSync} from 'node:fs';
import assert from 'node:assert/strict';
const cfg=JSON.parse(readFileSync(process.argv[2],'utf8'));
const {chromium}=await import(cfg.playwright_module);
mkdirSync(cfg.output_dir,{recursive:true,mode:0o700});
const browser=await chromium.launch({executablePath:process.env.HARNESS_BROWSER_EXECUTABLE,headless:true,args:['--no-sandbox']});
const context=await browser.newContext({viewport:{width:430,height:932}});
const hosts=new Map([[cfg.touch_host,cfg.touch_web],[cfg.leads_host,cfg.leads_web]]);
for(const [host,local]of hosts){assert.match(local,/^http:\/\/(127\.0\.0\.1|localhost):\d+$/);await context.route(`https://${host}/**`,async route=>{const incoming=new URL(route.request().url());const headers={...route.request().headers(),host,'x-forwarded-host':host,'x-forwarded-proto':'https'};const upstream=await route.fetch({url:local+incoming.pathname+incoming.search,headers,maxRedirects:0});await route.fulfill({response:upstream});});}
const page=await context.newPage();const evidence={classification:'工程Identity/传输代理；真实Touch与Leads页面、SSO桥和Go业务端点',requests:[],errors:[]};
page.on('pageerror',e=>evidence.errors.push(e.message));
page.on('response',r=>{const u=new URL(r.url());if(u.pathname.startsWith('/api/'))evidence.requests.push({method:r.request().method(),host:u.host,path:u.pathname,status:r.status()});});
try{
 await page.goto(`https://${cfg.touch_host}/work/leads`);
 await page.getByLabel('用户名').fill(cfg.email);await page.getByLabel('密码',{exact:true}).fill(cfg.password);await page.getByRole('button',{name:'登录',exact:true}).click();
 const row=page.locator(`[data-submission-ref="${cfg.source_ref}"]`);await row.waitFor({timeout:45000});assert.match(await row.innerText(),/接收方已确认入库/);
 await page.screenshot({path:cfg.output_dir+'/01-original-row.png',fullPage:true});
 const detailResponse=page.waitForResponse(r=>new URL(r.url()).pathname===`/api/leads/${cfg.expected_lead_id}/timeline`&&r.status()===200,{timeout:60000});
 await row.getByRole('button',{name:'进入获客详情',exact:true}).click();await detailResponse;
 await page.waitForURL(u=>u.hostname===cfg.leads_host&&u.pathname===`/leads/${cfg.expected_lead_id}`,{timeout:60000});await page.locator('[data-page="lead-detail"]').waitFor();
 assert.match(new URL(page.url()).searchParams.get('touch_follow_up')||'',/^fuv_[a-f0-9]{32}$/);evidence.original_detail=true;
 await page.screenshot({path:cfg.output_dir+'/02-original-lead-detail.png',fullPage:true});
 await page.getByRole('button',{name:'返回原门店',exact:true}).click();
 if(cfg.expect_return_blocked){await page.locator('[data-region="touch-follow-up-return"] [role="status"]').waitFor();assert.match(await page.locator('[data-region="touch-follow-up-return"]').innerText(),/未获平台登记|不可用/);evidence.return_blocked=true;}else{
  await page.waitForURL(u=>u.hostname===cfg.touch_host&&u.pathname===`/work/stores/${cfg.origin_store_id}`,{timeout:60000});await page.getByRole('heading',{name:cfg.origin_store_name,exact:true}).waitFor({timeout:30000});evidence.original_store_return=true;
 }
 await page.screenshot({path:cfg.output_dir+'/03-original-store-return.png',fullPage:true});assert.equal(evidence.errors.length,0);evidence.result='pass';
}catch(err){evidence.result='fail';evidence.failure=String(err);await page.screenshot({path:cfg.output_dir+'/failure.png',fullPage:true}).catch(()=>{});throw err;}finally{writeFileSync(cfg.output_dir+'/evidence.json',JSON.stringify(evidence,null,2),{mode:0o600});await browser.close();}
