/**
 * wrkf/role.ts — role bindings (wrkf.role.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.3 and §7.
 */

export interface WrkfRoleBinding {
  instanceId: string;
  role: string;
  principal_ref: string;
  deliveryRef?: string;
  lane?: string;
  bindingMode: string;
  boundAt: string;
}

export interface WrkfRoleListParams {
  task?: string;
  instanceId?: string;
}

export interface WrkfRoleBindParams {
  task?: string;
  instanceId?: string;
  role: string;
  principal_ref: string;
  deliveryRef?: string;
  lane?: string;
  bindingMode?: "required" | "optional" | "auto" | string;
}

export interface WrkfRoleUnbindParams {
  task?: string;
  instanceId?: string;
  role: string;
  principal_ref?: string;
}

export interface WrkfRoleSetParams {
  task?: string;
  instanceId?: string;
  roleMap: Record<string, string>;
}
