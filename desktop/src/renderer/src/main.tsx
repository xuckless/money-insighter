import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { App } from "./App";

import "./globals.css";

// Follow the OS theme: the shadcn palette switches on the .dark class.
const dark = window.matchMedia("(prefers-color-scheme: dark)");
const applyTheme = () => document.documentElement.classList.toggle("dark", dark.matches);
applyTheme();
dark.addEventListener("change", applyTheme);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
