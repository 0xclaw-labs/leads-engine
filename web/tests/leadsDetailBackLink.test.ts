import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// P0：线索详情页跟进卡里的链接原 href 指向 /leads/${id}——详情页自己，
// 点击等于原地不动（死链）。收敛为返回线索列表 /leads。

const page = () => readFileSync("src/app/(shell)/leads/[id]/page.tsx", "utf8");

describe("lead detail back link", () => {
  it("links back to the leads list instead of the detail page itself", () => {
    const src = page();
    expect(src).toContain('href="/leads"');
    expect(src).toContain("返回线索列表");
    expect(src).not.toContain("href={`/leads/${id}`}");
  });

  it("keeps the page-primary submit contract untouched", () => {
    const src = page();
    expect(src.split('data-page-primary="true"').length - 1).toBe(1);
    expect(src).not.toContain("<button");
  });
});
