import { useEffect, useState } from "react";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { SettingRow } from "@/ds/components/setting-row";
import { SettingsPage } from "@/ds/components/settings-page";
import { SettingsSection } from "@/ds/components/settings-section";
import { useToast } from "@/ds/components/toast";
import { DiscordIcon, SlackIcon } from "@/ds/icons/brands";
import { useRouter } from "@tanstack/react-router";
import { api, RequestError } from "../api";
import type { DiscordConnection, SlackConnection } from "../types";
import { formatRelative } from "./format";
import { useTitle } from "./use-title";

/* One connector as the loader found it: what is connected, or that the
 * deployment has no credentials for it. */
interface ConnectorState<T> {
  connection: T | null;
  unavailable: boolean;
}

/* What the integrations route loader delivers. */
export interface IntegrationsPage {
  slack: ConnectorState<SlackConnection>;
  discord: ConnectorState<DiscordConnection>;
}

/* Both connections arrive together. Each is a management surface, so a
 * member's request is refused and reads as no connection; the screen
 * shows its permission notice instead. A failure is reported in the
 * page rather than thrown, so an expired session reaches the sign-in
 * gate, not the router's error boundary. */
export async function loadIntegrationsPage(): Promise<IntegrationsPage> {
  const [slack, discord] = await Promise.all([
    connector(api.slackConnection(), "slack_not_configured"),
    connector(api.discordConnection(), "discord_not_configured"),
  ]);
  return { slack, discord };
}

function connector<T>(
  request: Promise<{ connection: T | null }>,
  notConfigured: string,
): Promise<ConnectorState<T>> {
  return request.then(
    (result) => ({ connection: result.connection, unavailable: false }),
    (reason: unknown) => ({
      connection: null,
      unavailable: reason instanceof RequestError && reason.code === notConfigured,
    }),
  );
}

/* Settings: integrations. Organization-level chat connectors; each
 * person's own identity in them lives under Notifications. */
export function IntegrationsScreen({
  page,
  canRead,
  canWrite,
}: {
  page: IntegrationsPage;
  canRead: boolean;
  /* Connect, sync, and disconnect workspaces. */
  canWrite: boolean;
}) {
  useTitle("Integrations");

  return (
    <SettingsPage
      title="Integrations"
      description="Let reviewers get notified in Slack and Discord."
    >
      {canRead && !canWrite ? (
        <Callout tone="neutral">
          You need the connection.write permission to connect or disconnect
          a workspace.
        </Callout>
      ) : null}

      {canRead ? (
        <>
          <SlackSection state={page.slack} canWrite={canWrite} />
          <DiscordSection state={page.discord} canWrite={canWrite} />
        </>
      ) : (
        <Callout tone="neutral">
          You need the connection.read permission to view this page.
        </Callout>
      )}
    </SettingsPage>
  );
}

const SLACK_ERRORS: Record<string, string> = {
  connection_check_unavailable: "We could not verify the existing Slack connection. Try connecting again shortly.",
  access_denied: "The install was declined in Slack.",
  install_expired: "The install took too long or was already used. Start it again.",
  workspace_taken: "That workspace is already connected to another organization.",
  slack_rejected: "Slack rejected the install. Check the app's redirect URL and credentials.",
};

/* The Slack workspace connection. Connecting is an organization-level
 * OAuth install a Slack admin completes in Slack; the page only opens the
 * door and shows what came back. Per-person Slack identities and delivery
 * build on this later. */
