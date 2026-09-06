import Link from "next/link";

export function AdjacentNav({
  prev,
  next,
}: {
  prev?: { href: string; label: string };
  next?: { href: string; label: string };
}) {
  if (!prev && !next) return null;
  return (
    <nav className="adjacent-nav" aria-label="Page">
      {prev ? (
        <Link href={prev.href} className="adjacent-link">
          <span className="kicker">Previous</span>
          <span className="adjacent-title">{prev.label}</span>
        </Link>
      ) : (
        <span className="hidden sm:block" />
      )}
      {next ? (
        <Link href={next.href} className="adjacent-link adjacent-link-next">
          <span className="kicker">Next</span>
          <span className="adjacent-title">{next.label}</span>
        </Link>
      ) : null}
    </nav>
  );
}
