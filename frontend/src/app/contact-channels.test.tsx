// @vitest-environment jsdom
import type { ComponentType, ReactNode } from "react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, RequestError } from "../api";
import type { ContactMethod } from "../types";
import { ChannelGuide, loadChannelsPage, type DiscordServer } from "./contact-channels";
import { resetReturnedProviderForTest } from "./oauth-outcome";
import { OnboardingContactStep } from "./onboarding-contacts";

const { access, config, navigate, invalidate, toast } = vi.hoisted(() => ({
  access: { connect: true },
  config: { discord_enabled: true, slack_enabled: true },
  navigate: vi.fn(),
  invalidate: vi.fn().mockResolvedValue(undefined),
  toast: vi.fn(),
}));
vi.mock("@tanstack/react-router", () => ({
  createLink: (component: ComponentType) => component,
  useNavigate: () => navigate,
  useRouter: () => ({ invalidate }),
  Link: ({ children }: { children: ReactNode }) => <a>{children}</a>,
}));
vi.mock("./principal", () => ({ useSession: () => ({ organization: { name: "Example" } }) }));
vi.mock("./authorization", () => ({ useAccess: () => ({ can: () => access.connect }) }));
vi.mock("./use-runtime-config", () => ({ useRuntimeConfig: () => ({ config }) }));
vi.mock("@/ds/components/toast", () => ({ useToast: () => ({ add: toast }) }));

beforeEach(() => {
  access.connect = true;
  config.discord_enabled = true;
  vi.clearAllMocks();
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function guide(server: DiscordServer | null = null, methods: ContactMethod[] = []) {
  return <ChannelGuide kind="discord" methods={methods} workspace={null} server={server}
    onDone={vi.fn()} onAddEmail={vi.fn()} onLinked={vi.fn()} />;
}

describe("Discord setup", () => {
  it("sends an owner to Integrations before offering personal linking", () => {
    render(guide());
    expect(screen.queryByRole("button", { name: "Link Discord" })).toBeNull();
    expect(screen.queryByText(/Ask an admin/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Go to Integrations" }));
    expect(navigate).toHaveBeenCalledWith({ to: "/settings/integrations" });
  });

  it("asks a member without connection permission to contact an admin", () => {
    access.connect = false;
    render(guide());
    expect(screen.getByText(/Ask an admin to connect Example's Discord server/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Go to Integrations" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Link Discord" })).toBeNull();
  });

  it("lets an ordinary member link once a server is connected", () => {
    access.connect = false;
    render(guide({ guild_name: "Community" }));
    expect(screen.getByText(/You must be a member of Community/)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Link Discord" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Go to Integrations" })).toBeNull();
  });

  it("shows Linked instead of another linking action", () => {
    render(guide({ guild_name: "Community" }, [{ kind: "discord", available: true } as ContactMethod]));
    expect(screen.getByText("Linked")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Link Discord" })).toBeNull();
  });

  it("refreshes setup after a disconnect and gives the owner actionable feedback", async () => {
    vi.spyOn(api, "beginDiscordLink").mockRejectedValue(new RequestError("missing", 404, "discord_not_connected"));
    const view = render(guide({ guild_name: "Community" }));
    invalidate.mockImplementationOnce(async () => { view.rerender(guide()); });
    fireEvent.click(screen.getByRole("button", { name: "Link Discord" }));
    await waitFor(() => expect(toast).toHaveBeenCalledWith(expect.objectContaining({
      description: "Connect your Discord server under Integrations first.",
    })));
    expect(invalidate).toHaveBeenCalledOnce();
    expect(screen.getByRole("button", { name: "Go to Integrations" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Link Discord" })).toBeNull();
  });

  it("does not mislabel an unrelated 404 as missing setup", async () => {
    vi.spyOn(api, "beginDiscordLink").mockRejectedValue(new RequestError("not found", 404, "not_found"));
    render(guide({ guild_name: "Community" }));
    fireEvent.click(screen.getByRole("button", { name: "Link Discord" }));
    await waitFor(() => expect(toast).toHaveBeenCalledWith(expect.objectContaining({ description: "not found" })));
    expect(invalidate).not.toHaveBeenCalled();
  });

  it("disables Discord during onboarding when the deployment has no app", () => {
    config.discord_enabled = false;
    render(<OnboardingContactStep page={{ methods: [], slackWorkspace: null, discordServer: null, rules: null, error: "" }}
      onChanged={vi.fn()} onContinue={vi.fn()} />);
    expect((screen.getByRole("button", { name: "Discord" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/Discord.*not set up on this deployment/)).toBeTruthy();
  });

  it("carries the server from the contact response into the shared page", async () => {
    vi.spyOn(api, "contactMethods").mockResolvedValue({ contact_methods: [], slack_workspace: null, discord_server: { guild_name: "Community" } });
    vi.spyOn(api, "notificationRules").mockRejectedValue(new Error("unavailable"));
    expect((await loadChannelsPage()).discordServer).toEqual({ guild_name: "Community" });
  });
});

describe("Chat channels in the guided setup", () => {
  const emptyPage = { methods: [], slackWorkspace: null, discordServer: null, rules: null, error: "" };
  let assign: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    assign = vi.fn();
    vi.spyOn(window, "location", "get").mockReturnValue({ ...window.location, assign } as Location);
  });

  it.each([
    ["Slack", "Connect Slack", "beginSlackInstall"],
    ["Discord", "Connect Discord", "beginDiscordInstall"],
  ] as const)("connects %s in place instead of leaving for Integrations", async (kind, action, begin) => {
    const started = vi.spyOn(api, begin).mockResolvedValue({ authorize_url: "https://provider.example/authorize" });
    render(<OnboardingContactStep page={emptyPage} onChanged={vi.fn()} onContinue={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: kind }));
    expect(screen.queryByRole("button", { name: "Go to Integrations" })).toBeNull();
    expect(screen.queryByText(/under Integrations/)).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: action }));
    await waitFor(() => expect(assign).toHaveBeenCalledWith("https://provider.example/authorize"));
    expect(started).toHaveBeenCalledWith("onboarding");
    expect(navigate).not.toHaveBeenCalled();
  });

  it("links Discord with the way back to the setup", async () => {
    const started = vi.spyOn(api, "beginDiscordLink").mockResolvedValue({ authorize_url: "https://discord.example/authorize" });
    render(<OnboardingContactStep page={{ ...emptyPage, discordServer: { guild_name: "Community" } }}
      onChanged={vi.fn()} onContinue={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Discord" }));
    fireEvent.click(screen.getByRole("button", { name: "Link Discord" }));
    await waitFor(() => expect(started).toHaveBeenCalledWith("onboarding"));
  });

  it("keeps the settings page's way to Integrations", () => {
    render(<ChannelGuide kind="slack" methods={[]} workspace={null} server={null}
      onDone={vi.fn()} onAddEmail={vi.fn()} onLinked={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Go to Integrations" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Connect Slack" })).toBeNull();
  });
});

describe("Coming back from a provider", () => {
  it("reopens the Slack guide after the install comes back", () => {
    window.history.replaceState(null, "", "/welcome?slack=connected");
    resetReturnedProviderForTest();
    render(<OnboardingContactStep page={{ methods: [], discordServer: null, rules: null, error: "", slackWorkspace: { team_name: "Acme" } }}
      onChanged={vi.fn()} onContinue={vi.fn()} />);
    expect(screen.getByRole("button", { name: "Link Slack" })).toBeTruthy();
    window.history.replaceState(null, "", "/");
    resetReturnedProviderForTest();
  });
});
