import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, memo } from "react";
import { createPortal } from "react-dom";
import {
  Activity,
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  Clock,
  Gauge,
  RefreshCw,
  Search,
  Server,
  Timer,
  WifiOff,
  X,
} from "lucide-react";

type NodeStatus = "online" | "offline" | "timeout" | "unknown";
type SubscriptionStatus = "ok" | "failed";

interface Subscription {
  id: number;
  name: string;
  url: string;
  created_at: string;
  last_refreshed_at: string | null;
  last_error: string | null;
  status: SubscriptionStatus;
}

interface NodeRecord {
  id: number;
  subscription_id: number;
  name: string;
  server: string;
  port: number;
  protocol: string;
  status: NodeStatus;
  latency_ms: number | null;
  last_checked: string | null;
  stale_since: string | null;
}

interface Stats {
  total: number;
  online: number;
  offline: number;
  timeout: number;
  avg_latency_ms: number | null;
}

interface CheckResponse {
  status: "started" | "running" | "cached" | "empty";
  total_nodes: number;
}

interface CheckHistoryPoint {
  status: NodeStatus;
  latency_ms: number | null;
  checked_at: string;
}

const statusConfig: Record<NodeStatus, {
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

const sortLabels = {
  latency: "延迟",
  name: "名称",
  status: "状态",
} as const;

const filterLabels = {
  all: "全部",
  online: "在线",
  offline: "离线",
  timeout: "超时",
  unknown: "未知",
} as const;

async function apiRequest<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...options,
  });
  const payload = await response.json();
  if (!response.ok) {
    throw new Error(payload.message || "请求失败");
  }
  return payload as T;
}

function normalizeNodeStatus(value: string | null | undefined): NodeStatus {
  if (value === "online" || value === "offline" || value === "timeout") {
    return value;
  }
  return "unknown";
}

function latencySortValue(latency: number | null): number {
  return latency == null ? Number.POSITIVE_INFINITY : latency;
}

