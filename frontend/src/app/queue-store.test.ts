// @vitest-environment jsdom
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ApprovalRequest } from "../types";

const approvals = vi.fn();
vi.mock("../api", () => ({ api: { approvals: () => approvals() }, APP_API_BASE: "/api/v1" }));
const checkSession = vi.fn();
vi.mock("./session-store", () => ({ checkSession: () => checkSession() }));

class FakeEventSource {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 2;
  static opened: FakeEventSource[] = [];
  readyState = FakeEventSource.CONNECTING;
  onerror: (() => void) | null = null;
  private listeners = new Map<string, (event: MessageEvent<string>) => void>();

  constructor(readonly url: string) {
    FakeEventSource.opened.push(this);
  }

  addEventListener(type: string, listener: (event: MessageEvent<string>) => void) {
    this.listeners.set(type, listener);
  }

  close() {
    this.readyState = FakeEventSource.CLOSED;
  }

  snapshot(requests: ApprovalRequest[]) {
    this.readyState = FakeEventSource.OPEN;
    this.listeners.get("queue")?.({ data: JSON.stringify(requests) } as MessageEvent<string>);
  }

  fail(readyState: number) {
    this.readyState = readyState;
    this.onerror?.();
  }
}

const request = { id: "request-0", status: "pending" } as ApprovalRequest;

/* The store keeps its layers at module scope, so every test loads a
 * fresh copy. */
async function load() {
  vi.resetModules();
  return import("./queue-store");
}

beforeEach(() => {
  FakeEventSource.opened = [];
  vi.stubGlobal("EventSource", FakeEventSource);
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  approvals.mockReset();
  checkSession.mockReset();
});

describe("queue store", () => {
  it("keeps a loaded snapshot without comment when the stream drops", async () => {
    const { ensureQueue, useQueue } = await load();
    approvals.mockResolvedValue({ requests: [request] });
    await ensureQueue("visible");
    const { result } = renderHook(() => useQueue("visible"));

    act(() => FakeEventSource.opened[0].fail(FakeEventSource.CONNECTING));

    expect(result.current).toEqual({ requests: [request], loaded: true, loadFailed: false });
    expect(checkSession).not.toHaveBeenCalled();
  });

  it("reports a queue that never loaded until a snapshot arrives", async () => {
    const { ensureQueue, useQueue } = await load();
    approvals.mockRejectedValue(new Error("unreachable"));
    await ensureQueue("visible");
    const { result } = renderHook(() => useQueue("visible"));
    expect(result.current.loadFailed).toBe(true);

    act(() => FakeEventSource.opened[0].snapshot([request]));

    expect(result.current).toEqual({ requests: [request], loaded: true, loadFailed: false });
  });

  it("reopens a stream the browser closed while someone is watching", async () => {
    const { useQueue } = await load();
    vi.useFakeTimers();
    const { unmount } = renderHook(() => useQueue("visible"));
    expect(FakeEventSource.opened).toHaveLength(1);

    act(() => FakeEventSource.opened[0].fail(FakeEventSource.CLOSED));
    // The refusal may be the session ending, which signs the tab out.
    expect(checkSession).toHaveBeenCalledTimes(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(4_999); });
    expect(FakeEventSource.opened).toHaveLength(1);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(FakeEventSource.opened).toHaveLength(2);

    // Nobody left to watch: a later close is not reopened.
    unmount();
    act(() => FakeEventSource.opened[1].fail(FakeEventSource.CLOSED));
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
    expect(FakeEventSource.opened).toHaveLength(2);
  });
});
