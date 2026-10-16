import { useEffect, useState, type ReactNode } from "react";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import {
  Card,
  CardBody,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/ds/components/card";
import { MailIcon } from "@/ds/icons";
import { DiscordIcon, SlackIcon } from "@/ds/icons/brands";
import { cn } from "@/ds/lib/cn";
import {
  ChannelGuide,
  ChannelRows,
  channelGuideCopy,
  EMAIL_FORM,
  EmailForm,
  isGuidedKind,
  type ChannelKind,
  type ChannelsPage,
} from "./contact-channels";
import { NotificationLadders, type LadderDraft } from "./notification-rules";
import { useAccess } from "./authorization";
import { contactKind, extensions } from "./extensions";
import { forgetReturnedProvider, returnedFromProvider } from "./oauth-outcome";
import { useSession } from "./principal";
import { useRuntimeConfig } from "./use-runtime-config";

/*
 * The two steps after the agents are chosen: where withHuman can reach
 * the person, then how each request climbs those channels. They come
 * before the agents are connected because the escalation path
 * provisioning writes notifies the person, and the test at the end
 * sends a real prompt through what they set here. Together they are the
 * Notifications settings page split over two steps, with the
 * add-a-channel picker inline rather than on a page of its own, because
 * the flow has the pane to itself and leaving it would lose the
 * person's place. For the same reason nothing in the step leaves the
 * flow: an admin connects Slack or Discord in place, and the provider
 * sends them back here with that channel's guide open again. Neither
 * step writes to the session until the rules
 * step's Continue, which records only that both were passed; the
 * channels and ladders themselves are the person's own settings.
 */

/* The picker's kinds, in the add-channel page's order: email, the
 * kinds the edition delivers to, then the chat kinds. */
function channelKinds(): { kind: ChannelKind; label: string; icon: ReactNode }[] {
  return [
    { kind: "email", label: "Email", icon: <MailIcon /> },
    ...extensions.contactKinds.map((entry) => ({ kind: entry.kind, label: entry.label, icon: entry.icon })),
    { kind: "slack", label: "Slack", icon: <SlackIcon /> },
    { kind: "discord", label: "Discord", icon: <DiscordIcon /> },
  ];
}

export function OnboardingContactStep({
  page,
  onChanged,
  onContinue,
}: {
  page: ChannelsPage;
  /* Reloads the page's channels and ladders after any change. */
  onChanged: () => Promise<void>;
  /* Moves on to the rules step. Nothing is written, so it never fails. */
  onContinue: () => void;
}) {
  const { methods, error: loadError } = page;
  const { config } = useRuntimeConfig();
  const session = useSession();
  const { can } = useAccess();
  const organization = session.organization?.name ?? "this organization";
  /* The kind being added, whose form or guide unfolds under the picker.
   * Back from a Slack or Discord round trip, that kind's guide. */
  const [picked, setPicked] = useState<ChannelKind | null>(returnedFromProvider);
  useEffect(forgetReturnedProvider, []);

  const kinds = channelKinds();
  const enabled = (kind: ChannelKind) =>
    kind === "email"
      ? true
      : kind === "slack"
        ? (config?.slack_enabled ?? false)
        : kind === "discord"
          ? (config?.discord_enabled ?? false)
          : config !== null && (contactKind(kind)?.available(config) ?? false);
  const unavailable = kinds.filter((entry) => !enabled(entry.kind)).map((entry) => entry.label);

  /* The unfolded flow's heading, and for a registered kind its flow. */
  const entry = picked === null || isGuidedKind(picked) || picked === "email" ? undefined : contactKind(picked);
  const pickedCopy =
    picked === null
      ? null
      : picked === "email"
        ? EMAIL_FORM
        : isGuidedKind(picked)
          ? channelGuideCopy(
              picked,
              organization,
              page.slackWorkspace,
              page.discordServer,
              can("connection.write"),
              "onboarding",
            )
          : (entry?.add ?? null);
  const Flow = entry?.add.component;

  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>Get notified</CardTitle>
          <CardDescription>
            Add the channels where you want to hear about calls waiting for your approval.
          </CardDescription>
        </div>
      </CardHeader>
      <CardBody className="flex flex-col gap-4">
        {loadError ? (
          <Callout tone="warn" title="Could not load your channels">
            {loadError}
          </Callout>
        ) : null}
        <div className="flex flex-col gap-2">
          <LabelLine>Your channels</LabelLine>
          <div className="rounded-lg border-hairline border-edge bg-canvas px-4">
            <ChannelRows methods={methods} onChanged={onChanged} />
          </div>
        </div>
        <div className="flex flex-col gap-2">
          <LabelLine>Add a channel</LabelLine>
          <div className="flex flex-wrap items-center gap-1.5" aria-label="Channel kinds">
            {kinds.map((choice) => (
              <Button
                key={choice.kind}
                size="sm"
                variant="neutral"
                disabled={!enabled(choice.kind)}
                aria-pressed={picked === choice.kind}
                className={cn(picked === choice.kind && "border-accent ring-1 ring-accent")}
                onClick={() => setPicked(picked === choice.kind ? null : choice.kind)}
              >
                {choice.icon}
                {choice.label}
              </Button>
            ))}
          </div>
          {unavailable.length > 0 ? (
            <p className="text-label font-normal text-ink-muted">
              {unavailable.join(" and ")} {unavailable.length === 1 ? "is" : "are"} not set up on
              this deployment.
            </p>
          ) : null}
          {picked !== null && pickedCopy !== null ? (
            <div className="overflow-hidden rounded-lg border-hairline border-edge bg-canvas">
              <div className="border-b-hairline border-edge px-4 py-3">
                <h4 className="text-body font-semibold text-ink">{pickedCopy.title}</h4>
                <p className="text-label font-normal text-ink-muted">{pickedCopy.description}</p>
              </div>
              {picked === "email" ? (
                <EmailForm
                  onAdded={async () => {
                    setPicked(null);
                    await onChanged();
                  }}
                  onCancel={() => setPicked(null)}
                />
              ) : isGuidedKind(picked) ? (
                <ChannelGuide
                  kind={picked}
                  methods={methods}
                  onDone={() => setPicked(null)}
                  onAddEmail={() => setPicked("email")}
                  onLinked={onChanged}
                  workspace={page.slackWorkspace}
                  server={page.discordServer}
                  returnTo="onboarding"
                />
              ) : Flow ? (
                <Flow
                  methods={methods}
                  onAdded={async () => {
                    setPicked(null);
                    await onChanged();
                  }}
                  onDone={() => setPicked(null)}
                />
              ) : null}
            </div>
          ) : null}
        </div>
      </CardBody>
      <CardFooter className="justify-end">
        <Button variant="primary" onClick={onContinue}>
          Continue
        </Button>
      </CardFooter>
    </Card>
  );
}

