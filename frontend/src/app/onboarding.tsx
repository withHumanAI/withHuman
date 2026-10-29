import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { Badge } from "@/ds/components/badge";
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
import { Checkbox } from "@/ds/components/checkbox";
import { useCopyToClipboard } from "@/ds/components/copy-button";
import { CodeBlock } from "@/ds/components/code-block";
import { ConfirmDialog } from "@/ds/components/confirm-dialog";
import { EmptyState } from "@/ds/components/empty-state";
import { HoverCard, HoverCardBody, HoverCardTitle } from "@/ds/components/hover-card";
import { List, ListRowContent } from "@/ds/components/list";
import { LoadingDots } from "@/ds/components/loading-dots";
import { RadioCard, RadioGroup } from "@/ds/components/radio-group";
import { Spinner } from "@/ds/components/spinner";
import { Table } from "@/ds/components/table";
import { TextField } from "@/ds/components/text-field";
import { Stepper, type Step } from "@/ds/components/stepper";
import { useSidebar } from "@/ds/components/sidebar-reopen";
import { useToast } from "@/ds/components/toast";
import { UnsavedDialog, UnsavedGuard } from "@/ds/components/unsaved-guard";
import { Page } from "@/ds/components/page";
import {
  AgentIcon,
  AlertIcon,
  CheckIcon,
  CopyIcon,
  MonitorIcon,
  ToolIcon,
} from "@/ds/icons";
import { cn } from "@/ds/lib/cn";
import { api } from "../api";
import type {
  OnboardingBegin,
  OnboardingDetection,
  OnboardingDiscoveryError,
  OnboardingInstallResult,
  OnboardingMCPServer,
  OnboardingOutcome,
  OnboardingSession,
  OnboardingStatus,
  OnboardingTool,
  RequestApprovalPipelineRevision,
  StoredBranchBlock,
} from "../types";
import { ENTRY_PIPELINE_KEY } from "../types";
import type { ChannelsPage } from "./contact-channels";
import { definitionsFromStored } from "./pipeline-blocks";
import { ladderLines, useLadderDraft } from "./notification-rules";
import { OnboardingApprovals, useTestActivity, type TestActivity } from "./onboarding-approvals";
import { OnboardingContactStep, OnboardingRulesStep } from "./onboarding-contacts";
import { refreshSession } from "./session-store";
import { useOnboardingSession } from "./use-onboarding-session";
import { useRuntimeConfig } from "./use-runtime-config";
import { useTitle } from "./use-title";

/* Display names for the runtime keys the CLI reports. */
const RUNTIME_NAMES: Record<string, string> = {
  "claude-code": "Claude Code",
  codex: "Codex",
  openclaw: "OpenClaw",
  pi: "Pi",
  hermes: "Hermes Agent",
  deepseek: "DeepSeek Harness",
  gemini: "Gemini CLI",
  antigravity: "Antigravity",
};

export function runtimeName(key: string): string {
  return RUNTIME_NAMES[key] ?? key;
}

/* A dev server has no release to download, so the one command, run from
 * the repository root, builds the CLI from the checkout and runs its setup
 * instead. */
function devSetupCommand(origin: string, code: string): string {
  /* ~/.local/bin needs no sudo, and the hooks the CLI installs carry its
   * absolute path, so it does not need to be on PATH. */
  return `mkdir -p ~/.local/bin && go build -o ~/.local/bin/withhuman ./cmd/withhuman-cli && ~/.local/bin/withhuman onboard --url ${origin} --code ${code}`;
}

const STEPS: Array<Pick<Step, "title" | "description">> = [
  {
    title: "Install the CLI",
    description: "One command on the machine the agent runs on.",
  },
  { title: "Choose agents", description: "Pick the runtimes the CLI found." },
  { title: "Get notified", description: "Choose where you get notified." },
  { title: "Notification rules", description: "Decide how each request reaches you." },
  { title: "Approval rules", description: "Decide which tool calls wait for you." },
  { title: "Test it", description: "Trigger a gated call and decide it here." },
];

/* Which step a session sits on; 6 is past the end. The CLI's phases
 * map straight to steps; the person's own steps are the stamps on the
 * session: the notification pair (steps 2 and 3) until channels_set_at,
 * the rules step (4) from then until rules_set_at once the hooks are in,
 * then the test. Provisioning and enrolment take a few seconds and stay
 * on the rules step, behind its connection line, rather than being a
 * step of their own. */
function stepIndex(session: OnboardingSession | null): number {
  switch (session?.status ?? null) {
    case null:
    case "awaiting_cli":
      return 0;
    case "connected":
    case "detected":
      return 1;
    case "agents_selected":
    case "tools_discovered":
      return session?.channels_set_at ? 4 : 2;
    case "provisioning":
    case "enrolled":
      return 4;
    case "installed":
      return session?.rules_set_at ? 5 : 4;
    case "tested":
      return 5;
    case "completed":
    case "skipped":
      return 6;
  }
}

/* The notification step the person is on, when the session alone does
 * not say: see the pane selection in OnboardingFlow. */
type SettingsStage = "channels" | "rules" | null;

function errorMessage(reason: unknown, fallback: string): string {
  return reason instanceof Error && reason.message ? reason.message : fallback;
}

function isTerminal(status: OnboardingStatus | null): boolean {
  return status === "completed" || status === "skipped";
}

/* The statuses in which the person's own steps (channels, notification
 * and approval rules) are open: agents chosen, session not over. */
function inPersonalSteps(status: OnboardingStatus | null): boolean {
  return (
    status === "agents_selected" ||
    status === "tools_discovered" ||
    status === "provisioning" ||
    status === "enrolled" ||
    status === "installed" ||
    status === "tested"
  );
}

export function OnboardingScreen({
  canOnboard,
  page,
}: {
  canOnboard: boolean;
  /* The person's channels and ladders, from the route loader. */
  page: ChannelsPage;
}) {
  useTitle("Welcome");
  return (
    <Page width="narrow">
      {canOnboard ? (
        <OnboardingFlow page={page} />
      ) : (
        <Card>
          <EmptyState
            icon={<AgentIcon width={18} height={18} />}
            title="Ask an owner to run the setup"
            description="You need permission to connect agents and review requests."
          />
        </Card>
      )}
    </Page>
  );
}

