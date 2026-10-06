// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ToastProvider } from "@/ds/components/toast";
import { TooltipProvider } from "@/ds/components/tooltip";
import type { ApprovalRequest } from "../types";
import { QueueScreen } from "./queue";
import { validateQueueSearch, type QueueFilter } from "./queue-search";

const useQueue = vi.fn((_scope: string) => ({ requests, loaded: true, loadFailed: false }));
vi.mock("./queue-store", () => ({
  useQueue: (scope: string) => useQueue(scope),
  refreshQueue: vi.fn(),
}));
vi.mock("./request-detail", () => ({ RequestDetail: () => null }));

const requests = ["pending", "pending", "approved"].map((status, index) => ({
  id: `request-${index}`,
  agent_name: "Test agent",
  agent_slug: "test-agent",
  agent_instance_name: "Test instance",
  tool: `tool-${index}`,
  arguments: {},
  status,
  created_at: "2026-09-21T10:00:00Z",
}) as ApprovalRequest);

beforeEach(() => {
  vi.stubGlobal("ResizeObserver", class {
    observe() {}
    unobserve() {}
    disconnect() {}
  });
  vi.stubGlobal("scrollTo", vi.fn());
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function renderQueue(split = false, filter: QueueFilter = "pending") {
  vi.stubGlobal("matchMedia", vi.fn(() => ({
    matches: split,
    addEventListener() {},
    removeEventListener() {},
  })));
  const root = createRootRoute({
    component: () => <ToastProvider><TooltipProvider><Outlet /></TooltipProvider></ToastProvider>,
  });
  const queue = createRoute({
    getParentRoute: () => root,
    path: "/queue",
    validateSearch: validateQueueSearch,
    component: () => <>
      <QueueScreen openID={null} preloaded={null} />
      <Link to="/queue/$requestId" params={{ requestId: "reference" }}>Reference link</Link>
    </>,
  });
  const loader = vi.fn(async ({ params }: { params: { requestId: string } }) => params.requestId);
  const detail = createRoute({
    getParentRoute: () => root,
    path: "/queue/$requestId",
    validateSearch: validateQueueSearch,
    loader,
    component: () => <div>Request opened</div>,
  });
  const search = filter === "pending" ? {} : { filter };
  const router = createRouter({
    routeTree: root.addChildren([queue, detail]),
    history: createMemoryHistory({ initialEntries: [filter === "pending" ? "/queue" : `/queue?filter=${filter}`] }),
    defaultPreload: "intent",
    defaultPreloadStaleTime: 10_000,
    defaultStaleTime: 10_000,
  });
  await router.load();
  render(<RouterProvider router={router} />);
  const list = await screen.findByRole("listbox", { name: "Review queue" });
  const rows = within(list).getAllByRole("option");
  const preload = vi.spyOn(router, "preloadRoute");
  vi.useFakeTimers();
  return { router, loader, preload, rows, search };
}

async function advance(ms: number) {
  await act(async () => { await vi.advanceTimersByTimeAsync(ms); });
}

describe("review queue intent preloading", () => {
  it.each([
    [false, "pending"], [false, "resolved"], [false, "all"],
    [true, "pending"], [true, "resolved"], [true, "all"],
  ] as const)("preloads on hover with split=%s and filter=%s", async (split, filter) => {
    const { router, loader, preload, rows, search } = await renderQueue(split, filter);
    const id = filter === "resolved" ? "request-2" : "request-0";
    const location = router.state.location.href;
    fireEvent.mouseEnter(rows[0]);
    expect(rows[0].dataset.highlighted).toBe("true");
    await advance(49);
    expect(loader).not.toHaveBeenCalled();
    await advance(1);
    expect(preload).toHaveBeenCalledWith(expect.objectContaining({
      to: "/queue/$requestId", params: { requestId: id }, search,
    }));
    expect(loader).toHaveBeenCalledTimes(1);
    expect(router.state.location.href).toBe(location);
    expect(rows[0].getAttribute("aria-selected")).toBe("false");

    fireEvent.mouseLeave(rows[0]);
    fireEvent.mouseEnter(rows[0]);
    await advance(50);
    expect(loader).toHaveBeenCalledTimes(1);
    await advance(10_001);
    fireEvent.mouseLeave(rows[0]);
    fireEvent.mouseEnter(rows[0]);
    await advance(50);
    expect(loader).toHaveBeenCalledTimes(2);

    fireEvent.click(rows[0]);
    await advance(0);
    expect(router.state.location.pathname).toBe(`/queue/${id}`);
    expect(router.state.location.search).toEqual(search);
    expect(loader).toHaveBeenCalledTimes(2);
  });

  it("switches layers from the heading menu and keeps the status filter", async () => {
    const { router } = await renderQueue(false, "all");
    expect(useQueue).toHaveBeenLastCalledWith("notified");
    const heading = screen.getByRole("button", { name: "Queue layer" });
    expect(heading.textContent).toContain("Sent to you");

    const choose = async (label: string) => {
      /* The menu opens from the keyboard, the interaction jsdom can
       * follow; the pointer path is Base UI's own. */
      fireEvent.keyDown(heading, { key: "ArrowDown" });
      await advance(0);
      const item = screen.getByRole("menuitem", { name: new RegExp(label) });
      fireEvent.click(item);
      await advance(0);
    };
    await choose("Yours to decide");
    expect(router.state.location.search).toEqual({ filter: "all", scope: "decidable" });
    expect(useQueue).toHaveBeenLastCalledWith("decidable");
    expect(heading.textContent).toContain("Yours to decide");

    await choose("All visible");
    expect(router.state.location.search).toEqual({ filter: "all", scope: "visible" });

    // Back to the default layer, which stays out of the URL.
    await choose("Sent to you");
    expect(router.state.location.search).toEqual({ filter: "all" });
    expect(useQueue).toHaveBeenLastCalledWith("notified");
  });

  it("uses the same delay and hover/focus cancellation as ordinary links", async () => {
    const { loader, rows } = await renderQueue();
    const link = screen.getByRole("link", { name: "Reference link" });
    for (const element of [rows[0], link]) {
      fireEvent.mouseEnter(element);
      await advance(25);
      fireEvent.mouseLeave(element);
      await advance(50);
      expect(loader).not.toHaveBeenCalled();
      fireEvent.focus(element);
      await advance(25);
      fireEvent.blur(element);
      await advance(50);
      expect(loader).not.toHaveBeenCalled();
    }
  });

  it("preloads the row reached by arrow keys without opening or selecting it", async () => {
    const { router, loader, rows } = await renderQueue();
    fireEvent.keyDown(rows[0], { key: "ArrowDown" });
    expect(document.activeElement).toBe(rows[1]);
    expect(rows[1].dataset.highlighted).toBe("true");
    await advance(50);
    expect(loader).toHaveBeenCalledWith(expect.objectContaining({ params: { requestId: "request-1" } }));
    expect(router.state.location.pathname).toBe("/queue");
    expect(rows[1].getAttribute("aria-selected")).toBe("false");
    fireEvent.keyDown(rows[1], { key: "Enter" });
    await advance(0);
    expect(router.state.location.pathname).toBe("/queue/request-1");
    expect(loader).toHaveBeenCalledTimes(1);
  });

  it("preloads immediately on touch without navigating", async () => {
    const { router, loader, rows } = await renderQueue(true);
    fireEvent.touchStart(rows[0]);
    await advance(0);
    expect(loader).toHaveBeenCalledTimes(1);
    expect(router.state.location.pathname).toBe("/queue");
  });

  it("retains checkbox, modifier, range and keyboard selection", async () => {
    const { router, rows } = await renderQueue(false, "all");
    fireEvent.click(within(rows[0]).getByRole("checkbox"));
    expect(rows[0].getAttribute("aria-selected")).toBe("true");
    fireEvent.click(rows[2], { shiftKey: true });
    expect(rows.every(row => row.getAttribute("aria-selected") === "true")).toBe(true);
    fireEvent.click(rows[1], { metaKey: true });
    expect(rows[1].getAttribute("aria-selected")).toBe("false");
    fireEvent.keyDown(rows[1], { key: " " });
    expect(rows[1].getAttribute("aria-selected")).toBe("true");
    fireEvent.keyDown(rows[1], { key: "Escape" });
    expect(rows.every(row => row.getAttribute("aria-selected") === "false")).toBe(true);
    expect(router.state.location.pathname).toBe("/queue");
  });

  it.each(["view", "double-click"])("retains opening through %s", async (method) => {
    const { router, rows } = await renderQueue();
    if (method === "view") fireEvent.click(within(rows[0]).getByRole("button", { name: "View" }));
    else fireEvent.doubleClick(rows[0]);
    await advance(0);
    expect(router.state.location.pathname).toBe("/queue/request-0");
  });
});

describe("review queue load failure", () => {
  it("says the queue could not load only when no snapshot ever arrived", async () => {
    useQueue.mockReturnValue({ requests: [], loaded: false, loadFailed: true });
    vi.stubGlobal("matchMedia", vi.fn(() => ({
      matches: false,
      addEventListener() {},
      removeEventListener() {},
    })));
    const root = createRootRoute({
      component: () => <ToastProvider><TooltipProvider><Outlet /></TooltipProvider></ToastProvider>,
    });
    const queue = createRoute({
      getParentRoute: () => root,
      path: "/queue",
      validateSearch: validateQueueSearch,
      component: () => <QueueScreen openID={null} preloaded={null} />,
    });
    const router = createRouter({
      routeTree: root.addChildren([queue]),
      history: createMemoryHistory({ initialEntries: ["/queue"] }),
    });
    await router.load();
    render(<RouterProvider router={router} />);

    expect(await screen.findByText("The queue could not load")).toBeTruthy();
    expect(screen.queryByRole("listbox", { name: "Review queue" })).toBeNull();
    useQueue.mockReset();
    useQueue.mockImplementation(() => ({ requests, loaded: true, loadFailed: false }));
  });
});
