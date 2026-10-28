import { Fragment, useState, type ComponentType, type FormEvent, type ReactNode } from "react";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { IconButton } from "@/ds/components/icon-button";
import { SettingRow } from "@/ds/components/setting-row";
import { TextField } from "@/ds/components/text-field";
import { useToast } from "@/ds/components/toast";
import { Tooltip } from "@/ds/components/tooltip";
import { CheckIcon, ClockIcon, MailIcon, MessageIcon, TrashIcon } from "@/ds/icons";
import { DiscordIcon, SlackIcon } from "@/ds/icons/brands";
import { api, RequestError } from "../api";
import type { ContactMethod, NotificationRules, OAuthReturn } from "../types";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { useAccess } from "./authorization";
import {
  contactKind,
  extensions,
  type ContactConfirmProps,
  type ContactMethodBadge,
  type ContactKind,
  type ContactMethodRow,
} from "./extensions";
import { useSession } from "./principal";

/*
 * A person's channels: the rows that list them, the form that adds a
 * typed-in one, and the guides for the kinds nobody types in. The
 * Notifications settings page and the guided setup's first step both
 * build on these, so a channel is added, confirmed, and removed the same
 * way wherever the person meets it. Core knows three kinds: email, which
 * is typed in and proved by a code, and the two chat identities. Every
 * other kind is one the edition registered (extensions.contactKinds),
 * and these screens draw it through its registry entry alone.
 */

/* The kinds core owns. */
export type CoreChannelKind = "email" | "slack" | "discord";

/* Every kind a picker offers: core's three, or a registered kind. */
export type ChannelKind = CoreChannelKind | (string & {});

/* The chat kinds nobody types in: a guide says how each gets set up. */
export type GuidedKind = "slack" | "discord";

export function isCoreChannelKind(value: unknown): value is CoreChannelKind {
  return value === "email" || value === "slack" || value === "discord";
}

export function isGuidedKind(value: unknown): value is GuidedKind {
  return value === "slack" || value === "discord";
}

export function isChannelKind(value: unknown): value is ChannelKind {
  return isCoreChannelKind(value) || (typeof value === "string" && contactKind(value) !== undefined);
}

/* What a screen that shows channels loads: the methods and the two
 * ladders. rules is null when that request failed; the ladders say so in
 * place. error is the methods request failing, which the screen reports
 * without giving up the rest of the page. */
/* The connected chat services. Guides use their names and presence to
 * explain whether a member can link or the organization needs setup. */
export interface SlackWorkspace {
  team_name: string;
}

export interface DiscordServer {
  guild_name: string;
}

export interface ChannelsPage {
  methods: ContactMethod[];
  slackWorkspace: SlackWorkspace | null;
  discordServer: DiscordServer | null;
  rules: NotificationRules | null;
  error: string;
}

/* The route loader shared by every screen that shows channels. A failed
 * load is reported in the data rather than thrown, so an expired session
 * reaches the sign-in gate, not the router's error boundary. The rules
 * failing alone leaves the channels usable. */
export async function loadChannelsPage(): Promise<ChannelsPage> {
  const [methods, rules] = await Promise.all([
    api.contactMethods().then(
      (response) => ({
        methods: response.contact_methods,
        slackWorkspace: response.slack_workspace,
        discordServer: response.discord_server,
        error: "",
      }),
      (reason: unknown) => ({
        methods: [] as ContactMethod[],
        slackWorkspace: null,
        discordServer: null,
        error: reason instanceof Error ? reason.message : "Could not load your channels",
      }),
    ),
    api.notificationRules().then(
      (response): NotificationRules | null => response,
      () => null,
    ),
  ]);
  return { ...methods, rules };
}

/* Linking Slack is one request: withHuman looks the person up in the
 * connected workspace by their own confirmed addresses. The refusals a
 * person can do something about get their own words. */
