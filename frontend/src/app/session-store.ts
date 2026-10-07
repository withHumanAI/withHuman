import { useSyncExternalStore } from "react";
import { api, onUnauthenticated, RequestError } from "../api";
import type { Session } from "../types";

/* The signed-in session, held at module scope for the life of the tab.
 *
 * The /_app route loader awaits the first fetch, so the shell renders
 * with the session in hand (in parallel with the page's own loader)
 * instead of behind a spinner. Signing in replaces the session in place
 * and signing out clears it; both are followed by a router invalidation
 * so route loaders run again under the new identity. A session that ends
 * on the server (it expired, or was signed out elsewhere) clears it too,
 * as soon as a request is refused or the tab comes back into view. */

export interface SessionState {
  /* Null while nobody is signed in. */
  session: Session | null;
  /* True once the first fetch has settled, whatever it found. */
  resolved: boolean;
  /* A failure other than "not signed in", for the sign-in screen. */
  error: string;
}

let state: SessionState = { session: null, resolved: false, error: "" };
const listeners = new Set<() => void>();
let inflight: Promise<void> | null = null;
let checking: Promise<void> | null = null;

function set(patch: Partial<SessionState>) {
  state = { ...state, ...patch };
  for (const listener of listeners) listener();
}

/* Fetches /me. Not signed in is a normal outcome, not an error. */
export function refreshSession(): Promise<void> {
  if (inflight) return inflight;
  inflight = api
    .me()
    .then((session) => set({ session, resolved: true, error: "" }))
    .catch((reason: unknown) => {
      const signedOut = reason instanceof RequestError && reason.status === 401;
      set({
        session: signedOut ? null : state.session,
        resolved: true,
        error: signedOut
          ? ""
          : reason instanceof Error
            ? reason.message
            : "Unable to load session",
      });
    })
    .finally(() => {
      inflight = null;
    });
  return inflight;
}

/* Signs the tab out if its session has ended on the server. A refused
 * request may predate a sign-in, so /me decides; any other outcome
 * leaves the session as it is. */
export function checkSession(): Promise<void> {
  if (!state.session) return Promise.resolve();
  if (checking) return checking;
  checking = api
    .me()
    .then(
      () => undefined,
      (reason: unknown) => {
        if (reason instanceof RequestError && reason.status === 401) {
          set({ session: null, error: "" });
        }
      },
    )
    .finally(() => {
      checking = null;
    });
  return checking;
}

onUnauthenticated(() => void checkSession());

/* For the /_app route loader: resolves once a session state exists. */
export function ensureSession(): Promise<void> {
  return state.resolved ? Promise.resolve() : refreshSession();
}

/* For route loaders that decide what to fetch by what the person
 * holds; call after ensureSession() so the answer is settled. */
export function currentSession(): Session | null {
  return state.session;
}

export function setSession(session: Session | null) {
  set({ session, resolved: true, error: "" });
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function snapshot() {
  return state;
}

export function useSessionState(): SessionState {
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}
