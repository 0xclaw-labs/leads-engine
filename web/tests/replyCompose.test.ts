import { afterEach, describe, expect, it, vi } from "vitest";
import { clearReplyCompose, recallReplyCompose, rememberReplyCompose } from "@/lib/reception";

// HUI-2598 D4：面板重渲染/短暂离开时，未保存回复按 (租户, 会话) 留在本机。
// 它不是服务端草稿；保存/发送成功后必须清除。

function memoryStorage() {
  const map = new Map<string, string>();
  return {
    getItem: (key: string) => map.get(key) ?? null,
    setItem: (key: string, value: string) => void map.set(key, value),
    removeItem: (key: string) => void map.delete(key),
  };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("reply compose memory", () => {
  it("remembers per tenant and session, and clears on demand", () => {
    vi.stubGlobal("window", { sessionStorage: memoryStorage() });
    rememberReplyCompose("tnt_a", "rcs_1", "草稿 A");
    rememberReplyCompose("tnt_b", "rcs_1", "草稿 B");
    expect(recallReplyCompose("tnt_a", "rcs_1")).toBe("草稿 A");
    expect(recallReplyCompose("tnt_b", "rcs_1")).toBe("草稿 B");
    clearReplyCompose("tnt_a", "rcs_1");
    expect(recallReplyCompose("tnt_a", "rcs_1")).toBe("");
    expect(recallReplyCompose("tnt_b", "rcs_1")).toBe("草稿 B");
  });

  it("clears when the text becomes blank", () => {
    vi.stubGlobal("window", { sessionStorage: memoryStorage() });
    rememberReplyCompose("tnt_a", "rcs_1", "草稿");
    rememberReplyCompose("tnt_a", "rcs_1", "  ");
    expect(recallReplyCompose("tnt_a", "rcs_1")).toBe("");
  });

  it("refuses unsafe tenant/session ids and disabled storage without throwing", () => {
    vi.stubGlobal("window", {
      sessionStorage: {
        getItem: () => {
          throw new Error("disabled");
        },
        setItem: () => {
          throw new Error("disabled");
        },
        removeItem: () => {
          throw new Error("disabled");
        },
      },
    });
    expect(() => rememberReplyCompose("tnt/a", "rcs_1", "x")).not.toThrow();
    expect(recallReplyCompose("tnt_a", "rcs/1")).toBe("");
    expect(() => clearReplyCompose("tnt_a", "rcs_1")).not.toThrow();
  });

  it("truncates local memory at the server body cap", () => {
    vi.stubGlobal("window", { sessionStorage: memoryStorage() });
    rememberReplyCompose("tnt_a", "rcs_1", "x".repeat(3000));
    expect(recallReplyCompose("tnt_a", "rcs_1")).toHaveLength(2000);
  });
});
