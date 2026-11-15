#!/usr/bin/env python3
"""真实 Touch + Leads 独立进程合同工程联调（Notify 传输明确为工程替身）。
只读 SQLite backup 复制已有门店；咨询/撤回走公开 HTTP；仅 CLI provision 创建
Leads 目标租户。绝不改 Touch outbox/ACK/同步状态，不把 HTTP 运行算浏览器验收。
运行前 source runtime/touch-env.sh。所有服务须在同一 exec 网络命名空间启动。
"""
import argparse
import hashlib
import hmac
import json
import os
from pathlib import Path
import secrets
import socket
import sqlite3
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.error import HTTPError
from urllib.request import Request, urlopen


def request(url, data=None, headers=None):
    body = None if data is None else json.dumps(data, separators=(",", ":"), ensure_ascii=False).encode()
    req = Request(url, data=body, headers={"Content-Type": "application/json", **(headers or {})})
    try:
        with urlopen(req, timeout=12) as res:
            return res.status, json.loads(res.read() or b"{}")
    except HTTPError as err:
        raw = err.read()
        try:
            parsed = json.loads(raw or b"{}")
        except ValueError:
            parsed = {"error": "non_json_response", "status": err.code}
        return err.code, parsed


def port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def wait_for(fn, label, timeout=55):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        result = fn()
        if result:
            return result
        time.sleep(.25)
    raise AssertionError("timeout: " + label)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--touch-repo", required=True, type=Path)
    parser.add_argument("--source-db", required=True, type=Path)
    parser.add_argument("--original-revoked-ref", required=True)
    parser.add_argument("--output-dir", required=True, type=Path)
    args = parser.parse_args()
    leads_repo = Path(__file__).resolve().parents[1]
    out = args.output_dir.resolve()
    out.mkdir(mode=0o700, parents=True, exist_ok=False)
    os.chmod(out, 0o700)
    copied = out / "touch-copy.db"
    with sqlite3.connect(f"file:{args.source_db.resolve()}?mode=ro", uri=True) as source, sqlite3.connect(copied) as target:
        source.backup(target)
    with sqlite3.connect(copied) as db:
        tenant, store = db.execute("SELECT tenant_id,store_id FROM lead_submissions WHERE submission_ref=?", (args.original_revoked_ref,)).fetchone()
        code = db.execute("SELECT store_code FROM store_storefronts WHERE store_id=?", (store,)).fetchone()[0]
        payload = json.loads(db.execute("SELECT payload_json FROM leads_outbox WHERE submission_ref=? AND kind='submit'", (args.original_revoked_ref,)).fetchone()[0])
        target_app = payload["event_profile"]["target_app"]
    touch_url, leads_url = [f"http://127.0.0.1:{port()}" for _ in range(2)]
    service_id = "local-leads-receiver"
    receiver_token, notify_token, ingest_secret = [secrets.token_urlsafe(32) for _ in range(3)]
    deliveries = []
    lock = threading.Lock()

    def ingest(envelope):
        raw = json.dumps(envelope, separators=(",", ":"), ensure_ascii=False).encode()
        signature = "sha256=" + hmac.new(ingest_secret.encode(), raw, hashlib.sha256).hexdigest()
        req = Request(leads_url + "/internal/v1/notify/ingest", data=raw, headers={"Content-Type": "application/json", "X-Notify-Signature": signature})
        try:
            with urlopen(req, timeout=12) as res:
                return res.status, json.loads(res.read())
        except HTTPError as err:
            return err.code, json.loads(err.read())

    class EngineeringNotifyRelay(BaseHTTPRequestHandler):
        def log_message(self, *unused):
            pass

        def respond(self, status, body):
            raw = json.dumps(body).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def do_GET(self):
            # No fabricated delivered verdict: this transport has no official
            # delivery-status service. ACK is independently verified at Touch.
            self.respond(200, {"deliveries": []})

        def do_POST(self):
            if self.path != "/internal/v1/notify/events" or self.headers.get("X-PilotSeaView-Internal-Token") != notify_token or self.headers.get("X-Notify-App-ID") != "touch-engine":
                self.respond(401, {"error": "engineering_transport_auth"})
                return
            envelope = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            original = envelope["data"]["event_profile"]["event_id"]
            local_transport_id = "engineering_notify_" + hashlib.sha256(original.encode()).hexdigest()[:16]
            envelope["id"] = local_transport_id
            # Deliver before returning publish acceptance. This exercises the
            # real unknown_source_event 409 until Touch marks forwarded itself.
            first = ingest(envelope)
            with lock:
                deliveries.append({"envelope": envelope, "first_status": first[0], "first_error": first[1].get("error")})
            self.respond(201, {"event_id": local_transport_id})

    relay = ThreadingHTTPServer(("127.0.0.1", 0), EngineeringNotifyRelay)
    threading.Thread(target=relay.serve_forever, daemon=True).start()
    relay_url = f"http://127.0.0.1:{relay.server_port}"
    procs, logs = [], []
    evidence = {"classification": "真实Touch+Leads进程/公开HTTP工程合同；Notify为工程relay；不是浏览器或正式Notify验收", "original_revoked_ref": args.original_revoked_ref, "source_tenant": tenant, "source_db_writes": False}
    try:
        for repo, binary in [(args.touch_repo, "touch-server"), (leads_repo, "leads-server")]:
            subprocess.run(["go", "build", "-o", str(out / binary), "./cmd/" + binary], cwd=repo / "server", check=True)
        target_tenant = subprocess.check_output([str(out / "leads-server"), "provision-tenant", "-db", str(out / "leads.db"), "-name", "Touch合同工程接收租户"], text=True).strip()
        evidence["target_tenant"] = target_tenant
        base_env = {k: v for k, v in os.environ.items() if not k.startswith(("TOUCH_", "LEADS_", "PLATFORM_", "FEATURE_"))}
        common = {**base_env, "GOWORK": "off", "PLATFORM_IDENTITY_BASE_URL": relay_url, "PLATFORM_IDENTITY_TOKEN": secrets.token_urlsafe(32)}
        touch_env = {**common, "TOUCH_HTTP_ADDR": touch_url.removeprefix("http://"), "TOUCH_DB_PATH": str(copied), "TOUCH_MEDIA_DIR": str(out / "media"), "TOUCH_INTERNAL_TOKEN": secrets.token_urlsafe(32), "FEATURE_LEADS_CAPTURE": "on", "FEATURE_STOREFRONT": "on", "PLATFORM_NOTIFY_BASE_URL": relay_url, "PLATFORM_NOTIFY_TOKEN": notify_token, "LEADS_TARGET_APP_ID": target_app, "LEADS_PHONE_PEPPER": secrets.token_urlsafe(32), "TOUCH_SERVICE_TOKENS": service_id + ":" + receiver_token, "TOUCH_SERVICE_ROLES": service_id + ":lead_receiver"}
        leads_env = {**common, "LEADS_HTTP_ADDR": leads_url.removeprefix("http://"), "LEADS_DB_PATH": str(out / "leads.db"), "LEADS_INTERNAL_TOKEN": secrets.token_urlsafe(32), "LEADS_APP_ID": target_app, "FEATURE_NOTIFY_INGEST": "on", "LEADS_DEDUP_PEPPER": secrets.token_urlsafe(32), "LEADS_INGEST_SOURCES": "touch-engine", "LEADS_INGEST_TOUCH_ENGINE_SECRET": ingest_secret, "LEADS_INGEST_TOUCH_ENGINE_FETCH_BASE": touch_url, "LEADS_INGEST_TOUCH_ENGINE_FETCH_TOKEN": receiver_token, "LEADS_INGEST_TOUCH_ENGINE_FETCH_SERVICE_ID": service_id, "LEADS_TENANT_BINDINGS": f"touch-engine/notify/{tenant}={target_tenant}@1"}
        for binary, env in [("touch-server", touch_env), ("leads-server", leads_env)]:
            log = open(out / (binary + ".log"), "wb"); logs.append(log)
            procs.append(subprocess.Popen([str(out / binary)], env=env, stdout=log, stderr=log))
        for origin in [touch_url, leads_url]:
            def ready():
                try:
                    return request(origin + "/healthz")[0] == 200
                except OSError:
                    return False
            wait_for(ready, "real server ready")
        headers = {"X-Service-ID": service_id, "X-Service-Token": receiver_token, "X-Leads-Tenant": tenant}
        record_path = touch_url + "/api/v1/internal/lead-records/" + args.original_revoked_ref
        status, tombstone = request(record_path, headers=headers)
        assert status == 200 and tombstone["tombstone"] and "phone" not in tombstone and "name" not in tombstone
        evidence["original_tombstone"] = {k: tombstone[k] for k in ["submission_ref", "source_revision", "authorization", "tombstone"]}
        assert request(record_path, headers={**headers, "X-Service-Token": "wrong"})[0] == 401
        assert request(record_path, headers={**headers, "X-Leads-Tenant": "wrong-source-tenant"})[0] == 404
        assert request(touch_url + "/internal/v1/lead-records/" + args.original_revoked_ref, headers={"X-Internal-Token": touch_env["TOUCH_INTERNAL_TOKEN"]})[0] in [404, 410]
        # The old normal-browser record was revoked before dispatch. Its actual
        # suppressed event cannot acquire a fabricated source acknowledgement.
        old_envelope = {"type": payload["event_profile"]["event_type"], "schema_version": 1, "app_id": "touch-engine", "tenant_id": tenant, "data": payload}
        old_status, _ = ingest(old_envelope)
        assert old_status == 503
        evidence["original_suppressed_event_ack_status"] = old_status
        phone = "139" + "".join(str(secrets.randbelow(10)) for _ in range(8))
        public = touch_url + "/api/v1/public/storefronts/" + code
        status, submitted = request(public + "/lead-submissions", {"name": "", "phone": phone, "consent_version": "v1", "consent": True, "marketing_optin": False, "channel": "web"})
        assert status == 201, (status, submitted)
        ref = submitted["submission_ref"]
        evidence["new_submission_ref"] = ref

        def deliver_kind(event_type):
            with lock:
                found = next((d for d in deliveries if d["envelope"]["data"]["event_profile"]["source_ref"] == ref and d["envelope"]["type"] == event_type), None)
            if not found:
                return False
            status, receipt = ingest(found["envelope"])
            if status != 200:
                return False
            return found, receipt

        submit_delivery, stored = wait_for(lambda: deliver_kind("lead.authorized_submitted"), "real stored ACK")
        assert submit_delivery["first_status"] == 503 and stored["source_ack"]["receiver_state"] == "stored"
        status, progress = request(public + "/lead-status", {"submission_ref": ref, "phone": phone})
        assert status == 200 and progress["state"] == "crm_received" and progress["receiver_state"] == "stored", progress
        evidence["stored_progress"] = progress
        status, revoked = request(public + "/lead-revocations", {"submission_ref": ref, "phone": phone})
        assert status == 200 and revoked["state"] == "revoked"
        revoke_delivery, revoked_receipt = wait_for(lambda: deliver_kind("lead.consent_revoked"), "real revoked ACK")
        assert revoke_delivery["first_status"] == 503 and revoked_receipt["source_ack"]["receiver_state"] == "revoked"
        status, replayed = ingest(submit_delivery["envelope"])
        assert status == 200 and replayed["source_ack"]["receiver_state"] == "revoked"
        status, progress = request(public + "/lead-status", {"submission_ref": ref, "phone": phone})
        assert status == 200 and progress["receiver_state"] == "revoked" and not progress["revocation_pending"]
        evidence["revoked_progress"] = progress
        with sqlite3.connect(f"file:{out / 'leads.db'}?mode=ro", uri=True) as db:
            assert db.execute("SELECT count(*) FROM leads").fetchone()[0] == 1
            assert db.execute("SELECT count(*) FROM contacts").fetchone()[0] == 1
            assert db.execute("SELECT count(*) FROM contact_consents WHERE revoked_at IS NULL").fetchone()[0] == 0
            evidence["leads_counts"] = {"contacts": 1, "leads": 1, "active_consents": 0}
        with sqlite3.connect(f"file:{copied}?mode=ro", uri=True) as db:
            evidence["actual_touch_receipts"] = [dict(zip(["source_event_id", "source_revision", "receiver_state"], row)) for row in db.execute("SELECT source_event_id,source_revision,receiver_state FROM lead_receipts WHERE submission_ref=? ORDER BY created_at,id", (ref,))]
            assert {r["receiver_state"] for r in evidence["actual_touch_receipts"]} == {"stored", "revoked"}
            assert db.execute("SELECT count(*) FROM lead_receipts WHERE submission_ref=?", (args.original_revoked_ref,)).fetchone()[0] == 0
        evidence["first_delivery_statuses"] = [submit_delivery["first_status"], revoke_delivery["first_status"]]
        evidence["result"] = "pass"
        (out / "evidence.json").write_text(json.dumps(evidence, ensure_ascii=False, indent=2))
        print(json.dumps(evidence, ensure_ascii=False, indent=2))
    finally:
        for proc in reversed(procs):
            proc.terminate()
        for proc in procs:
            try:
                proc.wait(timeout=8)
            except subprocess.TimeoutExpired:
                proc.kill(); proc.wait()
        relay.shutdown()
        for log in logs:
            log.close()


if __name__ == "__main__":
    main()
