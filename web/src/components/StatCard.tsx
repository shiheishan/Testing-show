import { AnimatedNumber } from "./AnimatedNumber";

export function StatCard({ icon: Icon, label, value, color, trend, suffix }: {
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
      {trend && <div className="text-[11px] text-white/45 mt-1">{trend}</div>}
    </div>
  );
}
