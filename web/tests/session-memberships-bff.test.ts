import { afterEach, expect, it } from "vitest";
import { createServer, type Server } from "node:http";
import { proxyToServer } from "../src/lib/bff";
let server:Server|undefined;
afterEach(async()=>{if(server){server.closeAllConnections();await new Promise<void>(resolve=>server!.close(()=>resolve()));server=undefined;}});
it("relays discovery through real HTTP without selected tenant and without asserted principal",async()=>{
 const requests:Array<{path:string,cookie?:string,principal?:string,token?:string,tenant?:string}>=[];
 server=createServer((req,res)=>{
  requests.push({path:req.url!,cookie:req.headers.cookie,principal:req.headers["x-principal-id"] as string|undefined,token:req.headers["x-internal-token"] as string|undefined,tenant:req.headers["x-tenant-id"] as string|undefined});
  res.setHeader("content-type","application/json");res.setHeader("cache-control","no-store");
  if(!req.headers.cookie){res.statusCode=401;res.end(JSON.stringify({error:"unauthenticated"}));return;}
  if(req.headers.cookie.includes("unavailable")){res.statusCode=503;res.end(JSON.stringify({error:"memberships_unavailable"}));return;}
  res.end(JSON.stringify({items:[{tenant_id:"actual-a",display_name:"原组织甲"},{tenant_id:"actual-b",display_name:"原组织乙"}]}));
 });
 await new Promise<void>(resolve=>server!.listen(0,"127.0.0.1",resolve));const address=server.address();if(!address||typeof address==="string")throw new Error("missing HTTP listener");
 const env={serverUrl:`http://127.0.0.1:${address.port}`,internalToken:"original-bff-token"};
 const call=(cookie?:string)=>proxyToServer(new Request("https://leads.modelxing.com/api/session/memberships",{headers:{...(cookie?{cookie}:{}),"x-principal-id":"foreign-principal","x-internal-token":"forged-browser-value"}}),env,["session","memberships"]);
 const success=await call("leads-engine_session=original-identity-issued");expect(success.status).toBe(200);expect(success.headers.get("cache-control")).toBe("no-store");expect(await success.json()).toEqual({items:[{tenant_id:"actual-a",display_name:"原组织甲"},{tenant_id:"actual-b",display_name:"原组织乙"}]});
 expect(requests[0]).toEqual({path:"/api/v1/session/memberships",cookie:"leads-engine_session=original-identity-issued",principal:undefined,token:"original-bff-token",tenant:undefined});
 expect((await call()).status).toBe(401);const unavailable=await call("leads-engine_session=unavailable");expect(unavailable.status).toBe(503);expect(await unavailable.json()).toEqual({error:"memberships_unavailable"});
});
