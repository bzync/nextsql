import fs from "node:fs";
import path from "node:path";
import { allDocs, docHref, docsNav, findDoc, type NavItem } from "./nav";
import { extractHeadings, extractSearchSections, type Heading } from "./markdown";
import type { DocsSearchEntry } from "./search";

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

export function searchIndex(): DocsSearchEntry[] {
  return docsNav.flatMap((group) =>
    group.items.flatMap((item) => {
      const doc = loadDoc(item.slug);
      const sections = doc ? extractSearchSections(doc.body) : [];
      const introduction = sections.find((section) => !section.id);
      const page: DocsSearchEntry = {
        id: `page:${item.slug}`,
        label: item.title,
        description: item.description,
        group: group.title,
        keywords: [item.slug, item.title, item.description, introduction?.content ?? ""],
        href: docHref(item.slug),
      };
      const sectionEntries = sections
        .filter((section): section is typeof section & { id: string; heading: string } =>
          Boolean(section.id && section.heading),
        )
        .map((section): DocsSearchEntry => ({
          id: `section:${item.slug}:${section.id}`,
          label: item.title,
          description: section.heading,
          group: group.title,
          keywords: [section.heading, section.content],
          href: `${docHref(item.slug)}#${section.id}`,
        }));
      return [page, ...sectionEntries];
    }),
  );
}
