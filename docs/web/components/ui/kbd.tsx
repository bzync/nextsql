import type { HTMLAttributes } from "react";
import { cn } from "@/lib/cn";

export function Kbd({ className, children, ...props }: HTMLAttributes<HTMLElement>) {
  return (
    <kbd
      className={cn(
        "inline-flex h-[18px] min-w-[18px] items-center justify-center rounded border border-black/15 bg-black/6 px-1.5 py-0.5 font-mono text-[10px] font-medium text-slate-600 dark:border-white/15 dark:bg-white/6 dark:text-slate-400",
        className,
      )}
      {...props}
    >
      {children}
    </kbd>
  );
}

export function KbdChord({ keys }: { keys: string[] }) {
  return (
    <span className="inline-flex items-center gap-0.5">
      {keys.map((key) => (
        <Kbd key={key}>{key}</Kbd>
      ))}
    </span>
  );
}
