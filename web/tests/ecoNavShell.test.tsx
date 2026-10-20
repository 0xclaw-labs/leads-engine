import { readFileSync } from "node:fs";
import { join } from "node:path";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { EcoTopNav } from "@/components/eco-nav/EcoTopNav";
import { blockedEcoNavModel } from "@/lib/eco-nav/load";
import { parseEcoNavDocument, pinLeadsApp, toViewModel, type EcoNavModel } from "@/lib/eco-nav/model";

function view(name: string): EcoNavModel {
  const raw = JSON.parse(readFileSync(join(__dirname, "eco-nav/fixtures", name), "utf8"));
  const parsed = parseEcoNavDocument(raw);
  if (!parsed.ok) throw new Error("fixture rejected");
  return pinLeadsApp(toViewModel(parsed.document, { provenance: "public_ai_context", statusSummary: "" }));
}

function render(model: EcoNavModel, extra: Partial<React.ComponentProps<typeof EcoTopNav>> = {}): string {
  return renderToStaticMarkup(
    React.createElement(EcoTopNav, {
      model,
      nickname: "owner@example.test",
      onTenantSwitch: () => undefined,
      onLaunchApp: () => undefined,
      onReturn: () => undefined,
      ...extra,
    }),
  );
}

describe("EcoTopNav launch/return/usage", () => {
  it("renders launchable apps as buttons carrying only app + registered target", () => {
    const html = render(view("ready-first-party.json"));
    expect(html).toContain('data-testid="eco-current-app"');
    expect(html).toContain('aria-current="page"');
    expect(html).toContain('data-eco-app-state="launchable"');
    expect(html).toContain('data-launch-target-id="ti-orders-web"');
    expect(html).toMatch(/<button[^>]*data-testid="eco-app-orders"/);
    expect(html).toContain('data-nav-intent="manual_switch"');
    expect(html).toContain('data-creates-handoff="false"');
    expect(html).not.toContain("lead_id");
  });

  it("shows blocked apps as non-executable with an honest reason", () => {
    const html = render(view("ready-first-party.json"));
    expect(html).toContain('data-eco-app-state="entitlement_required"');
    expect(html).toContain('aria-disabled="true"');
    expect(html).toContain("需开通权益");
  });

  it("offers the verified return source chip without claiming exact object restore", () => {
    const html = render(view("ready-first-party.json"));
    expect(html).toContain('data-testid="eco-return-source"');
    expect(html).toContain('data-source-app="orders"');
    expect(html).toContain('data-return-target="ti-orders-web"');
    expect(html).toContain('data-has-source-ref="true"');
    expect(html).toContain("订单 #123");
  });

  it("does not invent a return chip when the model has none", () => {
    const model = view("ready-first-party.json");
    expect(render({ ...model, return_context: null })).not.toContain('data-testid="eco-return-source"');
  });

  it("links account/usage to the existing subscription page (no second wallet UI)", () => {
    const html = render(view("ready-first-party.json"));
    expect(html).toContain('data-testid="eco-account-usage"');
    expect(html).toContain('href="/subscription"');
    expect(html).not.toContain("充值");
  });

  it("carries the work.light token scope on the bar", () => {
    const html = render(view("ready-first-party.json"));
    expect(html).toContain('data-pn-surface="work.light"');
    expect(html).toContain('data-pn-theme="light"');
  });

  it("shows a launch failure as an alert instead of silently doing nothing", () => {
    const html = render(view("ready-first-party.json"), { launchError: "目标应用这次不能切换。", launchBusy: true });
    expect(html).toContain('data-testid="eco-launch-error"');
    expect(html).toContain("目标应用这次不能切换。");
    expect(html).toContain("disabled");
  });

  it("renders nothing for a blocked model (fail-closed)", () => {
    const hidden = blockedEcoNavModel("hidden", "public_surface", "公开页不渲染生态导航");
    expect(render(hidden)).toBe("");
  });
});
