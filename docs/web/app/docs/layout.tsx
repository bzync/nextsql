import { DocsChrome } from "@/components/docs/DocsChrome";

export default function DocsLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return <DocsChrome>{children}</DocsChrome>;
}
