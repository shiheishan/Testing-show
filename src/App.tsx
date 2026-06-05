import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Activity, Clock, Gauge, RefreshCw, Server } from "lucide-react";

import type { CheckResponse, NodeRecord, NodeStatus, Stats, Subscription } from "./types";
import { filterLabels, sortLabels } from "./constants";
import { apiRequest, isAbortError } from "./lib/api";
import { checkMessage, formatTime, latencySortValue, normalizeNodeRecord } from "./lib/format";
import { useMediaQuery } from "./lib/useMediaQuery";
import { SlidingTabs } from "./components/SlidingTabs";
import { TestAllButton } from "./components/TestAllButton";
import { SearchBox } from "./components/SearchBox";
import { StatCard } from "./components/StatCard";
import { NodeMobileCard, NodeTableRow } from "./components/NodeRow";
import { SubscriptionSelector } from "./components/SubscriptionSelector";
import { NodeDetailModal } from "./components/NodeDetailModal";

export default function VPSMonitorPage() {
  const [subscriptions, setSubscriptions] = useState<Subscription[]>([]);
  const [nodes, setNodes] = useState<NodeRecord[]>([]);
  const [stats, setStats] = useState<Stats>({ total: 0, online: 0, offline: 0, timeout: 0, avg_latency_ms: null, engine_available: true });
  const [loading, setLoading] = useState(true);
  const [statusMessage, setStatusMessage] = useState("正在同步状态板...");
  const [testingAll, setTestingAll] = useState(false);
  const [testingNodeIds, setTestingNodeIds] = useState<ReadonlySet<number>>(() => new Set());
  const [detailNodeId, setDetailNodeId] = useState<number | null>(null);
  const [sortBy, setSortBy] = useState<"default" | "latency" | "name">("default");
  const [filterStatus, setFilterStatus] = useState<"all" | NodeStatus>("all");
  const [searchQuery, setSearchQuery] = useState("");
  const [selectedSubscriptionId, setSelectedSubscriptionId] = useState<number | null>(null);
  const [subscriptionsOpen, setSubscriptionsOpen] = useState(false);

  const subscriptionById = useMemo(() => {
    return new Map(subscriptions.map((item) => [item.id, item]));
  }, [subscriptions]);

  const loadAbortRef = useRef<AbortController | null>(null);
  const isDesktop = useMediaQuery("(min-width: 640px)");

  const loadData = useCallback(async (silent = false) => {
    loadAbortRef.current?.abort();
    const controller = new AbortController();
    loadAbortRef.current = controller;
    const { signal } = controller;
    if (!silent) setLoading(true);
    try {
      const params = new URLSearchParams();
      if (selectedSubscriptionId != null) {
        params.set("sub_id", String(selectedSubscriptionId));
      }
      const query = params.toString() ? `?${params.toString()}` : "";
      const [subscriptionPayload, nodePayload, statsPayload] = await Promise.all([
        apiRequest<{ subscriptions: Subscription[] }>("/api/subscriptions", { signal }),
        apiRequest<{ nodes: NodeRecord[] }>(`/api/nodes${query}`, { signal }),
        apiRequest<Stats>(`/api/nodes/stats${query}`, { signal }),
      ]);
      if (signal.aborted) return;
      setSubscriptions(subscriptionPayload.subscriptions);
      if (
        selectedSubscriptionId != null &&
        !subscriptionPayload.subscriptions.some((subscription) => subscription.id === selectedSubscriptionId)
      ) {
        setSelectedSubscriptionId(null);
      }
      setNodes(nodePayload.nodes.map(normalizeNodeRecord));
      setStats(statsPayload);
      setStatusMessage(subscriptionPayload.subscriptions.length ? "状态板已同步" : "暂无配置订阅");
    } catch (error) {
      if (isAbortError(error) || signal.aborted) return;
      setStatusMessage(error instanceof Error ? error.message : "同步失败");
    } finally {
      if (loadAbortRef.current === controller) {
        loadAbortRef.current = null;
      }
      if (!silent && !signal.aborted) setLoading(false);
    }
  }, [selectedSubscriptionId]);

  useEffect(() => {
    const initialLoad = window.setTimeout(() => {
      void loadData();
    }, 0);
    const id = window.setInterval(() => loadData(true), 5000);
    return () => {
      window.clearTimeout(initialLoad);
      window.clearInterval(id);
      loadAbortRef.current?.abort();
      loadAbortRef.current = null;
    };
  }, [loadData]);

  const detailNode = useMemo(
    () => (detailNodeId == null ? null : nodes.find((node) => node.id === detailNodeId) ?? null),
    [nodes, detailNodeId],
  );

  const filteredNodes = useMemo(() => {
    const q = searchQuery.trim().toLowerCase();
    const filtered = nodes.filter((node) => {
      if (filterStatus !== "all" && node.status !== filterStatus) return false;
      if (!q) return true;
      const subscription = subscriptionById.get(node.subscription_id);
      const haystack = `${node.name} ${node.protocol} ${node.server} ${subscription?.name ?? ""}`.toLowerCase();
      return haystack.includes(q);
    });

    return [...filtered].sort((left, right) => {
      if (sortBy === "default") {
        const subscriptionDiff = left.subscription_id - right.subscription_id;
        if (subscriptionDiff !== 0) return subscriptionDiff;
        const orderDiff = left.display_order - right.display_order;
        if (orderDiff !== 0) return orderDiff;
        return left.id - right.id;
      }
      if (sortBy === "latency") {
        const diff = latencySortValue(left.latency_ms) - latencySortValue(right.latency_ms);
        if (diff !== 0) return diff;
        return left.name.localeCompare(right.name, "zh-CN");
      }
      if (sortBy === "name") {
        return left.name.localeCompare(right.name, "zh-CN");
      }
      return 0;
    });
  }, [filterStatus, nodes, searchQuery, sortBy, subscriptionById]);

  const lastChecked = useMemo(() => {
    return nodes.map((node) => node.last_checked).filter(Boolean).sort().at(-1) ?? null;
  }, [nodes]);

  const failedSubscriptions = subscriptions.filter((item) => item.status === "failed").length;

  const handleTestAll = useCallback(async () => {
    setTestingAll(true);
    try {
      const payload = await apiRequest<CheckResponse>("/api/nodes/check", {
        method: "POST",
        body: JSON.stringify(selectedSubscriptionId == null ? {} : { sub_id: selectedSubscriptionId }),
      });
      setStatusMessage(checkMessage(payload.status, payload.total_nodes));
      await new Promise((resolve) => window.setTimeout(resolve, 1800));
      await loadData(true);
    } catch (error) {
      setStatusMessage(error instanceof Error ? error.message : "测速失败");
    } finally {
      setTestingAll(false);
    }
  }, [loadData, selectedSubscriptionId]);

  const handleTestNode = useCallback(async (id: number) => {
    setTestingNodeIds((previous) => {
      if (previous.has(id)) return previous;
      const next = new Set(previous);
      next.add(id);
      return next;
    });
    try {
      const payload = await apiRequest<CheckResponse>(`/api/nodes/${id}/check`, { method: "POST" });
      setStatusMessage(checkMessage(payload.status, payload.total_nodes));
      await new Promise((resolve) => window.setTimeout(resolve, 1200));
      await loadData(true);
    } catch (error) {
      setStatusMessage(error instanceof Error ? error.message : "测速失败");
    } finally {
      setTestingNodeIds((previous) => {
        if (!previous.has(id)) return previous;
        const next = new Set(previous);
        next.delete(id);
        return next;
      });
    }
  }, [loadData]);

  return (
    <div className="min-h-screen bg-[#181818] text-white">
      <nav className="border-b border-white/5 bg-[#181818]/90 backdrop-blur-md sticky top-0 z-40">
        <div className="max-w-7xl mx-auto px-6 h-14 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Activity className="w-5 h-5 text-sky-400" />
            <span className="text-sm font-medium tracking-wider text-white/90">VPS 节点监控</span>
          </div>
          <div className="flex items-center gap-4">
            <span className="text-[11px] text-white/55 flex items-center gap-1.5">
              <span className={`w-1.5 h-1.5 rounded-full ${failedSubscriptions ? "bg-amber-400" : "bg-emerald-400 animate-pulse"}`} />
              {statusMessage}
            </span>
          </div>
        </div>
      </nav>

      <div className="max-w-7xl mx-auto px-6 py-8">
        <div className="mb-8">
          <h1 className="text-3xl md:text-5xl font-light text-white/90 mb-3" style={{ fontFamily: '"Noto Serif SC", serif' }}>
            节点监控
          </h1>
          <p className="text-sm text-white/40">
            从配置订阅同步节点，由部署机器本地检测连通性与延迟 · 当前范围 {stats.total} 个节点
          </p>
        </div>

        {!stats.engine_available && (
          <div className="liquid-glass rounded-2xl border border-red-400/20 bg-red-500/[0.08] px-5 py-4 mb-8 flex items-start gap-3.5">
            <RefreshCw className="w-5 h-5 text-red-300/90 shrink-0 mt-0.5 animate-spin-silk" />
            <div className="min-w-0">
              <div className="text-sm font-medium text-red-200/90">测速引擎不可用</div>
              <div className="text-xs text-red-200/55 mt-1 leading-relaxed">
                mihomo 未安装或未能启动，节点延迟暂时无法检测；引擎恢复后状态会自动刷新。
              </div>
            </div>
          </div>
        )}

        <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-8">
          <StatCard icon={Server} label="总节点" value={stats.total} color="text-white/80" />
          <StatCard icon={Activity} label="在线" value={stats.online} color="text-emerald-400" trend={`${stats.offline + stats.timeout} 异常`} />
          <StatCard icon={Gauge} label="平均延迟" value={stats.avg_latency_ms ?? 0} suffix="ms" color="text-sky-400" trend={stats.avg_latency_ms == null ? "暂无检测结果" : "按最近检测结果"} />
          <StatCard icon={Clock} label="最后检测" value={formatTime(lastChecked)} color="text-amber-400" />
        </div>

        <div className="grid gap-3 mb-6 lg:grid-cols-[auto_1fr] lg:items-center">
          <div className="flex items-center gap-3">
            <TestAllButton testing={testingAll} onClick={handleTestAll} />
            <div className="min-w-0 flex-1 sm:flex-none">
              <SearchBox value={searchQuery} onChange={setSearchQuery} />
            </div>
          </div>
          <div className="flex flex-col gap-3 sm:flex-row sm:items-center lg:justify-end">
            <SlidingTabs
              options={["all", "online", "offline", "timeout", "unknown"] as const}
              value={filterStatus}
              onChange={setFilterStatus}
              labels={filterLabels}
            />
            <SlidingTabs
              options={["default", "latency", "name"] as const}
              value={sortBy}
              onChange={setSortBy}
              labels={sortLabels}
            />
          </div>
        </div>

        <div className="liquid-glass rounded-2xl">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between sm:gap-3 px-5 py-3 border-b border-white/5">
            <span className="text-sm text-white/60">
              节点列表{filteredNodes.length > 0 && <span className="text-white/40 ml-1">（{filteredNodes.length}）</span>}
            </span>
            <SubscriptionSelector
              subscriptions={subscriptions}
              selectedSubscriptionId={selectedSubscriptionId}
              open={subscriptionsOpen}
              onToggle={() => setSubscriptionsOpen((value) => !value)}
              onClose={() => setSubscriptionsOpen(false)}
              onSelect={(id) => {
                setSelectedSubscriptionId(id);
                setSubscriptionsOpen(false);
              }}
            />
          </div>
          {isDesktop ? (
            <div className="max-h-[58vh] overflow-auto rounded-b-2xl">
              <table className="w-full min-w-[720px] table-fixed">
                <thead>
                  <tr className="text-left text-[10px] text-white/30 uppercase tracking-wider">
                    <th className="sticky top-0 z-10 py-3 px-5 border-y border-l border-white/10 rounded-tl-xl font-medium w-[28%] bg-[#181818]/95 backdrop-blur-md">名称</th>
                    <th className="sticky top-0 z-10 py-3 px-4 border-y border-white/10 font-medium w-[90px] bg-[#181818]/95 backdrop-blur-md">协议</th>
                    <th className="sticky top-0 z-10 py-3 px-4 border-y border-white/10 font-medium w-[100px] bg-[#181818]/95 backdrop-blur-md">状态</th>
                    <th className="sticky top-0 z-10 py-3 px-4 border-y border-white/10 font-medium w-[80px] bg-[#181818]/95 backdrop-blur-md">延迟</th>
                    <th className="sticky top-0 z-10 py-3 px-4 border-y border-white/10 font-medium w-[120px] bg-[#181818]/95 backdrop-blur-md">最后检测</th>
                    <th className="sticky top-0 z-10 py-3 px-5 border-y border-r border-white/10 rounded-tr-xl font-medium text-right w-[120px] bg-[#181818]/95 backdrop-blur-md">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredNodes.map((node) => (
                    <NodeTableRow
                      key={node.id}
                      node={node}
                      subscription={subscriptionById.get(node.subscription_id)}
                      isTesting={testingNodeIds.has(node.id)}
                      testingAll={testingAll}
                      onOpenDetail={setDetailNodeId}
                      onTest={handleTestNode}
                    />
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <ul className="divide-y divide-white/[0.04]">
              {filteredNodes.map((node) => (
                <NodeMobileCard
                  key={node.id}
                  node={node}
                  subscription={subscriptionById.get(node.subscription_id)}
                  isTesting={testingNodeIds.has(node.id)}
                  testingAll={testingAll}
                  onOpenDetail={setDetailNodeId}
                  onTest={handleTestNode}
                />
              ))}
            </ul>
          )}
          {filteredNodes.length === 0 && (
            <div className="text-center py-16">
              <Server className="w-10 h-10 text-white/10 mx-auto mb-3" />
              <p className="text-sm text-white/45">
                {loading ? "正在加载节点..." : searchQuery ? "未找到匹配的节点" : "暂无符合条件的节点"}
              </p>
            </div>
          )}
        </div>
      </div>

      {detailNode && (
        <NodeDetailModal
          node={detailNode}
          onClose={() => setDetailNodeId(null)}
          onTest={handleTestNode}
          testing={testingNodeIds.has(detailNode.id)}
        />
      )}
    </div>
  );
}
