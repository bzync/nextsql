/**
 * Prefixes a root-relative public/ asset path with the site's basePath
 * (NEXT_PUBLIC_BASE_PATH, set by next.config.ts). Needed anywhere an asset
 * is referenced by raw path instead of next/link or an unoptimized
 * next/image src — neither of those auto-prefix basePath in this setup.
 */
export function assetPath(path: string): string {
  const basePath = process.env.NEXT_PUBLIC_BASE_PATH || "";
  return `${basePath}${path}`;
}
