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
      <span className="text-[14.5px] font-semibold leading-none tracking-[-0.02em]">
        NextSQL
      </span>
    </Link>
  );
}
