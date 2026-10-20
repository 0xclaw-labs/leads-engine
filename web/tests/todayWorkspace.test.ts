import { describe, expect, it } from "vitest";
import {
  attentionSummary,
  conversationActions,
  draftPrefill,
  rowReason,
  todayDestination,
  type ConversationFacts,
  type ReplyFacts,
} from "@/lib/todayWorkspace";
import { receptionOpenHref, TODAY_GROUPS, todayReturnHref, type DeskItem } from "@/lib/workbench";

// HUI-2626：今日工作台的纯规则。页面只画这里判过的事实。

function item(patch: Partial<DeskItem>): DeskItem {
  return { id: "desk_1", kind: "lead", ...patch };
}

describe("todayDestination", () => {
  it("opens the conversation for a reception row even when a lead exists", () => {
    const target = todayDestination(item({ id: "d1", kind: "reception", session_id: "rcs_1", lead_id: "lead_1" }));
    expect(target).toEqual({
      href: receptionOpenHref("rcs_1", "d1"),
      label: "打开会话",
      kind: "conversation",
    });
  });

  it("opens the conversation for an AI draft, not the lead page", () => {
    const target = todayDestination(item({ id: "d2", kind: "ai_draft", session_id: "rcs_2", lead_id: "lead_2" }));
    expect(target?.kind).toBe("conversation");
    expect(target?.href).toBe("/reception?session=rcs_2&focus=d2");
  });

  it("falls back to the lead then the opportunity", () => {
    expect(todayDestination(item({ id: "d3", kind: "lead", lead_id: "lead_3" }))?.kind).toBe("lead");
    expect(todayDestination(item({ id: "d4", kind: "opportunity", opportunity_id: "opp_1" }))?.kind).toBe("opportunity");
  });

  it("returns null when there is no safe destination", () => {
    expect(todayDestination(item({ id: "d5", kind: "lead" }))).toBeNull();
    expect(todayDestination(item({ id: "d6", kind: "reception", session_id: "" }))).toBeNull();
  });

  it("never invents a link from an engineering-only id", () => {
    const target = todayDestination(item({ id: "d7", kind: "lead", lead_id: "lead with space" }));
    expect(target).toBeNull();
  });
});

describe("attentionSummary", () => {
  it("keeps the fixed group order and counts non-empty groups", () => {
    const groups = {
      new_inquiry: [item({ id: "a" })],
      due_today: [item({ id: "b" }), item({ id: "c" })],
    } as never;
    const out = attentionSummary(groups);
    expect(out.total).toBe(3);
    expect(out.rows.map((row) => row.group)).toEqual(["new_inquiry", "due_today"]);
    expect(out.rows.map((row) => row.count)).toEqual([1, 2]);
    expect(out.rows.every((row) => TODAY_GROUPS.includes(row.group))).toBe(true);
    expect(out.rows[0].href).toBe("#today-group-new_inquiry");
  });

  it("is honestly empty when nothing needs attention", () => {
    const out = attentionSummary(null);
    expect(out).toEqual({ total: 0, rows: [], empty: true });
  });
});

describe("rowReason", () => {
  it("uses the group label plus the concrete next step", () => {
    const text = rowReason(item({ id: "a", kind: "lead", reason: "", next: { label: "安排下一次", at: "" } }), "due_today");
    expect(text).toContain("今天应跟进");
    expect(text).toContain("安排下一次");
  });

  it("keeps a recorded reason and never shows an engineering id", () => {
    expect(rowReason(item({ id: "a", kind: "reception", reason: "需要核对或转人工" }), "human_takeover")).toContain("需要核对或转人工");
    expect(rowReason(item({ id: "a", kind: "lead", reason: "cmp_1" }), "new_inquiry")).not.toContain("cmp_1");
  });

  it("says the draft is not auto-sent", () => {
    expect(rowReason(item({ id: "a", kind: "ai_draft" }), "ai_draft")).toContain("不会自动发送");
  });
});

describe("conversationActions", () => {
  const openHuman: ConversationFacts = { status: "open", mode: "human", epoch: 3, version: 5 };
  const openAI: ConversationFacts = { status: "open", mode: "ai", epoch: 2, version: 4 };
  const generated: ReplyFacts = { id: "r1", status: "generated", epoch: 3 };
  const approved: ReplyFacts = { id: "r1", status: "approved", epoch: 3 };

  it("lets a human takeover approve its own saved draft then send", () => {
    expect(conversationActions(openHuman, generated)).toMatchObject({
      canTakeover: false,
      canRelease: true,
      canApprove: true,
      canSend: false,
      canHumanReply: true,
      blockedReason: "",
    });
    expect(conversationActions(openHuman, approved)).toMatchObject({ canApprove: false, canSend: true });
  });

  it("requires assist or takeover before approving in an AI session", () => {
    expect(conversationActions(openAI, { ...generated, epoch: 2 })).toMatchObject({ canApprove: false, canSend: false });
    expect(conversationActions(openAI, { ...generated, epoch: 2 }).blockedReason).toContain("接管");
  });

  it("drops actions on a stale epoch or a closed session", () => {
    expect(conversationActions(openHuman, { ...generated, epoch: 2 }).canApprove).toBe(false);
    expect(conversationActions({ ...openHuman, status: "closed" }, generated).canSend).toBe(false);
    expect(conversationActions(null, generated).blockedReason).toContain("会话");
  });
});

describe("draftPrefill", () => {
  it("prefills the latest generated draft in a human takeover", () => {
    const replies = [
      { id: "r1", status: "generated", kind: "draft", body: "旧草稿", epoch: 3, session_version: 5 },
      { id: "r2", status: "superseded", kind: "draft", body: "作废", epoch: 3, session_version: 5 },
      { id: "r3", status: "generated", kind: "draft", body: "新草稿", epoch: 3, session_version: 5 },
    ];
    expect(draftPrefill(replies, "human")).toMatchObject({ replyId: "r3", body: "新草稿" });
  });

  it("does not prefill in AI mode or from a sent reply", () => {
    expect(draftPrefill([{ id: "r1", status: "generated", kind: "draft", body: "x", epoch: 1 }], "ai")).toBeNull();
    expect(draftPrefill([{ id: "r1", status: "sent", kind: "draft", body: "x", epoch: 1 }], "human")).toBeNull();
    expect(draftPrefill([], "human")).toBeNull();
  });
});

describe("return target token", () => {
  it("keeps the today return token only when it is a safe token", () => {
    expect(todayReturnHref("desk_1")).toBe("/?focus=desk_1");
    expect(todayReturnHref("../../etc")).toBe("/");
  });
});
