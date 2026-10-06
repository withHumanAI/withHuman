import { useNavigate } from "@tanstack/react-router";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { SettingRow } from "@/ds/components/setting-row";
import { SettingsPage } from "@/ds/components/settings-page";
import { SettingsSection } from "@/ds/components/settings-section";
import { PageBackLink } from "@/ds/components/page-header";
import { CheckIcon, MailIcon } from "@/ds/icons";
import { DiscordIcon, SlackIcon } from "@/ds/icons/brands";
import type { ContactMethod } from "../types";
import {
  ChannelGuide,
  channelGuideCopy,
  EMAIL_FORM,
  EmailForm,
  isGuidedKind,
  type ChannelKind,
  type GuidedKind,
  type SlackWorkspace,
  type DiscordServer,
} from "./contact-channels";
import { useAccess } from "./authorization";
import { contactKind, extensions, type ContactKind } from "./extensions";
import { useSession } from "./principal";
import { useRuntimeConfig } from "./use-runtime-config";
import { useTitle } from "./use-title";

export { isChannelKind, type ChannelKind } from "./contact-channels";

/*
 * Settings: add a channel. Its own page, not a dialog: first every kind
 * of channel there is, including the ones this deployment cannot offer
 * and the ones nobody types in, so the choice is never a mystery; then
 * the flow for the chosen kind.
 */

/* What the route loader delivers: the channels already set up, so the
 * picker can say which are linked. */
export interface AddChannelPage {
  methods: ContactMethod[];
  slackWorkspace: SlackWorkspace | null;
  discordServer: DiscordServer | null;
}

export function AddChannelScreen({ page, kind }: { page: AddChannelPage; kind?: ChannelKind }) {
  if (!kind) return <Picker methods={page.methods} />;
  if (kind === "email") return <AddEmail />;
  if (isGuidedKind(kind)) {
    return <Guide kind={kind} methods={page.methods} workspace={page.slackWorkspace} server={page.discordServer} />;
  }
  /* The route admits only registered kinds; a miss is the picker. */
  const entry = contactKind(kind);
  return entry ? <AddEditionKind entry={entry} methods={page.methods} /> : <Picker methods={page.methods} />;
}

/* Every kind of channel in one list, one row each: email, then the
 * kinds the edition delivers to, then the chat kinds. Kinds this
 * deployment cannot deliver stay on the list and say so; kinds nobody
 * types in (Slack, Discord) say how they arrive instead. */
function Picker({ methods }: { methods: ContactMethod[] }) {
  useTitle("Add a channel");
  const navigate = useNavigate();
  const { config } = useRuntimeConfig();
  const linked = (kind: ContactMethod["kind"]) => methods.some((method) => method.kind === kind);

  /* Each row's button says what adding that kind actually is. */
  const add = (kind: ChannelKind, label: string, enabled = true) => (
    <Button
      size="sm"
      variant="neutral"
      disabled={!enabled}
      onClick={() => void navigate({ to: "/settings/notifications/add", search: { kind } })}
    >
      {label}
    </Button>
  );
  const unavailable = " Not set up on this deployment.";

  return (
    <SettingsPage
      title="Add a channel"
      description="Add ways to get notified."
      back={<PageBackLink to="/settings/notifications">Notifications</PageBackLink>}
    >
      <SettingsSection>
        <SettingRow icon={<MailIcon />} label="Email" description="Get notified at another email address.">
          {add("email", "Add address")}
        </SettingRow>
        {extensions.contactKinds.map((entry) => {
          const available = config !== null && entry.available(config);
          return (
            <SettingRow
              key={entry.kind}
              icon={entry.icon}
              label={entry.label}
              description={available ? entry.description : `${entry.description} ${entry.unavailable}`}
            >
              {add(entry.kind, entry.action, available)}
            </SettingRow>
          );
        })}
        <SettingRow
          icon={<SlackIcon />}
          label="Slack"
          description={"Get notified via Slack DM." + (config?.slack_enabled ? "" : unavailable)}
        >
          {linked("slack") ? (
            <Badge tone="approve" icon={<CheckIcon />}>
              Linked
            </Badge>
          ) : (
            add("slack", "Link Slack", config?.slack_enabled ?? false)
          )}
        </SettingRow>
        <SettingRow
          icon={<DiscordIcon />}
          label="Discord"
          description={"Get notified via Discord DM." + (config?.discord_enabled ? "" : unavailable)}
        >
          {linked("discord") ? (
            <Badge tone="approve" icon={<CheckIcon />}>
              Linked
            </Badge>
          ) : (
            add("discord", "Link Discord", config?.discord_enabled ?? false)
          )}
        </SettingRow>
      </SettingsSection>
    </SettingsPage>
  );
}

/* The chat kinds nobody types in, as a page of their own. */
function Guide({
  kind,
  methods,
  workspace,
  server,
}: {
  kind: GuidedKind;
  methods: ContactMethod[];
  workspace: SlackWorkspace | null;
  server: DiscordServer | null;
}) {
  const navigate = useNavigate();
  const session = useSession();
  const { can } = useAccess();
  const copy = channelGuideCopy(kind, session.organization?.name ?? "this organization", workspace, server, can("connection.write"));
  useTitle(copy.title);

  return (
    <SettingsPage
      title={copy.title}
      description={copy.description}
      back={<PageBackLink to="/settings/notifications/add">All channels</PageBackLink>}
    >
      <SettingsSection inset="none">
        <ChannelGuide
          kind={kind}
          methods={methods}
          onDone={() => void navigate({ to: "/settings/notifications" })}
          onAddEmail={() =>
            void navigate({ to: "/settings/notifications/add", search: { kind: "email" } })
          }
          onLinked={() => void navigate({ to: "/settings/notifications" })}
          workspace={workspace}
          server={server}
        />
      </SettingsSection>
    </SettingsPage>
  );
}

/* The email form, as a page of its own. Adding lands back on the
 * Notifications page, where the new row carries the panel that waits
 * for the code. */
function AddEmail() {
  useTitle(EMAIL_FORM.title);
  const navigate = useNavigate();

  return (
    <SettingsPage
      title={EMAIL_FORM.title}
      description={EMAIL_FORM.description}
      back={<PageBackLink to="/settings/notifications/add">All channels</PageBackLink>}
    >
      <SettingsSection title={EMAIL_FORM.field} inset="none">
        <EmailForm
          onAdded={() => navigate({ to: "/settings/notifications", hash: "channels" })}
          onCancel={() => void navigate({ to: "/settings/notifications" })}
        />
      </SettingsSection>
    </SettingsPage>
  );
}

/* A registered kind's flow, as a page of its own: the kind says what
 * it is and core frames it. Adding lands back on the Notifications
 * page, where the new row carries whatever proves it. */
function AddEditionKind({ entry, methods }: { entry: ContactKind; methods: ContactMethod[] }) {
  useTitle(entry.add.title);
  const navigate = useNavigate();
  const Flow = entry.add.component;

  return (
    <SettingsPage
      title={entry.add.title}
      description={entry.add.description}
      back={<PageBackLink to="/settings/notifications/add">All channels</PageBackLink>}
    >
      <SettingsSection title={entry.add.section} inset="none">
        <Flow
          methods={methods}
          onAdded={() => navigate({ to: "/settings/notifications", hash: "channels" })}
          onDone={() => void navigate({ to: "/settings/notifications" })}
        />
      </SettingsSection>
    </SettingsPage>
  );
}
