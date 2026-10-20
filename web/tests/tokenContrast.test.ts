import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// HUI-2626：独立的语义 token 对比度门。
//
// 不读 resolved.json 的 verdict 当结论：从 tokens.css 的 work.light/light
// 变量值独立算 WCAG 2.x 对比度，再和合同声明的 minRatio 比较。声明通过但
// 实际不过、或页面用了合同没登记的配对，都直接红。
//
// 另加两条采用门：
//   - 业务 CSS 只能用 work.light 已存在的 --pn-* 变量；
//   - :root 回退块之外不得新增裸 hex。

const WEB = join(__dirname, "..");
const GENERATED = join(WEB, "src/styles/generated/painuo/v1");
const TOKENS_CSS = join(GENERATED, "tokens.css");
const RESOLVED = join(GENERATED, "resolved.json");
const SURFACE = "work.light";
const THEME = "light";

function read(path: string): string {
  return readFileSync(path, "utf8");
}

function stripComments(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, "");
}

function tokenBlock(css: string, surface: string, theme: string): Map<string, string> {
  const selector = `[data-pn-surface="${surface}"][data-pn-theme="${theme}"]`;
  const start = css.indexOf(selector);
  expect(start, `missing token block ${selector}`).toBeGreaterThanOrEqual(0);
  const open = css.indexOf("{", start);
  const close = css.indexOf("}", open);
  const body = css.slice(open + 1, close);
  const out = new Map<string, string>();
  for (const match of body.matchAll(/(--pn-[a-z0-9-]+)\s*:\s*([^;]+);/g)) {
    out.set(match[1], match[2].trim());
  }
  return out;
}

function roleVar(role: string): string {
  return `--pn-${role
    .replace(/[A-Z]/g, (char) => `-${char.toLowerCase()}`)
    .replace(/\./g, "-")}`;
}

function parseHex(value: string): [number, number, number] | null {
  const text = value.trim();
  const short = /^#([0-9a-f]{3})$/i.exec(text);
  if (short) {
    const [r, g, b] = short[1].split("").map((part) => parseInt(part + part, 16));
    return [r, g, b];
  }
  const long = /^#([0-9a-f]{6})$/i.exec(text);
  if (long) {
    return [parseInt(long[1].slice(0, 2), 16), parseInt(long[1].slice(2, 4), 16), parseInt(long[1].slice(4, 6), 16)];
  }
  return null;
}

