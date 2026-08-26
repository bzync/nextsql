"use client";

import { useEffect, useState } from "react";
import { SiteHeader } from "./SiteHeader";
import { SearchDialog } from "./Search";

export function SearchHeader() {
  const [search, setSearch] = useState(false);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "k" && (event.metaKey || event.ctrlKey)) {
        event.preventDefault();
        setSearch(true);
        return;
      }
      if (
        event.key === "/" &&
        !(event.target instanceof HTMLInputElement) &&
        !(event.target instanceof HTMLTextAreaElement)
      ) {
        event.preventDefault();
        setSearch(true);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <>
      <SiteHeader onSearch={() => setSearch(true)} />
      <SearchDialog open={search} onClose={() => setSearch(false)} />
    </>
  );
}