function SlackSection({
  state,
  canWrite,
}: {
  state: ConnectorState<SlackConnection>;
  canWrite: boolean;
}) {
  const toast = useToast();
  const router = useRouter();
  /* A refused install says the deployment has no credentials as surely
   * as the check does. */
  const [installRefused, setInstallRefused] = useState(false);
  const unavailable = state.unavailable || installRefused;
  const connection = state.connection;
  const disconnected = connection?.status === "disconnected";
  const [busy, setBusy] = useState(false);

  /* The install round-trips through slack.com, so the outcome arrives as
   * a query parameter rather than a response. Read it once, say what
   * happened, and take it off the URL so a reload does not repeat it. */
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const outcome = params.get("slack");
    const error = params.get("slack_error");
    if (!outcome && !error) return;
    if (outcome === "connected") {
      toast.add({ type: "approve", title: "Slack connected" });
    } else if (error) {
      toast.add({
        type: "deny",
        title: "Slack was not connected",
        description: SLACK_ERRORS[error] ?? "Something went wrong during the install. Try again.",
      });
    }
    params.delete("slack");
    params.delete("slack_error");
    const query = params.toString();
    window.history.replaceState(null, "", window.location.pathname + (query ? `?${query}` : ""));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function connect() {
    setBusy(true);
    try {
      const result = await api.beginSlackInstall();
      window.location.assign(result.authorize_url);
    } catch (reason) {
      setBusy(false);
      if (reason instanceof RequestError && reason.code === "slack_not_configured") {
        setInstallRefused(true);
        return;
      }
      toast.add({
        type: "deny",
        title: "Could not start the Slack install",
        description: reason instanceof Error ? reason.message : undefined,
      });
    }
  }

  async function sync() {
    setBusy(true);
    try {
      const result = await api.syncSlackMembers();
      await router.invalidate();
      toast.add({
        type: "approve",
        title: `${result.linked} of ${result.members} members linked`,
        description:
          result.linked < result.members
            ? "Members with a different Slack email can add it under Notifications."
            : undefined,
      });
    } catch (reason) {
      toast.add({
        type: "deny",
        title: "Could not sync members",
        description: reason instanceof Error ? reason.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  }

  async function disconnect() {
    setBusy(true);
    try {
      await api.disconnectSlack();
      await router.invalidate();
      toast.add({ type: "approve", title: "Slack disconnected" });
    } catch (reason) {
      toast.add({
        type: "deny",
        title: "Could not disconnect Slack",
        description: reason instanceof Error ? reason.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <SettingsSection
      title="Slack"
      description={
        unavailable
          ? "Slack is not set up on this deployment."
          : "Connect your workspace so reviewers get notified in Slack."
      }
    >
      <SettingRow
        icon={<SlackIcon />}
        label="Workspace"
        description={
          unavailable ? (
            "Not enabled on this deployment."
          ) : connection === null ? undefined : (
            <>
              {connection.team_name}, {disconnected ? "disconnected" : "connected"} {formatRelative(disconnected ? connection.disconnected_at ?? connection.installed_at : connection.installed_at)} ago.
              Bot user <span className="font-mono">{connection.bot_user_id}</span> in
              workspace <span className="font-mono">{connection.team_id}</span>.
            </>
          )
        }
      >
        {unavailable ? (
          <Badge tone="neutral">Not enabled</Badge>
        ) : connection === null ? (
          <Badge tone="neutral">Not connected</Badge>
        ) : (
          <Badge tone={disconnected ? "neutral" : "approve"}>{disconnected ? "Disconnected" : "Connected"}</Badge>
        )}
      </SettingRow>
      {unavailable ? null : connection === null || disconnected ? (
        canWrite ? (
          <SettingRow label={disconnected ? "Reconnect Slack" : "Connect Slack"} description={disconnected ? "Slack notifications are paused." : undefined}>
            <Button variant="primary" loading={busy} onClick={() => void connect()}>
              {disconnected ? "Reconnect" : "Connect"}
            </Button>
          </SettingRow>
        ) : null
      ) : (
        <>
          <SettingRow
            label="Linked members"
            description={
              <>
                {connection.linked_members === 1
                  ? "1 member is linked to their Slack account"
                  : `${connection.linked_members} members are linked to their Slack accounts`}
                .
              </>
            }
          >
            {canWrite ? (
              <Button variant="neutral" loading={busy} onClick={() => void sync()}>
                Sync members
              </Button>
            ) : null}
          </SettingRow>
          {canWrite ? (
            <SettingRow
              label="Disconnect"
              description="Stops Slack notifications."
            >
              <Button variant="danger" loading={busy} onClick={() => void disconnect()}>
                Disconnect
              </Button>
            </SettingRow>
          ) : null}
        </>
      )}
    </SettingsSection>
  );
}

/* The Discord server connection. Like Slack, connecting is an
 * organization-level OAuth install a server admin completes in Discord.
 * Unlike Slack there is no member sync: Discord offers no lookup by
 * email, so each person links their own account under Notifications. */
function DiscordSection({
  state,
  canWrite,
}: {
  state: ConnectorState<DiscordConnection>;
  canWrite: boolean;
}) {
  const toast = useToast();
  const router = useRouter();
  const [installRefused, setInstallRefused] = useState(false);
  const unavailable = state.unavailable || installRefused;
  const connection = state.connection;
  const [busy, setBusy] = useState(false);

  async function connect() {
    setBusy(true);
    try {
      const result = await api.beginDiscordInstall();
      window.location.assign(result.authorize_url);
    } catch (reason) {
      setBusy(false);
      if (reason instanceof RequestError && reason.code === "discord_not_configured") {
        setInstallRefused(true);
        return;
      }
      toast.add({
        type: "deny",
        title: "Could not start the Discord install",
        description: reason instanceof Error ? reason.message : undefined,
      });
    }
  }

  async function disconnect() {
    setBusy(true);
    try {
      await api.disconnectDiscord();
      await router.invalidate();
      toast.add({
        type: "approve",
        title: "Discord disconnected",
      });
    } catch (reason) {
      toast.add({
        type: "deny",
        title: "Could not disconnect Discord",
        description: reason instanceof Error ? reason.message : undefined,
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <SettingsSection
      title="Discord"
      description={
        unavailable
          ? "Discord is not set up on this deployment."
          : "Connect your server so reviewers get notified in Discord."
      }
    >
      <SettingRow
        icon={<DiscordIcon />}
        label="Server"
        description={
          unavailable ? (
            "Not enabled on this deployment."
          ) : connection === null ? (
            "No server is connected."
          ) : (
            <>
              {connection.guild_name}, connected {formatRelative(connection.installed_at)} ago.
              Server <span className="font-mono">{connection.guild_id}</span>.
            </>
          )
        }
      >
        {unavailable ? (
          <Badge tone="neutral">Not enabled</Badge>
        ) : connection === null ? (
          <Badge tone="neutral">Not connected</Badge>
        ) : (
          <Badge tone="approve">Connected</Badge>
        )}
      </SettingRow>
      {unavailable ? null : connection === null ? (
        canWrite ? (
          <SettingRow
            label="Connect Discord"
            description="Opens Discord to add the bot to a server."
          >
            <Button variant="primary" loading={busy} onClick={() => void connect()}>
              Connect
            </Button>
          </SettingRow>
        ) : null
      ) : (
        <>
          <SettingRow
            label="Linked members"
            description={
              <>
                {connection.linked_members === 1
                  ? "1 member has linked their Discord account"
                  : `${connection.linked_members} members have linked their Discord accounts`}
                .
              </>
            }
          />
          {canWrite ? (
            <SettingRow
              label="Disconnect"
              description="Removes the server and every member's Discord link."
            >
              <Button variant="danger" loading={busy} onClick={() => void disconnect()}>
                Disconnect
              </Button>
            </SettingRow>
          ) : null}
        </>
      )}
    </SettingsSection>
  );
}
