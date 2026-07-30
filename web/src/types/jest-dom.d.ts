// Side-effect import: triggers TypeScript to load the jest-dom /vitest
// subpath's declaration files, which augment Vitest's `Vi.Assertion`
// interface with `toBeInTheDocument`, `toHaveClass`, `toHaveTextContent`,
// etc. Without this, tsc in `--noEmit` mode rejects `expect(el).toBeInTheDocument()`
// in every `*.test.tsx` file under `src/`.
//
// We deliberately import the `/vitest` subpath, NOT the root entry, because:
//   - The root `@testing-library/jest-dom` augments jest's `Assertion`
//     namespace (NOT Vitest's), so its matchers would not be visible to tsc
//     when Vitest's `expect(...)` returns `Vi.Assertion`.
//   - The `/vitest` subpath's `types/vitest.d.ts` augments the correct namespace.
//
// Runtime matchers are registered via `vitest.setup.ts`'s separate
// `import "@testing-library/jest-dom/vitest";` (which calls `expect.extend`
// inside `setupFiles`). This file is purely for the TypeScript type
// augmentation that runs at `tsc -b`.
import "@testing-library/jest-dom/vitest";
