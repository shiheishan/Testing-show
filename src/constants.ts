import { Activity, AlertTriangle, Timer, WifiOff } from "lucide-react";

import type { NodeStatus } from "./types";

export const statusConfig: Record<NodeStatus, {
  icon: React.ElementType;
  color: string;
  bg: string;
  border: string;
  label: string;
  dot: string;
}> = {
  online: {
    icon: Activity,
    color: "text-emerald-400",
    bg: "bg-emerald-400/10",
    border: "border-emerald-400/20",
    label: "在线",
    dot: "bg-emerald-400",
  },
  offline: {
    icon: WifiOff,
    color: "text-red-400",
    bg: "bg-red-400/10",
    border: "border-red-400/20",
    label: "离线",
    dot: "bg-red-400",
  },
  timeout: {
    icon: Timer,
    color: "text-amber-400",
    bg: "bg-amber-400/10",
    border: "border-amber-400/20",
    label: "超时",
    dot: "bg-amber-400",
  },
  unknown: {
    icon: AlertTriangle,
    color: "text-white/35",
    bg: "bg-white/[0.04]",
    border: "border-white/10",
    label: "未知",
    dot: "bg-white/25",
  },
};

export const sortLabels = {
  default: "默认",
  latency: "延迟",
  name: "名称",
} as const;

export const filterLabels = {
  all: "全部",
  online: "在线",
  offline: "离线",
  timeout: "超时",
  unknown: "未知",
} as const;

export const historyStatusConfig: Record<NodeStatus, { color: string; label: string }> = {
  online: { color: "#38bdf8", label: "在线" },
  offline: { color: "#f87171", label: "离线" },
  timeout: { color: "#fbbf24", label: "超时" },
  unknown: { color: "rgba(255,255,255,0.32)", label: "未知" },
};