function OnboardingFlow({ page }: { page: ChannelsPage }) {
  const navigate = useNavigate();
  const router = useRouter();
  const toast = useToast();
  const { session, loading, error, setSession } = useOnboardingSession();
  const { config } = useRuntimeConfig();
  const [begin, setBegin] = useState<OnboardingBegin | null>(null);
  const [beginError, setBeginError] = useState("");
  const [beginning, setBeginning] = useState(false);
  const [closing, setClosing] = useState<OnboardingOutcome | null>(null);
  /* Going back from the notification steps to re-pick agents, while the
   * session itself stays where it is until the person saves. */
  const [reviewingAgents, setReviewingAgents] = useState(false);
  /* Which of the two notification steps is open. Before channels_set_at
   * null and "channels" are the channels step and "rules" the one after
   * it; from then on null is whatever comes next (the rules step, the
   * test) and either name a way back. They change nothing about the
   * session, so they are open from anywhere past the agent choice. */
  const [settingsStage, setSettingsStage] = useState<SettingsStage>(null);
  /* Back from the test to the approval rules, to change them after
   * seeing what a call looks like. */
  const [reviewingRules, setReviewingRules] = useState(false);
  /* Once the agents are provisioned there is no way back but a fresh
   * session, which the CLI has to be run for again; that deserves a
   * confirmation. */
  const [confirmRestart, setConfirmRestart] = useState(false);
  /* The rules step's ladders, held here so a way out of the step can
   * ask before it drops their edits. */
  const ladders = useLadderDraft(page.rules, page.methods, () => router.invalidate());
  const [leaving, setLeaving] = useState<(() => void) | null>(null);

  const status = session?.status ?? null;
  const terminal = isTerminal(status);
  const hostname = session?.machine.hostname || "this machine";

  /* What the test step has seen from the agents just enrolled: the
   * calls since the person left the rules step. Once a gated call has
   * arrived, deciding it is the way out and skipping no longer means
   * anything. */
  const activity = useTestActivity(
    session?.enrollments.map((enrollment) => enrollment.agent_slug) ?? [],
    session?.rules_set_at ?? session?.created_at ?? null,
  );
  const tested = (status === "installed" || status === "tested") && activity.tested;

  /* The writes that are the person's alone: passing their notification
   * steps, leaving the rules step, recording the request they decided. */
  const [advancing, setAdvancing] = useState(false);
  const [advanceError, setAdvanceError] = useState("");
  async function advance(write: () => Promise<OnboardingSession>, failure: string) {
    setAdvancing(true);
    setAdvanceError("");
    try {
      setSession(await write());
      setSettingsStage(null);
      setReviewingRules(false);
    } catch (reason) {
      const message = errorMessage(reason, "");
      setAdvanceError(message || failure);
      toast.add({ type: "deny", title: failure, description: message });
    } finally {
      setAdvancing(false);
    }
  }

  /* The setup wants the pane to itself: the sidebar is held closed
   * while the flow is open and released when the person leaves. Showing
   * it in between is theirs to do and ends the hold. */
  const sidebar = useSidebar();
  const hold = sidebar?.hold;
  useEffect(() => {
    if (!hold) return;
    hold(true);
    return () => hold(false);
  }, [hold]);

  /* Starting a session is the only way to get the setup command: with
   * no session, or one still waiting for the CLI from an earlier load,
   * begin a fresh one (which supersedes the old). Once per mount on its
   * own; the retry button and the done card call it again by hand. */
  const autoBegun = useRef(false);
  const start = useCallback(async () => {
    /* A session begun by hand is the one begun for this mount: the
     * effect below must not open a second one and supersede it. */
    autoBegun.current = true;
    setBeginning(true);
    setBeginError("");
    try {
      const result = await api.onboardingBegin();
      setBegin(result);
      setSession(result.session);
      setReviewingAgents(false);
      setReviewingRules(false);
      setSettingsStage(null);
    } catch (reason) {
      setBeginError(errorMessage(reason, "Could not start a setup session."));
    } finally {
      setBeginning(false);
    }
  }, [setSession]);

  useEffect(() => {
    if (loading || error || autoBegun.current) return;
    if (session !== null && session.status !== "awaiting_cli") return;
    autoBegun.current = true;
    void start();
  }, [loading, error, session, start]);

  useEffect(() => {
    setReviewingAgents(false);
  }, [status]);

  async function complete(outcome: OnboardingOutcome) {
    setClosing(outcome);
    try {
      await api.onboardingComplete(outcome);
      /* Finished or skipped, the workspace is theirs: open the sidebar
       * so the queue arrives with its navigation, whatever they had
       * before the setup held it closed. The session learns the outcome
       * and what is set up, so the sidebar's Finish setup card shows
       * what is left. */
      void refreshSession();
      sidebar?.reopen();
      void navigate({ to: "/queue" });
    } catch (reason) {
      toast.add({
        type: "deny",
        title: outcome === "skipped" ? "Could not skip the setup" : "Could not finish the setup",
        description: errorMessage(reason, ""),
      });
      setClosing(null);
    }
  }

  /* The test's way out: record the request decided here, then finish.
   * A finish that fails after the record leaves the session in tested,
   * where finishing alone is what is left. */
  async function finish(requestId: string) {
    setClosing("finished");
    try {
      if (status === "installed") setSession(await api.onboardingRecordTest(requestId));
      await api.onboardingComplete("finished");
      void refreshSession();
      sidebar?.reopen();
      void navigate({ to: "/queue" });
    } catch (reason) {
      toast.add({
        type: "deny",
        title: "Could not finish the setup",
        description: errorMessage(reason, ""),
      });
      setClosing(null);
    }
  }

  const expired = session !== null && !terminal && Date.parse(session.expires_at) <= Date.now();
  const personal = inPersonalSteps(status);
  const channelsDone = Boolean(session?.channels_set_at);
  const rulesDone =
    Boolean(session?.rules_set_at) && (status === "installed" || status === "tested");
  const onChannels =
    personal && (settingsStage === "channels" || (!channelsDone && settingsStage === null));
  const onRules = personal && !onChannels && settingsStage === "rules";
  const onTools = personal && !onChannels && !onRules && (!rulesDone || reviewingRules);
  const current = onChannels
    ? 2
    : onRules
      ? 3
      : onTools
        ? 4
        : reviewingAgents
          ? 1
          : stepIndex(session);
  /* Which done steps offer a way back. Before provisioning the agents
   * can be re-picked (the CLI discovers again); from provisioning on,
   * that means starting over. The notification steps and the rules
   * step are the person's own and stay open from wherever they are: a
   * prompt that did not arrive is usually a channel or a rule to fix,
   * and a test that surprised them is a rule to change. */
  const beforeProvisioning = status === "agents_selected" || status === "tools_discovered";
  function wayBack(index: number): (() => void) | undefined {
    if (session === null || terminal || expired || index >= current) return undefined;
    switch (index) {
      case 0:
        return () => setConfirmRestart(true);
      case 1:
        return beforeProvisioning ? () => setReviewingAgents(true) : () => setConfirmRestart(true);
      case 2:
        return personal ? () => setSettingsStage("channels") : undefined;
      case 3:
        return personal && channelsDone ? () => setSettingsStage("rules") : undefined;
      case 4:
        return () => {
          setSettingsStage(null);
          setReviewingRules(true);
        };
      default:
        return undefined;
    }
  }
  /* Leaving the rules step with edits to the ladders asks first. */
  function leaveRules(go: () => void) {
    if (onRules && ladders.dirty.length > 0) setLeaving(() => go);
    else go();
  }
  async function saveLadders() {
    try {
      await ladders.save();
    } catch (reason) {
      toast.add({ type: "deny", title: "Could not save your rules", description: errorMessage(reason, "") });
      throw reason;
    }
  }
  const steps: Step[] = STEPS.map((step, index) => {
    const back = wayBack(index);
    return {
      ...step,
      status: index < current ? "done" : index === current ? "current" : "upcoming",
      onSelect: back && (() => leaveRules(back)),
    };
  });

  let pane: ReactNode;
  if (loading) {
    pane = (
      <Card>
        <CardBody>
          <Waiting label="Loading your setup" />
        </CardBody>
      </Card>
    );
  } else if (error) {
    pane = (
      <Callout tone="warn" title="Could not load the setup session">
        {error}
      </Callout>
    );
  } else if (expired) {
    pane = (
      <Card>
        <CardHeader>
          <div>
            <CardTitle>This setup session expired</CardTitle>
            <CardDescription>
              Start again to get a new code.
            </CardDescription>
          </div>
        </CardHeader>
        <CardFooter>
          <Button variant="primary" loading={beginning} onClick={() => void start()}>
            Start again
          </Button>
        </CardFooter>
      </Card>
    );
  } else if (session === null || status === "awaiting_cli") {
    /* A session begun from another tab or machine supersedes this one;
     * the code this page holds then belongs to a closed session. */
    const staleBegin = begin !== null && session !== null && begin.session.id !== session.id;
    pane = (
      <InstallStep
        begin={staleBegin ? null : begin}
        error={
          staleBegin
            ? "A newer setup started in another tab or on another machine. Start again to get a new code."
            : beginError
        }
        beginning={beginning}
        onRetry={() => void start()}
        devBuild={config?.version === "dev" || config?.version === "single-binary"}
        origin={config?.public_origin ?? window.location.origin}
      />
    );
  } else if (status === "connected") {
    pane = (
      <WaitingCard
        title="Choose agents"
        description="The CLI is looking for agents."
        label={
          <>
            Detecting agents on <MachineBadge name={hostname} />
          </>
        }
      />
    );
  } else if (status === "detected" || (reviewingAgents && beforeProvisioning)) {
    pane = (
      <ChooseAgentsStep
        session={session}
        onSaved={(next) => {
          setSession(next);
          setReviewingAgents(false);
        }}
      />
    );
  } else if (terminal) {
    pane = (
      <DoneCard
        status={status}
        enrolled={session.enrollments.length > 0}
        restarting={beginning}
        onRestart={() => void start()}
        onQueue={() => void navigate({ to: "/queue" })}
        onAgents={() => void navigate({ to: "/agents" })}
      />
    );
  } else if (onChannels) {
    pane = (
      <OnboardingContactStep
        page={page}
        onChanged={() => router.invalidate()}
        onContinue={() => setSettingsStage("rules")}
      />
    );
  } else if (onRules) {
    pane = (
      <OnboardingRulesStep
        page={page}
        ladders={ladders}
        onChanged={() => router.invalidate()}
        error={channelsDone ? "" : advanceError}
        continuing={advancing}
        onContinue={
          channelsDone
            ? () => setSettingsStage(null)
            : () =>
                void advance(() => api.onboardingConfirmChannels(), "Could not save the step")
        }
      />
    );
  } else if (onTools) {
    pane = (
      <ApprovalRulesStep
        session={session}
        origin={config?.public_origin || window.location.origin}
        onSession={setSession}
        onBack={beforeProvisioning ? () => setReviewingAgents(true) : () => setSettingsStage("rules")}
        backLabel={beforeProvisioning ? "Back to agents" : "Back"}
        returning={reviewingRules && rulesDone}
        continuing={advancing}
        error={advanceError}
        /* Every Continue chooses the test calls again, since the rules
         * may have changed on the way back from the test. */
        onContinue={() => void advance(() => api.onboardingRecordRules(), "Could not continue")}
      />
    );
  } else {
    pane = (
      <TestStep
        session={session}
        origin={config?.public_origin || window.location.origin}
        page={page}
        activity={activity}
        finishing={closing === "finished"}
        onFinish={finish}
        onChannels={() => setSettingsStage("channels")}
      />
    );
  }

  return (
    <>
      <div className="flex flex-col items-center text-center">
        <h1 className="text-title-lg text-ink">Set up your first agent</h1>
        <p className="mt-1.5 max-w-[560px] text-body text-ink-muted">
          Connect your local agents to withHuman.
        </p>
      </div>
      <Stepper orientation="horizontal" steps={steps} />

      <UnsavedDialog
        open={leaving !== null}
        saving={ladders.saving}
        onKeepEditing={() => setLeaving(null)}
        onDiscard={() => {
          ladders.discard();
          setLeaving(null);
          leaving?.();
        }}
        onSave={() =>
          void saveLadders().then(
            () => {
              setLeaving(null);
              leaving?.();
            },
            () => setLeaving(null),
          )
        }
      />
      {onRules ? (
        <UnsavedGuard
          dirty={ladders.dirty.length > 0}
          saving={ladders.saving}
          onSave={saveLadders}
          onDiscard={ladders.discard}
        />
      ) : null}

      <ConfirmDialog
        open={confirmRestart}
        onOpenChange={setConfirmRestart}
        title="Start the setup over?"
        description="You'll get a new code to run on the machine again."
        confirmLabel="Start over"
        tone="approve"
        onConfirm={() => start()}
      />
      <div className="flex min-w-0 flex-col gap-4">
        {session?.last_error && !terminal ? (
          <Callout
            tone="warn"
            icon={<AlertIcon width={15} height={15} />}
            title={
              session.last_error.source === "cli"
                ? "The CLI reported a problem"
                : "Setup reported a problem"
            }
          >
            {session.last_error.message}
            {session.last_error.code ? (
              <span className="ml-1.5 font-mono text-[11px] text-ink-muted">
                {session.last_error.code}
              </span>
            ) : null}
          </Callout>
        ) : null}
        {pane}
      </div>
      {!terminal && !loading && !tested ? (
        <div className="flex justify-center">
          <Button
            variant="ghost"
            loading={closing === "skipped"}
            disabled={closing !== null}
            onClick={() => leaveRules(() => void complete("skipped"))}
          >
            Skip for now
          </Button>
        </div>
      ) : null}
    </>
  );
}

