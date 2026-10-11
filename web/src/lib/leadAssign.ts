// HUI-2598/2626 走查修复：线索「待分配」时详情页提供分配动作。
// 纯函数，无网络、无 DOM。权限以服务端为准：PATCH /api/v1/leads/{id} 仅 owner 可改派，
// 前端只用 /api/admin/members 能否读取来决定是否展示分配块（非 owner 读取失败 → 不展示）。

export interface MemberRow {
  id?: string;
  display_name?: string;
  role?: string;
  enabled?: boolean;
}

export interface MemberOption {
  id: string;
  label: string;
}

const ROLE_LABEL: Record<string, string> = {
  owner: "店主",
  sales: "销售",
  agent: "代理",
};

export function needsOwner(ownerLabel?: string): boolean {
  const label = (ownerLabel ?? "").trim();
  return label === "" || label === "待分配";
}

export function assignableMembers(rows: MemberRow[]): MemberOption[] {
  const out: MemberOption[] = [];
  for (const row of rows) {
    const id = (row.id ?? "").trim();
    if (!id || row.enabled !== true) continue;
    const name = (row.display_name ?? "").trim() || id;
    const role = ROLE_LABEL[row.role ?? ""] ?? "成员";
    out.push({ id, label: `${name}（${role}）` });
  }
  return out;
}

export function assignBody(memberId: string): { assigned_member_id: string } {
  return { assigned_member_id: memberId };
}
