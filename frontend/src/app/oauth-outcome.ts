import { useEffect } from "react";
import { useToast } from "@/ds/components/toast";

type Provider = "slack" | "discord";

const SLACK_ERRORS: Record<string, string> = {
  connection_check_unavailable: "We could not verify the existing Slack connection. Try connecting again shortly.",
  access_denied: "The install was declined in Slack.",
  install_expired: "The install took too long or was already used. Start it again.",
  workspace_taken: "That workspace is already connected to another organization.",
  slack_not_configured: "This deployment has no Slack application configured.",
  slack_rejected: "Slack rejected the install. Check the app's redirect URL and credentials.",
};

const DISCORD_ERRORS: Record<string, string> = {
  access_denied: "The request was declined in Discord.",
  install_expired: "The install took too long or was already used. Start it again.",
  guild_taken: "That server is already connected to another organization.",
  link_expired: "The link took too long or was already used. Start it again.",
  not_a_member:
    "That Discord account is not a member of your organization's server. Join the server, then link again.",
  account_taken: "That Discord account is already linked to another member.",
  no_connection: "Your organization's Discord server was disconnected before the link finished.",
  discord_not_configured: "This deployment has no Discord application configured.",
  discord_rejected: "Discord rejected the request. Try again.",
};

/* What each query parameter a callback can set means. */
const PARAMETERS: Record<string, { provider: Provider; failed?: string; errors?: Record<string, string> }> = {
  slack: { provider: "slack" },
  slack_error: { provider: "slack", failed: "Slack was not connected", errors: SLACK_ERRORS },
  discord: { provider: "discord" },
  discord_error: { provider: "discord", failed: "Discord was not connected", errors: DISCORD_ERRORS },
  discord_link_error: { provider: "discord", failed: "Discord was not linked", errors: DISCORD_ERRORS },
};

const SUCCESSES: Record<string, string> = {
  "slack:connected": "Slack connected",
  "discord:connected": "Discord connected",
  "discord:linked": "Discord linked",
};

function returnedProvider(search: string): Provider | null {
  const params = new URLSearchParams(search);
  for (const [key, meaning] of Object.entries(PARAMETERS)) {
    if (params.has(key)) return meaning.provider;
  }
  return null;
}

/* Read once when the app loads, before any screen renders or the hook
 * below cleans the URL. */
let returned: Provider | null = typeof window === "undefined" ? null : returnedProvider(window.location.search);

/* The provider whose round trip this page load came back from, until
 * forgetReturnedProvider: the guided setup reopens that channel's guide. */
export function returnedFromProvider(): Provider | null {
  return returned;
}

export function forgetReturnedProvider() {
  returned = null;
}

/* A Slack or Discord round trip always reloads the app, with the outcome
 * as a query parameter. Say what happened once, in the shared shell, and
 * take it off the URL so a reload does not repeat it. */
export function useOAuthOutcome() {
  const toast = useToast();
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    let found = false;
    for (const [key, meaning] of Object.entries(PARAMETERS)) {
      const value = params.get(key);
      if (value === null) continue;
      found = true;
      params.delete(key);
      if (meaning.failed) {
        toast.add({
          type: "deny",
          title: meaning.failed,
          description: meaning.errors?.[value] ?? "Something went wrong. Try again.",
        });
        continue;
      }
      const title = SUCCESSES[`${key}:${value}`];
      if (title) toast.add({ type: "approve", title });
    }
    if (!found) return;
    const query = params.toString();
    window.history.replaceState(null, "", window.location.pathname + (query ? `?${query}` : "") + window.location.hash);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
}

/* For tests: read the current URL again, as a fresh page load would. */
export function resetReturnedProviderForTest() {
  returned = returnedProvider(window.location.search);
}