/* The spinner line. The label can carry Badges inline (the runtimes
 * being worked on, the machine), which wrap with the words. */
function Waiting({ label }: { label: ReactNode }) {
  return (
    <div className="flex items-center gap-2.5 text-body text-ink-muted">
      <Spinner size={16} className="shrink-0" />
      <span className="flex flex-wrap items-center gap-x-1.5 gap-y-1">{label}</span>
    </div>
  );
}

/* The machine the CLI runs on, as machine content. */
function MachineBadge({ name }: { name: string }) {
  return (
    <Badge
      tone="neutral"
      icon={<MonitorIcon width={14} height={14} />}
      className="align-middle font-mono"
    >
      {name}
    </Badge>
  );
}

/* A step card whose turn it is not: the CLI is working. */
function WaitingCard({
  title,
  description,
  label,
  children,
}: {
  title: string;
  description: string;
  label: ReactNode;
  children?: ReactNode;
}) {
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </div>
      </CardHeader>
      <CardBody className="flex flex-col gap-3">
        <Waiting label={label} />
        {children}
      </CardBody>
    </Card>
  );
}

function InstallStep({
  begin,
  error,
  beginning,
  onRetry,
  devBuild,
  origin,
}: {
  begin: OnboardingBegin | null;
  error: string;
  beginning: boolean;
  onRetry: () => void;
  devBuild: boolean;
  origin: string;
}) {
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>Connect your first agent</CardTitle>
          <CardDescription>
            Install the withHuman CLI on the machine your agent runs on. It sends each of the
            agent's tool calls to withHuman before the call runs.
          </CardDescription>
        </div>
      </CardHeader>
      <CardBody className="flex flex-col gap-4">
        {error ? (
          <Callout tone="warn" title="Could not start a setup session">
            <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <span>{error}</span>
              <Button size="sm" loading={beginning} onClick={onRetry}>
                Try again
              </Button>
            </span>
          </Callout>
        ) : begin === null ? (
          <Waiting label="Preparing your setup code" />
        ) : (
          <>
            <CodeBlock
              label="Run this on the machine your agent runs on"
              code={devBuild ? devSetupCommand(origin, begin.setup_code) : begin.setup_command}
            />
          </>
        )}
      </CardBody>
      <CardFooter className="justify-start">
        <Badge tone="neutral" icon={<Spinner size={14} />}>
          Waiting for the CLI
        </Badge>
      </CardFooter>
    </Card>
  );
}

