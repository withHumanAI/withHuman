// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  forgetReturnedProvider,
  resetReturnedProviderForTest,
  returnedFromProvider,
  useOAuthOutcome,
} from "./oauth-outcome";

const { toast } = vi.hoisted(() => ({ toast: vi.fn() }));
vi.mock("@/ds/components/toast", () => ({ useToast: () => ({ add: toast }) }));

function Shell() {
  useOAuthOutcome();
  return null;
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  window.history.replaceState(null, "", "/");
  resetReturnedProviderForTest();
});

describe("OAuth outcomes", () => {
  it.each([
    ["/welcome?slack=connected", "approve", "Slack connected", "slack"],
    ["/welcome?slack_error=workspace_taken", "deny", "Slack was not connected", "slack"],
    ["/settings/integrations?discord=connected", "approve", "Discord connected", "discord"],
    ["/welcome?discord=linked", "approve", "Discord linked", "discord"],
    ["/welcome?discord_error=guild_taken", "deny", "Discord was not connected", "discord"],
    ["/settings/notifications?discord_link_error=access_denied", "deny", "Discord was not linked", "discord"],
  ])("says what %s means once and cleans the URL", (path, type, title, provider) => {
    window.history.replaceState(null, "", path);
    resetReturnedProviderForTest();
    expect(returnedFromProvider()).toBe(provider);
    const pathname = window.location.pathname;
    const view = render(<Shell />);
    expect(toast).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ type, title }));
    expect(window.location.pathname + window.location.search).toBe(pathname);
    view.unmount();
    render(<Shell />);
    expect(toast).toHaveBeenCalledOnce();
  });

  it("explains a refusal in words", () => {
    window.history.replaceState(null, "", "/welcome?slack_error=access_denied");
    render(<Shell />);
    expect(toast).toHaveBeenCalledWith(expect.objectContaining({ description: "The install was declined in Slack." }));
  });

  it("leaves other parameters alone", () => {
    window.history.replaceState(null, "", "/queue?status=pending");
    resetReturnedProviderForTest();
    render(<Shell />);
    expect(toast).not.toHaveBeenCalled();
    expect(window.location.search).toBe("?status=pending");
    expect(returnedFromProvider()).toBeNull();
  });

  it("forgets the provider once the setup has reopened its guide", () => {
    window.history.replaceState(null, "", "/welcome?slack=connected");
    resetReturnedProviderForTest();
    forgetReturnedProvider();
    expect(returnedFromProvider()).toBeNull();
  });
});
