// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, RequestError } from "../api";
import { AccessScreen } from "./access";
import type { DesktopSignInResult } from "./desktop-bridge";
import { registerExtensions, type SSODoor } from "./extensions";
import { stubWebStorage } from "./test-storage";
import { ensureRuntimeConfig } from "./use-runtime-config";

/* The door the way the hosted overlay registers one. Each group sets
 * the registry the way its edition would, so the file's order does not
 * matter. */
const SSO_DOOR: SSODoor = {
  beginURL: (slug) => `/sso/${slug}`,
  errors: { invitation_required: "You need an invitation first", sso_failed: "Single sign-on failed" },
};

const SSO_ORGANIZATIONS = [
  { id: "1", slug: "acme", name: "Acme", sso: true },
  { id: "2", slug: "globex", name: "Globex", sso: true },
];

const runtimeConfig = {
  public_origin: "http://localhost",
  version: "test",
  bootstrap_enabled: false,
  custom_permissions_enabled: false,
  webhook_enabled: false,
  slack_enabled: false,
  discord_enabled: false,
  social_providers: [
    { key: "google", label: "Google" },
    { key: "github", label: "GitHub" },
  ],
  password_login_offers_signup: false,
  cli_download_url: "",
  agent_mcp_servers: [],
  update_checks_enabled: false,
};

beforeEach(async () => {
  stubWebStorage();
  vi.spyOn(api, "runtimeConfig").mockResolvedValue(runtimeConfig);
  await ensureRuntimeConfig();
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function renderAccess() {
  return render(<AccessScreen onAuthenticated={() => {}} initialError="" />);
}

function badgesWithin(element: HTMLElement) {
  return within(element).queryAllByText("Last used");
}

/* Submits the email step and waits for the methods the server answered. */
async function submitEmail() {
  fireEvent.submit(screen.getByRole("button", { name: "Continue" }).closest("form")!);
  await waitFor(() => expect(api.loginMethods).toHaveBeenCalled());
}

describe("without an enterprise SSO door", () => {
  beforeEach(() => {
    registerExtensions({ loginMethods: { sso: null } });
  });

  it("offers no SSO button for the organizations the server names", async () => {
    localStorage.setItem("wh-email", "ada@acme.example");
    vi.spyOn(api, "loginMethods").mockResolvedValue({ password: true, sso_organizations: SSO_ORGANIZATIONS });
    renderAccess();
    await submitEmail();
    await waitFor(() => screen.getByLabelText("Password"));
    expect(screen.queryByRole("button", { name: /Continue with SSO/ })).toBeNull();
  });

  it("stays on the page for an account that has no password instead of leaving for a door it lacks", async () => {
    localStorage.setItem("wh-email", "ada@acme.example");
    vi.spyOn(api, "loginMethods").mockResolvedValue({ password: false, sso_organizations: [SSO_ORGANIZATIONS[0]] });
    renderAccess();
    await submitEmail();
    await waitFor(() => screen.getByText(/single sign-on. It is not available on this deployment/));
    expect(sessionStorage.getItem("wh-staged-login-method")).toBeNull();
    expect(screen.queryByRole("button", { name: /Continue with SSO/ })).toBeNull();
  });

  it("ignores an sso_error the query string carries", () => {
    window.history.replaceState(null, "", "/queue?sso_error=invitation_required");
    renderAccess();
    expect(screen.queryByText("You need an invitation first")).toBeNull();
    expect(window.location.search).toBe("");
  });
});

describe("with the hosted edition's SSO door", () => {
  beforeEach(() => {
    registerExtensions({ loginMethods: { sso: SSO_DOOR } });
  });

  it("offers one SSO button per organization, through the door's URL", async () => {
    localStorage.setItem("wh-email", "ada@acme.example");
    vi.spyOn(api, "loginMethods").mockResolvedValue({ password: true, sso_organizations: SSO_ORGANIZATIONS });
    /* Leaving the page is the door's job; jsdom cannot navigate, so
     * the call is recorded instead. */
    const assign = vi.fn();
    vi.stubGlobal("location", { href: window.location.href, search: "", assign });
    renderAccess();
    await submitEmail();
    const acme = await waitFor(() => screen.getByRole("button", { name: /Continue with SSO \(Acme\)/ }));
    expect(screen.getByRole("button", { name: /Continue with SSO \(Globex\)/ })).toBeTruthy();
    fireEvent.click(acme);
    expect(assign).toHaveBeenCalledWith("/sso/acme");
    expect(sessionStorage.getItem("wh-staged-login-method")).toBe("sso:acme");
  });

  it("explains the code the SSO callback came back with, with the door's fallback for one it does not name", () => {
    window.history.replaceState(null, "", "/queue?sso_error=invitation_required");
    renderAccess();
    expect(screen.getByText("You need an invitation first")).toBeTruthy();
    expect(window.location.search).toBe("");
    cleanup();
    window.history.replaceState(null, "", "/queue?sso_error=something_else");
    renderAccess();
    expect(screen.getByText("Single sign-on failed")).toBeTruthy();
  });
});

describe("last used badge", () => {
  /* The SSO badge sits on a button only an edition with a door renders. */
  beforeEach(() => {
    registerExtensions({ loginMethods: { sso: SSO_DOOR } });
  });

  it("shows nothing on a browser that never signed in", () => {
    renderAccess();
    expect(screen.queryByText("Last used")).toBeNull();
  });

  it("marks the social provider used last", () => {
    localStorage.setItem("wh-last-login-method", "social:github");
    renderAccess();
    expect(badgesWithin(screen.getByRole("button", { name: /Continue with GitHub/ }))).toHaveLength(1);
    expect(badgesWithin(screen.getByRole("button", { name: /Continue with Google/ }))).toHaveLength(0);
    expect(screen.getAllByText("Last used")).toHaveLength(1);
  });

  it("marks the email field after a password sign-in", () => {
    localStorage.setItem("wh-last-login-method", "password");
    renderAccess();
    const badge = screen.getByText("Last used");
    expect(badge.closest("button")).toBeNull();
    expect(badge.parentElement?.textContent).toContain("Email");
  });

  it("moves from the email field to the organization's SSO button", async () => {
    localStorage.setItem("wh-last-login-method", "sso:acme");
    localStorage.setItem("wh-email", "ada@acme.example");
    vi.spyOn(api, "loginMethods").mockResolvedValue({
      password: true,
      sso_organizations: [
        { id: "1", slug: "acme", name: "Acme", sso: true },
        { id: "2", slug: "globex", name: "Globex", sso: true },
      ],
    });
    renderAccess();
    expect(screen.getByText("Last used").closest("button")).toBeNull();

    fireEvent.submit(screen.getByRole("button", { name: "Continue" }).closest("form")!);
    await waitFor(() => screen.getByRole("button", { name: /Continue with SSO \(Acme\)/ }));

    const badges = screen.getAllByText("Last used");
    expect(badges).toHaveLength(1);
    expect(badges[0].closest("button")?.textContent).toContain("Continue with SSO (Acme)");
  });

  it("stages the password attempt so the session gate can commit it", async () => {
    localStorage.setItem("wh-email", "ada@acme.example");
    vi.spyOn(api, "loginMethods").mockResolvedValue({ password: true, sso_organizations: [] });
    vi.spyOn(api, "login").mockRejectedValue(new Error("nope"));
    renderAccess();
    const form = screen.getByRole("button", { name: "Continue" }).closest("form")!;
    fireEvent.submit(form);
    await waitFor(() => screen.getByLabelText("Password"));
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "hunter2hunter2" } });
    fireEvent.submit(form);
    await waitFor(() => expect(api.login).toHaveBeenCalled());
    expect(sessionStorage.getItem("wh-staged-login-method")).toBe("password");
    expect(localStorage.getItem("wh-last-login-method")).toBeNull();
  });
});

