import React from "react"; // classic JSX runtime（vitest esbuild + tsconfig jsx:preserve）所需的基建 import
import { renderToString } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Checkbox } from "@/components/ui/checkbox";

// F-a11y P1 加固：ui 原语必须真正承接调用方传入的 label（渲染 Label htmlFor + 关联 id），
// 并完整透传 aria-invalid / aria-describedby。node 环境无 jsdom，走 react-dom/server SSR 路径。

function firstMatch(html: string, re: RegExp): string | undefined {
  return html.match(re)?.[1];
}

describe("ui 原语 label 承接与错误关联（F-a11y P1 加固）", () => {
  it("Input 传 label 时渲染 Label 关联控件 id", () => {
    const html = renderToString(<Input label="姓名" value="x" onChange={() => {}} />);
    const forId = firstMatch(html, /<label[^>]*for="([^"]+)"/);
    const controlId = firstMatch(html, /<input[^>]*id="([^"]+)"/);
    expect(forId).toBeTruthy();
    expect(forId).toBe(controlId);
    expect(html).toContain(">姓名<");
  });

  it("Textarea 传 label 时渲染关联 Label", () => {
    const html = renderToString(<Textarea label="备注" value="x" onChange={() => {}} />);
    const forId = firstMatch(html, /<label[^>]*for="([^"]+)"/);
    const controlId = firstMatch(html, /<textarea[^>]*id="([^"]+)"/);
    expect(forId).toBe(controlId);
    expect(html).toContain(">备注<");
  });

  it("Checkbox 传 label 时渲染关联 Label", () => {
    const html = renderToString(
      <Checkbox label="同意" checked={false} onCheckedChange={() => {}} />
    );
    const forId = firstMatch(html, /<label[^>]*for="([^"]+)"/);
    const controlId = firstMatch(html, /<input[^>]*id="([^"]+)"/);
    expect(forId).toBe(controlId);
    expect(html).toContain(">同意<");
  });

  it("aria-invalid / aria-describedby 透传到原生控件", () => {
    const html = renderToString(
      <Input label="邮箱" aria-invalid aria-describedby="email-error" value="" onChange={() => {}} />
    );
    expect(html).toContain('aria-invalid="true"');
    expect(html).toContain('aria-describedby="email-error"');
  });

  it("未传 label 时不渲染 Label，保持既有透传行为", () => {
    const html = renderToString(<Input aria-label="搜索" value="" onChange={() => {}} />);
    expect(html).not.toContain("<label");
  });
});
