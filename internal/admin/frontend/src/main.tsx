import "@bzync/rui/styles.css";
import "./app.css";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ThemeProvider } from "@bzync/rui";
import { MotionConfig } from "framer-motion";
import { App } from "./App";

const root = document.getElementById("root");
if (!root) throw new Error("missing #root");
root.textContent = "";

createRoot(root).render(
  <StrictMode>
    <ThemeProvider defaultTheme="system" storageKey="nextsql-admin-theme" applyToRoot>
      <MotionConfig reducedMotion="user">
        <App />
      </MotionConfig>
    </ThemeProvider>
  </StrictMode>,
);

// PWA installability: registering the service worker is what makes the
// browser offer "Install"/"Add to Home Screen" alongside the manifest.
// Registration silently no-ops in a non-secure context (a non-loopback
// plaintext deployment, which this server refuses to serve on anyway) or
// where service workers aren't supported — the app is fully usable without
// it either way, this only adds offline app-shell loading and installability.
if ("serviceWorker" in navigator) {
  window.addEventListener("load", () => {
    navigator.serviceWorker.register("/sw.js").catch(() => {});
  });
}
