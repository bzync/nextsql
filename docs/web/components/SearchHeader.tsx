"use client";

import { useCommand } from "@bzync/rui";
import { SiteHeader } from "./SiteHeader";

export function SearchHeader() {
  const { setOpen } = useCommand();
  return <SiteHeader onSearch={() => setOpen(true)} />;
}
