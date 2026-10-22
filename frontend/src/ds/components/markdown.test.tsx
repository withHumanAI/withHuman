// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Markdown } from "./markdown";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Markdown", () => {
  it("links a bare web address, opening it in a new tab", () => {
    render(<Markdown>{"Fixing the bug in https://github.com/example/repo/issues/42 before the release."}</Markdown>);
    const link = screen.getByRole("link", { name: "https://github.com/example/repo/issues/42" });
    expect(link.getAttribute("href")).toBe("https://github.com/example/repo/issues/42");
    expect(link.getAttribute("target")).toBe("_blank");
    expect(link.getAttribute("rel")).toBe("noopener noreferrer");
  });

  it("renders neither raw HTML nor a script link", () => {
    const { container } = render(<Markdown>{'<b>bold</b> [click](javascript:alert(1))'}</Markdown>);
    expect(container.querySelector("b")).toBeNull();
    expect(screen.getByText("click").getAttribute("href") ?? "").not.toContain("javascript");
  });

  it("collapses long text behind Show more", () => {
    vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockReturnValue(400);
    vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockReturnValue(120);
    const { container } = render(<Markdown lines={5}>{"A long explanation."}</Markdown>);
    const text = container.querySelector<HTMLElement>("[style]");
    expect(text?.style.webkitLineClamp).toBe("5");
    fireEvent.click(screen.getByRole("button", { name: "Show more" }));
    expect(text?.style.webkitLineClamp).toBe("");
    fireEvent.click(screen.getByRole("button", { name: "Show less" }));
    expect(screen.getByRole("button", { name: "Show more" })).toBeTruthy();
  });

  it("shows no toggle when the text fits", () => {
    render(<Markdown lines={5}>{"A short explanation."}</Markdown>);
    expect(screen.queryByRole("button")).toBeNull();
  });
});
