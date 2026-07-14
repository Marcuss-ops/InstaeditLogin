import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";
// Registers jest-dom's matchers with Vitest's `expect.extend(...)` at
// runtime. Vitest runs this setup file from `vite.config.ts`'s
// `setupFiles`, so the import side-effect fires before any test executes.
//
// Type-augmentation (so tsc knows about `toBeInTheDocument`,
// `toHaveClass`, `toHaveTextContent`) is loaded separately by
// `src/types/jest-dom.d.ts`, which is part of `tsconfig.app.json`'s
// `include: ["src"]` scope. The runtime import here and the type-only
// import there intentionally live in different files because they target
// different toolchains (Vitest runtime vs. tsc).
import "@testing-library/jest-dom/vitest";

afterEach(() => {
  cleanup();
});
