import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { allDocs } from "@/lib/nav";
import { loadDoc } from "@/lib/content";
import { DocPage } from "@/components/docs/DocPage";

export function generateStaticParams() {
  return allDocs().map((doc) => ({ slug: doc.slug }));
}

export async function generateMetadata({
  params,
}: {
  params: Promise<{ slug: string }>;
}): Promise<Metadata> {
  const { slug } = await params;
  const doc = loadDoc(slug);
  if (!doc) return { title: "Not found" };
  return {
    title: doc.title,
    description: doc.description,
  };
}

export default async function Page({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  const doc = loadDoc(slug);
  if (!doc) notFound();
  return <DocPage doc={doc} />;
}
