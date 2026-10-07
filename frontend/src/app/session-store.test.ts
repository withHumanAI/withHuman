import { afterEach, describe, expect, it, vi } from "vitest";
import type { Session } from "../types";

const session = { principal: { actor_id: "user-1" }, assignments: [] } as unknown as Session;

function unauthenticated() {
  return new Response(JSON.stringify({ error: { code: "unauthenticated", message: "Sign in" } }), { status: 401 });
}

function ok(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200 });
}

/* The store and its listener live at module scope, so every test loads
 * fresh copies of both. */
async function load(responses: Response[]) {
  vi.resetModules();
  const fetch = vi.fn(async () => responses.shift() ?? ok(session));
  vi.stubGlobal("fetch", fetch);
  const store = await import("./session-store");
  const { api } = await import("../api");
  store.setSession(session);
  return { store, api, fetch };
}

/* Lets the listener's /me check settle. */
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("session store", () => {
  it("signs the tab out once a refused request finds the session gone", async () => {
    const { store, api, fetch } = await load([unauthenticated(), unauthenticated()]);

    await expect(api.approvals("visible")).rejects.toThrow("Sign in");
    await settle();

    expect(fetch).toHaveBeenLastCalledWith("/api/v1/me", expect.anything());
    expect(store.currentSession()).toBeNull();
  });

  it("keeps a session that /me still answers for", async () => {
    // A refusal that predates a sign-in must not sign the new session out.
    const { store, api } = await load([unauthenticated(), ok(session)]);

    await expect(api.approvals("visible")).rejects.toThrow("Sign in");
    await settle();

    expect(store.currentSession()).toBe(session);
  });

  it("leaves the session alone when the check cannot reach the server", async () => {
    const { store } = await load([new Response("", { status: 502 })]);

    await store.checkSession();

    expect(store.currentSession()).toBe(session);
  });

  it("ignores refusals that are not about the session", async () => {
    const wrongPassword = new Response(
      JSON.stringify({ error: { code: "invalid_credentials", message: "Wrong password" } }),
      { status: 401 },
    );
    const { store, api, fetch } = await load([wrongPassword]);

    await expect(api.approvals("visible")).rejects.toThrow("Wrong password");
    await settle();

    expect(fetch).toHaveBeenCalledTimes(1);
    expect(store.currentSession()).toBe(session);
  });
});