function luminance([r, g, b]: [number, number, number]): number {
  const channel = (value: number) => {
    const s = value / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
}

function contrast(fg: string, bg: string): number | null {
  const a = parseHex(fg);
  const b = parseHex(bg);
  if (!a || !b) return null;
  const la = luminance(a);
  const lb = luminance(b);
  return (Math.max(la, lb) + 0.05) / (Math.min(la, lb) + 0.05);
}

interface ContrastRow {
  pair: string;
  fgRole: string;
  bgRole: string;
  minRatio: number;
  verdict: string;
  surface: string;
  theme: string;
  profiles: string[];
}

function leadsRows(): ContrastRow[] {
  const resolved = JSON.parse(read(RESOLVED)) as { contrast: { rows: ContrastRow[] } };
  return resolved.contrast.rows.filter(
    (row) => row.surface === SURFACE && row.theme === THEME && row.profiles.includes("leads-web"),
  );
}

describe("semantic token contrast (work.light/light, leads-web)", () => {
  const tokens = tokenBlock(stripComments(read(TOKENS_CSS)), SURFACE, THEME);
  const rows = leadsRows();

  it("finds the full leads-web pair set in the generated contract", () => {
    expect(rows.length).toBe(99);
    expect(new Set(rows.map((row) => row.pair)).size).toBe(99);
  });

  it("independently recomputes every declared pair and finds no violation", () => {
    const failures: string[] = [];
    for (const row of rows) {
      const fgVar = roleVar(row.fgRole);
      const bgVar = roleVar(row.bgRole);
      const fg = tokens.get(fgVar);
      const bg = tokens.get(bgVar);
      if (!fg || !bg) {
        failures.push(`${row.pair}: token missing (${fgVar}=${fg ?? "?"}, ${bgVar}=${bg ?? "?"})`);
        continue;
      }
      const ratio = contrast(fg, bg);
      if (ratio === null) {
        failures.push(`${row.pair}: non-hex token value (${fg} / ${bg})`);
        continue;
      }
      if (row.verdict !== "pass") failures.push(`${row.pair}: declared verdict ${row.verdict}`);
      if (ratio + 1e-9 < row.minRatio) {
        failures.push(`${row.pair}: ${ratio.toFixed(3)} < ${row.minRatio} (${fg} on ${bg})`);
      }
    }
    expect(failures, failures.join("\n")).toEqual([]);
  });

  it("would fail on a real violation (the math is not a PASS constant)", () => {
    // #777777 on white is ~4.48:1, below the 4.5 normal-text floor.
    expect(contrast("#777777", "#ffffff")).toBeLessThan(4.5);
    expect(contrast("#000000", "#ffffff")).toBeGreaterThan(4.5);
    const surface = tokens.get("--pn-bg-surface");
    expect(surface).toBeTruthy();
    expect(contrast("#777777", surface as string)).toBeLessThan(4.5);
  });

  it("covers the semantic pairs the today/nav CSS actually uses", () => {
    const used = [
      "text.primary.on.bg.surface",
      "text.primary.on.bg.raised",
      "text.muted.on.bg.page",
      "text.muted.on.bg.surface",
      "text.secondary.on.bg.surface",
      "brand.ink.on.brand.accent",
      "brand.text.on.brand.weak",
      "status.warning.fg.on.status.warning.weak",
      "status.danger.fg.on.status.danger.weak",
      "status.info.fg.on.status.info.weak",
      "border.focus.on.bg.surface",
    ];
    const pairs = new Set(rows.map((row) => row.pair));
    for (const pair of used) expect(pairs.has(pair), `used pair not declared: ${pair}`).toBe(true);
  });
});

describe("token adoption in touched CSS", () => {
  const files = ["src/app/globals.css", "src/components/eco-nav/eco-top-nav.module.css"];
  const tokens = tokenBlock(stripComments(read(TOKENS_CSS)), SURFACE, THEME);

  it("only references --pn-* roles that exist in work.light/light", () => {
    const missing: string[] = [];
    for (const file of files) {
      const css = stripComments(read(join(WEB, file)));
      for (const match of css.matchAll(/var\((--pn-[a-z0-9-]+)/g)) {
        if (!tokens.has(match[1])) missing.push(`${file}: ${match[1]}`);
      }
    }
    expect(missing, missing.join("\n")).toEqual([]);
  });

  it("keeps raw hex confined to the :root public-form fallback", () => {
    const offenders: string[] = [];
    for (const file of files) {
      let css = stripComments(read(join(WEB, file)));
      if (file.endsWith("globals.css")) {
        const root = css.indexOf(":root");
        if (root >= 0) {
          const open = css.indexOf("{", root);
          const close = css.indexOf("}", open);
          css = css.slice(0, root) + css.slice(close + 1);
        }
      }
      for (const match of css.matchAll(/#[0-9a-fA-F]{3,8}\b/g)) {
        offenders.push(`${file}: ${match[0]}`);
      }
    }
    expect(offenders, offenders.join("\n")).toEqual([]);
  });

  it("keeps the today/nav surfaces on semantic roles", () => {
    const globals = stripComments(read(join(WEB, "src/app/globals.css")));
    expect(globals).toContain(".attention-strip");
    expect(globals).toContain("var(--pn-bg-surface");
    expect(globals).toContain("var(--pn-status-warning-weak");
    expect(globals).toContain("var(--pn-radius-card");
    const nav = stripComments(read(join(WEB, "src/components/eco-nav/eco-top-nav.module.css")));
    expect(nav).toContain("var(--pn-bg-surface");
    expect(nav).toContain("var(--pn-border-focus");
    expect(nav).not.toMatch(/#[0-9a-fA-F]{3,8}/);
  });
});
