import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { LanguagePicker } from "./LanguagePicker";

describe("LanguagePicker", () => {
  it("opens from the flag and saves the selected language", () => {
    const onChange = vi.fn();
    render(<LanguagePicker value="en" onChange={onChange} label="Language for channel-one" />);

    const trigger = screen.getByRole("button", { name: "Language for channel-one" });
    expect(trigger).toHaveAttribute("aria-haspopup", "menu");
    expect(trigger).toHaveAttribute("aria-expanded", "false");
    expect(trigger).toHaveAttribute("title", "English · clicca per cambiare");

    fireEvent.click(trigger);
    expect(trigger).toHaveAttribute("aria-expanded", "true");
    fireEvent.click(screen.getByRole("menuitem", { name: "Italiano" }));

    expect(onChange).toHaveBeenCalledWith("it");
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("can clear the language and closes on escape or outside click", () => {
    const onChange = vi.fn();
    render(<LanguagePicker value="it" onChange={onChange} label="Language for channel-one" />);

    const trigger = screen.getByRole("button", { name: "Language for channel-one" });
    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole("menuitem", { name: "Nessuna lingua" }));
    expect(onChange).toHaveBeenCalledWith("");

    fireEvent.click(trigger);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();

    fireEvent.click(trigger);
    fireEvent.pointerDown(document.body);
    expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  });

  it("supports arrow, home/end, enter, and restores focus to the trigger", () => {
    const onChange = vi.fn();
    render(<LanguagePicker value="en" onChange={onChange} label="Language for channel-one" />);

    const trigger = screen.getByRole("button", { name: "Language for channel-one" });
    fireEvent.click(trigger);
    const menu = screen.getByRole("menu");
    expect(screen.getByRole("menuitem", { name: "English" })).toHaveFocus();

    fireEvent.keyDown(menu, { key: "Home" });
    expect(screen.getByRole("menuitem", { name: "Nessuna lingua" })).toHaveFocus();
    fireEvent.keyDown(menu, { key: "End" });
    expect(screen.getByRole("menuitem", { name: "繁體中文" })).toHaveFocus();
    fireEvent.keyDown(menu, { key: "ArrowUp" });
    expect(screen.getByRole("menuitem", { name: "中文" })).toHaveFocus();
    fireEvent.keyDown(menu, { key: "Enter" });

    expect(onChange).toHaveBeenCalledWith("zh");
    expect(trigger).toHaveFocus();
  });
});