describe("a refused password", () => {
  beforeEach(() => {
    registerExtensions({ loginMethods: { sso: null } });
    localStorage.setItem("wh-email", "ada@acme.example");
    vi.spyOn(api, "loginMethods").mockResolvedValue({ password: true, sso_organizations: [] });
    vi.spyOn(api, "login").mockRejectedValue(
      new RequestError("that email and password do not match", 401, "invalid_credentials"),
    );
  });

  afterEach(() => {
    /* The store keeps the loaded object, so the flag is reset on it. */
    runtimeConfig.password_login_offers_signup = false;
  });

  async function submitPassword() {
    renderAccess();
    const form = screen.getByRole("button", { name: "Continue" }).closest("form")!;
    fireEvent.submit(form);
    await waitFor(() => screen.getByLabelText("Password"));
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "hunter2hunter2" } });
    fireEvent.submit(form);
  }

  it("says the credentials do not match", async () => {
    await submitPassword();
    await screen.findByText("That email and password do not match.");
  });

  it("points to the inbox where the edition mails a signup link", async () => {
    runtimeConfig.password_login_offers_signup = true;
    await submitPassword();
    await screen.findByText(
      "That email and password do not match. If you're new to withHuman, check your inbox for a link to create an account.",
    );
  });
});

describe("a social sign-in without an account", () => {
  beforeEach(() => {
    registerExtensions({ loginMethods: { sso: null } });
    window.history.replaceState(null, "", "/queue?social=continue");
    vi.spyOn(api, "socialContinue").mockResolvedValue({
      status: "organization_required",
      email: "ada@example.com",
      expires_at: "2026-10-15T12:00:00Z",
    });
  });

  it("asks for the organization to found and signs the founder in", async () => {
    const principal = { actor_id: "user-1" } as never;
    const found = vi
      .spyOn(api, "socialFoundOrganization")
      .mockResolvedValue({ status: "complete", principal });
    const onAuthenticated = vi.fn();
    render(<AccessScreen onAuthenticated={onAuthenticated} initialError="" />);
    expect(await screen.findByText("ada@example.com")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Yes, create an account" }));
    const name = await screen.findByLabelText("Organization name");
    fireEvent.change(name, { target: { value: "Acme" } });
    fireEvent.submit(name.closest("form")!);
    await waitFor(() => expect(onAuthenticated).toHaveBeenCalledWith(principal));
    expect(found).toHaveBeenCalledWith("Acme");
  });

  it("starts over when the held sign-in has expired", async () => {
    vi.spyOn(api, "socialFoundOrganization").mockRejectedValue(
      new RequestError("That sign-in attempt expired.", 401, "social_flow_invalid"),
    );
    renderAccess();
    fireEvent.click(await screen.findByRole("button", { name: "Yes, create an account" }));
    const name = await screen.findByLabelText("Organization name");
    fireEvent.change(name, { target: { value: "Acme" } });
    fireEvent.submit(name.closest("form")!);
    await screen.findByText("That sign-in attempt expired. Start again from this page.");
    expect(screen.queryByLabelText("Organization name")).toBeNull();
    expect(screen.getByRole("heading", { name: "Sign in" })).toBeTruthy();
  });

  it("asks before creating an account and goes back to sign in on no", async () => {
    const found = vi.spyOn(api, "socialFoundOrganization");
    renderAccess();
    await screen.findByRole("heading", { name: "Create a new account?" });
    expect(screen.queryByLabelText("Organization name")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "No, use a different account" }));
    expect(screen.getByRole("heading", { name: "Sign in" })).toBeTruthy();
    expect(found).not.toHaveBeenCalled();
  });
});

