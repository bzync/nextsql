import type { HTMLAttributes, ReactNode } from "react";
import { cn } from "@/lib/cn";

export type CalloutVariant = "info" | "success" | "warning" | "error" | "muted";

const variants: Record<CalloutVariant, string> = {
  info: "bg-sky-50 border-sky-200/80 text-sky-600 dark:bg-sky-500/8 dark:border-sky-500/20 dark:text-sky-400",
  success: "bg-emerald-50 border-emerald-200/80 text-emerald-700 dark:bg-emerald-500/8 dark:border-emerald-500/20 dark:text-emerald-400",
  warning: "bg-amber-50 border-amber-200/80 text-amber-700 dark:bg-amber-500/8 dark:border-amber-500/20 dark:text-amber-400",
  error: "bg-red-50 border-red-200/80 text-red-600 dark:bg-red-500/8 dark:border-red-500/20 dark:text-red-400",
  muted: "bg-black/[0.03] border-black/[0.08] text-slate-500 dark:bg-white/[0.03] dark:border-white/[0.08] dark:text-slate-400",
};

export function Callout({
  variant = "info",
  title,
  className,
  children,
  ...props
}: HTMLAttributes<HTMLDivElement> & {
  variant?: CalloutVariant;
  title?: ReactNode;
}) {
  return (
    <div
      role="note"
      className={cn("flex gap-3 rounded-xl border px-4 py-3.5", variants[variant], className)}
      {...props}
    >
      <span className="mt-0.5 shrink-0">{icon(variant)}</span>
      <div className="min-w-0 flex-1">
        {title ? (
          <p className="mb-0.5 text-[13px] font-semibold leading-snug text-gray-900 dark:text-white">{title}</p>
        ) : null}
        <div className="text-[13px] leading-relaxed text-slate-600 dark:text-slate-400">{children}</div>
      </div>
    </div>
  );
}

function icon(variant: CalloutVariant) {
  const path =
    variant === "success"
      ? "M22 11.08V12a10 10 0 1 1-5.93-9.14 M22 4 12 14.01l-3-3"
      : variant === "warning" || variant === "error"
        ? "M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z M12 9v4 M12 17h.01"
        : "M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20z M12 16v-4 M12 8h.01";
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d={path} />
    </svg>
  );
}
