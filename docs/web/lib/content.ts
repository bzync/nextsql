import fs from "node:fs";
import path from "node:path";
import { allDocs, findDoc, type NavItem } from "./nav";
import { extractHeadings, type Heading } from "./markdown";

const CONTENT_DIR = path.join(process.cwd(), "content", "docs");

export type DocPage = NavItem & {
  body: string;
  headings: Heading[];
};

export function loadDoc(slug: string): DocPage | null {
  const meta = findDoc(slug);
  if (!meta) return null;
  const file = path.join(CONTENT_DIR, `${slug}.md`);
  if (!fs.existsSync(file)) return null;
  const body = fs.readFileSync(file, "utf8");
  return { ...meta, body, headings: extractHeadings(body) };
}

export function loadAllDocs(): DocPage[] {
  return allDocs()
    .map((item) => loadDoc(item.slug))
    .filter((doc): doc is DocPage => doc !== null);
}

export function searchIndex() {
  return loadAllDocs().map((doc) => ({
    title: doc.title,
    slug: doc.slug,
    description: doc.description,
    headings: doc.headings.map((h) => h.text),
  }));
}