export function useSlackLink(onLinked: () => void | Promise<void>) {
  const toast = useToast();
  const session = useSession();
  const [linking, setLinking] = useState(false);
  const organization = session.organization?.name ?? "this organization";
  async function link() {
    setLinking(true);
    try {
      const result = await api.linkSlack();
      toast.add({
        type: "approve",
        title: "Slack linked",
        description: result.contact_method.label || undefined,
      });
      await onLinked();
    } catch (reason: unknown) {
      const description =
        reason instanceof RequestError && reason.code === "slack_no_match"
          ? "No Slack account matches your confirmed email addresses. Add the email you use in Slack, then try again."
          : reason instanceof RequestError && reason.status === 404
            ? `Ask an admin to connect ${organization}'s Slack workspace under Integrations first.`
            : reason instanceof RequestError && reason.status === 409
              ? "That Slack account is already linked to another member."
              : reason instanceof RequestError && reason.code === "slack_not_configured"
                ? "This deployment has no Slack application configured."
                : reason instanceof Error
                  ? reason.message
                  : undefined;
      toast.add({ type: "deny", title: "Could not link Slack", description });
    } finally {
      setLinking(false);
    }
  }
  return { link, linking };
}

/* Starting the Discord link: the link is completed by Discord, so
 * leaving the page is the success path; only a refusal to begin is
 * reported here. Discord sends the person back to returnTo. */
export function useDiscordLink(returnTo: OAuthReturn = "settings") {
  const toast = useToast();
  const router = useRouter();
  const { can } = useAccess();
  const session = useSession();
  const [linking, setLinking] = useState(false);
  const organization = session.organization?.name ?? "this organization";
  async function link() {
    setLinking(true);
    try {
      const result = await api.beginDiscordLink(returnTo);
      window.location.assign(result.authorize_url);
    } catch (reason: unknown) {
      setLinking(false);
      const description =
        reason instanceof RequestError && reason.code === "discord_not_connected"
          ? can("connection.write")
            ? "Connect your Discord server under Integrations first."
            : `Ask an admin to connect ${organization}'s Discord server under Integrations first.`
          : reason instanceof RequestError && reason.code === "discord_not_configured"
            ? "This deployment has no Discord application configured."
            : reason instanceof Error
              ? reason.message
              : undefined;
      if (reason instanceof RequestError && reason.code === "discord_not_connected") {
        await router.invalidate();
      }
      toast.add({ type: "deny", title: "Could not start linking Discord", description });
    }
  }
  return { link, linking };
}

/* A typed-in kind's row: the value in mono, a badge while it waits to
 * be confirmed, removable, and proved by the panel the kind hands over
 * (CodeConfirmPanel for one whose code is delivered to it, null for one
 * confirmed some other way). Email's row; an edition's typed-in kinds
 * build theirs the same way. */
export function typedMethodRow({
  removeFailure,
  confirm,
}: {
  removeFailure: string;
  confirm: ComponentType<ContactConfirmProps> | null;
}): ContactMethodRow {
  return {
    title: (method) => <span className="font-mono">{method.value}</span>,
    badge: unconfirmedBadge,
    removeLabel: (method) => `Remove ${method.value}`,
    removeFailure,
    confirm,
  };
}

function unconfirmedBadge(method: ContactMethod): ContactMethodBadge | null {
  return method.verified ? null : { tone: "warn", label: "Unconfirmed", icon: <ClockIcon /> };
}

const EMAIL_ROW = typedMethodRow({
  removeFailure: "Could not remove that address",
  confirm: CodeConfirmPanel,
});

/* The account email: every account message goes there, so it is never
 * removed here and needs no proving. */
const ACCOUNT_EMAIL_ROW: ContactMethodRow = {
  ...EMAIL_ROW,
  badge: () => null,
  cannotRemove: () => "Your account email can't be removed.",
  confirm: null,
};

/* The chat identities, by the name the service knows them by. Slack is
 * linked by the organization's workspace, so it is not unlinked here. */
const SLACK_ROW: ContactMethodRow = {
  title: (method) => method.label || method.value,
  badge: (method) => (method.available ? null : { tone: "neutral", label: "Unavailable" }),
  removeLabel: () => "Unlink Slack",
  removeFailure: "Could not unlink Slack",
  cannotRemove: () => "Slack is linked through your organization's workspace.",
  confirm: null,
};

const DISCORD_ROW: ContactMethodRow = {
  title: (method) => method.label || method.value,
  badge: () => null,
  removeLabel: () => "Unlink Discord",
  removeFailure: "Could not unlink Discord",
  confirm: null,
};

