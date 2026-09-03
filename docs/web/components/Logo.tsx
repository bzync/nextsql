import Image from "next/image";
import Link from "next/link";
import { assetPath } from "@/lib/asset-path";

export function Mark({ className = "h-[30px] w-[30px]" }: { className?: string }) {
  return (
    <Image
      src={assetPath("/icons/icon-192.png")}
      alt=""
      width={30}
      height={30}
      className={className}
      aria-hidden="true"
    />
  );
}

export function Logo({
  href = "/",
  className = "",
}: {
  href?: string;
  className?: string;
}) {
  return (
    <Link
      href={href}
      className={`inline-flex items-center gap-2 text-foreground ${className}`}
    >
      <Mark />
      <span className="font-brand text-[14px] font-bold leading-none tracking-normal">
        Next<span className="text-[13px] font-semibold text-blue-500 dark:text-blue-400">SQL</span>
      </span>
    </Link>
  );
}
