// Local Touch-engine record source stand-in (test values only). Not a repo artifact.
import http from "node:http";
const TOKEN = process.env.LEADS_WALK_FETCH_TOKEN;
const recs = new Map();
http.createServer((req, res) => {
  const send = (code, obj) => { res.writeHead(code, { "content-type": "application/json" }); res.end(typeof obj === "string" ? obj : JSON.stringify(obj)); };
  if (req.method === "POST" && req.url === "/__put") {
    let b = ""; req.on("data", c => b += c); req.on("end", () => { const o = JSON.parse(b); recs.set(o.ref, o.record); send(200, { ok: true }); });
    return;
  }
  if (req.headers["x-internal-token"] !== TOKEN) return send(401, { error: "no" });
  const m = req.url.match(/^\/internal\/v1\/lead-records\/([^/?]+)$/);
  if (req.method !== "GET" || !m || !recs.has(decodeURIComponent(m[1]))) return send(404, { error: "not_found" });
  return send(200, recs.get(decodeURIComponent(m[1])));
}).listen(29320, "127.0.0.1", () => console.log("record source on 29320"));
