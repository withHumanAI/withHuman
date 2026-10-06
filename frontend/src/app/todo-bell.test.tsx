// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { afterEach, describe, expect, it } from "vitest";
import type { TodoItem } from "./extensions";
import { TodoBell } from "./todo-bell";

afterEach(cleanup);

async function renderBell(items: TodoItem[]) {
  const root = createRootRoute({ component: () => <Outlet /> });
  const queue = createRoute({
    getParentRoute: () => root,
    path: "/queue",
    component: () => <TodoBell items={items} />,
  });
  const router = createRouter({
    routeTree: root.addChildren([queue]),
    history: createMemoryHistory({ initialEntries: ["/queue"] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
}

const connection: TodoItem = {
  key: "connection:github/write",
  title: "Connect GitHub · Write",
  description: "Claude Code uses it with your own account.",
  to: "/settings/connections",
};

describe("TodoBell", () => {
  it("carries no mark and says you are all set when nothing is left", async () => {
    await renderBell([]);
    const bell = await screen.findByRole("button", { name: "To do" });
    expect(bell.querySelector("[aria-hidden].rounded-full")).toBeNull();
    fireEvent.click(bell);
    expect(await screen.findByText("You're all set.")).toBeTruthy();
  });

  it("marks the bell and links each item to where it gets done", async () => {
    await renderBell([connection, { ...connection, key: "connection:github/read", title: "Connect GitHub · Read" }]);
    const bell = await screen.findByRole("button", { name: "To do, 2 things" });
    expect(bell.querySelector("[aria-hidden].rounded-full")).not.toBeNull();
    fireEvent.click(bell);
    const item = await screen.findByRole("menuitem", { name: /Connect GitHub · Write/ });
    expect(item.getAttribute("href")).toBe("/settings/connections");
    expect(item.textContent).toContain("Claude Code uses it with your own account.");
  });
});
