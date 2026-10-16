import { useEffect, useRef, useState } from "react";
import {
  createFileRoute,
  Outlet,
  useLocation,
  useNavigate,
  useRouter,
} from "@tanstack/react-router";
import { LogoSplash, type LogoSplashDirection } from "@/ds/components/logo-splash";
import { Spinner } from "@/ds/components/spinner";
import { ToastProvider } from "@/ds/components/toast";
import { TooltipProvider } from "@/ds/components/tooltip";
import { ThemeProvider } from "@/ds/theme/theme-provider";
import { api } from "../api";
import { useOAuthOutcome } from "../app/oauth-outcome";
import { AccessScreen } from "../app/access";
import { commitStagedLoginMethod } from "../app/login-method";
import { consumeLoginSplash } from "../app/login-splash";
import { SessionContext } from "../app/principal";
import { ReferenceCards } from "../app/reference-cards";
import { checkSession, ensureSession, refreshSession, setSession, useSessionState } from "../app/session-store";
import { readPendingOAuth } from "../app/oauth-consent";
import { ensureQueue } from "../app/queue-store";
import { Shell } from "../app/shell";

/* The authenticated app: providers, the session gate, and the shell
 * around every workspace route. The session loads here, alongside the
 * page's own loader, so the shell and the page arrive together. It is
 * fetched once per tab; sign-in and sign-out replace it explicitly. The
 * queue's notified layer loads beside it, so the count on the sidebar's
 * queue entry is there at first paint rather than a beat later; without
 * a session the fetch is refused and retried once sign-in invalidates
 * the loaders. */
export const Route = createFileRoute("/_app")({
  loader: async () => {
    await Promise.all([ensureSession(), ensureQueue()]);
  },
  staleTime: Infinity,
  pendingComponent: Booting,
  component: AppLayout,
});

/* Only once the session fetch has stalled past the router's pending
 * threshold; a quick start never shows it. */
function Booting() {
  return (
    <div className="ds-root flex min-h-screen items-center justify-center gap-2.5">
      <Spinner size={16} />
      <span className="text-body text-ink-muted">Loading withHuman</span>
    </div>
  );
}

function AppLayout() {
  return (
    <ThemeProvider>
      <ToastProvider>
        <TooltipProvider>
          <SessionGate />
        </TooltipProvider>
      </ToastProvider>
    </ThemeProvider>
  );
}

function SessionGate() {
  // Consume OAuth feedback before onboarding redirects away from the callback page.
  useOAuthOutcome();
  const { session, error } = useSessionState();
  /* The brand splash plays over the shell once per sign-in (the W
   * unfolding into the lockup: raised by the access screen here, or
   * left as a flag by a ceremony that ended in a full navigation) and
   * once per sign-out (the lockup collapsing back into the W). */
  const [splash, setSplash] = useState<LogoSplashDirection | null>(() =>
    consumeLoginSplash() ? "expand" : null,
  );
  const navigate = useNavigate();
  const location = useLocation();
  const router = useRouter();

  /* A session that arrived through a sign-in (in the gate here, or a
   * ceremony that ended in a full navigation) settles the method the
   * access screen marks as last used next time. */
  const signedIn = session !== null;
  useEffect(() => {
    if (signedIn) commitStagedLoginMethod();
  }, [signedIn]);

  /* A tab left open past its session goes to sign-in when the person
   * comes back to it, before they act on a page that can no longer load. */
  useEffect(() => {
    if (!signedIn) return;
    const check = () => {
      if (document.visibilityState === "visible") void checkSession();
    };
    document.addEventListener("visibilitychange", check);
    window.addEventListener("focus", check);
    return () => {
      document.removeEventListener("visibilitychange", check);
      window.removeEventListener("focus", check);
    };
  }, [signedIn]);

  /* A person who can run the guided setup lands on it while the
   * organization has not finished or skipped it. Once per page load:
   * someone who leaves /welcome for another page is not bounced back
   * until they reload. */
  const onboardingChecked = useRef(false);
  const onboardingRequired = session?.onboarding?.required === true;
  useEffect(() => {
    if (!onboardingRequired || onboardingChecked.current) return;
    onboardingChecked.current = true;
    if (location.pathname !== "/welcome" && location.pathname !== "/connect/authorize") {
      void navigate({ to: "/welcome", replace: true });
    }
  }, [onboardingRequired, location.pathname, navigate]);

  const splashOverlay = splash ? (
    <LogoSplash direction={splash} onDone={() => setSplash(null)} />
  ) : null;
  if (!session) {
    return (
      <>
        <AccessScreen
          onAuthenticated={(principal) => {
            setSplash("expand");
            setSession({ principal, assignments: [] });
            /* Route loaders ran before the session existed and were
             * refused; run them again now that requests carry it, and
             * fetch the enriched identity (name, organization, assignments). */
            void router.invalidate();
            void refreshSession();
          }}
          initialError={error}
        />
        {splashOverlay}
      </>
    );
  }
  /* A consent page that had to sign the person in through a provider
   * lands here afterwards; send them back to finish authorizing. */
  const pendingOAuth = readPendingOAuth();
  if (pendingOAuth !== null && location.pathname !== "/oauth/authorize") {
    window.location.replace(`/oauth/authorize${pendingOAuth}`);
    return null;
  }
  const signOut = () => {
    setSplash("collapse");
    void api.logout().finally(() => setSession(null));
  };
  return (
    <SessionContext.Provider value={{ ...session, signOut }}>
      <ReferenceCards>
        <Shell onSignOut={signOut}>
          <Outlet />
        </Shell>
      </ReferenceCards>
      {splashOverlay}
    </SessionContext.Provider>
  );
}
