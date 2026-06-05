import { useEffect, useState } from "react";
import { RefreshCw, X } from "lucide-react";

import type { CheckHistoryPoint, NodeRecord } from "../types";
import { statusConfig } from "../constants";
import { apiRequest } from "../lib/api";
import { normalizeNodeStatus } from "../lib/format";
import { LatencyBadge } from "./LatencyBadge";
import { LatencyHistoryChart } from "./LatencyHistoryChart";

export function NodeDetailModal({
  node,
  onClose,
  onTest,
  testing,
}: {
  node: NodeRecord;
  onClose: () => void;
  onTest: (id: number) => void;
  testing: boolean;
}) {
  const status = statusConfig[node.status];
  const StatusIcon = status.icon;
  const [historyState, setHistoryState] = useState<{
    nodeId: number;
    points: CheckHistoryPoint[];
    loading: boolean;
  }>({ nodeId: node.id, points: [], loading: true });
  const history = historyState.nodeId === node.id ? historyState.points : [];
  const historyLoading = historyState.nodeId === node.id ? historyState.loading : true;

  useEffect(() => {
    let cancelled = false;
    apiRequest<{ points: CheckHistoryPoint[] }>(`/api/nodes/${node.id}/history?window=1h`)
      .then((payload) => {
        if (!cancelled) {
          setHistoryState({
            nodeId: node.id,
            points: payload.points.map((point) => ({
              ...point,
              status: normalizeNodeStatus(point.status),
              latency_ms: point.latency_ms ?? null,
              status_message: point.status_message ?? null,
            })),
            loading: false,
          });
        }
      })
      .catch(() => {
        if (!cancelled) {
          setHistoryState({ nodeId: node.id, points: [], loading: false });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [node.id]);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handleKeyDown);
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      document.body.style.overflow = previousOverflow;
    };
  }, [onClose]);

  const handleBackdropClick = (event: React.MouseEvent<HTMLDivElement>) => {
    if (event.target === event.currentTarget) onClose();
  };

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={node.name}
      onClick={handleBackdropClick}
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm p-4"
    >
      <div className="liquid-glass rounded-3xl w-full max-w-2xl max-h-[85vh] overflow-y-auto">
        <div className="p-5 sm:p-6 border-b border-white/5 flex items-start justify-between gap-3">
          <div className="flex items-start gap-3 sm:gap-4 min-w-0">
            <div className={`w-12 h-12 rounded-2xl ${status.bg} flex items-center justify-center border ${status.border} flex-shrink-0`}>
              <StatusIcon className={`w-6 h-6 ${status.color}`} />
            </div>
            <div className="min-w-0">
              <h2 className="text-lg sm:text-xl font-medium text-white/90 leading-snug break-words line-clamp-2">{node.name}</h2>
              <p className="text-xs text-white/55 font-mono mt-1 break-all">{node.server}:{node.port}</p>
            </div>
          </div>
          <div className="flex items-center gap-1 sm:gap-2 flex-shrink-0">
            <button
              onClick={() => onTest(node.id)}
              disabled={testing}
              aria-label="测速"
              className="text-xs tracking-wider text-white/40 hover:text-white/70 transition-colors flex items-center justify-center gap-1.5 w-11 h-11 sm:w-auto sm:h-auto sm:px-3 sm:py-1.5 rounded-lg hover:bg-white/5 disabled:opacity-20"
            >
              <RefreshCw className={`w-4 h-4 sm:w-3.5 sm:h-3.5 ${testing ? "animate-spin-silk" : ""}`} />
              <span className="hidden sm:inline">测速</span>
            </button>
            <button
              onClick={onClose}
              aria-label="关闭"
              className="text-white/30 hover:text-white/60 flex items-center justify-center w-11 h-11 sm:w-auto sm:h-auto sm:p-1 rounded-lg hover:bg-white/5"
            >
              <X className="w-5 h-5" />
            </button>
          </div>
        </div>

        <div className="p-5 sm:p-6 space-y-6">
          <div className="grid grid-cols-3 gap-2 sm:gap-4">
            <div className="liquid-glass rounded-xl p-3 sm:p-4 text-center min-w-0">
              <div className="text-[10px] text-white/30 tracking-wider uppercase mb-1">状态</div>
              <div className={`text-base sm:text-lg font-medium truncate ${status.color}`}>{status.label}</div>
            </div>
            <div className="liquid-glass rounded-xl p-3 sm:p-4 text-center min-w-0">
              <div className="text-[10px] text-white/30 tracking-wider uppercase mb-1">延迟</div>
              <LatencyBadge latency={node.latency_ms} size="lg" />
            </div>
            <div className="liquid-glass rounded-xl p-3 sm:p-4 text-center min-w-0">
              <div className="text-[10px] text-white/30 tracking-wider uppercase mb-1">协议</div>
              <div className="text-sm sm:text-lg font-mono text-sky-400 truncate">{node.protocol || "--"}</div>
            </div>
          </div>

          {node.status_message && (
            <div className="rounded-xl border border-white/[0.06] bg-white/[0.025] px-3.5 py-3 text-xs text-white/35 break-words">
              {node.status_message}
            </div>
          )}

          <div className="liquid-glass rounded-xl p-4">
            <div className="flex items-center justify-between mb-3">
              <span className="text-[10px] text-white/40 tracking-wider uppercase">近 1 小时延迟波形</span>
              <span className="text-[10px] text-white/45 tabular-nums">{history.length} 点</span>
            </div>
            {historyLoading ? (
              <div className="h-[180px] flex items-center justify-center text-xs text-white/45">正在加载波形...</div>
            ) : history.length > 0 ? (
              <LatencyHistoryChart points={history} />
            ) : (
              <div className="h-[180px] flex items-center justify-center text-xs text-white/45">暂无近 1 小时检测记录</div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