function ChooseAgentsStep({
  session,
  onSaved,
}: {
  session: OnboardingSession;
  onSaved: (next: OnboardingSession) => void;
}) {
  const installed = session.detections.filter((detection) => detection.installed);
  /* The agents on the machine first, then the ones it lacks, each group
   * in the order the CLI reported them. */
  const ordered = [
    ...installed,
    ...session.detections.filter((detection) => !detection.installed),
  ];
  const [selected, setSelected] = useState<Set<string>>(
    () =>
      new Set(
        session.selected_runtimes.filter((runtime) =>
          installed.some((detection) => detection.runtime === runtime),
        ),
      ),
  );
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const hostname = session.machine.hostname || "this machine";

  function toggle(runtime: string, checked: boolean) {
    setSelected((current) => {
      const next = new Set(current);
      if (checked) next.add(runtime);
      else next.delete(runtime);
      return next;
    });
  }

  async function save() {
    setSaving(true);
    setError("");
    try {
      onSaved(await api.onboardingSelectAgents(Array.from(selected)));
    } catch (reason) {
      setError(errorMessage(reason, "Could not save the selection."));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>Choose agents</CardTitle>
          <CardDescription>
            {installed.length > 0 ? (
              <>
                We found {installed.length} {installed.length === 1 ? "agent" : "agents"} on{" "}
                <MachineBadge name={hostname} />. Choose the ones to connect. Their tool calls
                will go through your approval rules.
              </>
            ) : (
              <>
                We found no agents on <MachineBadge name={hostname} />.
              </>
            )}
          </CardDescription>
        </div>
      </CardHeader>
      <CardBody className="flex flex-col gap-2">
        {ordered.map((detection) => (
          <DetectionOption
            key={detection.runtime}
            detection={detection}
            checked={selected.has(detection.runtime)}
            onCheckedChange={(checked) => toggle(detection.runtime, checked)}
          />
        ))}
        {installed.length === 0 ? (
          <Callout tone="neutral" title="No agents to connect">
            Install one of the agents above, then run the setup command again.
          </Callout>
        ) : null}
        {error ? <Callout tone="warn">{error}</Callout> : null}
      </CardBody>
      <CardFooter>
        <Button
          variant="primary"
          loading={saving}
          disabled={selected.size === 0}
          onClick={() => void save()}
        >
          Continue
        </Button>
      </CardFooter>
    </Card>
  );
}

function DetectionOption({
  detection,
  checked,
  onCheckedChange,
}: {
  detection: OnboardingDetection;
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
}) {
  const servers = detection.mcp_servers?.length ?? 0;
  const where = [detection.version, detection.binary_path].filter(Boolean).join("  ");
  return (
    <label
      className={cn(
        /* A well inside the step card: canvas on surface, no shadow, the
         * platform's nesting rule (see escalation-tree's wellClass). */
        "flex items-start gap-3 rounded-lg border-hairline border-edge bg-canvas px-4 py-3.5",
        detection.installed
          ? "cursor-pointer has-data-checked:border-accent has-data-checked:ring-1 has-data-checked:ring-accent"
          : /* The platform's disabled look, as on a button or checkbox:
             * the whole row steps back, not only the title. */
            "pointer-events-none opacity-50",
      )}
      aria-disabled={!detection.installed || undefined}
    >
      <span className="flex h-5 shrink-0 items-center">
        <Checkbox
          checked={checked}
          disabled={!detection.installed}
          onCheckedChange={(next) => onCheckedChange(next === true)}
          aria-label={runtimeName(detection.runtime)}
        />
      </span>
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="flex items-center justify-between gap-3">
          <span
            className={cn(
              "text-body font-medium",
              detection.installed ? "text-ink" : "text-ink-muted",
            )}
          >
            {runtimeName(detection.runtime)}
          </span>
          {!detection.installed ? (
            <Badge tone="neutral" className="-mr-2">
              Not found
            </Badge>
          ) : detection.already_hooked ? (
            <Badge tone="accent" icon={<CheckIcon width={14} height={14} />} className="-mr-2">
              Already connected
            </Badge>
          ) : null}
        </span>
        {detection.installed && where ? (
          <span className="truncate font-mono text-[11px] text-ink-muted">{where}</span>
        ) : null}
        {detection.installed && servers > 0 ? (
          <span className="text-label font-normal text-ink-muted">
            {servers} MCP {servers === 1 ? "server" : "servers"} configured
          </span>
        ) : null}
      </span>
    </label>
  );
}

const NO_TOOLS: ReadonlySet<string> = new Set();

/* One badge per MCP server the runtime is configured with. A server
 * the CLI could not list is red with an alert, and every badge opens a
 * card on hover: where the server comes from, how many tools it gave,
 * and for a failure the CLI's message verbatim. */
function ServerBadges({
  servers,
  tools,
  errors,
}: {
  servers: OnboardingMCPServer[];
  tools: OnboardingTool[];
  errors: OnboardingDiscoveryError[];
}) {
  const failures = new Map(errors.map((failure) => [failure.server, failure.message]));
  /* Failures first, so what needs attention is where the eye lands;
   * within each group the CLI's order stands. */
  const ordered = [
    ...servers.filter((server) => failures.has(server.name)),
    ...servers.filter((server) => !failures.has(server.name)),
  ];
  return (
    <div className="flex flex-wrap items-center gap-1.5" aria-label="MCP servers">
      {ordered.map((server) => {
        const failure = failures.get(server.name);
        const listed = tools.filter((tool) => tool.server === server.name);
        const count = listed.length;

        return (
          <HoverCard
            key={server.name}
            content={
              <>
                <HoverCardTitle>
                  <span className="flex min-w-0 items-center gap-1.5">
                    <span className="min-w-0 truncate font-mono text-[12px]">{server.name}</span>
                    {server.transport ? (
                      <Badge fill icon={false} className="font-mono">
                        {server.transport}
                      </Badge>
                    ) : null}
                  </span>
                  {failure !== undefined ? (
                    <Badge tone="deny" icon={<AlertIcon width={14} height={14} />}>
                      Not listed
                    </Badge>
                  ) : (
                    <Badge tone="neutral">
                      {count} {count === 1 ? "tool" : "tools"}
                    </Badge>
                  )}
                </HoverCardTitle>
                {failure !== undefined ? (
                  <HoverCardBody>
                    <CodeBlock label="error" code={failure} wrap />
                  </HoverCardBody>
                ) : count > 0 ? (
                  <HoverCardBody>
                    <ToolNames server={server.name} tools={listed} />
                  </HoverCardBody>
                ) : null}
              </>
            }
          >
            <Badge
              tone={failure !== undefined ? "deny" : "approve"}
              icon={
                failure !== undefined ? (
                  <AlertIcon width={14} height={14} />
                ) : (
                  <CheckIcon width={14} height={14} />
                )
              }
              className="font-mono"
            >
              {server.name}
            </Badge>
          </HoverCard>
        );
      })}
    </div>
  );
}

/* Every tool a server gave, as a read-only scrolling list; the list
 * below the badges is where they are chosen. */
function ToolNames({ server, tools }: { server: string; tools: OnboardingTool[] }) {
  return (
    <div className="-mx-2 max-h-[240px] overflow-y-auto">
      <List
        items={tools}
        getId={(tool) => tool.name}
        selected={NO_TOOLS}
        onSelectedChange={() => undefined}
        selectable={false}
        openButton={false}
        aria-label={`${server} tools`}
      >
        {(tool) => (
          <ListRowContent
            title={<span className="font-mono text-[12px]">{tool.name}</span>}
            description={tool.description}
          />
        )}
      </List>
    </div>
  );
}


/* The statuses in which the runtimes' agents exist, so their rules can
 * be read and written. */
function agentsExist(status: OnboardingStatus): boolean {
  return (
    status === "provisioning" || status === "enrolled" || status === "installed" || status === "tested"
  );
}

/* The two ways to answer the step's one question. */
type Way = "agent" | "manual";

/* Installs the published withHuman skills into the person's coding
 * agent; withhuman-pipelines is the one the prompt names. */
const SKILLS_COMMAND = "npx skills add https://withhuman.ai";

/* Where a coding agent's MCP client reaches withHuman's own settings. */
const MCP_PATH = "/api/mcp/v1";

/* An agent the setup connects: its runtime, which keys its branch of
 * the entry pipeline, and the slug and name it was enrolled under. */
interface OnboardingAgent {
  runtime: string;
  slug: string;
  name: string;
}

function onboardingAgents(session: OnboardingSession): OnboardingAgent[] {
  return session.selected_runtimes.map((runtime) => {
    const enrollment = session.enrollments.find((entry) => entry.runtime === runtime);
    return { runtime, slug: enrollment?.agent_slug ?? runtime, name: enrollment?.agent_name ?? runtimeName(runtime) };
  });
}

/* What to paste into a coding agent so it writes the rules through
 * withHuman's MCP server, guided by the withhuman-pipelines skill:
 * connect, learn the grammar, read the agents' tools, propose what
 * should wait, confirm with the person, write, check. Each agent's rules
 * live in a branch of the entry pipeline keyed by its runtime; the
 * prompt names those branches and the setup's escalation path so it
 * touches nothing else. */
function agentPrompt(origin: string, agents: OnboardingAgent[], pathKey: string): string {
  const endpoint = `${origin}${MCP_PATH}`;
  const list = agents.map((agent) => `${agent.name} (agent slug ${agent.slug}, branch id "${agent.runtime}")`).join(", ");
  return [
    "Use the withhuman-pipelines skill to help me decide which of my AI agents' tool calls should wait for my approval in withHuman, then write those rules.",
    "",
    "1. Connect the withHuman MCP server if it is not connected yet, and sign in when asked:",
    `   Claude Code: claude mcp add --transport http withhuman ${endpoint}`,
    `   Codex: codex mcp add withhuman --url ${endpoint}`,
    "2. Call read_reference to learn how pipeline blocks and conditions are written.",
    `3. Read the current rules with get_pipeline for pipeline_key "entry". Each of these agents has its own branch block at the top level: ${list}.`,
    `4. List each agent's tools with list_tool_catalog (agent_slug ${agents.map((agent) => agent.slug).join(", ")}). Go through them and suggest which calls should wait for my approval because they are consequential: they delete or overwrite data, change things outside this machine (git pushes, deploys, cloud resources), send messages, or spend money. For tools that can do both safe and risky things, such as the shell, suggest rules on the arguments (for example git push or rm -rf) instead of gating the whole tool. Keep reading, searching and running tests without me.`,
    "5. Show me your suggestions with a short reason for each, and ask me to confirm or change them before you write anything.",
    `6. Write the rules with update_pipeline for pipeline_key "entry", changing only the blocks inside those branches and leaving every other block exactly as it is. Set escalation_path "${pathKey}" on every block that can send a call to me, and end each branch with an outcome block that approves the rest.`,
    "7. Try two or three sample calls with preview_pipeline, one that should wait and one that should not, and tell me what happened.",
    "8. For each agent, give me a prompt I can run that makes a call these rules send to me, so I can test the setup.",
  ].join("\n");
}

/* What to paste into a coding agent when no stock test call reaches a
 * person for these agents: it reads their branches of the entry
 * pipeline and writes a prompt that makes one call the rules send to
 * the person, harmless if approved, checked with a preview first. */
function testPrompt(origin: string, agents: OnboardingAgent[]): string {
  const list = joinNames(agents.map((agent) => `${agent.name} (agent slug ${agent.slug})`));
  const each = agents.length > 1 ? " for each agent" : "";
  return [
    `Use the withhuman-pipelines skill and the withHuman MCP server at ${origin}${MCP_PATH} to read my approval rules for ${list}.`,
    `Pick one call${each} that the rules send to me and that is harmless if I approve it. Check it with preview_pipeline.`,
    `Then give me a short prompt${each} that makes that call.`,
  ].join(" ");
}

/* Names joined the way a sentence wants them: "Codex and Pi". */
function joinNames(names: string[]): string {
  if (names.length <= 1) return names[0] ?? "";
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

/*
 * The approval rules step: one question and two ways to answer it, as
 * tiles that each open their panel below: the withHuman skills and a
 * prompt for the person's own coding agent, or a table per agent to
 * tick tools by hand, which is only fit for a first try. Nothing is
 * chosen at first and the starting rules, which approve every call,
 * stand until something is written, so the coding agent writing them is
 * never held. The agents are connected behind the question
 * (provisioning, enrolment, the hooks) and Continue opens once the hooks
 * are installed.
 */
function ApprovalRulesStep({
  session,
  origin,
  onSession,
  onBack,
  backLabel,
  returning,
  continuing,
  error,
  onContinue,
}: {
  session: OnboardingSession;
  origin: string;
  onSession: (next: OnboardingSession) => void;
  onBack: () => void;
  backLabel: string;
  /* Back from the test: Continue returns there without a write. */
  returning: boolean;
  continuing: boolean;
  error: string;
  onContinue: () => void;
}) {
  const runtimes = session.selected_runtimes;
  const status = session.status;
  const hostname = session.machine.hostname || "this machine";
  const names = joinNames(runtimes.map(runtimeName));

  /* Arriving here with the channels set and the tools discovered is
   * what provisions the agents: the choice of agents was made two steps
   * ago and every way of answering needs them to exist. Once per
   * session; a failure shows with a retry. */
  const [provisionError, setProvisionError] = useState("");
  const [provisioning, setProvisioning] = useState(false);
  const provisionedFor = useRef<string | null>(null);
  const provision = useCallback(async () => {
    setProvisioning(true);
    setProvisionError("");
    try {
      onSession(await api.onboardingProvision());
    } catch (reason) {
      setProvisionError(errorMessage(reason, "The agents could not be set up."));
      provisionedFor.current = null;
    } finally {
      setProvisioning(false);
    }
  }, [onSession]);
  useEffect(() => {
    if (status !== "tools_discovered" || !session.channels_set_at) return;
    if (provisionedFor.current === session.id) return;
    provisionedFor.current = session.id;
    void provision();
  }, [status, session.channels_set_at, session.id, provision]);

  /* The rules as they stand, read from the entry pipeline's active
   * revision (each agent's rules are a branch of it) and re-read
   * whenever the session moves, which every write does. undefined until
   * the first read answers. */
  const exist = agentsExist(status);
  const [pipelineTick, setPipelineTick] = useState(0);
  const [entry, setEntry] = useState<RequestApprovalPipelineRevision | null | undefined>(undefined);
  useEffect(() => {
    if (!exist) return;
    let cancelled = false;
    void api
      .activeRequestApprovalPipeline(ENTRY_PIPELINE_KEY)
      .then((revision) => revision, () => null)
      .then((revision) => {
        if (!cancelled) setEntry(revision);
      });
    return () => {
      cancelled = true;
    };
    /* version and the coding agent's poll are the triggers. */
  }, [exist, session.version, pipelineTick]);

  const installed = status === "installed" || status === "tested";
  const attention = session.install_results.filter((result) => !result.ok || result.manual);
  /* Nothing is chosen until the person picks a way. */
  const [way, setWay] = useState<Way | null>(null);

  /* A coding agent writes through the MCP server, which the session
   * never hears about, so the rules are re-read while that way is open. */
  useEffect(() => {
    if (way !== "agent" || !exist) return;
    const timer = window.setInterval(() => setPipelineTick((tick) => tick + 1), 4000);
    return () => window.clearInterval(timer);
  }, [way, exist]);

  const agents = onboardingAgents(session);

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-col gap-2">
          <CardTitle>Set up your approval rules</CardTitle>
          <CardDescription>
            Approval rules let you choose which tool calls wait for your approval. All other calls
            run right away. Your rules are saved in your approval pipeline.
          </CardDescription>
          <ConnectionStatus
            session={session}
            names={names}
            hostname={hostname}
            provisioning={provisioning}
            error={provisionError}
            onRetry={() => void provision()}
          />
        </div>
      </CardHeader>
      <CardBody className="flex flex-col gap-5">
        {attention.map((result) => (
          <InstallCallout key={result.runtime} result={result} />
        ))}
        <RadioGroup
          aria-label="How to set up your rules"
          value={way}
          onValueChange={(next) => setWay(next as Way)}
          className="grid grid-cols-1 gap-3 sm:grid-cols-2"
        >
          <RadioCard
            value="agent"
            icon={<AgentIcon width={16} height={16} />}
            label={
              <span className="flex flex-wrap items-center gap-1.5">
                Use your coding agent
                <Badge fill icon={false}>
                  Recommended
                </Badge>
              </span>
            }
            description="Add the withHuman skills to your coding agent and write the rules together."
          />
          <RadioCard
            value="manual"
            icon={<ToolIcon width={16} height={16} />}
            label="Pick tools manually"
            description="Tick the tools that should wait for your approval."
          />
        </RadioGroup>
        {way === "agent" ? (
          <AgentWay
            prompt={agentPrompt(origin, agents, session.generated.escalation_path_key)}
            agents={agents}
            ready={exist}
            entry={entry ?? null}
          />
        ) : way === "manual" ? (
          <WayPanel title="Choose the tools that wait for you">
            <Callout tone="warn" icon={<AlertIcon width={15} height={15} />}>
              Every call to a tool you tick will wait for you, even the harmless ones. For real use,
              set up your rules with your coding agent.
            </Callout>
            <div className="flex flex-col gap-6">
              {runtimes.map((runtime) => (
                <GatedToolsTable
                  key={runtime}
                  runtime={runtime}
                  servers={
                    session.detections.find((detection) => detection.runtime === runtime)
                      ?.mcp_servers ?? []
                  }
                  tools={session.tools[runtime] ?? []}
                  errors={session.discovery_errors[runtime] ?? []}
                  gated={exist && entry !== undefined ? gatedToolsOf(entry, runtime) : null}
                  ready={exist && entry !== undefined}
                  onSession={onSession}
                />
              ))}
            </div>
          </WayPanel>
        ) : null}
        {error ? (
          <Callout tone="warn" title="Could not continue">
            {error}
          </Callout>
        ) : null}
      </CardBody>
      <CardFooter className="justify-between gap-4">
        <Button variant="ghost" onClick={onBack} disabled={continuing}>
          {backLabel}
        </Button>
        <div className="flex items-center gap-3">
          <span className="text-label font-normal text-ink-muted">You can change this later.</span>
          <Button variant="primary" loading={continuing} disabled={!installed} onClick={onContinue}>
            {returning ? "Back to the test" : "Continue"}
          </Button>
        </div>
      </CardFooter>
    </Card>
  );
}

/* The panel a chosen way opens under the tiles: a recessed well with
 * a heading, so the tiles read as the question and this as the answer. */
function WayPanel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-4 rounded-xl border-hairline border-edge bg-canvas p-5">
      <h3 className="text-body font-medium text-ink">{title}</h3>
      {children}
    </section>
  );
}

