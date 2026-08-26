import type { ButtonHTMLAttributes } from "react";
import { cn } from "@/lib/cn";

export type ButtonVariant = "primary" | "secondary" | "ghost" | "outline" | "destructive";
export type ButtonSize = "sm" | "md" | "lg";

const base =
  "inline-flex items-center justify-center cursor-pointer select-none whitespace-nowrap font-medium transition-all duration-150 disabled:opacity-40 disabled:cursor-not-allowed focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/70 focus-visible:ring-offset-1 focus-visible:ring-offset-white dark:focus-visible:ring-offset-navy-950 rounded-lg";

const variants: Record<ButtonVariant, string> = {
  primary:
    "bg-blue-600 hover:bg-blue-500 active:bg-blue-700 text-white border border-blue-700/45 dark:border-blue-400/25 shadow-[0_8px_18px_-12px_rgba(29,78,216,0.75),inset_0_1px_0_rgba(255,255,255,0.18),inset_0_-1px_0_rgba(0,0,0,0.10)]",
  secondary:
    "bg-white dark:bg-white/[0.07] hover:bg-black/[0.04] dark:hover:bg-white/[0.10] active:bg-black/[0.07] dark:active:bg-white/[0.13] text-slate-800 dark:text-slate-200 border border-black/[0.12] dark:border-white/[0.09] shadow-[0_1px_2px_rgba(15,23,42,0.04)] dark:shadow-[inset_0_1px_0_rgba(255,255,255,0.045)]",
  ghost:
    "bg-transparent hover:bg-black/[0.05] dark:hover:bg-white/[0.06] active:bg-black/[0.08] dark:active:bg-white/[0.09] text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-100",
  outline:
    "bg-transparent border border-black/[0.16] dark:border-white/[0.16] hover:border-black/[0.28] dark:hover:border-white/[0.28] text-slate-700 dark:text-slate-200 hover:text-slate-900 dark:hover:text-white hover:bg-black/[0.03] dark:hover:bg-white/[0.04]",
  destructive:
    "bg-red-600 hover:bg-red-500 active:bg-red-700 text-white border border-red-700/40 dark:border-red-400/25",
};

const sizes: Record<ButtonSize, string> = {
  sm: "h-8 px-3 text-xs gap-1.5",
  md: "h-9 px-4 text-sm gap-2",
  lg: "h-10 px-5 text-sm gap-2.5",
};

export function buttonClassName({
  variant = "primary",
  size = "md",
  className,
}: {
  variant?: ButtonVariant;
  size?: ButtonSize;
  className?: string;
} = {}) {
  return cn(base, variants[variant], sizes[size], className);
}

export function Button({
  variant = "primary",
  size = "md",
  className,
  children,
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant;
  size?: ButtonSize;
}) {
  return (
    <button className={buttonClassName({ variant, size, className })} {...props}>
      {children}
    </button>
  );
}
