"use client";
import { useSearchParams } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { currentCrmScopeEpoch, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { failureText } from "@/lib/productShell";

export function TouchFollowUpReturn() {
  const visit = useSearchParams().get("touch_follow_up");
  const scope = useCrmScope();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const current = useRef(scope.tenant);
  current.current = scope.tenant;

  useEffect(() => {
    setError("");
    setBusy(false);
  }, [scope.tenant, visit]);

  if (!visit) return null;

  async function go() {
    const tenant = scope.tenant;
    const epoch = currentCrmScopeEpoch();
    if (!tenant) return;
    setBusy(true);
    setError("");
    try {
      const res = await fetch(`/api/touch-follow-up-visits/${encodeURIComponent(visit!)}/return`, {
        method: "POST",
        headers: { "x-tenant-id": tenant },
      });
      const data = await res.json().catch(() => ({}));
      if (current.current !== tenant || currentCrmScopeEpoch() !== epoch) return;
      if (!res.ok || typeof data.launch_url !== "string") {
        setError(res.ok ? "原店返回入口当前不可用，请稍后重试。" : failureText(data, res.status));
        return;
      }
      window.location.assign(data.launch_url);
    } catch {
      if (current.current === tenant && currentCrmScopeEpoch() === epoch) {
        setError("没有连上原店返回服务，可稍后重试。");
      }
    } finally {
      if (current.current === tenant && currentCrmScopeEpoch() === epoch) {
        setBusy(false);
      }
    }
  }

  return (
    <div data-region="touch-follow-up-return">
      <Button variant="outline" size="sm" type="button" disabled={busy || !scope.tenant} onClick={() => void go()}>
        {busy ? "正在核验返回入口…" : "返回原门店"}
      </Button>
      {error ? <p role="status" className="muted">{error}</p> : null}
    </div>
  );
}
