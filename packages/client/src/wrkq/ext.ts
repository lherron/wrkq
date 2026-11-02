/** Writer-owned extensions, independently fenced from their subject. */
import type { WrkqTaskState } from "./task.js";

export type WrkqExtSubject =
  | { task: string; container?: never; room?: never }
  | { container: string; task?: never; room?: never }
  | { room: string; task?: never; container?: never };

export interface WrkqExt {
  subject: string;
  ns: string;
  kind: string;
  id: string;
  body: Record<string, unknown>;
  etag: number;
  createdAt: string;
  updatedAt: string;
  updatedByPrincipalRef: string;
  updatedByScopeRef?: string;
}
export interface WrkqExtSubjectRef {
  type: "task" | "container" | "room";
  id: string;
  state?: string;
}
export interface WrkqExtListItem extends Omit<WrkqExt, "subject"> {
  subject: WrkqExtSubjectRef;
}
export interface WrkqExtShowParams {
  subject: WrkqExtSubject;
  ns: string;
  kind: string;
  id: string;
}
export interface WrkqExtPutParams extends WrkqExtShowParams {
  body: Record<string, unknown>;
  /** Absent: upsert; zero: create only; positive: exact extension etag. */
  ifMatch?: number;
  principalRef?: string;
}
export interface WrkqExtPutResult { ext: WrkqExt; created: boolean }
export interface WrkqExtShowResult { ext: WrkqExt }
export interface WrkqExtListViewParams {
  subject?: WrkqExtSubject;
  ns?: string;
  kind?: string;
  subjectStates?: WrkqTaskState[];
  limit?: number;
  cursor?: string;
}
export interface WrkqExtListView {
  items: WrkqExtListItem[];
  nextCursor?: string;
}
export interface WrkqExtDeleteParams extends WrkqExtShowParams {
  ifMatch?: number;
}
export interface WrkqExtDeleteResult { deleted: boolean }
