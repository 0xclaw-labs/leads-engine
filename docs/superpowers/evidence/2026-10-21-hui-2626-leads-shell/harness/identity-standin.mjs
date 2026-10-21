// Local protocol-compatible identity stand-in (test values only). Not a repo artifact.
import http from "node:http";
const INTERNAL = process.env.STANDIN_INTERNAL_TOKEN || "";
const SESSIONS = { "tok_owner_walk": { principal_id: "usr_owner_walk", email: "owner-walk@leads.local" },
                   "tok_sales_walk": { principal_id: "usr_sales_walk", email: "sales-walk@leads.local" } };
http.createServer((req, res) => {
  let body = ""; req.on("data", c => body += c); req.on("end", () => {
    const send = (code, obj) => { res.writeHead(code, { "content-type": "application/json" }); res.end(JSON.stringify(obj)); };
    if (req.method !== "POST" || req.url !== "/internal/v1/identity/session/resolve") return send(404, { error: "not_found" });
    let input = {}; try { input = JSON.parse(body || "{}"); } catch { return send(400, { error: "bad_json" }); }
    const sess = SESSIONS[input.session_token];
    if (!sess) return send(200, { authenticated: false });
    return send(200, { authenticated: true, app_id: req.headers["x-app-id"] || "", session: sess });
  });
}).listen(29310, "127.0.0.1", () => console.log("identity standin on 29310"));
