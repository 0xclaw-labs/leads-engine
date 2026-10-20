import type { ReactNode } from "react";
import { CrmShell } from "@/components/eco-nav/CrmShell";

/** 认证后的 CRM 壳。公开表单 /f 不经过这里，因此不挂 EcoTopNav。 */
export default function ShellLayout({ children }: Readonly<{ children: ReactNode }>) {
  return <CrmShell>{children}</CrmShell>;
}