/* The coding-agent way: the prompt to paste, and a line per agent that
 * turns into a tick once its rules change, read by polling because the
 * agent writes through the MCP server, not this session. */
function AgentWay({
  prompt,
  agents,
  ready,
  entry,
}: {
  prompt: string;
  agents: OnboardingAgent[];
  ready: boolean;
  entry: RequestApprovalPipelineRevision | null;
}) {
  const { copy, copied } = useCopyToClipboard(prompt);
  /* Each agent's branch as it stood when this way opened; a different
   * one is the coding agent's work. The branch itself is compared, not
   * the entry revision, which bumps on anyone's edit. */
  const baseline = useRef<Record<string, string>>({});
  const current: Record<string, string | undefined> = {};
  for (const agent of agents) {
    const branch = runtimeBranchOf(entry, agent.runtime);
    const shape = branch ? JSON.stringify(definitionsFromStored([branch])) : undefined;
    current[agent.runtime] = shape;
    if (shape !== undefined && !(agent.runtime in baseline.current)) {
      baseline.current[agent.runtime] = shape;
    }
  }
  return (
    <WayPanel title="Write the rules with your coding agent">
      <div className="flex flex-col gap-2">
        <StepLabel>1. Add the withHuman skills to your coding agent</StepLabel>
        <CodeBlock code={SKILLS_COMMAND} className="bg-surface" />
      </div>
      <StepLabel>2. Paste this prompt into your coding agent</StepLabel>
      <p className="max-h-[400px] overflow-y-auto rounded-lg border-hairline border-edge bg-surface p-4 text-body leading-[21px] whitespace-pre-wrap text-ink-secondary [overflow-wrap:anywhere]">
        {prompt}
      </p>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <ul className="flex flex-col gap-1.5">
          {agents.map((agent) => {
            const now = current[agent.runtime];
            const before = baseline.current[agent.runtime];
            const changed = now !== undefined && before !== undefined && now !== before;
            return (
              <li key={agent.runtime} className="flex items-center gap-2 text-label font-normal text-ink-muted">
                {changed ? (
                  <CheckIcon width={14} height={14} className="shrink-0 text-approve" aria-hidden />
                ) : (
                  <LoadingDots aria-label="Waiting" />
                )}
                <span>
                  {changed ? (
                    <>
                      <span className="text-ink">{agent.name}</span>'s rules were updated
                    </>
                  ) : ready ? (
                    `Waiting for new rules for ${agent.name}`
                  ) : (
                    `Connecting ${agent.name} first`
                  )}
                </span>
              </li>
            );
          })}
        </ul>
        <Button variant="primary" size="sm" onClick={() => void copy()}>
          {copied ? (
            <CheckIcon width={14} height={14} aria-hidden />
          ) : (
            <CopyIcon width={14} height={14} aria-hidden />
          )}
          {copied ? "Copied" : "Copy prompt"}
        </Button>
      </div>
    </WayPanel>
  );
}

