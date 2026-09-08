// The NextSQL brand mark: the product's actual icon asset (copied from
// docs/web/public/icons/icon-192.png, the same file docs/web and NextSQL
// Manager use — see docs/web/components/Logo.tsx), not a redrawn
// approximation. esbuild's `.png: "dataurl"` loader (build.mjs) inlines it
// into app.js at build time, so nothing is fetched at runtime.
import icon from "./assets/nextsql-icon.png";
import { Inline, Text } from "@bzync/rui";

export function Mark({ size = 28, className, decorative = false }: { size?: number; className?: string; decorative?: boolean }) {
  // The source PNG already has its rounded-square corners baked in
  // (transparent outside them) — no extra clipping needed here.
  return <img src={icon} width={size} height={size} alt={decorative ? "" : "NextSQL"} aria-hidden={decorative || undefined} className={className} />;
}

export function Wordmark({
  size = "md",
  className,
}: {
  size?: "sm" | "md" | "lg";
  className?: string;
}) {
  const markPx = size === "sm" ? 22 : size === "lg" ? 34 : 27;
  const textPx = size === "sm" ? 13 : size === "lg" ? 19 : 16;
  return (
    <Inline
      className={className}
      gap="xs"
      align="center"
      wrap={false}
      aria-label="NextSQL Admin"
      style={{ gap: Math.round(markPx * 0.32) }}
    >
      <Mark size={markPx} decorative />
      <Text as="span" weight="bold" wrap="nowrap" style={{ fontSize: textPx, letterSpacing: "-0.02em", lineHeight: 1 }}>
        NextSQL
      </Text>
    </Inline>
  );
}
