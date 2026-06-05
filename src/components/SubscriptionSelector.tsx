import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ChevronDown } from "lucide-react";

import type { Subscription } from "../types";
import { formatTime } from "../lib/format";

export function SubscriptionSelector({
  subscriptions,
  selectedSubscriptionId,
  open,
  onToggle,
  onClose,
  onSelect,
}: {
  subscriptions: Subscription[];
  selectedSubscriptionId: number | null;
  open: boolean;
  onToggle: () => void;
  onClose: () => void;
  onSelect: (id: number | null) => void;
}) {
  const triggerRef = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const closeTimerRef = useRef<number | null>(null);
  const [panelPosition, setPanelPosition] = useState<{ top: number; right: number } | null>(null);
  const [panelMounted, setPanelMounted] = useState(open);

  const clearCloseTimer = useCallback(() => {
    if (closeTimerRef.current == null) return;
    window.clearTimeout(closeTimerRef.current);
    closeTimerRef.current = null;
  }, []);

  const updatePanelPosition = useCallback(() => {
    const rect = triggerRef.current?.getBoundingClientRect();
    if (!rect) return;
    setPanelPosition({
      top: rect.bottom + 8,
      right: Math.max(16, window.innerWidth - rect.right),
    });
  }, []);

  const finishPanelClose = useCallback(() => {
    clearCloseTimer();
    closeTimerRef.current = window.setTimeout(() => {
      setPanelMounted(false);
      closeTimerRef.current = null;
    }, 180);
  }, [clearCloseTimer]);

  const handleToggle = useCallback(() => {
    if (open) {
      onToggle();
      finishPanelClose();
      return;
    }
    clearCloseTimer();
    setPanelMounted(true);
    onToggle();
  }, [clearCloseTimer, finishPanelClose, onToggle, open]);

  const handleClose = useCallback(() => {
    onClose();
    finishPanelClose();
  }, [finishPanelClose, onClose]);

  const handleSelect = useCallback((id: number | null) => {
    onSelect(id);
    finishPanelClose();
  }, [finishPanelClose, onSelect]);

  useEffect(() => clearCloseTimer, [clearCloseTimer]);

  useLayoutEffect(() => {
    if (!open) return;
    updatePanelPosition();
    window.addEventListener("resize", updatePanelPosition);
    window.addEventListener("scroll", updatePanelPosition, true);
    return () => {
      window.removeEventListener("resize", updatePanelPosition);
      window.removeEventListener("scroll", updatePanelPosition, true);
    };
  }, [open, updatePanelPosition]);

  useEffect(() => {
    if (!open) return;

    const handleMouseDown = (event: MouseEvent) => {
      const target = event.target as Node;
      if (triggerRef.current?.contains(target) || panelRef.current?.contains(target)) {
        return;
      }
      handleClose();
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") handleClose();
    };

    document.addEventListener("mousedown", handleMouseDown);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("mousedown", handleMouseDown);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [handleClose, open]);

  if (subscriptions.length === 0) {
    return <span className="text-xs text-white/20">暂无配置订阅</span>;
  }

  const selected = subscriptions.find((item) => item.id === selectedSubscriptionId) ?? null;
  const failedCount = subscriptions.filter((item) => item.status === "failed").length;
  const summary = selected ?? subscriptions[0];
  const statusFailed = selected ? selected.status === "failed" : failedCount > 0;

  return (
    <div ref={triggerRef} className="flex items-center gap-2 text-xs text-white/55 min-w-0">
      <span className={`w-1.5 h-1.5 rounded-full flex-shrink-0 ${statusFailed ? "bg-amber-400" : "bg-emerald-400"}`} />
      <span className="truncate min-w-0 flex-1 sm:flex-none sm:max-w-[140px]">{selected?.name ?? summary.name}</span>
      <span className="text-white/25 flex-shrink-0">·</span>
      <span className={`flex-shrink-0 ${statusFailed ? "text-amber-300" : "text-emerald-300"}`}>
        {selected ? (selected.status === "failed" ? "失败" : "正常") : failedCount ? `${failedCount} 个异常` : "正常"}
      </span>
      <button
        onClick={handleToggle}
        className="flex items-center justify-center w-8 h-8 -mr-2 sm:w-auto sm:h-auto sm:p-1 text-white/40 hover:text-white/70 transition-colors flex-shrink-0"
        aria-label={open ? "收起订阅栏" : "展开订阅栏"}
      >
        <ChevronDown className={`w-3.5 h-3.5 sm:w-3 sm:h-3 transition-transform ${open ? "rotate-180" : ""}`} />
      </button>
      {panelMounted && panelPosition && createPortal(
        <SubscriptionPanel
          panelRef={panelRef}
          position={panelPosition}
          open={open}
          subscriptions={subscriptions}
          selectedSubscriptionId={selectedSubscriptionId}
          onSelect={handleSelect}
        />,
        document.body,
      )}
    </div>
  );
}

function SubscriptionPanel({
  panelRef,
  position,
  open,
  subscriptions,
  selectedSubscriptionId,
  onSelect,
}: {
  panelRef: { current: HTMLDivElement | null };
  position: { top: number; right: number };
  open: boolean;
  subscriptions: Subscription[];
  selectedSubscriptionId: number | null;
  onSelect: (id: number | null) => void;
}) {
  const failedCount = subscriptions.filter((item) => item.status === "failed").length;
  return (
    <div
      ref={panelRef}
      className={`fixed z-[80] w-[240px] max-h-[320px] overflow-y-auto rounded-2xl border border-white/10 bg-[#202020]/95 backdrop-blur-xl shadow-[0_18px_48px_rgba(0,0,0,0.45)] ${
        open ? "animate-dropdown-in" : "animate-dropdown-out"
      }`}
      style={{ top: position.top, right: position.right }}
    >
      <div className="divide-y divide-white/[0.04]">
        <SubscriptionPanelItem
          title="全部订阅"
          subtitle="显示所有订阅节点"
          meta={`${subscriptions.length} 个订阅`}
          failed={failedCount > 0}
          active={selectedSubscriptionId == null}
          onClick={() => onSelect(null)}
        />
        {subscriptions.map((subscription) => (
          <SubscriptionPanelItem
            key={subscription.id}
            title={subscription.name}
            subtitle={subscription.url}
            meta={`刷新于 ${formatTime(subscription.last_refreshed_at)}`}
            failed={subscription.status === "failed"}
            active={selectedSubscriptionId === subscription.id}
            onClick={() => onSelect(subscription.id)}
          />
        ))}
      </div>
    </div>
  );
}

function SubscriptionPanelItem({
  title,
  subtitle,
  meta,
  failed,
  active,
  onClick,
}: {
  title: string;
  subtitle: string;
  meta: string;
  failed: boolean;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className={`w-full text-left px-4 py-3 transition-colors ${
        active
          ? "bg-white/[0.07]"
          : "hover:bg-white/[0.04]"
      }`}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-[11px] text-white/75 truncate font-medium">{title}</span>
        <span className={`text-[10px] px-1.5 py-0.5 rounded-full ${
          failed
            ? "bg-amber-400/10 text-amber-300"
            : "bg-emerald-400/10 text-emerald-300"
        }`}>
          {failed ? "异常" : "正常"}
        </span>
      </div>
      <p className="text-[10px] text-white/45 mt-1 truncate font-mono">{subtitle}</p>
      <p className="text-[10px] text-white/40 mt-0.5">{meta}</p>
    </button>
  );
}
