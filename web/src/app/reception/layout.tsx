import type { ReactNode } from "react";
import { CrmShell } from "@/components/eco-nav/CrmShell";

/** 接待台与 CRM 共用工作范围。公开访客页 /r 不走这里。 */
export default function ReceptionLayout({ children }: Readonly<{ children: ReactNode }>) {
  return <CrmShell>{children}</CrmShell>;
}