function formatTime(value: string | null): string {
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

function checkMessage(status: CheckResponse["status"], total: number): string {
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

function LatencyBadge({ latency, size = "sm" }: { latency: number | null; size?: "sm" | "lg" }) {
  if (latency === null) {
    return <span className={`${size === "lg" ? "text-base" : "text-xs"} text-white/20 font-mono`}>--</span>;
  }
  const color =
    latency < 50 ? "text-emerald-400"
    : latency < 100 ? "text-sky-400"
    : latency < 200 ? "text-amber-400"
    : "text-red-400";

  return (
    <span className={`${size === "lg" ? "text-lg" : "text-sm"} font-mono ${color} tabular-nums font-medium`}>
      {latency}ms
    </span>
  );
}

function AnimatedNumber({ value, suffix = "", className = "" }: { value: number; suffix?: string; className?: string }) {
  const [display, setDisplay] = useState(value);
  const displayRef = useRef(value);
  const frameRef = useRef<number>(0);
  const hasMounted = useRef(false);

  useEffect(() => {
    if (!hasMounted.current) {
      hasMounted.current = true;
      displayRef.current = value;
      return;
    }

    const start = displayRef.current;
    const end = value;
    if (start === end) return;

    const duration = 600;
    const startTime = performance.now();

    const tick = (now: number) => {
      const elapsed = now - startTime;
      const progress = Math.min(elapsed / duration, 1);
      const eased = 1 - Math.pow(1 - progress, 4);
      const current = Math.round(start + (end - start) * eased);
      displayRef.current = current;
      setDisplay(current);
      if (progress < 1) {
        frameRef.current = requestAnimationFrame(tick);
      }
    };

    frameRef.current = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frameRef.current);
  }, [value]);

  return <span className={className}>{display}{suffix}</span>;
}

function SlidingTabs<T extends string>({
  options,
  value,
  onChange,
  labels,
}: {
  options: readonly T[];
  value: T;
  onChange: (v: T) => void;
  labels: Record<T, string>;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [indicator, setIndicator] = useState({ left: 0, width: 0, opacity: 0 });
  const activeIndex = options.indexOf(value);

  useLayoutEffect(() => {
    const update = () => {
      const container = containerRef.current;
      if (!container) return;
      const buttons = container.querySelectorAll<HTMLButtonElement>("button");
      const activeBtn = buttons[activeIndex];
      if (!activeBtn) return;
      const cRect = container.getBoundingClientRect();
      const bRect = activeBtn.getBoundingClientRect();
      setIndicator({
        left: bRect.left - cRect.left,
        width: bRect.width,
        opacity: 1,
      });
    };
    update();
    const id = requestAnimationFrame(update);
    window.addEventListener("resize", update);
    return () => {
      cancelAnimationFrame(id);
      window.removeEventListener("resize", update);
    };
  }, [activeIndex]);

  return (
    <div ref={containerRef} className="relative flex items-center gap-1 liquid-glass rounded-xl px-2 py-1.5">
      <div
        className="absolute top-1 bottom-1 rounded-lg bg-white/10 transition-all duration-300 ease-[cubic-bezier(0.4,0,0.2,1)]"
        style={{ left: indicator.left, width: indicator.width, opacity: indicator.opacity }}
      />
      {options.map((opt) => (
        <button
          key={opt}
          onClick={() => onChange(opt)}
          className={`relative z-10 px-3 py-1 rounded-lg text-[10px] tracking-wider transition-colors duration-200 whitespace-nowrap ${
            value === opt ? "text-white/90" : "text-white/30 hover:text-white/60"
          }`}
        >
          {labels[opt]}
        </button>
      ))}
    </div>
  );
}

const TestAllButton = memo(function TestAllButton({
  testing,
  onClick,
}: {
  testing: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      disabled={testing}
      className="liquid-glass rounded-xl px-5 py-2.5 text-xs tracking-wider text-white/60 hover:text-white/90 transition-colors flex items-center gap-2 disabled:opacity-40"
    >
      <RefreshCw className={`w-3.5 h-3.5 ${testing ? "animate-spin-silk" : ""}`} />
      {testing ? "检测中..." : "全部测速"}
    </button>
  );
});

const SearchBox = memo(function SearchBox({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  const [focused, setFocused] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  const handleBlur = () => {
    if (!value.trim()) setFocused(false);
  };

  const handleClick = () => {
    if (!focused) setFocused(true);
    requestAnimationFrame(() => inputRef.current?.focus());
  };

  const handleClear = (event: React.MouseEvent) => {
    event.preventDefault();
    onChange("");
    inputRef.current?.focus();
  };

  return (
    <div
      className={`liquid-glass rounded-xl flex items-center gap-2 transition-all duration-300 ease-[cubic-bezier(0.4,0,0.2,1)] overflow-hidden ${
        focused ? "px-4 py-2 w-[220px]" : "px-2 py-2 w-9 cursor-pointer"
      }`}
      onClick={handleClick}
    >
      <Search className="w-3.5 h-3.5 text-white/30 flex-shrink-0" />
      {focused && (
        <>
          <input
            ref={inputRef}
            type="text"
            value={value}
            onChange={(event) => onChange(event.target.value)}
            placeholder="搜索节点..."
            className="bg-transparent text-xs text-white/70 placeholder:text-white/20 focus:outline-none w-full"
            onBlur={handleBlur}
          />
          {value && (
            <button onMouseDown={handleClear} className="text-white/30 hover:text-white/60 flex-shrink-0">
              <X className="w-3 h-3" />
            </button>
          )}
        </>
      )}
    </div>
  );
});

export default function VPSMonitorPage() {
  const [subscriptions, setSubscriptions] = useState<Subscription[]>([]);
  const [nodes, setNodes] = useState<NodeRecord[]>([]);
  const [stats, setStats] = useState<Stats>({ total: 0, online: 0, offline: 0, timeout: 0, avg_latency_ms: null });
  const [loading, setLoading] = useState(true);
  const [statusMessage, setStatusMessage] = useState("正在同步状态板...");
  const [testingAll, setTestingAll] = useState(false);
  const [testingNodeId, setTestingNodeId] = useState<number | null>(null);
  const [detailNode, setDetailNode] = useState<NodeRecord | null>(null);
  const [sortBy, setSortBy] = useState<"latency" | "name" | "status">("latency");
  const [filterStatus, setFilterStatus] = useState<"all" | NodeStatus>("all");
  const [searchQuery, setSearchQuery] = useState("");
  const [selectedSubscriptionId, setSelectedSubscriptionId] = useState<number | null>(null);
  const [subscriptionsOpen, setSubscriptionsOpen] = useState(false);

  const subscriptionById = useMemo(() => {
    return new Map(subscriptions.map((item) => [item.id, item]));
  }, [subscriptions]);

  const loadData = useCallback(async (silent = false) => {
    if (!silent) setLoading(true);
    try {
      const params = new URLSearchParams();
      if (selectedSubscriptionId != null) {
        params.set("sub_id", String(selectedSubscriptionId));
      }
      const query = params.toString() ? `?${params.toString()}` : "";
      const [subscriptionPayload, nodePayload, statsPayload] = await Promise.all([
        apiRequest<{ subscriptions: Subscription[] }>("/api/subscriptions"),
        apiRequest<{ nodes: NodeRecord[] }>(`/api/nodes${query}`),
        apiRequest<Stats>(`/api/nodes/stats${query}`),
      ]);
      setSubscriptions(subscriptionPayload.subscriptions);
      if (
        selectedSubscriptionId != null &&
        !subscriptionPayload.subscriptions.some((subscription) => subscription.id === selectedSubscriptionId)
      ) {
        setSelectedSubscriptionId(null);
      }
      setNodes(nodePayload.nodes.map((node) => ({ ...node, status: normalizeNodeStatus(node.status) })));
      setStats(statsPayload);
      setStatusMessage(subscriptionPayload.subscriptions.length ? "状态板已同步" : "暂无配置订阅");
    } catch (error) {
      setStatusMessage(error instanceof Error ? error.message : "同步失败");
    } finally {
      if (!silent) setLoading(false);
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
    };
  }, [loadData]);

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
      if (sortBy === "latency") {
        const diff = latencySortValue(left.latency_ms) - latencySortValue(right.latency_ms);
        if (diff !== 0) return diff;
        return left.name.localeCompare(right.name, "zh-CN");
      }
      if (sortBy === "name") {
        return left.name.localeCompare(right.name, "zh-CN");
      }
      const order: Record<NodeStatus, number> = { online: 0, timeout: 1, offline: 2, unknown: 3 };
      const diff = order[left.status] - order[right.status];
      if (diff !== 0) return diff;
      return latencySortValue(left.latency_ms) - latencySortValue(right.latency_ms);
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
    setTestingNodeId(id);
    try {
      const payload = await apiRequest<CheckResponse>(`/api/nodes/${id}/check`, { method: "POST" });
      setStatusMessage(checkMessage(payload.status, payload.total_nodes));
      await new Promise((resolve) => window.setTimeout(resolve, 1200));
      await loadData(true);
    } catch (error) {
      setStatusMessage(error instanceof Error ? error.message : "测速失败");
    } finally {
      setTestingNodeId(null);
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
            <span className="text-[10px] text-white/30 flex items-center gap-1.5">
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

        <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-8">
          <StatCard icon={Server} label="总节点" value={stats.total} color="text-white/80" />
          <StatCard icon={Activity} label="在线" value={stats.online} color="text-emerald-400" trend={`${stats.offline + stats.timeout} 异常`} />
          <StatCard icon={Gauge} label="平均延迟" value={stats.avg_latency_ms ?? 0} suffix="ms" color="text-sky-400" trend={stats.avg_latency_ms == null ? "暂无检测结果" : "按最近检测结果"} />
          <StatCard icon={Clock} label="最后检测" value={formatTime(lastChecked)} color="text-amber-400" />
        </div>

        <div className="flex flex-wrap items-center gap-3 mb-6">
          <TestAllButton testing={testingAll} onClick={handleTestAll} />
          <SearchBox value={searchQuery} onChange={setSearchQuery} />
          <div className="flex-1" />
          <SlidingTabs
            options={["all", "online", "offline", "timeout", "unknown"] as const}
            value={filterStatus}
            onChange={setFilterStatus}
            labels={filterLabels}
          />
          <SlidingTabs
            options={["latency", "name", "status"] as const}
            value={sortBy}
            onChange={setSortBy}
            labels={sortLabels}
          />
        </div>

        <div className="liquid-glass rounded-2xl">
          <div className="flex items-center justify-between px-5 py-3 border-b border-white/5">
            <span className="text-sm text-white/60">
              节点列表{filteredNodes.length > 0 && <span className="text-white/30 ml-1">（{filteredNodes.length}）</span>}
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
          <div className="max-h-[58vh] overflow-auto rounded-b-2xl">
            <table className="w-full min-w-[720px] table-fixed">
              <thead>
                <tr className="text-left text-[10px] text-white/30 uppercase tracking-wider">
                  <th className="sticky top-0 z-10 py-3 px-5 border-b border-white/5 font-medium w-[28%] bg-[#181818]/95 backdrop-blur-md">名称</th>
                  <th className="sticky top-0 z-10 py-3 px-4 border-b border-white/5 font-medium w-[90px] bg-[#181818]/95 backdrop-blur-md">协议</th>
                  <th className="sticky top-0 z-10 py-3 px-4 border-b border-white/5 font-medium w-[100px] bg-[#181818]/95 backdrop-blur-md">状态</th>
                  <th className="sticky top-0 z-10 py-3 px-4 border-b border-white/5 font-medium w-[80px] bg-[#181818]/95 backdrop-blur-md">延迟</th>
                  <th className="sticky top-0 z-10 py-3 px-4 border-b border-white/5 font-medium w-[120px] bg-[#181818]/95 backdrop-blur-md">最后检测</th>
                  <th className="sticky top-0 z-10 py-3 px-5 border-b border-white/5 font-medium text-right w-[120px] bg-[#181818]/95 backdrop-blur-md">操作</th>
                </tr>
              </thead>
              <tbody>
                {filteredNodes.map((node) => {
                  const status = statusConfig[node.status];
                  const StatusIcon = status.icon;
                  const subscription = subscriptionById.get(node.subscription_id);
                  return (
                    <tr
                      key={node.id}
                      className="text-sm border-b border-white/[0.03] hover:bg-white/[0.03] transition-colors cursor-pointer"
                      onClick={() => setDetailNode(node)}
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
                              handleTestNode(node.id);
                            }}
                            disabled={testingAll || testingNodeId === node.id}
                            className="text-[10px] tracking-wider text-white/30 hover:text-white/70 transition-colors flex items-center gap-1 disabled:opacity-20 py-1 px-2 rounded hover:bg-white/5"
                          >
                            <RefreshCw className={`w-3 h-3 ${testingNodeId === node.id ? "animate-spin-silk" : ""}`} />
                            测速
                          </button>
                          <button
                            onClick={(event) => {
                              event.stopPropagation();
                              setDetailNode(node);
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
                })}
              </tbody>
            </table>
          </div>
          {filteredNodes.length === 0 && (
            <div className="text-center py-16">
              <Server className="w-10 h-10 text-white/10 mx-auto mb-3" />
              <p className="text-sm text-white/20">
                {loading ? "正在加载节点..." : searchQuery ? "未找到匹配的节点" : "暂无符合条件的节点"}
              </p>
            </div>
          )}
        </div>
      </div>

      {detailNode && (
        <NodeDetailModal
          node={detailNode}
          onClose={() => setDetailNode(null)}
          onTest={handleTestNode}
          testing={testingNodeId === detailNode.id}
        />
      )}
    </div>
  );
}

function StatCard({ icon: Icon, label, value, color, trend, suffix }: {
  icon: React.ElementType;
  label: string;
  value: string | number;
  color: string;
  trend?: string;
  suffix?: string;
}) {
  const isNumeric = typeof value === "number";
  return (
    <div className="liquid-glass rounded-2xl p-5">
      <div className="flex items-center justify-between mb-3">
        <span className="text-[10px] text-white/30 tracking-wider uppercase">{label}</span>
        <Icon className="w-4 h-4 text-white/20" />
      </div>
      <div className={`text-2xl font-mono font-medium ${color}`}>
        {isNumeric ? <AnimatedNumber value={value} suffix={suffix ?? ""} /> : value}
      </div>
      {trend && <div className="text-[10px] text-white/20 mt-1">{trend}</div>}
    </div>
  );
}

function SubscriptionSelector({
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
  const [panelPosition, setPanelPosition] = useState<{ top: number; right: number } | null>(null);

  const updatePanelPosition = useCallback(() => {
    const rect = triggerRef.current?.getBoundingClientRect();
    if (!rect) return;
    setPanelPosition({
      top: rect.bottom + 8,
      right: Math.max(16, window.innerWidth - rect.right),
    });
  }, []);

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
      onClose();
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };

    document.addEventListener("mousedown", handleMouseDown);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("mousedown", handleMouseDown);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [open, onClose]);

  if (subscriptions.length === 0) {
    return <span className="text-xs text-white/20">暂无配置订阅</span>;
  }

  const selected = subscriptions.find((item) => item.id === selectedSubscriptionId) ?? null;
  const failedCount = subscriptions.filter((item) => item.status === "failed").length;
  const summary = selected ?? subscriptions[0];
  const statusFailed = selected ? selected.status === "failed" : failedCount > 0;

  return (
    <div ref={triggerRef} className="flex items-center gap-2 text-xs text-white/40">
      <span className={`w-1.5 h-1.5 rounded-full ${statusFailed ? "bg-amber-400" : "bg-emerald-400"}`} />
      <span className="truncate max-w-[90px]">{selected?.name ?? summary.name}</span>
      <span className="text-white/20">·</span>
      <span className={statusFailed ? "text-amber-300" : "text-emerald-300"}>
        {selected ? (selected.status === "failed" ? "失败" : "正常") : failedCount ? `${failedCount} 个异常` : "正常"}
      </span>
      <button
        onClick={onToggle}
        className="p-1 -mr-1 text-white/25 hover:text-white/60 transition-colors"
        aria-label={open ? "收起订阅栏" : "展开订阅栏"}
      >
        <ChevronDown className={`w-3 h-3 transition-transform ${open ? "rotate-180" : ""}`} />
      </button>
      {open && panelPosition && createPortal(
        <SubscriptionPanel
          panelRef={panelRef}
          position={panelPosition}
          subscriptions={subscriptions}
          selectedSubscriptionId={selectedSubscriptionId}
          onSelect={onSelect}
        />,
        document.body,
      )}
    </div>
  );
}

function SubscriptionPanel({
  panelRef,
  position,
  subscriptions,
  selectedSubscriptionId,
  onSelect,
}: {
  panelRef: { current: HTMLDivElement | null };
  position: { top: number; right: number };
  subscriptions: Subscription[];
  selectedSubscriptionId: number | null;
  onSelect: (id: number | null) => void;
}) {
  const failedCount = subscriptions.filter((item) => item.status === "failed").length;
  return (
    <div
      ref={panelRef}
      className="fixed z-[80] w-[240px] max-h-[320px] overflow-y-auto rounded-2xl border border-white/10 bg-[#202020]/95 backdrop-blur-xl shadow-[0_18px_48px_rgba(0,0,0,0.45)] animate-dropdown-in"
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
      <p className="text-[10px] text-white/25 mt-1 truncate font-mono">{subtitle}</p>
      <p className="text-[10px] text-white/25 mt-0.5">{meta}</p>
    </button>
  );
}

function NodeDetailModal({
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

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm p-4">
      <div className="liquid-glass rounded-3xl w-full max-w-2xl max-h-[85vh] overflow-y-auto">
        <div className="p-6 border-b border-white/5 flex items-start justify-between">
          <div className="flex items-center gap-4 min-w-0">
            <div className={`w-12 h-12 rounded-2xl ${status.bg} flex items-center justify-center border ${status.border} flex-shrink-0`}>
              <StatusIcon className={`w-6 h-6 ${status.color}`} />
            </div>
            <div className="min-w-0">
              <h2 className="text-xl font-medium text-white/90 truncate">{node.name}</h2>
              <p className="text-xs text-white/30 font-mono mt-0.5">{node.server}:{node.port}</p>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <button
              onClick={() => onTest(node.id)}
              disabled={testing}
              className="text-xs tracking-wider text-white/40 hover:text-white/70 transition-colors flex items-center gap-1.5 px-3 py-1.5 rounded-lg hover:bg-white/5 disabled:opacity-20"
            >
              <RefreshCw className={`w-3.5 h-3.5 ${testing ? "animate-spin-silk" : ""}`} />
              测速
            </button>
            <button onClick={onClose} className="text-white/30 hover:text-white/60 p-1">
              <X className="w-5 h-5" />
            </button>
          </div>
        </div>

        <div className="p-6 space-y-6">
          <div className="grid grid-cols-3 gap-4">
            <div className="liquid-glass rounded-xl p-4 text-center">
              <div className="text-[10px] text-white/30 tracking-wider uppercase mb-1">状态</div>
              <div className={`text-lg font-medium ${status.color}`}>{status.label}</div>
            </div>
            <div className="liquid-glass rounded-xl p-4 text-center">
              <div className="text-[10px] text-white/30 tracking-wider uppercase mb-1">延迟</div>
              <LatencyBadge latency={node.latency_ms} size="lg" />
            </div>
            <div className="liquid-glass rounded-xl p-4 text-center">
              <div className="text-[10px] text-white/30 tracking-wider uppercase mb-1">协议</div>
              <div className="text-lg font-mono text-sky-400">{node.protocol || "--"}</div>
            </div>
          </div>

          <div className="liquid-glass rounded-xl p-4">
            <div className="flex items-center justify-between mb-3">
              <span className="text-[10px] text-white/30 tracking-wider uppercase">近 1 小时延迟波形</span>
              <span className="text-[10px] text-white/20">{history.length} 点</span>
            </div>
            {historyLoading ? (
              <div className="h-[180px] flex items-center justify-center text-xs text-white/25">正在加载波形...</div>
            ) : history.length > 0 ? (
              <LatencyHistoryChart points={history} />
            ) : (
              <div className="h-[180px] flex items-center justify-center text-xs text-white/25">暂无近 1 小时检测记录</div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

function LatencyHistoryChart({ points }: { points: CheckHistoryPoint[] }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const dpr = window.devicePixelRatio || 1;
    const width = canvas.offsetWidth;
    const height = canvas.offsetHeight;
    canvas.width = width * dpr;
    canvas.height = height * dpr;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, width, height);

    const padding = { top: 14, right: 16, bottom: 24, left: 42 };
    const chartW = width - padding.left - padding.right;
    const chartH = height - padding.top - padding.bottom;
    const values = points.map((point) => point.latency_ms).filter((value): value is number => value != null);
    const min = values.length ? Math.min(...values) : 0;
    const max = values.length ? Math.max(...values) : 1;
    const range = Math.max(1, max - min);

    ctx.strokeStyle = "rgba(255,255,255,0.04)";
    ctx.lineWidth = 1;
    for (let i = 0; i <= 4; i++) {
      const y = padding.top + (chartH / 4) * i;
      ctx.beginPath();
      ctx.moveTo(padding.left, y);
      ctx.lineTo(padding.left + chartW, y);
      ctx.stroke();
    }

    ctx.fillStyle = "rgba(255,255,255,0.18)";
    ctx.font = "10px monospace";
    ctx.textAlign = "right";
    for (let i = 0; i <= 4; i++) {
      const value = Math.round(max - (range / 4) * i);
      ctx.fillText(`${value}ms`, padding.left - 8, padding.top + (chartH / 4) * i + 3);
    }

    ctx.textAlign = "center";
    const start = new Date(points[0]?.checked_at ?? Date.now());
    const end = new Date(points.at(-1)?.checked_at ?? Date.now());
    const labels = [start, new Date((start.getTime() + end.getTime()) / 2), end];
    labels.forEach((date, index) => {
      const x = padding.left + (chartW / 2) * index;
      ctx.fillText(date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", hour12: false }), x, height - 7);
    });

    const validPoints = points
      .map((point, index) => {
        if (point.latency_ms == null) return null;
        const x = padding.left + (points.length === 1 ? chartW : (index / (points.length - 1)) * chartW);
        const y = padding.top + chartH - ((point.latency_ms - min) / range) * chartH;
        return { x, y, latency: point.latency_ms };
      })
      .filter((point): point is { x: number; y: number; latency: number } => point != null);

    if (!validPoints.length) {
      ctx.fillStyle = "rgba(255,255,255,0.22)";
      ctx.textAlign = "center";
      ctx.fillText("近 1 小时没有成功延迟点", width / 2, height / 2);
      return;
    }

    const gradient = ctx.createLinearGradient(0, padding.top, 0, padding.top + chartH);
    gradient.addColorStop(0, "rgba(56, 189, 248, 0.20)");
    gradient.addColorStop(1, "rgba(56, 189, 248, 0.00)");

    ctx.beginPath();
    ctx.moveTo(validPoints[0].x, padding.top + chartH);
    validPoints.forEach((point) => ctx.lineTo(point.x, point.y));
    ctx.lineTo(validPoints.at(-1)!.x, padding.top + chartH);
    ctx.closePath();
    ctx.fillStyle = gradient;
    ctx.fill();

    ctx.beginPath();
    ctx.strokeStyle = "#38bdf8";
    ctx.lineWidth = 1.6;
    ctx.lineCap = "round";
    ctx.lineJoin = "round";
    validPoints.forEach((point, index) => {
      if (index === 0) ctx.moveTo(point.x, point.y);
      else ctx.lineTo(point.x, point.y);
    });
    ctx.stroke();

    validPoints.forEach((point) => {
      ctx.beginPath();
      ctx.arc(point.x, point.y, 2.2, 0, Math.PI * 2);
      ctx.fillStyle =
        point.latency < 100 ? "#34d399"
        : point.latency < 200 ? "#fbbf24"
        : "#f87171";
      ctx.fill();
    });
  }, [points]);

  return <canvas ref={canvasRef} className="w-full" style={{ height: "180px" }} />;
}
