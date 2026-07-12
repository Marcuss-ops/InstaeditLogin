import { BrowserRouter } from "react-router-dom";
import { Nav } from "./Nav";
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

function renderNav() {
  return render(
    <BrowserRouter>
      <Nav />
    </BrowserRouter>
  );
}

describe("Nav", () => {
  it("renders the InstaEdit logo link pointing to /accounts", () => {
    renderNav();
    const logo = screen.getByText("InstaEdit");
    expect(logo.closest("a")?.getAttribute("href")).toBe("/accounts");
  });

  it("renders all nav links", () => {
    renderNav();
    expect(screen.getAllByText("Accounts").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("Compose").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("Posts").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("Settings").length).toBeGreaterThanOrEqual(1);
  });

  it("shows the mobile menu toggle button", () => {
    renderNav();
    expect(screen.getByTestId("mobile-menu-toggle")).toBeDefined();
  });

  it("opens the mobile menu when the toggle is clicked", async () => {
    renderNav();
    const user = userEvent.setup();
    const mobileMenu = screen.getByTestId("mobile-menu");
    expect(mobileMenu.classList.contains("hidden")).toBe(true);

    await user.click(screen.getByTestId("mobile-menu-toggle"));
    expect(mobileMenu.classList.contains("flex")).toBe(true);
  });

  it("closes the mobile menu when the toggle is clicked again", async () => {
    renderNav();
    const user = userEvent.setup();
    await user.click(screen.getByTestId("mobile-menu-toggle"));
    expect(screen.getByTestId("mobile-menu").classList.contains("flex")).toBe(true);
    await user.click(screen.getByTestId("mobile-menu-toggle"));
    expect(screen.getByTestId("mobile-menu").classList.contains("hidden")).toBe(true);
  });
});
