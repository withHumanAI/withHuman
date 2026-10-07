import { useCallback, useMemo, useSyncExternalStore } from "react";
import { api, APP_API_BASE } from "../api";
import type { ApprovalRequest, ApprovalReview } from "../types";
import { DEFAULT_QUEUE_SCOPE, type QueueScope } from "./queue-search";
import { checkSession } from "./session-store";

/* The live review queue, shared by every screen that shows it.
 *
 * One snapshot per queue layer lives at module scope for the life of the
 * tab. Route loaders await the first snapshot of the layer they show, so
 * a queue screen never paints its empty state on the way in; the events
 * stream (which sends a full snapshot on connect and on every change)
 * keeps a layer current while any screen is subscribed to it, and a
 * screen that comes back later renders the last snapshot instantly while
 * the stream reconnects. Switching layers switches streams; the layer
 * left behind keeps its snapshot for the next visit. */

export interface QueueState {
  requests: ApprovalRequest[];
  /* True once any snapshot has arrived: tells "nothing yet" from
   * "nothing". */
  loaded: boolean;
  /* True while no snapshot has arrived and the last attempt to fetch one
   * failed. Once a snapshot exists a failure changes nothing on screen:
   * the stream reconnects and its next snapshot replaces the old one. */
  loadFailed: boolean;
}

/* A stream stays open this long after the last subscriber leaves, so
 * moving between the list and a request, or a quick detour to another
 * page, does not tear the connection down and back up. */
const IDLE_CLOSE_MS = 5_000;
/* How long a stream the browser gave up on waits before it is reopened. */
const RECONNECT_MS = 5_000;

interface Layer {
  scope: QueueScope;
  state: QueueState;
  listeners: Set<() => void>;
  subscribers: number;
  source: EventSource | null;
  idleClose: ReturnType<typeof setTimeout> | null;
  reconnect: ReturnType<typeof setTimeout> | null;
  inflight: Promise<void> | null;
}

const layers = new Map<QueueScope, Layer>();

function layer(scope: QueueScope): Layer {
  let found = layers.get(scope);
  if (!found) {
    found = {
      scope,
      state: { requests: [], loaded: false, loadFailed: false },
      listeners: new Set(),
      subscribers: 0,
      source: null,
      idleClose: null,
      reconnect: null,
      inflight: null,
    };
    layers.set(scope, found);
  }
  return found;
}

function set(target: Layer, patch: Partial<QueueState>) {
  target.state = { ...target.state, ...patch };
  for (const listener of target.listeners) listener();
}

/* Fetches a fresh snapshot of one layer. A failure never throws, so a
 * route loader never trips the router's error boundary (the session gate
 * handles an expired session); before the first snapshot it sets
 * loadFailed, after it the last snapshot stays. */
export function refreshQueue(scope: QueueScope = DEFAULT_QUEUE_SCOPE): Promise<void> {
  const target = layer(scope);
  if (target.inflight) return target.inflight;
  target.inflight = api
    .approvals(scope)
    .then(({ requests }) => set(target, { requests, loaded: true, loadFailed: false }))
    .catch(() => {
      if (!target.state.loaded) set(target, { loadFailed: true });
    })
    .finally(() => {
      target.inflight = null;
    });
  return target.inflight;
}

/* For route loaders: resolves as soon as a snapshot of the layer exists.
 * A revisit resolves immediately; the stream brings anything newer. */
export function ensureQueue(scope: QueueScope = DEFAULT_QUEUE_SCOPE): Promise<void> {
  return layer(scope).state.loaded ? Promise.resolve() : refreshQueue(scope);
}

function connect(target: Layer) {
  if (target.source) return;
  const source = new EventSource(
    `${APP_API_BASE}/events?${new URLSearchParams({ scope: target.scope })}`,
  );
  source.addEventListener("queue", (event) => {
    const requests = JSON.parse(
      (event as MessageEvent<string>).data,
    ) as ApprovalRequest[];
    set(target, { requests, loaded: true, loadFailed: false });
  });
  source.onerror = () => {
    if (!target.state.loaded) set(target, { loadFailed: true });
    /* The browser retries a dropped connection itself but gives up for
     * good when the server answers with an error (a proxy's 502 during a
     * deploy, or a 401 once the session has ended), so a closed stream
     * checks the session and is reopened here while anyone watches. */
    if (source.readyState !== EventSource.CLOSED || target.source !== source) return;
    void checkSession();
    target.source = null;
    target.reconnect ??= setTimeout(() => {
      target.reconnect = null;
      if (target.subscribers > 0) connect(target);
    }, RECONNECT_MS);
  };
  target.source = source;
}

function disconnect(target: Layer) {
  if (target.reconnect) {
    clearTimeout(target.reconnect);
    target.reconnect = null;
  }
  target.source?.close();
  target.source = null;
}

function subscribe(target: Layer, listener: () => void) {
  target.listeners.add(listener);
  target.subscribers += 1;
  if (target.idleClose) {
    clearTimeout(target.idleClose);
    target.idleClose = null;
  }
  connect(target);
  return () => {
    target.listeners.delete(listener);
    target.subscribers -= 1;
    if (target.subscribers === 0 && !target.idleClose) {
      target.idleClose = setTimeout(() => {
        target.idleClose = null;
        if (target.subscribers === 0) disconnect(target);
      }, IDLE_CLOSE_MS);
    }
  };
}

/* Subscribes the calling component to one live layer of the queue. */
export function useQueue(scope: QueueScope = DEFAULT_QUEUE_SCOPE): QueueState {
  const subscribeLayer = useCallback(
    (listener: () => void) => subscribe(layer(scope), listener),
    [scope],
  );
  const snapshot = useCallback(() => layer(scope).state, [scope]);
  return useSyncExternalStore(subscribeLayer, snapshot, snapshot);
}

/* The number of requests sent to this person that still wait on them:
 * the pending part of the notified layer. The shell shows it beside
 * the queue entry, so the layer stays live while the app is open. */
export function useNotifiedPendingCount(): number {
  const { requests } = useQueue("notified");
  return useMemo(
    () => requests.filter((request) => request.status === "pending").length,
    [requests],
  );
}

/* Review context a route loader fetched ahead of the detail panel, so
 * the reviewer and activity sections render with the request instead of
 * arriving a beat later. The panel still refetches on mount to follow
 * status and claim changes; identical data costs no layout. */
const reviews = new Map<string, ApprovalReview>();

export function preloadReview(id: string): Promise<void> {
  return api
    .approvalReview(id)
    .then((review) => {
      reviews.set(id, review);
    })
    .catch(() => undefined);
}

export function preloadedReview(id: string): ApprovalReview | null {
  return reviews.get(id) ?? null;
}
