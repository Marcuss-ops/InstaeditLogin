import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { ToastViewport } from "./ToastViewport";
import { toastBus } from "./toast-bus";

function push(kind: "success" | "error" | "info" | "warning", msg: string) {
  act(() => {
    toastBus.push(kind, msg, 5000);
  });
}

describe("ToastViewport", () => {
  beforeEach(() => {
    // The bus is a module-level singleton, owned by `vitest.setup.ts`'s
    // `afterEach` (which calls `toastBus.__resetForTests()`). The per-file
    // setup here only needs to flip to fake timers; the bus starts clean.
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders nothing when the queue is empty", () => {
    render(<ToastViewport />);
    expect(screen.queryByTestId("toast-viewport")).toBeNull();
  });

  it("renders a viewport region with role=region + aria-live=polite + aria-label", () => {
    push("info", "Region check");
    render(<ToastViewport />);
    const viewport = screen.getByTestId("toast-viewport");
    expect(viewport.getAttribute("role")).toBe("region");
    expect(viewport.getAttribute("aria-live")).toBe("polite");
    expect(viewport.getAttribute("aria-label")).toBe("Notifications");
  });

  it("renders a success toast with role=status + green palette", () => {
    push("success", "Saved!");
    render(<ToastViewport />);
    const toast = screen.getByTestId("toast-success");
    expect(toast.getAttribute("role")).toBe("status");
    expect(toast.textContent).toMatch(/Saved!/);
    // Color comes from Tailwind class strings; assert the green tokens are present.
    expect(toast.className).toMatch(/bg-green-50/);
    expect(toast.className).toMatch(/border-green-300/);
    expect(toast.className).toMatch(/text-green-900/);
  });

  it("renders an error toast with role=alert + aria-live=assertive + red palette", () => {
    push("error", "Boom");
    render(<ToastViewport />);
    const toast = screen.getByTestId("toast-error");
    expect(toast.getAttribute("role")).toBe("alert");
    // role=alert already implies assertive; we additionally set it
    // explicitly so ATs that read the leaf element go assertive.
    expect(toast.getAttribute("aria-live")).toBe("assertive");
    expect(toast.textContent).toMatch(/Boom/);
    expect(toast.className).toMatch(/bg-red-50/);
    expect(toast.className).toMatch(/border-red-300/);
  });

  it("renders warning (amber) and info (blue) variants with role=status", () => {
    push("warning", "Be careful");
    push("info", "FYI");
    render(<ToastViewport />);
    expect(screen.getByTestId("toast-warning").getAttribute("role")).toBe("status");
    expect(screen.getByTestId("toast-warning").className).toMatch(/bg-amber-50/);
    expect(screen.getByTestId("toast-info").getAttribute("role")).toBe("status");
    expect(screen.getByTestId("toast-info").className).toMatch(/bg-blue-50/);
  });

  it("auto-dismisses after 5s", () => {
    push("info", "transient");
    render(<ToastViewport />);
    expect(screen.getByTestId("toast-info")).toBeTruthy();
    act(() => {
      vi.advanceTimersByTime(5000);
    });
    expect(screen.queryByTestId("toast-info")).toBeNull();
  });

  it("Escape key dismisses ALL toasts at once", () => {
    push("info", "one");
    push("warning", "two");
    render(<ToastViewport />);
    expect(toastBus.__sizeForTests()).toBe(2);
    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    });
    expect(toastBus.__sizeForTests()).toBe(0);
  });

  it("dismiss button removes the associated toast", () => {
    // `fireEvent.click` (sync) over `userEvent.click` (async) — the
    // latter's await chain races with React's flush under fake timers.
    // RTL 16+ wraps `fireEvent.*` in `act()` internally, so no manual
    // wrap is needed.
    push("info", "single");
    render(<ToastViewport />);
    expect(toastBus.__sizeForTests()).toBe(1);
    expect(screen.getByTestId("toast-info")).toBeTruthy();
    fireEvent.click(screen.getByTestId("toast-dismiss-info"));
    expect(toastBus.__sizeForTests()).toBe(0);
    expect(screen.queryByTestId("toast-info")).toBeNull();
  });

  it("renders multiple toasts in queue order (FIFO)", () => {
    push("info", "first");
    push("info", "second");
    push("info", "third");
    render(<ToastViewport />);
    const items = screen
      .getByTestId("toast-viewport")
      .querySelectorAll("[data-toast-id]");
    expect(items.length).toBe(3);
    expect(items[0].textContent).toMatch(/first/);
    expect(items[1].textContent).toMatch(/second/);
    expect(items[2].textContent).toMatch(/third/);
  });

  it("dismiss button has aria-label for assistive tech", () => {
    push("info", "labeled");
    render(<ToastViewport />);
    const dismiss = screen.getByTestId("toast-dismiss-info");
    expect(dismiss.getAttribute("aria-label")).toBe("Dismiss notification");
  });
});
