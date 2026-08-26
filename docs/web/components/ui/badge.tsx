import type { HTMLAttributes } from "react";
import { cn } from "@/lib/cn";

export type BadgeVariant = "default" | "success" | "warning" | "error" | "info" | "muted";

const variants: Record<BadgeVariant, string> = {
  default: "bg-blue-50 dark:bg-blue-500/[0.12] text-blue-600 dark:text-blue-400 border-blue-200/80 dark:border-blue-500/20",
  success: "bg-emerald-50 dark:bg-emerald-500/[0.12] text-emerald-700 dark:text-emerald-400 border-emerald-200/80 dark:border-emerald-500/20",
  warning: "bg-amber-50 dark:bg-amber-500/[0.12] text-amber-700 dark:text-amber-400 border-amber-200/80 dark:border-amber-500/20",
  error: "bg-red-50 dark:bg-red-500/[0.12] text-red-600 dark:text-red-400 border-red-200/80 dark:border-red-500/20",
  info: "bg-sky-50 dark:bg-sky-500/[0.12] text-sky-700 dark:text-sky-400 border-sky-200/80 dark:border-sky-500/20",
  muted: "bg-black/[0.05] dark:bg-white/[0.07] text-slate-600 dark:text-slate-400 border-black/[0.08] dark:border-white/[0.09]",
};

const dots: Record<BadgeVariant, string> = {
  default: "bg-blue-500 dark:bg-blue-400",
  success: "bg-emerald-400",
  warning: "bg-amber-400",
  error: "bg-red-400",
  info: "bg-sky-400",
  muted: "bg-slate-400",
};

export function Badge({
  variant = "default",
  dot = false,
  pulse = false,
  className,
  children,
  ...props
}: HTMLAttributes<HTMLSpanElement> & {
  variant?: BadgeVariant;
  dot?: boolean;
  pulse?: boolean;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border whitespace-nowrap px-2 py-0.5 text-[11.5px] gap-1.5 font-medium",
        variants[variant],
        className,
      )}
      {...props}
    >
      {dot ? (
        <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", dots[variant], pulse && "animate-pulse")} />
      ) : null}
      {children}
    </span>
  );
}
