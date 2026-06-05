import { memo } from "react";
import { RefreshCw } from "lucide-react";

export const TestAllButton = memo(function TestAllButton({
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
