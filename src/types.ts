export type NodeStatus = "online" | "offline" | "timeout" | "unknown";
export type SubscriptionStatus = "ok" | "failed";

export interface Subscription {
  id: number;
  name: string;
  url: string;
  created_at: string;
  last_refreshed_at: string | null;
  last_error: string | null;
  status: SubscriptionStatus;
}

export interface NodeRecord {
  id: number;
  subscription_id: number;
  display_order: number;
  name: string;
  server: string;
  port: number;
  protocol: string;
  status: NodeStatus;
  latency_ms: number | null;
  status_message: string | null;
  last_checked: string | null;
  stale_since: string | null;
}

export interface Stats {
  total: number;
  online: number;
  offline: number;
  timeout: number;
  avg_latency_ms: number | null;
  engine_available: boolean;
}

export interface CheckResponse {
  status: "started" | "running" | "cached" | "empty";
  total_nodes: number;
}

export interface CheckHistoryPoint {
  status: NodeStatus;
  latency_ms: number | null;
  status_message: string | null;
  checked_at: string;
}
