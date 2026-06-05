import { memo, useRef } from "react";
import { ChevronRight, RefreshCw } from "lucide-react";

import type { NodeRecord, Subscription } from "../types";
import { statusConfig } from "../constants";
import { formatTime } from "../lib/format";
import { LatencyBadge } from "./LatencyBadge";

export interface NodeRowProps {
  node: NodeRecord;
  subscription: Subscription | undefined;
  isTesting: boolean;
  testingAll: boolean;
  onOpenDetail: (id: number) => void;
  onTest: (id: number) => void;
}

export const NodeTableRow = memo(function NodeTableRow({
  node,
  subscription,
  isTesting,
  testingAll,
  onOpenDetail,
  onTest,
}: NodeRowProps) {
  const status = statusConfig[node.status];
  const StatusIcon = status.icon;
  return (
    <tr
      className="text-sm border-b border-white/[0.03] hover:bg-white/[0.03] transition-colors cursor-pointer"
      onClick={() => onOpenDetail(node.id)}
    >
      <td className="py-3.5 px-5">
        <div className="flex items-center gap-3">
          <div className={`w-8 h-8 rounded-lg ${status.bg} flex items-center justify-center border ${status.border} flex-shrink-0`}>
            <StatusIcon className={`w-4 h-4 ${status.color}`} />
          </div>
          <div className="min-w-0 overflow-hidden">
            <div className="text-white/90 font-medium text-sm truncate">{node.name}</div>
            <div className="text-[10px] text-white/30 truncate">{subscription?.name ?? `订阅 #${node.subscription_id}`}</div>
          </div>
        </div>
      </td>
      <td className="py-3.5 px-4">
        <span className="inline-flex items-center px-2.5 py-1 rounded-full text-[10px] tracking-wider text-sky-300 bg-sky-400/10 border border-sky-400/15">
          {node.protocol || "--"}
        </span>
      </td>
      <td className="py-3.5 px-4">
        <span className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-[10px] tracking-wider ${status.bg} ${status.color} border ${status.border}`}>
          <span className={`w-1.5 h-1.5 rounded-full ${status.dot}`} />
          {status.label}
        </span>
      </td>
      <td className="py-3.5 px-4">
        <LatencyBadge latency={node.latency_ms} />
      </td>
      <td className="py-3.5 px-4 text-white/30 text-xs">
        {formatTime(node.last_checked)}
      </td>
      <td className="py-3.5 px-5 text-right">
        <div className="flex items-center justify-end gap-1">
          <button
            onClick={(event) => {
              event.stopPropagation();
              onTest(node.id);
            }}
            disabled={testingAll || isTesting}
            className="text-[10px] tracking-wider text-white/30 hover:text-white/70 transition-colors flex items-center gap-1 disabled:opacity-20 py-1 px-2 rounded hover:bg-white/5"
          >
            <RefreshCw className={`w-3 h-3 ${isTesting ? "animate-spin-silk" : ""}`} />
            测速
          </button>
          <button
            onClick={(event) => {
              event.stopPropagation();
              onOpenDetail(node.id);
            }}
            className="text-[10px] tracking-wider text-white/30 hover:text-white/70 transition-colors flex items-center gap-1 py-1 px-2 rounded hover:bg-white/5"
          >
            详情
            <ChevronRight className="w-3 h-3" />
          </button>
        </div>
      </td>
    </tr>
  );
});

export const NodeMobileCard = memo(function NodeMobileCard({
  node,
  subscription,
  isTesting,
  testingAll,
  onOpenDetail,
  onTest,
}: NodeRowProps) {
  const status = statusConfig[node.status];
  const StatusIcon = status.icon;
  const touchRef = useRef<{ x: number; y: number; moved: boolean } | null>(null);
  return (
    <li
      role="button"
      tabIndex={0}
      aria-label={`查看 ${node.name} 详情`}
      onTouchStart={(event) => {
        const touch = event.touches[0];
        touchRef.current = { x: touch.clientX, y: touch.clientY, moved: false };
      }}
      onTouchMove={(event) => {
        const start = touchRef.current;
        if (!start) return;
        const touch = event.touches[0];
        if (Math.abs(touch.clientX - start.x) > 10 || Math.abs(touch.clientY - start.y) > 10) {
          start.moved = true;
        }
      }}
      onClick={() => {
        if (touchRef.current?.moved) {
          touchRef.current = null;
          return;
        }
        touchRef.current = null;
        onOpenDetail(node.id);
      }}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onOpenDetail(node.id);
        }
      }}
      className="px-4 py-3.5 flex items-center gap-3 cursor-pointer active:bg-white/[0.04] transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-sky-400/40 focus-visible:ring-inset"
    >
      <div className={`w-10 h-10 rounded-lg ${status.bg} flex items-center justify-center border ${status.border} flex-shrink-0`}>
        <StatusIcon className={`w-4 h-4 ${status.color}`} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <div className="text-white/90 font-medium text-sm truncate flex-1 min-w-0">{node.name}</div>
          <LatencyBadge latency={node.latency_ms} />
        </div>
        <div className="mt-1.5 flex items-center gap-2 text-[11px] text-white/40 overflow-hidden">
          <span className={`inline-flex items-center gap-1 ${status.color} flex-shrink-0`}>
            <span className={`w-1.5 h-1.5 rounded-full ${status.dot}`} />
            {status.label}
          </span>
          <span className="text-white/15">·</span>
          <span className="font-mono uppercase tracking-wider truncate">{node.protocol || "--"}</span>
          <span className="text-white/15">·</span>
          <span className="truncate">{formatTime(node.last_checked)}</span>
        </div>
        <div className="mt-1 text-[11px] text-white/50 truncate">
          {subscription?.name ?? `订阅 #${node.subscription_id}`}
        </div>
      </div>
      <button
        type="button"
        onClick={(event) => {
          event.stopPropagation();
          onTest(node.id);
        }}
        disabled={testingAll || isTesting}
        className="flex items-center justify-center w-11 h-11 rounded-lg text-white/40 hover:text-white/80 active:bg-white/5 disabled:opacity-20 flex-shrink-0"
        aria-label="测速"
      >
        <RefreshCw className={`w-4 h-4 ${isTesting ? "animate-spin-silk" : ""}`} />
      </button>
    </li>
  );
});
