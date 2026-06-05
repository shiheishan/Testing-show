import type { CheckResponse, NodeRecord, NodeStatus } from "../types";

export function normalizeNodeStatus(value: string | null | undefined): NodeStatus {
  if (value === "online" || value === "offline" || value === "timeout") {
    return value;
  }
  return "unknown";
}

export function normalizeNodeRecord(node: NodeRecord): NodeRecord {
  return {
    ...node,
    display_order: node.display_order ?? node.id,
    status: normalizeNodeStatus(node.status),
    status_message: node.status_message ?? null,
  };
}

export function latencySortValue(latency: number | null): number {
  return latency == null ? Number.POSITIVE_INFINITY : latency;
}

export function formatTime(value: string | null): string {
  if (!value) return "--";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const diff = Date.now() - date.getTime();
  if (diff < 60000) return "刚刚";
  if (diff < 3600000) return `${Math.floor(diff / 60000)}分钟前`;
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}小时前`;
  return date.toLocaleString("zh-CN", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

export function checkMessage(status: CheckResponse["status"], total: number): string {
  switch (status) {
    case "started":
      return `检测任务已启动，共 ${total} 个节点`;
    case "running":
      return "已有检测任务在运行，稍后自动同步结果";
    case "cached":
      return "刚刚检测过，已复用最近结果";
    case "empty":
      return "当前没有可检测节点";
  }
}
