import React from "react";
import { renderToString } from "react-dom/server";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { describe, expect, it } from "vitest";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";

// LD-08 收口锁定：
// 1. destructive 必须有成对的 foreground 令牌（:root / .dark 两档），实测 WCAG 对比度 ≥4.5:1；
// 2. Button / Badge 的 destructive 变体真正消费 bg-destructive + text-destructive-foreground；
// 3. tailwind.config.js 暴露 destructive.foreground 映射；
// 4. 旧手写 `.card` 类在业务源码归零（已迁移到 bg-card/text-card-foreground 令牌工具类）。

const WEB = join(__dirname, "..");
const GLOBALS = join(WEB, "src/app/globals.css");
const CONFIG = join(WEB, "tailwind.config.js");

function read(path: string): string {
  return readFileSync(path, "utf8");
}

function stripComments(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, "");
}

function block(css: string, selector: string): string {
  const re = new RegExp(`${selector}\\s*\\{`, "g");
  let m: RegExpExecArray | null;
  while ((m = re.exec(css))) {
    const open = m.index + m[0].length - 1;
    const close = css.indexOf("}", open);
    const body = css.slice(open + 1, close);
    if (body.includes("--ui-destructive")) return body;
  }
  throw new Error(`missing token block ${selector} with --ui-destructive`);
}

function token(body: string, name: string): string {
  const match = body.match(new RegExp(`${name}\\s*:\\s*([^;]+);`));
  expect(match, `token ${name} missing`).toBeTruthy();
  return match![1].trim();
}

function parseOklch(value: string): [number, number, number] {
  const m = /oklch\(\s*([\d.]+)\s+([\d.]+)\s+([\d.]+)\s*\)/.exec(value);
  expect(m, `not an oklch value: ${value}`).toBeTruthy();
  return [Number(m![1]), Number(m![2]), Number(m![3])];
}

// OKLCH → linear sRGB → sRGB（矩阵取自 Oklab 规范 / culori convertOklabToLrgb）。
function oklchToSrgb([L, C, H]: [number, number, number]): [number, number, number] {
  const h = (H * Math.PI) / 180;
  const a = C * Math.cos(h);
  const b = C * Math.sin(h);
  const Lm = Math.pow(L + 0.3963377773761749 * a + 0.2158037573099136 * b, 3);
  const Mm = Math.pow(L - 0.1055613458156586 * a - 0.0638541728258133 * b, 3);
  const Sm = Math.pow(L - 0.0894841775298119 * a - 1.2914855480194092 * b, 3);
  const lin = [
    4.0767416360759574 * Lm - 3.3077115392580616 * Mm + 0.2309699031821044 * Sm,
    -1.2684379732850317 * Lm + 2.6097573492876887 * Mm - 0.3413193760026573 * Sm,
    -0.0041960761386756 * Lm - 0.7034186179359362 * Mm + 1.7076146940746117 * Sm,
  ];
  return lin.map((v) => {
    const x = Math.min(1, Math.max(0, v));
    return x <= 0.0031308 ? 12.92 * x : 1.055 * Math.pow(x, 1 / 2.4) - 0.055;
  }) as [number, number, number];
}

function luminance([r, g, b]: [number, number, number]): number {
  const channel = (v: number) => {
    const s = v;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

function contrast(fg: [number, number, number], bg: [number, number, number]): number {
  const l1 = luminance(fg);
  const l2 = luminance(bg);
  return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
}

describe("LD-08 destructive 前景配对", () => {
  const css = stripComments(read(GLOBALS));
  const root = block(css, ":root");
  const dark = block(css, ".dark");

  it("两档都声明 --ui-destructive-foreground 且与 destructive 对比 ≥4.5:1", () => {
    const pairs: Array<[string, string, string]> = [
      [":root", root, "light"],
      [".dark", dark, "dark"],
    ];
    for (const [label, body] of pairs) {
      const bg = parseOklch(token(body, "--ui-destructive"));
      const fg = parseOklch(token(body, "--ui-destructive-foreground"));
      const ratio = contrast(oklchToSrgb(fg), oklchToSrgb(bg));
      expect(ratio, `${label} destructive / destructive-foreground 对比不足`).toBeGreaterThanOrEqual(4.5);
    }
  });

  it("Button destructive 变体挂 bg-destructive 与 text-destructive-foreground", () => {
    const html = renderToString(<Button variant="destructive">删除</Button>);
    expect(html).toContain("bg-destructive");
    expect(html).toContain("text-destructive-foreground");
  });

  it("Badge destructive 变体挂 bg-destructive 与 text-destructive-foreground", () => {
    const html = renderToString(<Badge variant="destructive">风险</Badge>);
    expect(html).toContain("bg-destructive");
    expect(html).toContain("text-destructive-foreground");
  });

  it("tailwind config 暴露 destructive.foreground 映射", () => {
    const config = read(CONFIG);
    expect(config).toMatch(/destructive:\s*\{/);
    expect(config).toContain("foreground: 'var(--ui-destructive-foreground)'");
  });
});

describe("LD-08 legacy `.card` 旧类归零", () => {
  function walk(dir: string, out: string[] = []): string[] {
    for (const entry of readdirSync(dir)) {
      if (entry === "node_modules" || entry.startsWith(".")) continue;
      const p = join(dir, entry);
      const s = statSync(p);
      if (s.isDirectory()) walk(p, out);
      else if (/\.tsx$/.test(entry)) out.push(p);
    }
    return out;
  }

  it("业务源码不再使用裸 card 旧类 token", () => {
    const offenders: string[] = [];
    for (const file of walk(join(WEB, "src"))) {
      const src = read(file);
      for (const match of src.matchAll(/className="([^"]*)"/g)) {
        if (match[1].split(/\s+/).includes("card")) {
          offenders.push(`${relative(WEB, file)}: ${match[1]}`);
        }
      }
    }
    expect(offenders, offenders.join("\n")).toEqual([]);
  });
});
