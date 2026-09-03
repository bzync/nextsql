import { buttonVariants, buttonSizes, type ButtonVariant, type ButtonSize } from "@bzync/rui";
import { cn } from "@/lib/cn";

export type { ButtonVariant, ButtonSize };
export { Button } from "@bzync/rui";

const base =
  "inline-flex items-center justify-center cursor-pointer select-none whitespace-nowrap transition-[color,background-color,border-color,box-shadow] duration-150 disabled:opacity-40 disabled:cursor-not-allowed focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-blue-500/70 focus-visible:ring-offset-1 focus-visible:ring-offset-white dark:focus-visible:ring-offset-navy-950";

/** Button-styled classes for non-<button> elements (e.g. <Link>) — built on rui's own variant/size tokens. */
export function buttonClassName({
  variant = "primary",
  size = "md",
  className,
}: {
  variant?: ButtonVariant;
  size?: ButtonSize;
  className?: string;
} = {}) {
  return cn(base, buttonVariants[variant], buttonSizes[size], className);
}