/* A method of a kind this bundle did not register still lists: by its
 * name when it has one, else its raw value, removable, with nothing
 * here to prove it. */
const UNKNOWN_KIND_ROW: ContactMethodRow = {
  title: (method) => method.label || <span className="font-mono">{method.value}</span>,
  badge: unconfirmedBadge,
  removeLabel: (method) => `Remove ${method.label || method.value}`,
  removeFailure: "Could not remove that method",
  confirm: null,
};

/* The methods of kinds core does not own: the registered kinds in the
 * order the edition registered them, then any kind this bundle does not
 * know, as the server listed them. */
function editionMethods(methods: ContactMethod[]): { method: ContactMethod; entry: ContactKind | undefined }[] {
  const foreign = methods.filter((method) => !isCoreChannelKind(method.kind));
  const registered = extensions.contactKinds.flatMap((entry) =>
    foreign.filter((method) => method.kind === entry.kind).map((method) => ({ method, entry })),
  );
  const unknown = foreign
    .filter((method) => contactKind(method.kind) === undefined)
    .map((method) => ({ method, entry: undefined }));
  return [...registered, ...unknown];
}

/* Every channel the person has, one SettingRow each: the addresses,
 * then the edition's kinds, each drawn as its entry says, then the
 * linked chat identities, with the ones still waiting on you (and the
 * panel that proves each) at the end. Rows separate with hairlines and expect the caller's
 * horizontal inset. Every change is followed by onChanged, a reload of
 * the caller's page, so the list always shows what the server holds. */
export function ChannelRows({
  methods,
  onChanged,
}: {
  methods: ContactMethod[];
  onChanged: () => Promise<void>;
}) {
  const toast = useToast();
  const [busyId, setBusyId] = useState<string | null>(null);

  function fail(title: string, reason: unknown) {
    toast.add({
      type: "deny",
      title,
      description: reason instanceof Error ? reason.message : undefined,
    });
  }

  /* Runs one call on a method's behalf: the row shows busy meanwhile, a
   * refusal becomes a toast under the failure title, and the answer
   * comes back, or null when it failed. Reloading is the caller's. */
  async function act<T>(id: string, action: () => Promise<T>, failure: string): Promise<T | null> {
    setBusyId(id);
    try {
      return await action();
    } catch (reason: unknown) {
      fail(failure, reason);
      return null;
    } finally {
      setBusyId(null);
    }
  }

  async function remove(method: ContactMethod, shape: ContactMethodRow) {
    const call = method.kind === "discord" ? () => api.unlinkDiscord() : () => api.removeContactMethod(method.id);
    if ((await act(method.id, call, shape.removeFailure)) !== null) await onChanged();
  }

  const emails = methods.filter((method) => method.kind === "email");
  const slack = methods.find((method) => method.kind === "slack") ?? null;
  const discord = methods.find((method) => method.kind === "discord") ?? null;

  const entries: { method: ContactMethod; kind: string; icon: ReactNode; shape: ContactMethodRow }[] = [
    ...emails.map((method) =>
      method.primary
        ? { method, kind: "Account email", icon: <MailIcon />, shape: ACCOUNT_EMAIL_ROW }
        : { method, kind: "Email", icon: <MailIcon />, shape: EMAIL_ROW },
    ),
    ...editionMethods(methods).map(({ method, entry }) => ({
      method,
      kind: entry?.label ?? method.kind,
      icon: entry?.icon ?? <MessageIcon />,
      shape: entry?.row ?? UNKNOWN_KIND_ROW,
    })),
    ...(slack ? [{ method: slack, kind: "Slack", icon: <SlackIcon />, shape: SLACK_ROW }] : []),
    ...(discord ? [{ method: discord, kind: "Discord", icon: <DiscordIcon />, shape: DISCORD_ROW }] : []),
  ];
  /* Methods still being confirmed go last, each group in its own order. */
  const ordered = [...entries.filter(({ method }) => method.verified), ...entries.filter(({ method }) => !method.verified)];

  return (
    <>
      {ordered.map(({ method, kind, icon, shape }) => (
        <Fragment key={method.id}>
          <MethodRow
            method={method}
            kind={kind}
            icon={icon}
            row={shape}
            busy={busyId === method.id}
            act={(action, failure) => act(method.id, action, failure)}
            onRemove={() => void remove(method, shape)}
            onRefresh={onChanged}
          />
          {method.kind === "slack" && !method.available ? (
            <Callout tone="neutral">Slack is unavailable. Link your account again after the workspace is reconnected.</Callout>
          ) : null}
        </Fragment>
      ))}
    </>
  );
}

