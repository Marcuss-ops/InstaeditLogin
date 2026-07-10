import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { Nav } from "./Nav";

function renderNav() {
  return render(
    <MemoryRouter>
      <Nav />
    </MemoryRouter>
  );
}

describe("Nav", () => {
  it("renders the InstaEdit logo link", () => {
    renderNav();
    const logo = screen.getByText("InstaEdit");
    expect(logo.closest("a")?.getAttribute("href")).toBe("/");
  });

  it("renders the Get started link pointing to /login", () => {
    renderNav();
    const links = screen.getAllByRole("link", { name: "Get started" });
    expect(links.length).toBeGreaterThanOrEqual(1);
    expect(links[0].getAttribute("href")).toBe("/login");
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
    expect(mobileMenu.classList.contains("hidden")).toBe(false);
  });

  it("closes the mobile menu when a link is clicked", async () => {
    renderNav();
    const user = userEvent.setup();

    await user.click(screen.getByTestId("mobile-menu-toggle"));

    const mobileMenu = screen.getByTestId("mobile-menu");
    expect(mobileMenu.classList.contains("flex")).toBe(true);

    // Click the "Get started" link inside the mobile menu (the last one).
    const links = screen.getAllByRole("link", { name: "Get started" });
    await user.click(links[links.length - 1]);

    expect(mobileMenu.classList.contains("hidden")).toBe(true);
  });

  it("closes the mobile menu when the toggle is clicked again", async () => {
    renderNav();
    const user = userEvent.setup();

    await user.click(screen.getByTestId("mobile-menu-toggle")); // open
    expect(screen.getByTestId("mobile-menu").classList.contains("flex")).toBe(true);

    await user.click(screen.getByTestId("mobile-menu-toggle")); // close
    expect(screen.getByTestId("mobile-menu").classList.contains("hidden")).toBe(true);
  });
});
