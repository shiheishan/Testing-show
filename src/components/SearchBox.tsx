import { memo, useRef, useState } from "react";
import { Search, X } from "lucide-react";

export const SearchBox = memo(function SearchBox({
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
        focused ? "px-4 py-2 w-full sm:w-[220px]" : "px-2 py-2 w-9 cursor-pointer"
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
