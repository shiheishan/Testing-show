export function LatencyBadge({ latency, size = "sm" }: { latency: number | null; size?: "sm" | "lg" }) {
  if (latency === null) {
    return <span className={`${size === "lg" ? "text-base" : "text-xs"} text-white/35 font-mono`}>--</span>;
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
