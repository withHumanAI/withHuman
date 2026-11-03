import type { LoginResponse } from "../types";

/* The withHuman desktop app's sign-in bridge. Google refuses sign-in in
 * an embedded window, so the app runs provider and SSO sign-in in the
 * default browser through the native hand-off and returns what the
 * exchange answered. Its preload puts the bridge on window for the
 * server's own origin only; a browser never has one. */
export type DesktopSignIn =
  | { kind: "social"; provider: string }
  | { kind: "sso"; organization: string };

/* The exchange's login answer, the error code the callback or the
 * exchange came back with, or a hand-off a newer sign-in replaced. */
export type DesktopSignInResult =
  | { status: "answered"; response: LoginResponse }
  | { status: "failed"; code: string }
  | { status: "superseded" };

export interface DesktopBridge {
  signIn: (request: DesktopSignIn) => Promise<DesktopSignInResult>;
}

export function desktopBridge(): DesktopBridge | null {
  const bridge = (window as { withhumanDesktop?: Partial<DesktopBridge> }).withhumanDesktop;
  return typeof bridge?.signIn === "function" ? (bridge as DesktopBridge) : null;
}
