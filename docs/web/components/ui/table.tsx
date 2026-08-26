import type { HTMLAttributes, TdHTMLAttributes, ThHTMLAttributes } from "react";
import { cn } from "@/lib/cn";

export function Table({ className, ...props }: HTMLAttributes<HTMLTableElement>) {
  return (
    <div className="w-full overflow-x-auto rounded-lg border border-black/[0.10] bg-white shadow-[0_1px_2px_rgba(15,23,42,0.04)] dark:border-white/[0.07] dark:bg-white/[0.02] dark:shadow-none">
      <table className={cn("w-full text-sm", className)} {...props} />
    </div>
  );
}

export function TableHeader({ className, ...props }: HTMLAttributes<HTMLTableSectionElement>) {
  return (
    <thead
      className={cn("border-b border-black/[0.07] bg-black/[0.025] dark:border-white/[0.07] dark:bg-white/[0.03]", className)}
      {...props}
    />
  );
}

export function TableBody({ className, ...props }: HTMLAttributes<HTMLTableSectionElement>) {
  return <tbody className={cn("divide-y divide-black/[0.05] dark:divide-white/[0.05]", className)} {...props} />;
}

export function TableRow({ className, ...props }: HTMLAttributes<HTMLTableRowElement>) {
  return (
    <tr className={cn("transition-colors duration-100 hover:bg-black/[0.02] dark:hover:bg-white/[0.025]", className)} {...props} />
  );
}

export function TableHead({ className, ...props }: ThHTMLAttributes<HTMLTableCellElement>) {
  return (
    <th
      className={cn(
        "px-3 py-2.5 text-left text-[11px] font-semibold uppercase tracking-normal text-slate-500 sm:px-4 sm:py-3 dark:text-slate-400",
        className,
      )}
      {...props}
    />
  );
}

export function TableCell({ className, ...props }: TdHTMLAttributes<HTMLTableCellElement>) {
  return <td className={cn("px-3 py-2.5 text-[13px] text-slate-600 sm:px-4 sm:py-3 dark:text-slate-300", className)} {...props} />;
}