/* One method's row, drawn as its kind says a method reads: the kind's
 * icon with its name as the tooltip, the title, a badge only for a
 * state that needs the person, the quiet remove (disabled with the
 * reason when the list cannot remove it), and for a method still
 * waiting on the person, the panel that proves it. */
function MethodRow({
  method,
  kind,
  icon,
  row,
  busy,
  act,
  onRemove,
  onRefresh,
}: {
  method: ContactMethod;
  kind: string;
  icon: ReactNode;
  row: ContactMethodRow;
  busy: boolean;
  act: ContactConfirmProps["act"];
  onRemove: () => void;
  onRefresh: () => Promise<void>;
}) {
  const badge = row.badge(method);
  const blocked = row.cannotRemove?.(method) ?? null;
  const Confirm = row.confirm;
  const removeButton = (
    <IconButton
      aria-label={row.removeLabel(method)}
      variant="ghost"
      size="sm"
      disabled={busy || blocked !== null}
      onClick={onRemove}
    >
      <TrashIcon />
    </IconButton>
  );
  return (
    <SettingRow
      icon={
        <Tooltip content={kind}>
          <span role="img" aria-label={kind} className="flex">
            {icon}
          </span>
        </Tooltip>
      }
      label={row.title(method)}
      panel={
        !method.verified && Confirm ? (
          <Confirm method={method} busy={busy} act={act} onRefresh={onRefresh} />
        ) : null
      }
    >
      {badge ? (
        <Badge tone={badge.tone} icon={badge.icon}>
          {badge.label}
        </Badge>
      ) : null}
      {blocked !== null ? (
        /* A disabled button takes no pointer events, so the reason
         * hangs off a focusable wrapper. */
        <Tooltip content={blocked}>
          <span tabIndex={0} className="inline-flex rounded-sm">
            {removeButton}
          </span>
        </Tooltip>
      ) : (
        removeButton
      )}
    </SettingRow>
  );
}

/* The panel for a kind whose code is delivered to the method: the code
 * field, Confirm, and a fresh code on request. Email's, and any
 * edition kind's that proves itself the same way. */
export function CodeConfirmPanel({ method, busy, act, onRefresh }: ContactConfirmProps) {
  const [code, setCode] = useState("");

  return (
    <form
      className="flex flex-wrap items-end gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        void act(() => api.verifyContactMethod(method.id, code), "That code did not work").then(
          async (verified) => {
            if (verified === null) return;
            setCode("");
            await onRefresh();
          },
        );
      }}
    >
      <TextField
        label="Confirmation code"
        value={code}
        onChange={(event) => setCode(event.target.value)}
        inputMode="numeric"
        autoComplete="one-time-code"
        placeholder="123456"
        className="w-[140px]"
        mono
        required
      />
      <Button type="submit" variant="neutral" loading={busy}>
        Confirm
      </Button>
      <Button
        type="button"
        variant="ghost"
        onClick={() => void act(() => api.resendContactMethodCode(method.id), "Could not send a new code")}
        disabled={busy}
      >
        Send a new code
      </Button>
    </form>
  );
}

/* The words a typed-in kind's form needs. */
export interface ContactFormCopy {
  title: string;
  description: string;
  field: string;
  type: "email" | "tel";
  placeholder: string;
  hint?: string;
  submit: string;
  failure: string;
}

export const EMAIL_FORM: ContactFormCopy = {
  title: "Add an email address",
  description: "We'll send a code to confirm it.",
  field: "Address",
  type: "email",
  placeholder: "you@example.com",
  submit: "Send code",
  failure: "Could not add that address",
};

/* The footer every add flow ends on: Cancel or Done, then the one
 * action, framed by the caller (a settings section, a well in the
 * guided setup). */
export function FlowFooter({ children }: { children: ReactNode }) {
  return (
    <div className="flex justify-end gap-2.5 border-t-hairline border-edge px-4 py-3">{children}</div>
  );
}