describe("in the desktop app", () => {
  beforeEach(() => {
    registerExtensions({ loginMethods: { sso: SSO_DOOR } });
  });

  function stubDesktop(result: DesktopSignInResult) {
    const signIn = vi.fn().mockResolvedValue(result);
    vi.stubGlobal("withhumanDesktop", { signIn });
    return signIn;
  }

  it("hands a provider sign-in to the app and signs in with its answer", async () => {
    const principal = { actor_id: "user-1" } as never;
    const signIn = stubDesktop({ status: "answered", response: { status: "complete", principal } });
    const begin = vi.spyOn(api, "socialBegin");
    const onAuthenticated = vi.fn();
    render(<AccessScreen onAuthenticated={onAuthenticated} initialError="" />);
    fireEvent.click(await screen.findByRole("button", { name: /Continue with Google/ }));
    await waitFor(() => expect(onAuthenticated).toHaveBeenCalledWith(principal));
    expect(signIn).toHaveBeenCalledWith({ kind: "social", provider: "google" });
    expect(begin).not.toHaveBeenCalled();
    expect(sessionStorage.getItem("wh-staged-login-method")).toBe("social:google");
  });

  it("offers the organization picker when the answer carries a ticket", async () => {
    stubDesktop({
      status: "answered",
      response: {
        status: "select_organization",
        ticket: "whl_ticket",
        expires_at: "2026-11-03T12:00:00Z",
        organizations: [{ id: "3", slug: "initech", name: "Initech", sso: false }],
      },
    });
    renderAccess();
    fireEvent.click(await screen.findByRole("button", { name: /Continue with GitHub/ }));
    expect(await screen.findByRole("heading", { name: "Choose an organization" })).toBeTruthy();
    expect(screen.getByText("Initech")).toBeTruthy();
  });

  it("explains the code a failed hand-off came back with", async () => {
    stubDesktop({ status: "failed", code: "provider_denied" });
    renderAccess();
    fireEvent.click(await screen.findByRole("button", { name: /Continue with Google/ }));
    expect(await screen.findByText("The sign-in was cancelled at the provider.")).toBeTruthy();
  });

  it("hands SSO to the app instead of leaving the page", async () => {
    localStorage.setItem("wh-email", "ada@acme.example");
    vi.spyOn(api, "loginMethods").mockResolvedValue({ password: true, sso_organizations: SSO_ORGANIZATIONS });
    const signIn = stubDesktop({ status: "failed", code: "invitation_required" });
    const assign = vi.fn();
    vi.stubGlobal("location", { href: window.location.href, search: "", assign });
    renderAccess();
    await submitEmail();
    fireEvent.click(await screen.findByRole("button", { name: /Continue with SSO \(Acme\)/ }));
    expect(await screen.findByText("You need an invitation first")).toBeTruthy();
    expect(signIn).toHaveBeenCalledWith({ kind: "sso", organization: "acme" });
    expect(assign).not.toHaveBeenCalled();
  });
});