/* A numbered step's quiet label inside a way's panel. */
function StepLabel({ children }: { children: ReactNode }) {
  return (
    <div className="flex h-[26px] items-center gap-2 text-label text-ink-muted select-none">
      <span>{children}</span>
      <span className="flex-1 border-t-hairline border-edge" />
    </div>
  );
}

/* The one line under the question while the agents are still being
 * connected, or why connecting failed; nothing once they are. */
function ConnectionStatus({
  session,
  names,
  hostname,
  provisioning,
  error,
  onRetry,
}: {
  session: OnboardingSession;
  names: string;
  hostname: string;
  provisioning: boolean;
  error: string;
  onRetry: () => void;
}) {
  if (error) {
    return (
      <span className="flex flex-wrap items-center gap-x-3 gap-y-1 text-body text-deny">
        <span>{error}</span>
        <Button size="sm" loading={provisioning} onClick={onRetry}>
          Try again
        </Button>
      </span>
    );
  }
  let text: string;
  switch (session.status) {
    case "agents_selected":
      text = `Finding the tools ${names} can call.`;
      break;
    case "tools_discovered":
    case "provisioning":
      text = `Connecting ${names} on ${hostname}.`;
      break;
    case "enrolled":
      text = `Installing hooks for ${names} on ${hostname}.`;
      break;
    default:
      /* Connected: nothing to say, the question stands alone. */
      return null;
  }
  return (
    <CardDescription>
      <span className="flex items-center gap-2">
        <Spinner size={14} className="shrink-0" />
        <span>{text}</span>
      </span>
    </CardDescription>
  );
}