/* The form for a typed-in kind: the value field, whatever the kind
 * puts under it (children: the agreement a kind may need before its
 * first message), the refusal, and a footer of Cancel and the submit.
 * The caller owns the call: submit waits until ready says the extras
 * are satisfied, and what the call threw shows under the failure
 * title. Adding leaves the form working until the caller takes it
 * away. */
export function ContactMethodForm({
  copy,
  children,
  ready = true,
  onSubmit,
  onCancel,
}: {
  copy: ContactFormCopy;
  children?: ReactNode;
  ready?: boolean;
  onSubmit: (value: string) => Promise<void>;
  onCancel: () => void;
}) {
  const [value, setValue] = useState("");
  const [working, setWorking] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmed = value.trim();
    if (!trimmed || !ready) return;
    setWorking(true);
    setError(null);
    try {
      await onSubmit(trimmed);
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : copy.failure);
      setWorking(false);
    }
  }

  return (
    <form onSubmit={submit}>
      <div className="flex flex-col gap-4 px-4 py-4">
        <TextField
          label={copy.field}
          type={copy.type}
          placeholder={copy.placeholder}
          description={copy.hint}
          value={value}
          onChange={(event) => setValue(event.target.value)}
          autoFocus
          required
          className="max-w-[360px]"
        />
        {children}
        {error ? (
          <Callout tone="deny" title={copy.failure}>
            {error}
          </Callout>
        ) : null}
      </div>
      <FlowFooter>
        <Button type="button" variant="neutral" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" variant="primary" loading={working} disabled={!value.trim() || !ready}>
          {copy.submit}
        </Button>
      </FlowFooter>
    </form>
  );
}

/* Email's form. Adding sends a code to the address; the new row on the
 * caller's channel list carries the panel that waits for it. The value
 * in the toast is the stored one, so an address typed with stray
 * spaces reads back as kept. */
export function EmailForm({
  onAdded,
  onCancel,
}: {
  onAdded: (created: ContactMethod) => void | Promise<void>;
  onCancel: () => void;
}) {
  const toast = useToast();
  return (
    <ContactMethodForm
      copy={EMAIL_FORM}
      onCancel={onCancel}
      onSubmit={async (value) => {
        const created = await api.addContactMethod("email", value);
        toast.add({
          type: "accent",
          title: "Code sent",
          description: `Enter the code we sent to ${created.contact_method.value}.`,
        });
        await onAdded(created.contact_method);
      }}
    />
  );
}

/* The chat kinds nobody types in. Guides explain whether the
 * organization needs setup before a person can link their account. In
 * the guided setup (returnTo "onboarding") an admin connects it in place
 * rather than under Integrations. */
export function channelGuideCopy(
  kind: GuidedKind,
  organization: string,
  workspace: SlackWorkspace | null,
  server: DiscordServer | null,
  canConnect: boolean,
  returnTo: OAuthReturn = "settings",
) {
  const inPlace = returnTo === "onboarding";
  return {
    slack: {
      title: "Add Slack",
      description: "Get notified via Slack DM.",
      body: workspace
        ? `Link your Slack account in ${workspace.team_name}. It must use one of your confirmed email addresses.`
        : canConnect
          ? inPlace
            ? "Connect your Slack workspace first."
            : "Install withHuman in your Slack workspace under Integrations first."
          : `Ask an admin to install withHuman in ${organization}'s Slack workspace under Integrations first.`,
      done: "Linked",
    },
    discord: {
      title: "Add Discord",
      description: "Get notified via Discord DM.",
      body: server
        ? `Link your Discord account to get notified by direct message. You must be a member of ${server.guild_name} and allow direct messages from its members.`
        : canConnect
          ? inPlace
            ? "Connect your Discord server first."
            : "Connect your Discord server under Integrations first."
          : `Ask an admin to connect ${organization}'s Discord server under Integrations first.`,
      done: "Linked",
    },
  }[kind];
}

/* Starting the organization's Slack or Discord install from where the
 * person is, for the guided setup, which must not send them to
 * Integrations. The provider sends them back to returnTo. */
