import "@bzync/rui/styles.css";
import "./app.css";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ThemeProvider } from "@bzync/rui";
import { App } from "./App";

const root = document.getElementById("root");
if (!root) throw new Error("missing #root");
root.textContent = "";

createRoot(root).render(
  <StrictMode>
    <ThemeProvider defaultTheme="system" storageKey="nsm-theme">
      <App />
    </ThemeProvider>
  </StrictMode>,
);
