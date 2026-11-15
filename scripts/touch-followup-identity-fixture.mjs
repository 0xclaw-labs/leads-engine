#!/usr/bin/env node
// Explicit engineering Identity substitute, NOT production registration.
// It creates no tenant/member/lead/consent. Config must name existing test
// principals, allowed tenants and registered targets. Store config mode 0600.
import http from 'node:http';
import {readFileSync} from 'node:fs';
import {randomBytes} from 'node:crypto';
const cfg=JSON.parse(readFileSync(process.argv[2],'utf8'));
const sessions=new Map(),refreshes=new Map(),codes=new Map();
const opaque=()=>randomBytes(24).toString('base64url');
function active(principal){const user=cfg.users.find(u=>u.principal_id===principal);return user&&user.enabled!==false&&!(cfg.revocations_file&&JSON.parse(readFileSync(cfg.revocations_file,'utf8')).includes(principal));}
function pair(app,user){const access=opaque(),refresh=opaque();sessions.set(access,{app,principal:user.principal_id,email:user.email});refreshes.set(refresh,{app,user});return{access_token:access,refresh_token:refresh,token_type:'Bearer',expires_in:3600};}
const server=http.createServer(async(req,res)=>{
 const reply=(status,out,cookies=[])=>{res.writeHead(status,{'content-type':'application/json','cache-control':'no-store',...(cookies.length?{'set-cookie':cookies}:{})});res.end(JSON.stringify(out));};
 if(req.url==='/healthz'){reply(200,{engineering_fixture:true});return;}
 let raw='';for await(const chunk of req){raw+=chunk;if(raw.length>8192){reply(413,{});return;}}
 let body;try{body=JSON.parse(raw||'{}');}catch{reply(400,{});return;}
 if(req.url==='/api/auth/sso/exchange'){
  const issued=codes.get(body.code);const app=issued&&cfg.apps[issued.app];
  if(!issued||issued.used||issued.expires<Date.now()||!app||req.headers.host!==app.host||req.headers.origin!==`https://${app.host}`||!active(issued.principal)){reply(403,{error:'exchange_refused'});return;}
  issued.used=true;const user=cfg.users.find(u=>u.principal_id===issued.principal);const tokens=pair(issued.app,user);
  reply(200,{ok:true},[`${issued.app}_session=${tokens.access_token}; Path=/; HttpOnly; Secure; SameSite=Lax`]);return;
 }
 const appID=req.headers['x-app-id'];const app=cfg.apps[appID];
 if(!app||req.headers['x-pilotseaview-internal-token']!==app.token){reply(401,{error:'fixture_service_identity'});return;}
 if(req.url==='/internal/v1/identity/login'){
  const user=cfg.users.find(u=>u.email===body.email&&u.password===body.password);
  if(!user||!active(user.principal_id)||body.app_id!==appID){reply(401,{});return;}reply(200,pair(appID,user));return;
 }
 if(req.url==='/internal/v1/identity/session/resolve'){
  const session=sessions.get(body.session_token);if(!session||session.app!==appID||!active(session.principal)){reply(200,{authenticated:false});return;}
  reply(200,{authenticated:true,app_id:appID,session:{principal_id:session.principal,email:session.email}});return;
 }
 if(req.url==='/internal/v1/identity/refresh'){
  const saved=refreshes.get(body.refresh_token);if(!saved||saved.app!==appID||body.app_id!==appID||!active(saved.user.principal_id)){reply(401,{});return;}refreshes.delete(body.refresh_token);reply(200,pair(appID,saved.user));return;
 }
 if(req.url==='/internal/v1/identity/revocations'){refreshes.delete(body.refresh_token);reply(200,{ok:true});return;}
 if(req.url==='/internal/v1/identity/shell/launch'){
  const allowed=['principal_id','tenant_id','app_id','target_id'];if(Object.keys(body).length!==4||Object.keys(body).some(k=>!allowed.includes(k))){reply(400,{});return;}
  const target=cfg.apps[body.app_id];const user=cfg.users.find(u=>u.principal_id===body.principal_id);
  const disabled=cfg.disabled_targets_file?JSON.parse(readFileSync(cfg.disabled_targets_file,'utf8')):[];
  if(!target||!active(body.principal_id)||!user.tenants?.[body.app_id]?.includes(body.tenant_id)||target.target_id!==body.target_id||disabled.includes(body.target_id)){reply(403,{error:'target_or_membership_denied'});return;}
  const code=opaque(),h='hnd_'+randomBytes(16).toString('hex');codes.set(code,{app:body.app_id,principal:body.principal_id,expires:Date.now()+60000,used:false});
  const url=new URL(target.launch_url);url.searchParams.set('code',code);url.searchParams.set('h',h);reply(200,{ok:true,app_id:body.app_id,target_id:body.target_id,launch_url:url.toString()});return;
 }
 reply(404,{});
});
server.listen(cfg.port,'127.0.0.1',()=>console.log('Engineering Identity fixture listening; no production registration implied.'));
