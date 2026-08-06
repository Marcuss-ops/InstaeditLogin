import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import {
  FUNCTIONAL_ICON_CATALOG,
  FunctionalIcon,
  getFunctionalIcon,
} from "./FunctionalIcons";

describe("functional Lucide icon catalog", () => {
  it("keeps shared UI symbols grouped by semantic purpose", () => {
    expect(getFunctionalIcon("navigation", "back")).toBe(FUNCTIONAL_ICON_CATALOG.navigation.back);
    expect(getFunctionalIcon("status", "success")).toBe(FUNCTIONAL_ICON_CATALOG.status.success);
    expect(getFunctionalIcon("product", "video")).toBe(FUNCTIONAL_ICON_CATALOG.product.video);
  });

  it("renders a semantic icon and fails closed for unknown names", () => {
    const { container } = render(
      <>
        <FunctionalIcon group="actions" name="save" data-testid="save-icon" />
        <FunctionalIcon group="actions" name="not-in-catalog" data-testid="unknown-icon" />
      </>,
    );

    expect(container.querySelector('[data-testid="save-icon"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="unknown-icon"]')).toBeNull();
  });
});
