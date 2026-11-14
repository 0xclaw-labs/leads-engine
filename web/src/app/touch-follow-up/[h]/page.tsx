"use client";
import { useParams, useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { createHandoffRequest, continuationDestination } from "@/lib/follow-up-continuation";
import { commitCrmTenant, currentCrmScopeEpoch } from "@/lib/eco-nav/use-crm-scope";
import { observeCrmSsoAccountChanges, resetCrmScopeAfterSso } from "@/lib/eco-nav/sso-scope-reset";
export default function TouchFollowUpContinuation() {
    const { h } = useParams<{
        h: string;
    }>();
    const router = useRouter();
    const [error, setError] = useState("");
    const pending = useRef(createHandoffRequest((handoff: string) => fetch(`/api/touch-follow-ups/${encodeURIComponent(handoff)}/resume`, { method: "POST" })));
    useEffect(() => {
        let alive = true;
        setError("");
        resetCrmScopeAfterSso();
        const epoch = currentCrmScopeEpoch();
        const stop = observeCrmSsoAccountChanges(() => {
            alive = false;
            setError("账号已切换，请回碰一碰原线索重新进入。");
        });
        // Clone before reading: StrictMode observes the same one-time request twice.
        pending.current(h).then(res => res.clone().json().then(data => ({ res, data }))).then(({ res, data }) => { if (!alive || currentCrmScopeEpoch() !== epoch)
            return; if (!res.ok) {
            setError(data.message || "原线索接续已失效，请回碰一碰重新进入。");
            return;
        } if (!data.known) {
            router.replace(continuationDestination(data));
            return;
        } commitCrmTenant(data.crm_tenant_id); router.replace(continuationDestination(data)); }).catch(() => { if (alive)
            setError("没有连上服务。请回碰一碰线索列表重试。"); });
        return () => { alive = false; stop(); };
    }, [h, router]);
    return <main><h1>正在打开原线索</h1><p role="status">{error || "正在核验当前身份、原线索映射和顾客许可…"}</p>{error ? <a href="/">返回获客工作台</a> : null}</main>;
}