/* One runtime's rules: the top-level branch of the entry pipeline whose
 * block id is the runtime, as the guided setup writes it. */
function runtimeBranchOf(entry: RequestApprovalPipelineRevision | null, runtime: string): StoredBranchBlock | undefined {
  const block = entry?.blocks.find((candidate) => candidate.id === runtime);
  return block?.type === "branch" ? block : undefined;
}

/* The tools a runtime's branch gates, when its first block is the
 * starting rule's shape: a branch on the tool name being one of a list
 * that holds one Outcome block asking a human. Read by shape, not by
 * block key, so the same rule written in the editor reads the same. An
 * empty set otherwise, because rules a coding agent wrote do not reduce
 * to a list, and a missing branch gates nothing yet. */
function gatedToolsOf(entry: RequestApprovalPipelineRevision | null, runtime: string): Set<string> {
  const first = runtimeBranchOf(entry, runtime)?.config.blocks[0];
  if (!first || first.type !== "branch") return new Set();
  const [only, ...rest] = first.config.blocks;
  if (!only || rest.length > 0 || only.type !== "outcome" || only.config.outcome !== "human") return new Set();
  const when = toolListCondition(first.config.when);
  return when ? new Set(when.map(String)) : new Set();
}

/* The list in "tool name is one of": a bare leaf, as the setup writes
 * it, or the one leaf of a group, as the editor saves it. */
function toolListCondition(when: Record<string, unknown>): unknown[] | undefined {
  const node = when as { field?: string; operator?: string; value?: unknown; all?: unknown[]; any?: unknown[] };
  const group = node.all ?? node.any;
  if (group) {
    return group.length === 1 && group[0] && typeof group[0] === "object"
      ? toolListCondition(group[0] as Record<string, unknown>)
      : undefined;
  }
  if (node.field !== "/request/tool" || node.operator !== "in" || !Array.isArray(node.value)) return undefined;
  return node.value;
}

/* Every tool one runtime can call, as a table with a checkbox per row:
 * the by-hand way to say what waits. The selection starts from what the
 * agent's rules gate now and is written when applied. */
