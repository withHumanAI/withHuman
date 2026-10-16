// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider } from "@/ds/theme/theme-provider";
import type { Session } from "../types";
import { AccountMenu } from "./account-menu";
import { SessionContext } from "./principal";
import { stubWebStorage } from "./test-storage";

beforeEach(stubWebStorage);
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

async function renderMenu() {
  vi.stubGlobal("matchMedia", vi.fn(() => ({
    matches: false,
    addEventListener() {},
    removeEventListener() {},
  })));
  const session = {
    principal: { actor_type: "human", actor_id: "u1", organization_id: "org-1", permissions: [], grants: [] },
    assignments: [],
    signOut: () => {},
  } as unknown as Session & { signOut: () => void };
  const root = createRootRoute({
    component: () => (
      <SessionContext.Provider value={session}>
        <ThemeProvider>
          <AccountMenu onSignOut={() => {}} />
        </ThemeProvider>
      </SessionContext.Provider>
    ),
  });
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
}

describe("account menu", () => {
  it("links to the community Discord in a new tab", async () => {
    await renderMenu();
    fireEvent.click(await screen.findByRole("button", { name: "Account menu" }));
    const link = await screen.findByRole("menuitem", { name: "Join our Discord" });
    expect(link.getAttribute("href")).toBe("https://discord.gg/fwSryh9p8");
    expect(link.getAttribute("target")).toBe("_blank");
  });
});
