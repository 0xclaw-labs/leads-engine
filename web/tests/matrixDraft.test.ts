import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";
import {
  MATRIX_DRAFT_NOTICE,
  presentMatrixDraft,
  presentMatrixDraftRow,
} from "../src/lib/matrixDraft";

const forbidden = ["已发布", "发布成功", "已送出", "已扣费", "已触达", "已转化"];

describe("matrix draft source panel display", () => {
  it("keeps unknown plan, item, post ids and metrics as null (—), never zero or a claim", () => {
    const view = presentMatrixDraft({
      draft_id: "d_1",
      status: "draft",
      revision: 3,
      asset_id: "asset_copy_1",
      content_type: "copy",
      published: false,
      charged: false,
      matrix_plan_id: null,
      matrix_item_id: null,
      external_post_id: null,
      matrix_reach: null,
      matrix_likes: null,
      matrix_leads: null,
    });
    expect(view.draftId).toBe("d_1");
    expect(view.status).toBe("draft");
    expect(view.published).toBe(false);
    expect(view.charged).toBe(false);
    expect(view.matrixPlanID).toBe("—");
    expect(view.matrixItemID).toBe("—");
    expect(view.externalPostID).toBe("—");
    expect(view.matrixReach).toBe("—");
    expect(view.matrixLikes).toBe("—");
    expect(view.matrixLeads).toBe("—");
    expect(view.notice).toBe(MATRIX_DRAFT_NOTICE);
  });

  it("shows supplied facts verbatim and never upgrades a draft into a publish", () => {
    const view = presentMatrixDraft({
      draft_id: "d_2",
      status: "draft",
      published: false,
      charged: false,
      matrix_plan_id: "plan_9",
      matrix_item_id: "item_9",
      external_post_id: "post_9",
      matrix_reach: 42,
      matrix_likes: null,
      matrix_leads: null,
    });
    expect(view.matrixPlanID).toBe("plan_9");
    expect(view.matrixReach).toBe("42");
    expect(view.matrixLikes).toBe("—");
    expect(view.published).toBe(false);
    expect(view.charged).toBe(false);
    for (const phrase of forbidden) {
      expect(view.notice).not.toContain(phrase);
    }
  });

  it("the row presenter tolerates missing fields from older servers", () => {
    const view = presentMatrixDraftRow({ draft_id: "d_3" });
    expect(view.draftId).toBe("d_3");
    expect(view.matrixPlanID).toBe("—");
    expect(view.matrixReach).toBe("—");
  });

  it("keeps the page copy from claiming a publish, send, or charge", () => {
    const files = ["../src/lib/matrixDraft.ts", "../src/components/MatrixDraftPanel.tsx"];
    for (const file of files) {
      const src = readFileSync(new URL(file, import.meta.url), "utf8");
      for (const phrase of forbidden) {
        expect(src, file).not.toContain(phrase);
      }
    }
  });

  it("the panel shows the server return anchor and links back to the campaign page", () => {
    const view = presentMatrixDraft({ draft_id: "d_4", return_path: "/campaigns/camp_1" });
    expect(view.returnPath).toBe("/campaigns/camp_1");
    const src = readFileSync(new URL("../src/components/MatrixDraftPanel.tsx", import.meta.url), "utf8");
    expect(src).toContain("/leads/${campaignId}");
    expect(src).not.toContain("window.location");
  });
});
