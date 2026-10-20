import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const root = join(dirname(fileURLToPath(import.meta.url)), "../..");

function source(path: string): string {
  return readFileSync(join(root, path), "utf8");
}

describe("EcoTopNav boundaries", () => {
  it("keeps the public form and the root layout free of EcoTopNav", () => {
    expect(source("src/app/f/[id]/page.tsx")).not.toMatch(/EcoTopNav/);
    expect(source("src/app/layout.tsx")).not.toMatch(/EcoTopNav/);
    expect(source("src/app/(shell)/layout.tsx")).toMatch(/EcoTopNav|CrmShell/);
  });

  it("does not import the service-draft handoff path from the nav", () => {
    const nav = source("src/components/eco-nav/EcoTopNav.tsx");
    const shell = source("src/components/eco-nav/CrmShell.tsx");
    expect(nav + shell).not.toMatch(/serviceDraft|service-draft|handoffsender/);
  });

  it("does not hard-code ecosystem hosts in the nav implementation", () => {
    const files = [
      "src/lib/eco-nav/model.ts",
      "src/lib/eco-nav/fixture.ts",
      "src/lib/eco-nav/load.ts",
      "src/components/eco-nav/EcoTopNav.tsx",
      "src/components/eco-nav/CrmShell.tsx",
    ];
    for (const file of files) {
      const text = source(file);
      expect(text).not.toMatch(/[a-z0-9-]+\.(example|huigoo|invalid)\b/);
      // HUI-2227：launch_url 只能出现在 BFF 规划器与“接收后跳转”的壳层；
      // 导航渲染/模型/夹具不得自己拼 URL。
      if (file.endsWith("EcoTopNav.tsx") || file.endsWith("model.ts") || file.endsWith("fixture.ts") || file.endsWith("load.ts")) {
        expect(text).not.toMatch(/launch_url/);
      }
      expect(text).not.toMatch(/https?:\/\//);
    }
  });

  it("launches only through the own BFF with app/target pair", () => {
    const shell = source("src/components/eco-nav/CrmShell.tsx");
    expect(shell).toContain("/api/eco-nav/launch");
    expect(shell).toContain("launch_target_id");
    expect(shell).not.toMatch(/https?:\/\//);
    const plan = source("src/lib/eco-nav/launch.ts");
    expect(plan).toContain("/internal/v1/identity/shell/launch");
    expect(plan).toContain("LEADS_ECO_LAUNCH_HOSTS");
    expect(plan).not.toMatch(/[a-z0-9-]+\.(example|huigoo|invalid)\b/);
  });
});
