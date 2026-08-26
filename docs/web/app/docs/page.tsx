import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { loadDoc } from "@/lib/content";
import { DocPage } from "@/components/docs/DocPage";

export const metadata: Metadata = {
  title: "Documentation",
  description: "End-to-end documentation for NextSQL, an open-source multimodel database.",
};

export default function DocsIndex() {
  const doc = loadDoc("introduction");
  if (!doc) notFound();
  return <DocPage doc={doc} />;
}