/* The step after the channels: both ladders, and the Continue that
 * saves any edits to them and then records the pair of steps on the
 * session the first time through. The draft lives with the flow, which
 * asks before a way out that would drop it. */
export function OnboardingRulesStep({
  page,
  ladders,
  onChanged,
  onContinue,
  continuing,
  error,
}: {
  page: ChannelsPage;
  ladders: LadderDraft;
  /* Reloads the page's channels and ladders. */
  onChanged: () => Promise<void>;
  onContinue: () => void;
  continuing: boolean;
  /* Why the last Continue did not get anywhere. */
  error: string;
}) {
  const [saveError, setSaveError] = useState("");

  const next = async () => {
    setSaveError("");
    try {
      await ladders.save();
    } catch (reason) {
      setSaveError(reason instanceof Error ? reason.message : "Could not save your rules.");
      return;
    }
    onContinue();
  };

  const problem = saveError || error;

  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>Decide how each request reaches you</CardTitle>
          <CardDescription>
            Choose which channels notify you, and when. Urgent and standard requests each have
            their own order.
          </CardDescription>
        </div>
      </CardHeader>
      <CardBody className="flex flex-col gap-5">
        <NotificationLadders
          ladders={ladders}
          methods={page.methods}
          onRetry={() => void onChanged()}
          embedded
        />
        {problem ? (
          <Callout tone="warn" title="Could not continue">
            {problem}
          </Callout>
        ) : null}
      </CardBody>
      <CardFooter className="justify-end">
        <Button
          variant="primary"
          loading={ladders.saving || continuing}
          onClick={() => void next()}
        >
          Continue
        </Button>
      </CardFooter>
    </Card>
  );
}

/* A quiet group label inside a card, the tools step's line. */
function LabelLine({ children }: { children: ReactNode }) {
  return (
    <div className="flex h-[26px] items-center gap-2 text-label text-ink-muted select-none">
      <span>{children}</span>
      <span className="flex-1 border-t-hairline border-edge" />
    </div>
  );
}