function GatedToolsTable({
  runtime,
  servers,
  tools,
  errors,
  gated,
  ready,
  onSession,
}: {
  runtime: string;
  servers: OnboardingMCPServer[];
  tools: OnboardingTool[];
  errors: OnboardingDiscoveryError[];
  /* What the active rules gate, or null while unknown. */
  gated: Set<string> | null;
  ready: boolean;
  onSession: (next: OnboardingSession) => void;
}) {
  const name = runtimeName(runtime);
  const toast = useToast();
  /* One row per tool name: the pipeline gates by name, so two servers'
   * identically named tools are one choice. */
  const rows = tools.filter((tool, index) => tools.findIndex((other) => other.name === tool.name) === index);
  const gatedKey = gated ? Array.from(gated).sort().join("\n") : null;
  const [selected, setSelected] = useState<Set<string>>(() => new Set(gated ?? []));
  const [query, setQuery] = useState("");
  const [applying, setApplying] = useState(false);
  useEffect(() => {
    setSelected(new Set(gatedKey === null ? [] : gatedKey.split("\n").filter(Boolean)));
  }, [gatedKey]);
  const changed = gatedKey !== null && Array.from(selected).sort().join("\n") !== gatedKey;
  const needle = query.trim().toLowerCase();
  const shown = needle
    ? rows.filter((tool) =>
        [tool.name, tool.description ?? "", tool.server ?? ""].some((text) => text.toLowerCase().includes(needle)),
      )
    : rows;

  async function apply() {
    setApplying(true);
    try {
      onSession(await api.onboardingSetGatedTools(runtime, Array.from(selected)));
    } catch (reason) {
      toast.add({ type: "deny", title: `Could not change ${name}'s rules`, description: errorMessage(reason, "") });
    } finally {
      setApplying(false);
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <span className="text-body font-medium text-ink">{name}</span>
          <span className="text-label font-normal text-ink-muted">
            {selected.size} of {rows.length} wait
            {errors.length > 0
              ? `, ${errors.length} of ${servers.length} MCP ${servers.length === 1 ? "server" : "servers"} could not be listed`
              : null}
          </span>
        </div>
        <div className="flex items-center gap-2">
          <TextField
            aria-label={`Search ${name} tools`}
            placeholder="Search tools"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            className="w-[200px]"
          />
          <Button size="sm" variant="primary" loading={applying} disabled={!ready || !changed} onClick={() => void apply()}>
            Apply
          </Button>
        </div>
      </div>
      {servers.length > 0 ? <ServerBadges servers={servers} tools={tools} errors={errors} /> : null}
      {rows.length === 0 ? (
        <p className="text-label font-normal text-ink-muted">The CLI found no tools.</p>
      ) : (
        <div className="max-h-[320px] overflow-y-auto rounded-lg border-hairline border-edge bg-canvas">
          <Table<OnboardingTool>
            aria-label={`${name} tools`}
            items={shown}
            getId={(tool) => tool.name}
            selected={new Set(shown.filter((tool) => selected.has(tool.name)).map((tool) => tool.name))}
            onSelectedChange={(ids) => {
              const next = new Set(selected);
              for (const tool of shown) {
                if (ids.has(tool.name)) next.add(tool.name);
                else next.delete(tool.name);
              }
              setSelected(next);
            }}
            columns={[
              {
                id: "tool",
                label: "Tool",
                width: 260,
                cell: (tool) => (
                  <span className="flex min-w-0 items-center gap-2">
                    <span className="truncate font-mono text-[12px] text-ink">{tool.name}</span>
                    <ToolSourceBadge tool={tool} />
                  </span>
                ),
              },
              {
                id: "description",
                label: "What it does",
                grow: true,
                cell: (tool) => (
                  <span className="block truncate text-label font-normal text-ink-muted" title={tool.description}>
                    {tool.description || (tool.suggested ? "Suggested for review" : "")}
                  </span>
                ),
              },
            ]}
          />
        </div>
      )}
    </div>
  );
}

/* Where a tool comes from: the MCP server's name in mono so two servers'
 * identically named tools tell apart, or "Built-in" for the runtime's own. */
function ToolSourceBadge({ tool }: { tool: OnboardingTool }) {
  return tool.server ? (
    <Badge fill icon={false} className="max-w-[140px] font-mono">
      <span className="truncate">{tool.server}</span>
    </Badge>
  ) : (
    <Badge fill icon={false}>
      Built-in
    </Badge>
  );
}

/* A runtime whose hooks need a hand: why, and the snippet to add. */
function InstallCallout({ result }: { result: OnboardingInstallResult }) {
  const name = runtimeName(result.runtime);
  return (
    <Callout
      tone="warn"
      icon={<AlertIcon width={15} height={15} />}
      title={result.manual ? `${name} needs a manual step` : `Hooks for ${name} were not installed`}
    >
      <div className="flex flex-col gap-2">
        {result.message ? <span>{result.message}</span> : null}
        {result.snippet ? <CodeBlock code={result.snippet} className="bg-surface" /> : null}
      </div>
    </Callout>
  );
}

/* The commands that make each enrolled runtime call a gated tool. The
 * runtimes without one have rules that let every simple test call run,
 * so the person asks their coding agent for a prompt that makes a call
 * the rules send to them, and runs it in those runtimes. */
function TestCommands({ session, origin }: { session: OnboardingSession; origin: string }) {
  const commanded = session.selected_runtimes.filter((runtime) => session.test_commands[runtime]);
  const uncommanded = onboardingAgents(session).filter((agent) => !session.test_commands[agent.runtime]);
  return (
    <>
      {commanded.map((runtime) => {
        const test = session.test_commands[runtime];
        return (
          <div key={runtime} className="flex flex-col gap-1.5">
            <CodeBlock label={runtimeName(runtime)} code={test.command} />
            {test.note ? <p className="text-label font-normal text-ink-muted">{test.note}</p> : null}
          </div>
        );
      })}
      {uncommanded.length > 0 ? <TestPrompt prompt={testPrompt(origin, uncommanded)} agents={uncommanded} /> : null}
    </>
  );
}

/* The prompt that asks the person's coding agent for a test prompt,
 * and where to run what it gives back. */
function TestPrompt({ prompt, agents }: { prompt: string; agents: OnboardingAgent[] }) {
  const { copy, copied } = useCopyToClipboard(prompt);
  return (
    <div className="flex flex-col gap-2">
      <StepLabel>Ask your coding agent for a test prompt</StepLabel>
      <p className="max-h-[400px] overflow-y-auto rounded-lg border-hairline border-edge bg-canvas p-4 text-body leading-[21px] whitespace-pre-wrap text-ink-secondary [overflow-wrap:anywhere]">
        {prompt}
      </p>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="text-label font-normal text-ink-muted">
          Then run the prompt it gives you in {joinNames(agents.map((agent) => agent.name))}.
        </span>
        <Button variant="primary" size="sm" onClick={() => void copy()}>
          {copied ? (
            <CheckIcon width={14} height={14} aria-hidden />
          ) : (
            <CopyIcon width={14} height={14} aria-hidden />
          )}
          {copied ? "Copied" : "Copy prompt"}
        </Button>
      </div>
    </div>
  );
}

/* The last step: a gated call, decided here, whose prompt also went
 * out through the channels and rules set two steps ago. The card says
 * where the prompt goes, and the way out asks whether it arrived:
 * Finish if it did, back to the channels if it did not. Only the
 * arrival is verified; delivery is the person's word. */
function TestStep({
  session,
  origin,
  page,
  activity,
  finishing,
  onFinish,
  onChannels,
}: {
  session: OnboardingSession;
  origin: string;
  page: ChannelsPage;
  activity: TestActivity;
  finishing: boolean;
  /* Records the decided request and finishes; offered once a loop has
   * closed, since the step is deciding it here, not watching it arrive. */
  onFinish: (requestId: string) => void;
  onChannels: () => void;
}) {
  const hostname = session.machine.hostname || "this machine";
  const attention = session.install_results.filter((result) => !result.ok || result.manual);
  const lines = page.rules ? ladderLines(page.rules.standard, page.methods) : [];
  const decided = activity.closed[0] ?? null;
  /* Whether any agent has a stock test command; without one the person
   * runs a prompt their coding agent gave them. */
  const commanded = session.selected_runtimes.some((runtime) => session.test_commands[runtime]);
  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader>
          <div>
            <CardTitle>Try it</CardTitle>
            <CardDescription>
              {commanded ? (
                <>
                  Run this on <MachineBadge name={hostname} />. It makes a tool call your rules send
                  to you. Approve or deny it below.
                </>
              ) : (
                <>
                  On <MachineBadge name={hostname} />, run a prompt in your agent that needs your
                  approval. Approve or deny the request below.
                </>
              )}
            </CardDescription>
          </div>
        </CardHeader>
        <CardBody className="flex flex-col gap-4">
          {attention.map((result) => (
            <InstallCallout key={result.runtime} result={result} />
          ))}
          <TestCommands session={session} origin={origin} />
          <div className="flex flex-col gap-2">
            <div className="flex h-[26px] items-center gap-2 text-label text-ink-muted select-none">
              <span>Where the notifications go</span>
              <span className="flex-1 border-t-hairline border-edge" />
            </div>
            {lines.length > 0 ? (
              <ul className="flex flex-col gap-1 rounded-lg border-hairline border-edge bg-canvas px-4 py-3">
                {lines.map((line) => (
                  <li key={line} className="text-body text-ink">
                    {line}
                  </li>
                ))}
              </ul>
            ) : (
              <Callout tone="neutral">
                Your notification settings could not be loaded. The prompt goes to your account
                email.
              </Callout>
            )}
          </div>
        </CardBody>
      </Card>

      <OnboardingApprovals
        activity={activity}
        waitingDescription={
          commanded ? "Run the command above. The request will appear here." : "The request will appear here."
        }
        next={
          decided ? (
            /* One row: the question and its "no" on the left, Finish on the right. */
            <div className="flex flex-1 flex-wrap items-center justify-between gap-2.5">
              <div className="flex flex-wrap items-center gap-2.5">
                <span className="text-label font-normal text-ink-muted">
                  Did the prompt reach you?
                </span>
                <Button variant="ghost" onClick={onChannels}>
                  No, check my channels
                </Button>
              </div>
              <Button variant="primary" loading={finishing} onClick={() => onFinish(decided.id)}>
                Yes, finish
              </Button>
            </div>
          ) : null
        }
      />
    </div>
  );
}

function DoneCard({
  status,
  enrolled,
  restarting,
  onRestart,
  onQueue,
  onAgents,
}: {
  status: OnboardingStatus | null;
  /* Agents were connected before the flow ended, so a skip left only
   * the notification steps undone. */
  enrolled: boolean;
  restarting: boolean;
  onRestart: () => void;
  onQueue: () => void;
  onAgents: () => void;
}) {
  const skipped = status === "skipped";
  return (
    <Card>
      <EmptyState
        icon={<CheckIcon width={18} height={18} />}
        title={skipped ? "Setup skipped" : "Setup complete"}
        description={
          !skipped
            ? "Calls that need your approval will appear in the review queue."
            : enrolled
              ? "Your agents are connected. Choose how you get notified under Settings, Notifications."
              : "You can run the guided setup any time from the command palette."
        }
        action={
          <div className="flex flex-wrap items-center justify-center gap-2.5">
            <Button variant="primary" onClick={onQueue}>
              Open the review queue
            </Button>
            <Button onClick={onAgents}>See your agents</Button>
            <Button variant="ghost" loading={restarting} onClick={onRestart}>
              Set up another machine
            </Button>
          </div>
        }
      />
    </Card>
  );
}
