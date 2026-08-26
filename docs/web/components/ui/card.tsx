import type { HTMLAttributes } from "react";
import { cn } from "@/lib/cn";

export type CardVariant = "default" | "elevated" | "bordered";

const variants: Record<CardVariant, string> = {
  default: "portal-surface border",
  elevated:
    "bg-white dark:bg-navy-800/95 border border-black/[0.10] dark:border-white/[0.08] shadow-[0_14px_36px_-22px_rgba(15,23,42,0.40)] dark:shadow-[0_18px_48px_-26px_rgba(0,0,0,0.90),inset_0_1px_0_rgba(255,255,255,0.055)]",
  bordered: "bg-white/60 border border-black/[0.10] dark:bg-transparent dark:border-white/[0.09]",
};

export function Card({
  variant = "default",
  className,
  ...props
}: HTMLAttributes<HTMLDivElement> & { variant?: CardVariant }) {
  return <div className={cn("rounded-lg", variants[variant], className)} {...props} />;
}

export function CardHeader({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("border-b border-black/[0.055] px-4 py-3 dark:border-white/[0.06] sm:px-5 sm:py-4", className)}
      {...props}
    />
  );
}

export function CardBody({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("px-4 py-3 sm:px-5 sm:py-4", className)} {...props} />;
}

export function CardFooter({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn("border-t border-black/[0.055] px-4 py-3 dark:border-white/[0.06] sm:px-5 sm:py-4", className)}
      {...props}
    />
  );
}

export function CardTitle({ className, ...props }: HTMLAttributes<HTMLHeadingElement>) {
  return <h3 className={cn("text-sm font-semibold leading-tight text-slate-900 dark:text-white", className)} {...props} />;
}

export function CardDescription({ className, ...props }: HTMLAttributes<HTMLParagraphElement>) {
  return (
    <p className={cn("mt-1 text-[12.5px] leading-relaxed text-slate-500 dark:text-slate-400", className)} {...props} />
  );
}