function useChannelInstall(kind: GuidedKind, returnTo: OAuthReturn) {
  const toast = useToast();
  const [installing, setInstalling] = useState(false);
  async function install() {
    setInstalling(true);
    try {
      const result =
        kind === "slack" ? await api.beginSlackInstall(returnTo) : await api.beginDiscordInstall(returnTo);
      window.location.assign(result.authorize_url);
    } catch (reason: unknown) {
      setInstalling(false);
      const label = kind === "slack" ? "Slack" : "Discord";
      const description =
        reason instanceof RequestError && reason.code === `${kind}_not_configured`
          ? `This deployment has no ${label} application configured.`
          : reason instanceof Error
            ? reason.message
            : undefined;
      toast.add({ type: "deny", title: `Could not start the ${label} install`, description });
    }
  }
  return { install, installing };
}

/* One guide's body and footer, framed by the caller: the explanation
 * with a badge once the channel exists, then Done (or Cancel for a chat
 * link not yet made) and the one action that starts it. */
export function ChannelGuide({
  kind,
  methods,
  onDone,
  onAddEmail,
  onLinked,
  workspace,
  server,
  returnTo = "settings",
}: {
  kind: GuidedKind;
  methods: ContactMethod[];
  onDone: () => void;
  /* Slack finds people by a confirmed email address: this opens that
   * form for someone the workspace does not know by their current ones. */
  onAddEmail: () => void;
  /* Called once Slack is linked, so the caller can refresh the list. */
  onLinked: () => void | Promise<void>;
  /* The connected Slack workspace, or null when an admin has yet to
   * install withHuman there. */
  workspace: SlackWorkspace | null;
  server: DiscordServer | null;
  /* Where a Slack or Discord round trip comes back to. The guided setup
   * passes "onboarding" and connects the organization in place. */
  returnTo?: OAuthReturn;
}) {
  const session = useSession();
  const navigate = useNavigate();
  const { can } = useAccess();
  const { link, linking } = useDiscordLink(returnTo);
  const { link: linkSlack, linking: linkingSlack } = useSlackLink(onLinked);
  const { install, installing } = useChannelInstall(kind, returnTo);
  const organization = session.organization?.name ?? "this organization";
  const linked = methods.some((method) => method.kind === kind && method.available);
  const canConnect = can("connection.write");
  const copy = channelGuideCopy(kind, organization, workspace, server, canConnect, returnTo);
  /* Chat linking needs an organization connection first. Members who
   * can set one up connect it in place in the guided setup and go to
   * Integrations elsewhere; everyone else reads why. */
  const slackReady = kind === "slack" && !linked && workspace !== null;
  const slackNeedsInstall = kind === "slack" && !linked && workspace === null;
  const discordReady = kind === "discord" && !linked && server !== null;
  const discordNeedsInstall = kind === "discord" && !linked && server === null;

  return (
    <>
      <div className="flex items-start justify-between gap-6 px-4 py-4">
        <p className="max-w-[560px] text-body text-ink-secondary">{copy.body}</p>
        {linked ? (
          <Badge tone="approve" icon={<CheckIcon />}>
            {copy.done}
          </Badge>
        ) : null}
      </div>
      <FlowFooter>
        <Button type="button" variant="neutral" onClick={onDone}>
          {discordReady || slackReady ? "Cancel" : "Done"}
        </Button>
        {slackReady ? (
          <>
            <Button type="button" variant="neutral" onClick={onAddEmail}>
              Add an email address
            </Button>
            <Button
              type="button"
              variant="primary"
              loading={linkingSlack}
              onClick={() => void linkSlack()}
            >
              Link Slack
            </Button>
          </>
        ) : null}
        {(slackNeedsInstall || discordNeedsInstall) && canConnect ? (
          returnTo === "onboarding" ? (
            <Button type="button" variant="primary" loading={installing} onClick={() => void install()}>
              {slackNeedsInstall ? "Connect Slack" : "Connect Discord"}
            </Button>
          ) : (
            <Button
              type="button"
              variant="primary"
              onClick={() => void navigate({ to: "/settings/integrations" })}
            >
              Go to Integrations
            </Button>
          )
        ) : null}
        {discordReady ? (
          <Button type="button" variant="primary" loading={linking} onClick={() => void link()}>
            Link Discord
          </Button>
        ) : null}
      </FlowFooter>
    </>
  );
}
