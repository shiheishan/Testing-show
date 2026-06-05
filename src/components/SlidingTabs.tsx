import { useLayoutEffect, useRef, useState } from "react";

export function SlidingTabs<T extends string>({
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
