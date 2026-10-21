#!/usr/bin/env python3
"""Independent WCAG contrast report for the generated work.light/light tokens.

Reads web/src/styles/generated/painuo/v1/{tokens.css,resolved.json}; recomputes
ratios from the CSS variable values (does NOT trust resolved.json verdicts).
"""
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[5]
GEN = ROOT / "web/src/styles/generated/painuo/v1"
SURFACE, THEME = "work.light", "light"
PAIRS = [
    "text.primary.on.bg.surface", "text.primary.on.bg.raised",
    "text.muted.on.bg.page", "text.muted.on.bg.surface",
    "text.secondary.on.bg.surface", "brand.ink.on.brand.accent",
    "brand.text.on.brand.weak", "status.warning.fg.on.status.warning.weak",
    "status.danger.fg.on.status.danger.weak", "status.info.fg.on.status.info.weak",
    "border.focus.on.bg.surface",
]


def token_block(css, surface, theme):
    marker = f'[data-pn-surface="{surface}"][data-pn-theme="{theme}"]'
    start = css.index(marker)
    body = css[css.index("{", start) + 1:css.index("}", start)]
    return dict(re.findall(r"(--pn-[a-z0-9-]+)\s*:\s*([^;]+);", body))


def role_var(role):
    return "--pn-" + re.sub(r"\.", "-", re.sub(r"([A-Z])", lambda m: "-" + m.group(1).lower(), role))


def parse_hex(value):
    value = value.strip()
    short = re.fullmatch(r"#([0-9a-f]{3})", value, re.I)
    if short:
        return tuple(int(c * 2, 16) for c in short.group(1))
    long = re.fullmatch(r"#([0-9a-f]{6})", value, re.I)
    if long:
        return tuple(int(long.group(1)[i:i + 2], 16) for i in (0, 2, 4))
    return None


def lum(rgb):
    def ch(v):
        s = v / 255
        return s / 12.92 if s <= 0.03928 else ((s + 0.055) / 1.055) ** 2.4
    return 0.2126 * ch(rgb[0]) + 0.7152 * ch(rgb[1]) + 0.0722 * ch(rgb[2])


def ratio(a, b):
    la, lb = lum(a), lum(b)
    return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)


css = (GEN / "tokens.css").read_text()
tokens = token_block(css, SURFACE, THEME)
resolved = json.loads((GEN / "resolved.json").read_text())
rows = [r for r in resolved["contrast"]["rows"]
        if r["surface"] == SURFACE and r["theme"] == THEME and "leads-web" in r["profiles"]]
report = {"surface": f"{SURFACE}/{THEME}", "declaredPairs": len(rows), "checkedPairs": 0,
          "failures": [], "minRatio": None, "maxRatio": None, "pagePairs": []}
for row in rows:
    fg, bg = tokens.get(role_var(row["fgRole"])), tokens.get(role_var(row["bgRole"]))
    if not fg or not bg:
        report["failures"].append({"pair": row["pair"], "reason": "missing token"})
        continue
    parsed_fg, parsed_bg = parse_hex(fg), parse_hex(bg)
    if not parsed_fg or not parsed_bg:
        report["failures"].append({"pair": row["pair"], "reason": "non-hex value", "fg": fg, "bg": bg})
        continue
    r = ratio(parsed_fg, parsed_bg)
    report["checkedPairs"] += 1
    entry = {"pair": row["pair"], "ratio": round(r, 3), "minRatio": row["minRatio"],
             "fg": fg, "bg": bg, "profile": "leads-web"}
    if report["minRatio"] is None or entry["ratio"] < report["minRatio"]["ratio"]:
        report["minRatio"] = entry
    if report["maxRatio"] is None or entry["ratio"] > report["maxRatio"]["ratio"]:
        report["maxRatio"] = entry
    if r + 1e-9 < row["minRatio"]:
        report["failures"].append(entry)
for pair in PAIRS:
    hit = next((r for r in rows if r["pair"] == pair), None)
    report["pagePairs"].append({"pair": pair, "declared": hit is not None})
print(json.dumps(report, ensure_ascii=False, indent=2))
