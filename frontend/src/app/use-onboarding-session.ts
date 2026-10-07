import { useCallback, useEffect, useState } from "react";
import { api, APP_API_BASE, RequestError } from "../api";
import type { OnboardingSession } from "../types";
import { checkSession } from "./session-store";

/* The member's guided-setup session: fetched once on mount (404 means
 * there is none, not a failure) and then followed over the session
 * events stream, which sends the whole record on every version change.
 * The stream is reopened when the session id changes so a session
 * begun after the first connection is followed too. A stale snapshot
 * (an older version of the same session) never overwrites a newer one,
 * whichever path it arrived by. */
export function useOnboardingSession() {
  const [session, setSessionState] = useState<OnboardingSession | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const setSession = useCallback((next: OnboardingSession | null) => {
    setSessionState((current) => {
      if (
        next &&
        current &&
        current.id === next.id &&
        next.version < current.version
      ) {
        return current;
      }
      return next;
    });
  }, []);

  const refresh = useCallback(
    () =>
      api
        .onboardingSession()
        .then((next) => {
          setSession(next);
          setError("");
        })
        .catch((reason: unknown) => {
          if (reason instanceof RequestError && reason.status === 404) {
            setSessionState(null);
            setError("");
            return;
          }
          setError(
            reason instanceof Error ? reason.message : "Unable to load the setup session",
          );
        }),
    [setSession],
  );

  useEffect(() => {
    void refresh().finally(() => setLoading(false));
  }, [refresh]);

  const sessionId = session?.id ?? null;
  useEffect(() => {
    if (loading) return;
    const source = new EventSource(`${APP_API_BASE}/onboarding/session/events`);
    source.addEventListener("session", (event) => {
      setSession(
        JSON.parse((event as MessageEvent<string>).data) as OnboardingSession,
      );
    });
    /* EventSource reconnects on its own; the session is also refetched
     * on every screen action, so a dropped stream is not surfaced. One
     * the server refused may mean the sign-in session has ended. */
    source.onerror = () => {
      if (source.readyState === EventSource.CLOSED) void checkSession();
    };
    return () => source.close();
  }, [loading, sessionId, setSession]);

  return { session, loading, error, refresh, setSession };
}
