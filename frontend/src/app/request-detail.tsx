import {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { Avatar } from "@/ds/components/avatar";
import { HoverCard } from "@/ds/components/hover-card";
import { Referenced } from "@/ds/components/reference";
import { TargetBadge } from "@/ds/components/reviewer-target";
import { Badge, type Tone } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { CodeBlock } from "@/ds/components/code-block";
import { ConfirmDialog } from "@/ds/components/confirm-dialog";
import { CopyButton } from "@/ds/components/copy-button";
import {
  DescriptionItem,
  DescriptionList,
} from "@/ds/components/description-list";
import { Disclosure } from "@/ds/components/disclosure";
import { TextArea } from "@/ds/components/text-area";
import { Timeline, TimelineItem } from "@/ds/components/timeline";
import { useToast } from "@/ds/components/toast";
import { ToggleGroup, ToggleGroupItem } from "@/ds/components/toggle-group";
import { InfoCard } from "@/ds/components/info-card";
import { Markdown } from "@/ds/components/markdown";
import { Tooltip } from "@/ds/components/tooltip";
import { Page } from "@/ds/components/page";
import { PageHeader, PageBackButton } from "@/ds/components/page-header";
import {
  AgentIcon,
  AlertIcon,
  CheckIcon,
  ClockIcon,
  DashIcon,
  PersonIcon,
  PlusIcon,
  ShieldIcon,
  XIcon,
} from "@/ds/icons";
import { cn } from "@/ds/lib/cn";
import { api } from "../api";
import { AddReviewerMenu } from "./add-reviewer-menu";
import { ArgumentView } from "./argument-view";
import { planArguments, renderTitle, titleText, type TitleSegment } from "./tool-presentation";
import { useAccess } from "./authorization";
import { auditLinkProps, extensions, useAuditLinks } from "./extensions";
import { preloadedReview } from "./queue-store";
import { usePrincipal } from "./principal";
import { DefaultRouteCard } from "./reference-cards";
import { ReferenceChip } from "./reference-chip";
import { nextEscalation, type EscalationCountdown } from "./escalation-countdown";
import type {
  AddedReviewer,
  ApprovalRequest,
  ApprovalReview,
  ApprovalRouting,
  ApprovalRoutingTarget,
  Decision,
  ReviewViewer,
} from "../types";
import {
  formatAbsolute,
  formatBetween,
  formatElapsed,
  formatRemaining,
  humanizeKey,
  humanizeTool,
} from "./format";

/*
 * One request, read top to bottom the way a reviewer decides: what is
 * this and exactly what will happen, why the agent wants it and what the
 * automated checks thought, the decision, then when and where it went,
 * with the full history folded away underneath. Times read as distance
 * from now; the exact moment is one hover away. What the automated
 * checks thought and the request's history beyond its routing come from
 * an edition's audit log, through the request detail slots in
 * extensions.tsx; the open edition shows the routing, the reviewers,
 * and the decision.
 */

/* Under this share of the deadline budget the time left turns warn. */
const URGENT_BUDGET_SHARE = 0.25;

type ArgumentsView = "summary" | "json";
const ARGUMENTS_VIEW_KEY = "withhuman.queue.arguments_view";

/* The reviewer's last choice between the summary and the raw JSON,
 * remembered per browser so a developer lands on JSON every time. */
function readArgumentsView(): ArgumentsView {
  try {
    return localStorage.getItem(ARGUMENTS_VIEW_KEY) === "json" ? "json" : "summary";
  } catch {
    return "summary";
  }
}

function writeArgumentsView(view: ArgumentsView) {
  try {
    localStorage.setItem(ARGUMENTS_VIEW_KEY, view);
  } catch {
    /* Private mode or blocked storage: the choice lasts for this view. */
  }
}

/* A clock that ticks once a minute so elapsed and remaining spans stay
 * honest while the request sits open. */
function useNow(intervalMs = 60_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), intervalMs);
    return () => window.clearInterval(id);
  }, [intervalMs]);
  return now;
}

export function RequestDetail({
  request,
  onBack,
  onDecided,
  afterDecision,
  nextPending,
  onOpenNext,
}: {
  request: ApprovalRequest;
  /* In split view the list stays visible, so there is no back. */
  onBack?: () => void;
  onDecided: () => Promise<unknown> | void;
  /* Runs once this person's decision is saved and the queue refreshed. */
  afterDecision?: () => void;
  /* How many other pending requests wait, and how to open the next one;
   * absent when this request was opened outside the queue. */
  nextPending?: number;
  onOpenNext?: () => void;
}) {
  const [note, setNote] = useState("");
  const [noteError, setNoteError] = useState("");
  const [saving, setSaving] = useState<"approved" | "denied" | null>(null);
  const [claiming, setClaiming] = useState<"claim" | "release" | null>(null);
  const [review, setReview] = useState<ApprovalReview | null>(() =>
    preloadedReview(request.id),
  );
  /* The break-glass ceremony: which decision is waiting on the confirm,
   * and whether the dialog is up. The status outlives the open flag so
   * the dialog keeps its words while it fades out. */
  const [confirmStatus, setConfirmStatus] = useState<"approved" | "denied">("approved");
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [addingReviewer, setAddingReviewer] = useState(false);
  const [argumentsView, setArgumentsView] = useState<ArgumentsView>(readArgumentsView);
  const noteRef = useRef<HTMLTextAreaElement>(null);
  const principal = usePrincipal();
  const navigate = useNavigate();
  /* request.decide binds at organization scope only, so the flat check
   * is the whole answer; routing narrows who is asked, never who may. */
  const { holdsAnywhere } = useAccess();
  const auditLinkFor = useAuditLinks();
  const canDecide = holdsAnywhere("request.decide");
  const toast = useToast();
  const now = useNow();

  const decision = request.decision;
  const claim = request.claim;
  const claimIsMine = claim?.membership_id === principal.membership_id;
  const pending = request.status === "pending";

  /* Until the review answers (or if it cannot), the request alone
   * decides what the panel offers, as before; the server still refuses
   * a decision the routing does not allow. */
  const viewer: ReviewViewer | undefined = review?.viewer;
  const decidable = viewer?.decidable ?? "yes";
  const addedReviewers: AddedReviewer[] = review?.added_reviewers ?? [];

  /* The review context follows the request's status, claim, and newest
   * change, which are what move it after creation; while it waits, the
   * escalation executor moves it on its own timers, so the minute tick
   * refetches too. A failed fetch leaves the view without it; the
   * request alone is enough to decide. */
  const reviewTick = pending ? now : 0;
  useEffect(() => {
    let stale = false;
    api
      .approvalReview(request.id)
      .then((next) => {
        if (!stale) setReview(next);
      })
      .catch(() => {
        if (!stale) setReview(null);
      });
    return () => {
      stale = true;
    };
  }, [request.id, request.status, request.updated_at, claim?.id, reviewTick]);

  useEffect(() => {
    setNote("");
    setNoteError("");
  }, [request.id]);

  const submit = useCallback(
    async (status: "approved" | "denied", outsideRouting: boolean) => {
      setSaving(status);
      try {
        await api.decide(request.id, status, note, { outsideRouting });
        await onDecided();
        toast.add({
          type: status === "approved" ? "approve" : "deny",
          title: `Request ${status}`,
          description: requestTitle(request),
        });
        afterDecision?.();
      } catch (reason) {
        toast.add({
          type: "deny",
          title: "Decision failed",
          description: reason instanceof Error ? reason.message : undefined,
        });
      } finally {
        setSaving(null);
      }
    },
    [note, request, onDecided, afterDecision, toast],
  );

  /* The buttons and the shortcuts both land here. A denial needs its
   * note first; a decision outside routing needs the person to say so
   * before anything is sent. */
  const decide = useCallback(
    async (status: "approved" | "denied") => {
      if (saving || confirmOpen) return;
      if (status === "denied" && note.trim() === "") {
        setNoteError("Tell the agent why.");
        noteRef.current?.focus();
        return;
      }
      if (decidable === "break_glass") {
        setConfirmStatus(status);
        setConfirmOpen(true);
        return;
      }
      await submit(status, false);
    },
    [saving, confirmOpen, note, decidable, submit],
  );

  /* A approves and D denies, as long as nothing editable has focus, no
   * modifier is held, and no dialog is up to swallow the key. */
  const showPanel =
    pending && canDecide && !(decidable === "no" && viewer?.reason === "no_permission");
  const canAct = showPanel && decidable !== "no" && !confirmOpen && !addingReviewer;
  useEffect(() => {
    if (!canAct) return;
    function onKeyDown(event: KeyboardEvent) {
      if (event.metaKey || event.ctrlKey || event.altKey || event.repeat) return;
      const target = event.target as HTMLElement | null;
      if (
        target &&
        (target.isContentEditable ||
          ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName))
      ) {
        return;
      }
      const key = event.key.toLowerCase();
      if (key !== "a" && key !== "d") return;
      event.preventDefault();
      void decide(key === "a" ? "approved" : "denied");
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [canAct, decide]);

  /* The claim rendered is server truth from the queue stream; the next
   * SSE snapshot confirms or clears whatever a mutation changed. */
  async function toggleClaim(action: "claim" | "release") {
    setClaiming(action);
    try {
      if (action === "claim") {
        await api.claim(request.id);
      } else {
        await api.releaseClaim(request.id);
      }
      await onDecided();
    } catch (reason) {
      toast.add({
        type: "deny",
        title: action === "claim" ? "Claim failed" : "Release failed",
        description: reason instanceof Error ? reason.message : undefined,
      });
    } finally {
      setClaiming(null);
    }
  }

  const routing = review?.routing;
  const { assessments: Assessments, cause: Cause, history: EditionHistory } = extensions.requestDetail;
  const budget = new Date(request.deadline_at).getTime() - new Date(request.created_at).getTime();
  const left = new Date(request.deadline_at).getTime() - now;
  const urgent = pending && budget > 0 && left / budget < URGENT_BUDGET_SHARE;
  const countdown = pending ? nextEscalation(routing) : null;
  const plan = useMemo(
    () => planArguments(request.arguments, request.presentation),
    [request.arguments, request.presentation],
  );
  const title = useMemo(
    () => renderTitle(request.presentation?.title, request.arguments),
    [request.presentation, request.arguments],
  );
  const summary = request.summary?.text;
  const showSummary = argumentsView === "summary";
  const payloadJSON = JSON.stringify(request.arguments, null, 2);
  const canEditPresentations = holdsAnywhere("organization.settings.write");

  return (
    <Page width="narrow" className="@container gap-5">
      {/* Header: the plain title, then who asked read as a phrase: by the
          agent (a link to its page, which lists its instances), and the
          instance of it that is running, named as such because an
          instance can be a conversation or a job as easily as a machine.
          The exact tool name lives in the history's detail list. */}
      <PageHeader back={onBack ? <PageBackButton onClick={onBack}>Queue</PageBackButton> : undefined}>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
          {/* A summary takes the title's place, marked as written by AI;
              the verified title stays visible underneath it. */}
          {summary ? (
            <div className="flex w-full flex-col gap-0.5">
              <p className="text-micro text-ink-faint">AI summary</p>
              <h1 className="text-title-lg text-ink">{summary}</h1>
              <p className="text-body text-ink-muted">
                {title ? <TitleSegments segments={title} /> : humanizeTool(request.tool)}
              </p>
            </div>
          ) : (
            <h1 className="text-title-lg text-ink">
              {title ? (
                <TitleSegments segments={title} valueClassName="text-ink-secondary" />
              ) : (
                humanizeTool(request.tool)
              )}
            </h1>
          )}
          <span className="flex flex-wrap items-center gap-1 text-label text-ink-muted">
            by
            <Referenced
              reference={{ kind: "agent", slug: request.agent_slug, name: request.agent_name }}
              focusable={false}
            >
              <Badge
                icon={<AgentIcon width={14} height={14} />}
                tone="neutral"
                onClick={() => void navigate({ to: "/agents/$agentSlug", params: { agentSlug: request.agent_slug } })}
              >
                {request.agent_name}
                <span className="font-mono text-[12px] font-normal text-ink-muted">{request.agent_slug}</span>
              </Badge>
            </Referenced>
            <span className="text-ink-faint">·</span>
            <Badge icon={<span className="text-label text-ink-muted">instance</span>} tone="neutral">
              {request.agent_instance_name}
            </Badge>
            {request.requester_display_name ? (
              <>
                for
                <ReferenceChip
                  reference={{
                    kind: "person",
                    id: request.requester_membership_id,
                    name: request.requester_display_name,
                  }}
                />
              </>
            ) : null}
          </span>
        </div>
      </PageHeader>

      {decision ? <DecisionCallout request={request} decision={decision} /> : null}

      {/* The requested action, first: a compact labelled summary of the
          arguments by default, the raw JSON one click away. */}
      <div className="flex flex-col overflow-hidden rounded-lg border-hairline border-edge bg-surface shadow-sm">
        <div className="flex h-10 items-center justify-between gap-3 border-b-hairline border-edge pr-2 pl-4">
          <span className="flex items-center gap-1 text-label text-ink-secondary">
            {pending ? "The agent wants to call" : "The agent wanted to call"}
            <ReferenceChip
              reference={{ kind: "tool", name: request.tool, server: request.server }}
              link={auditLinkProps(auditLinkFor({ q: request.tool }))}
            />
            with these arguments
          </span>
          <span className="flex items-center gap-1.5">
            <ToggleGroup
              aria-label="Arguments view"
              value={argumentsView}
              onValueChange={(value) => {
                const next = value as ArgumentsView;
                setArgumentsView(next);
                writeArgumentsView(next);
              }}
            >
              <ToggleGroupItem value="summary">Summary</ToggleGroupItem>
              <ToggleGroupItem value="json">JSON</ToggleGroupItem>
            </ToggleGroup>
            <CopyButton
              value={JSON.stringify({ tool: request.tool, arguments: request.arguments }, null, 2)}
              aria-label="Copy payload as JSON"
            />
          </span>
        </div>
        {showSummary ? (
          <ArgumentView plan={plan} />
        ) : (
          <pre className="overflow-x-auto bg-canvas p-3 font-mono text-[12px] leading-[19px] text-ink-secondary">
            {payloadJSON}
          </pre>
        )}
        {request.agent_reasoning ? (
          <div className="border-t-hairline border-edge px-4 py-3">
            <div className="flex flex-col gap-1.5 border-l-2 border-edge-strong pl-3.5">
              <p className="flex items-center gap-1.5 text-micro text-ink-faint">
                Agent's explanation
                <InfoCard
                  title="Agent's explanation"
                  docsLinks={[{ href: "https://withhuman.ai/docs/web-app/review-queue#request-details", label: "Reviewing request details" }]}
                >
                  Written by the agent. It has not been checked.
                </InfoCard>
              </p>
              <Markdown className="max-w-[560px] text-body text-ink-secondary" lines={5}>{request.agent_reasoning}</Markdown>
            </div>
          </div>
        ) : null}
        {canEditPresentations ? <PresentationFooter request={request} /> : null}
      </div>

      {Assessments ? <Assessments request={request} /> : null}

      {/* The decision, right after what and why: one line says who holds
          the claim, then the note and the two buttons. Someone who holds
          the permission but was not asked sees why instead. */}
      {showPanel && decidable === "no" ? (
        <BlockedCallout reason={viewer?.reason} routing={routing} />
      ) : showPanel ? (
        <div className="flex flex-col gap-4 rounded-lg border-hairline border-edge bg-surface p-5 shadow-sm">
          <div className="flex items-center justify-between gap-3">
            <span className="flex items-center gap-2.5 text-body text-ink-secondary">
              {claim ? (
                <Referenced
                  reference={{ kind: "person", id: claim.membership_id, name: claim.display_name || "Reviewer" }}
                >
                  <Avatar name={claim.display_name || "Reviewer"} size="sm" tooltip={false} />
                </Referenced>
              ) : (
                <PersonIcon width={15} height={15} className="shrink-0 text-ink-muted" />
              )}
              {claim ? (
                claimIsMine ? (
                  <>You are reviewing this. Your claim releases in {formatRemaining(claim.expires_at, now)}.</>
                ) : (
                  <>
                    {claim.display_name || "Another reviewer"} is reviewing this. Their claim releases in{" "}
                    {formatRemaining(claim.expires_at, now)}. You can still decide.
                  </>
                )
              ) : (
                "Nobody is reviewing this yet."
              )}
            </span>
            <span className="flex shrink-0 items-center gap-1">
              {viewer?.targeted ? (
                <AddReviewerMenu
                  requestId={request.id}
                  excludedMembershipIds={[
                    ...(principal.membership_id ? [principal.membership_id] : []),
                    ...addedReviewers.map((reviewer) => reviewer.membership_id),
                  ]}
                  trigger={
                    <Button variant="ghost" size="sm">
                      <PlusIcon width={14} height={14} />
                      Add reviewer
                    </Button>
                  }
                  onOpenChange={setAddingReviewer}
                  onAdded={setReview}
                />
              ) : null}
              {claim && !claimIsMine ? null : (
                <Button
                  variant={claim ? "ghost" : "neutral"}
                  size="sm"
                  loading={claiming !== null}
                  disabled={claiming !== null}
                  onClick={() => void toggleClaim(claim ? "release" : "claim")}
                >
                  {claim ? "Release" : "Claim"}
                </Button>
              )}
            </span>
          </div>
          <TextArea
            ref={noteRef}
            label="Note to the agent"
            error={noteError || undefined}
            value={note}
            onChange={(event) => {
              setNote(event.target.value);
              if (noteError) setNoteError("");
            }}
            placeholder="Why this decision is the right one"
          />
          <div className="grid grid-cols-2 gap-2.5">
            <Tooltip content="Deny" keys="D">
              <Button
                variant="danger"
                size="lg"
                loading={saving === "denied"}
                disabled={saving !== null}
                onClick={() => void decide("denied")}
              >
                Deny
              </Button>
            </Tooltip>
            <Tooltip content="Approve" keys="A">
              <Button
                variant="approve"
                size="lg"
                loading={saving === "approved"}
                disabled={saving !== null}
                onClick={() => void decide("approved")}
              >
                Approve
              </Button>
            </Tooltip>
          </div>
          {decidable === "break_glass" ? (
            <p className="text-micro font-normal text-ink-faint">
              This request was not sent to you. Deciding it is recorded as outside routing.
            </p>
          ) : null}
        </div>
      ) : null}

      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title="Decide outside routing?"
        description="This request was not sent to you. Your decision is recorded as outside routing."
        confirmLabel={confirmStatus === "denied" ? "Deny outside routing" : "Approve outside routing"}
        tone={confirmStatus === "denied" ? "danger" : "approve"}
        onConfirm={() => submit(confirmStatus, true)}
      />

      {/* Timing and routing share compact cells; the pipeline explanation
          gets a full row so block and pipeline names have room to wrap. */}
      <div className="grid grid-cols-2 gap-y-4 border-y-hairline border-edge py-3.5 @xl:grid-cols-4">
        <Fact label="Requested" value={<When at={request.created_at} now={now} />} first />
        {pending ? (
          <Fact
            label="Expires"
            value={
              <span className="flex flex-col items-start gap-0.5">
                <When at={request.deadline_at} now={now} tone={urgent ? "warn" : undefined} />
                <EscalationCountdownLine countdown={countdown} routing={routing} now={now} />
              </span>
            }
          />
        ) : decision?.status === "expired" ? (
          <Fact label="Expired" value={<When at={decision.decided_at} now={now} />} />
        ) : decision?.status === "cancelled" ? (
          <Fact label="Withdrawn" value={<When at={decision.decided_at} now={now} />} />
        ) : decision ? (
          <Fact label="Decided" value={<When at={decision.decided_at} now={now} />} />
        ) : null}
        <Fact
          label="Sent to"
          className="border-l-0 pl-0 @xl:border-l-hairline @xl:pl-4"
          value={
            <span className="flex flex-col items-start gap-0.5">
              {/* The pull that lines the chip's icon up with the label
                  wraps the chip, so its hover bubble keeps its shape. */}
              {routing?.outcome === "agent_owner" && routing.agent_owner && !routing.agent_owner_handed_off_at ? (
                <>
                  <span className="-ml-2 inline-flex">
                    <TargetBadge target={{ type: "user", id: routing.agent_owner.id, name: routing.agent_owner.name }} />
                  </span>
                  <span className="text-label font-normal text-ink-faint">The agent's owner</span>
                </>
              ) : (routing?.outcome === "escalated" || routing?.outcome === "agent_owner") && routing.escalation_path_key ? (
                <>
                  <span className="-ml-2 inline-flex">
                    <TargetBadge
                      target={{
                        type: "escalation_path",
                        id: routing.escalation_path_key,
                        name: routing.escalation_path_name || routing.escalation_path_key,
                      }}
                    />
                  </span>
                  {agentOwnerFallbackNote(routing) ? (
                    <span className="text-label font-normal text-ink-faint">{agentOwnerFallbackNote(routing)}</span>
                  ) : null}
                </>
              ) : routing ? (
                <span className="-ml-2 inline-flex">
                  <HoverCard
                    content={
                      <DefaultRouteCard routing={routing} breakGlassOnly={viewer?.reason === "break_glass_only"} />
                    }
                  >
                    <TargetBadge target={null} fallbackLabel="Default escalation path" />
                  </HoverCard>
                </span>
              ) : request.decision?.channel === "pipeline" ? (
                "Nobody, decided automatically"
              ) : (
                <span className="text-ink-muted">Not routed yet</span>
              )}
              {/* People a targeted reviewer pulled in, widening the route. */}
              {addedReviewers.map((reviewer) => (
                <span
                  key={reviewer.membership_id}
                  className="-ml-2 flex flex-wrap items-center text-label font-normal text-ink-faint"
                >
                  <ReferenceChip
                    reference={{ kind: "person", id: reviewer.membership_id, name: reviewer.display_name }}
                  />
                  added by
                  <ReferenceChip
                    reference={{
                      kind: "person",
                      id: reviewer.added_by_membership_id,
                      name: reviewer.added_by_display_name,
                    }}
                  />
                </span>
              ))}
            </span>
          }
        />
        <Fact
          label="Reviewer"
          value={
            <span className="inline-flex items-center gap-1.5">
              {decision && decision.status !== "expired" && decision.status !== "cancelled" && decision.reviewer_display_name ? (
                <Referenced reference={{ kind: "person", name: decision.reviewer_display_name }}>
                  <Avatar name={decision.reviewer_display_name} size="sm" tooltip={false} />
                </Referenced>
              ) : !decision && claim ? (
                <Referenced
                  reference={{ kind: "person", id: claim.membership_id, name: claim.display_name || "Reviewer" }}
                >
                  <Avatar name={claim.display_name || "Reviewer"} size="sm" tooltip={false} />
                </Referenced>
              ) : null}
              <Tooltip content={reviewerDetail(request, review, claimIsMine, now)}>
                <span className="inline-flex cursor-default items-center">
                  {decision && decision.status !== "expired" && decision.status !== "cancelled" && decision.reviewer_display_name ? (
                    decision.reviewer_display_name
                  ) : decision && decision.channel === "pipeline" ? (
                    "The pipeline"
                  ) : decision ? (
                    <span className="text-ink-muted">Nobody</span>
                  ) : claim ? (
                    claimIsMine ? "You" : claim.display_name || "Another reviewer"
                  ) : (
                    <span className="text-ink-muted">Nobody yet</span>
                  )}
                </span>
              </Tooltip>
            </span>
          }
        />
        {request.consumption ? (
          <Fact label="Used" first value={<When at={request.consumption.consumed_at} now={now} />} />
        ) : null}
        {request.consumption?.outcome ? (
          <Fact
            label="Result"
            value={
              <span className="flex flex-col items-start gap-0.5">
                <Badge className="-ml-2" tone={request.consumption.outcome.status === "succeeded" ? "approve" : "deny"}>
                  {request.consumption.outcome.status === "succeeded" ? "Succeeded" : "Failed"}
                </Badge>
                {request.consumption.outcome.note ? (
                  <span className="text-label font-normal text-ink-faint">{request.consumption.outcome.note}</span>
                ) : null}
              </span>
            }
          />
        ) : null}
        {Cause ? <Cause request={request} /> : null}
      </div>

      <div className="flex flex-col gap-2">
        {EditionHistory ? (
          <EditionHistory request={request} review={review} now={now} />
        ) : (
          <History request={request} review={review} now={now} />
        )}
        <Details request={request} review={review} />
      </div>

      {!pending && onOpenNext && nextPending ? (
        <div className="flex items-center justify-between gap-3 pt-1">
          <p className="text-body text-ink-muted">
            {nextPending === 1 ? "1 more request is waiting." : `${nextPending} more requests are waiting.`}
          </p>
          <Button variant="neutral" size="sm" onClick={onOpenNext}>
            Next pending request
          </Button>
        </div>
      ) : null}
    </Page>
  );
}

/* One cell of the facts strip: a quiet label, the fact, and one line of
 * context under it. */
/* A moment as its distance from now ("1h 5m ago", "in 22h 55m"); the
 * exact time is one hover away. */
function When({ at, now, tone }: { at: string; now: number; tone?: "warn" }) {
  const past = new Date(at).getTime() <= now;
  return (
    <Tooltip content={formatAbsolute(at)}>
      <span className={cn("cursor-default", tone === "warn" && "text-warn")}>
        {past ? `${formatElapsed(at, now)} ago` : `in ${formatRemaining(at, now)}`}
      </span>
    </Tooltip>
  );
}

/* What the escalation executor does next, under the expiry: "Escalates
 * in 12m" while a later level waits behind the armed timer, and the other
 * waits the machine can be in. Which path or policy, and what happens at
 * that moment, is one hover away. Nothing while nothing is armed. */
function EscalationCountdownLine({
  countdown,
  routing,
  now,
}: {
  countdown: EscalationCountdown | null;
  routing: ApprovalRouting | undefined;
  now: number;
}) {
  if (!countdown) return null;
  const pathName = routing?.escalation_path_name || routing?.escalation_path_key || "the escalation path";
  const document = countdown.kind !== "paused" && countdown.team
    ? `${countdown.team.name}'s escalation policy`
    : pathName;
  let text: string;
  let explanation: string;
  if (countdown.kind === "paused") {
    text = "Escalation paused";
    explanation = "Paused while someone has claimed the request.";
  } else {
    const due = new Date(countdown.at).getTime() <= now;
    const when = due ? "now" : `in ${formatRemaining(countdown.at, now)}`;
    const moment = formatAbsolute(countdown.at);
    const who = countdown.team ? `${countdown.team.name} ` : "";
    switch (countdown.kind) {
      case "next_level":
        text = `${who}${countdown.team ? "escalates" : "Escalates"} ${when}`;
        explanation = `${moment}: the next level of ${document} is notified.`;
        break;
      case "last_level":
        text = `${who}${countdown.team ? "escalation ends" : "Escalation ends"} ${when}`;
        explanation = `The last level of ${document} has been notified. After ${moment}, the request waits until it expires.`;
        break;
      case "repeat":
        text = `${who}${countdown.team ? "repeats" : "Repeats"} ${when}`;
        explanation = `${moment}: ${document} runs again from its first level.`;
        break;
      case "deferred":
        text = `${who}${countdown.team ? "resumes" : "Resumes"} ${when}`;
        explanation = `Waiting for working hours. ${moment}: ${document} continues.`;
        break;
    }
  }
  return (
    <Tooltip content={explanation}>
      <span className="cursor-default text-label font-normal text-ink-secondary">{text}</span>
    </Tooltip>
  );
}

/* One cell of the facts strip: a quiet label and the fact. Exported for
 * the edition's own fact rows on this page. */
export function Fact({
  label,
  value,
  first = false,
  className,
}: {
  label: string;
  value: ReactNode;
  first?: boolean;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex min-w-0 flex-col gap-0.5 px-4",
        first ? "pl-0" : "border-l-hairline border-edge",
        className,
      )}
    >
      <span className="text-micro text-ink-faint">{label}</span>
      <span className="flex min-h-5 items-center text-body font-medium text-ink">{value}</span>
    </div>
  );
}

/* Why the viewer cannot decide, in place of the buttons. The panel is
 * gone entirely when they lack the permission; this is for people who
 * hold it and were not asked. */
function BlockedCallout({
  reason,
  routing,
}: {
  reason: ReviewViewer["reason"];
  routing: ApprovalRouting | undefined;
}) {
  if (reason === "break_glass_only") {
    return (
      <Callout
        tone="neutral"
        icon={<ShieldIcon width={15} height={15} />}
        title="This request matched no escalation path"
      >
        Only break-glass reviewers can decide it.
      </Callout>
    );
  }
  if (
    (routing?.outcome === "escalated" || (routing?.outcome === "agent_owner" && routing.agent_owner_handed_off_at)) &&
    routing.targets.length === 0
  ) {
    const pathName = routing.escalation_path_name || routing.escalation_path_key || "The escalation path";
    return (
      <Callout
        tone="neutral"
        icon={<ShieldIcon width={15} height={15} />}
        title="This request's path reaches nobody"
      >
        {pathName} notifies nobody for this request. Only a break-glass reviewer can decide it.
      </Callout>
    );
  }
  const named = routing?.targets.length ? describeTargets(routing.targets) : null;
  return (
    <Callout
      tone="neutral"
      icon={<ShieldIcon width={15} height={15} />}
      title="This request was sent to someone else"
    >
      {named
        ? `Ask ${named} to decide it.`
        : "Ask one of the people it was sent to."}
    </Callout>
  );
}

/* "Ana, Ben, or the Payments team": the reachable targets as one phrase. */
function describeTargets(targets: ApprovalRoutingTarget[]): string {
  const names = targets.map((target) =>
    target.type === "team" ? `the ${target.name || "team"} team` : target.name || "a colleague",
  );
  if (names.length === 1) return names[0];
  return `${names.slice(0, -1).join(", ")}, or ${names[names.length - 1]}`;
}

function reviewerDetail(
  request: ApprovalRequest,
  review: ApprovalReview | null,
  claimIsMine: boolean,
  now: number,
): ReactNode {
  const decision = request.decision;
  const claim = request.claim;
  if (decision?.status === "cancelled") return "the agent withdrew it";
  if (decision && decision.status !== "expired") {
    if (decision.channel === "pipeline") return "no human was needed";
    return `via ${decision.channel}`;
  }
  if (decision) return "the deadline passed";
  if (claim) {
    return `${claimIsMine ? "your" : "their"} claim releases in ${formatRemaining(claim.expires_at, now)}`;
  }
  return "claim it from the panel below";
}

/* The outcome record. Expiry is a fact about time and withdrawal one
 * about the agent, not a reviewer's decision, so neither gets a reviewer
 * line. */
function DecisionCallout({ request, decision }: { request: ApprovalRequest; decision: Decision }) {
  const when = (
    <span className="mt-0.5 block text-micro font-normal text-ink-muted">
      {formatAbsolute(decision.decided_at)}
      <span className="mx-1.5">·</span>
      {formatBetween(request.created_at, decision.decided_at)} after the request
      {decision.status !== "expired" && decision.channel && decision.channel !== "pipeline" ? (
        <>
          <span className="mx-1.5">·</span>
          via {decision.channel}
        </>
      ) : null}
    </span>
  );
  if (decision.status === "expired") {
    return (
      <Callout tone="neutral" icon={<ClockIcon width={15} height={15} />} title="Expired">
        Nobody decided before the deadline. The agent was told no.
        {when}
      </Callout>
    );
  }
  if (decision.status === "cancelled") {
    return (
      <Callout tone="neutral" icon={<DashIcon width={15} height={15} />} title="Withdrawn">
        {request.agent_name} on {request.agent_instance_name} withdrew the request before anyone decided.
        {when}
      </Callout>
    );
  }
  const approved = decision.status === "approved";
  const label = approved ? "Approved" : "Denied";
  const by =
    decision.channel === "pipeline"
      ? `${label} automatically by the pipeline`
      : `${label} by ${decision.reviewer_display_name || "a reviewer"}`;
  return (
    <Callout
      tone={approved ? "approve" : "deny"}
      icon={approved ? <CheckIcon width={15} height={15} /> : <XIcon width={15} height={15} />}
      title={by}
    >
      {decision.note ? `"${decision.note}"` : "No note was recorded."}
      {when}
    </Callout>
  );
}

/* One row of the request's history. */
export interface Moment {
  key: string;
  at: string;
  tone: Tone;
  marker?: ReactNode;
  title: ReactNode;
  detail?: ReactNode;
  /* A live moment is happening now; it shows no timestamp of its own. */
  live?: boolean;
}

/* The moments core knows without an audit log: the request, where it
 * was routed, who was pulled in since, and how it ended, in time order,
 * with the live wait last. An edition adds what its log tells (what the
 * automated checks concluded, the escalation steps, the claims) through
 * extra, and names the pipeline in an automatic decision through
 * pipelineReference. */
export function requestMoments(
  request: ApprovalRequest,
  review: ApprovalReview | null,
  now: number,
  options: { extra?: Moment[]; pipelineReference?: ReactNode } = {},
): Moment[] {
  const agent = <ReferenceChip inline reference={{ kind: "agent", slug: request.agent_slug, name: request.agent_name }} />;
  const list: Moment[] = [
    { key: "requested", at: request.created_at, tone: "accent", title: <>Requested by {agent}</> },
    ...(options.extra ?? []),
  ];
  const routing = review?.routing;
  const path = routing?.escalation_path_key ? (
    <ReferenceChip inline
      reference={{
        kind: "escalation_path",
        key: routing.escalation_path_key,
        name: routing.escalation_path_name || routing.escalation_path_key,
      }}
    />
  ) : null;
  if (routing) {
    const owner = routing.agent_owner ? (
      <ReferenceChip inline reference={{ kind: "person", id: routing.agent_owner.id, name: routing.agent_owner.name }} />
    ) : null;
    list.push({
      key: "routed",
      at: routing.created_at,
      tone: "neutral",
      title:
        routing.outcome === "agent_owner"
          ? <>Sent to {owner ?? "the agent's owner"}, the agent's owner</>
          : routing.outcome === "escalated"
          ? <>Sent to {path ?? "the escalation path"}</>
          : <>
              Sent to{" "}
              <HoverCard
                content={<DefaultRouteCard routing={routing} breakGlassOnly={review?.viewer.reason === "break_glass_only"} />}
                triggerClassName="inline rounded-sm"
              >
                every authorized reviewer
              </HoverCard>
            </>,
      detail: routing.urgency === "interrupt" ? "interrupt" : "standard urgency",
    });
    if (routing.agent_owner_handed_off_at) {
      list.push({
        key: "agent-owner-handoff",
        at: routing.agent_owner_handed_off_at,
        tone: "neutral",
        title: path ? <>Sent to {path} instead</> : <>Sent to every authorized reviewer instead</>,
        detail: "the agent's owner was deactivated",
      });
    }
  }
  for (const reviewer of review?.added_reviewers ?? []) {
    list.push({
      key: `added-${reviewer.membership_id}`,
      at: reviewer.created_at,
      tone: "neutral",
      marker: <PlusIcon width={14} height={14} />,
      title: (
        <>
          <ReferenceChip inline reference={{ kind: "person", id: reviewer.membership_id, name: reviewer.display_name }} />{" "}
          added as a reviewer
        </>
      ),
      detail: (
        <>
          by <ReferenceChip inline reference={{ kind: "person", id: reviewer.added_by_membership_id, name: reviewer.added_by_display_name }} />
        </>
      ),
    });
  }
  const decision = request.decision;
  if (decision?.status === "expired") {
    list.push({
      key: "expired",
      at: decision.decided_at,
      tone: "neutral",
      marker: <ClockIcon width={14} height={14} />,
      title: "Expired",
      detail: "the agent was told no",
    });
  } else if (decision?.status === "cancelled") {
    list.push({
      key: "cancelled",
      at: decision.decided_at,
      tone: "neutral",
      marker: <DashIcon width={14} height={14} />,
      title: <>Withdrawn by {agent}</>,
      detail: "the agent stopped waiting",
    });
  } else if (decision) {
    const approved = decision.status === "approved";
    const label = approved ? "Approved" : "Denied";
    list.push({
      key: "decided",
      at: decision.decided_at,
      tone: approved ? "approve" : "deny",
      marker: approved ? <CheckIcon width={14} height={14} /> : <XIcon width={14} height={14} />,
      title: decision.channel === "pipeline" ? (
        <>{label} automatically by {options.pipelineReference ?? "the pipeline"}</>
      ) : (
        <>
          {label} by {decision.reviewer_display_name
            ? <ReferenceChip inline reference={{ kind: "person", name: decision.reviewer_display_name }} />
            : "a reviewer"}
        </>
      ),
      detail: "the agent was told and continued",
    });
  }
  const consumption = request.consumption;
  if (consumption) {
    list.push({
      key: "used",
      at: consumption.consumed_at,
      tone: "neutral",
      title: <>Used by {agent}</>,
      detail: "the call started",
    });
  }
  if (consumption?.outcome) {
    const succeeded = consumption.outcome.status === "succeeded";
    list.push({
      key: "result",
      at: consumption.outcome.reported_at,
      tone: succeeded ? "approve" : "deny",
      marker: succeeded ? <CheckIcon width={14} height={14} /> : <XIcon width={14} height={14} />,
      title: succeeded ? "Call succeeded" : "Call failed",
      detail: consumption.outcome.note,
    });
  }
  list.sort((a, b) => new Date(a.at).getTime() - new Date(b.at).getTime());
  if (request.status === "pending") {
    list.push({
      key: "waiting",
      at: request.created_at,
      tone: "warn",
      marker: <ClockIcon width={14} height={14} />,
      title: "Waiting for a decision",
      detail: `${formatElapsed(request.created_at, now)} so far`,
      live: true,
    });
  }
  return list;
}

/* The history, closed by default since most of it (requested, routed)
 * is the same for every request: every row with its exact time, and an
 * optional action beside the label, such as a link into an audit log. */
export function HistoryDisclosure({ moments, now, action }: { moments: Moment[]; now: number; action?: ReactNode }) {
  return (
    <Disclosure label="Request history" action={action}>
      <Timeline>
        {moments.map((moment) => (
          <TimelineItem key={moment.key} tone={moment.tone} marker={moment.marker}>
            <p className="text-body text-ink">
              {moment.title}
              {moment.detail ? <span className="text-ink-muted"> · {moment.detail}</span> : null}
            </p>
            {moment.live ? null : (
              <p className="text-micro font-normal text-ink-faint">
                {formatAbsolute(moment.at)}
                <span className="mx-1.5">·</span>
                {formatElapsed(moment.at, now)} ago
              </p>
            )}
          </TimelineItem>
        ))}
      </Timeline>
    </Disclosure>
  );
}

/* The open edition's history: what core knows on its own. */
function History({ request, review, now }: { request: ApprovalRequest; review: ApprovalReview | null; now: number }) {
  const moments = useMemo(() => requestMoments(request, review, now), [request, review, now]);
  return <HistoryDisclosure moments={moments} now={now} />;
}

/* The machine detail: ids, the deadline, policy scope, and the adapter's
 * runtime context, for whoever needs to cross-reference. */
function Details({ request, review }: { request: ApprovalRequest; review: ApprovalReview | null }) {
  const decision = request.decision;
  const routing = review?.routing;
  return (
    <Disclosure
      label="Details"
      action={
        <CopyButton
          value={JSON.stringify(
            {
              tool: request.tool,
              agent: request.agent_name,
              agent_slug: request.agent_slug,
              agent_instance: request.agent_instance_name,
              agent_instance_id: request.agent_instance_id,
              status: request.status,
              requested_at: request.created_at,
              decided_at: decision?.decided_at,
              deadline_at: request.deadline_at,
              escalation_path: routing?.escalation_path_key,
              context: request.context,
              request_id: request.id,
            },
            null,
            2,
          )}
          aria-label="Copy details as JSON"
        />
      }
    >
      <div className="flex flex-col gap-3">
        {/* The first row carries its own padding; pull it up so the list
            starts as close under the label as the timeline does. */}
        <DescriptionList className="-mt-1.5">
          <DescriptionItem term="Tool" mono>
            {request.tool}
          </DescriptionItem>
          <DescriptionItem term="Agent">{request.agent_name}</DescriptionItem>
          <DescriptionItem term="Agent slug" mono>
            {request.agent_slug}
          </DescriptionItem>
          <DescriptionItem term="Instance">{request.agent_instance_name}</DescriptionItem>
          <DescriptionItem term="Instance id" mono>
            {request.agent_instance_id}
          </DescriptionItem>
          <DescriptionItem term="Deadline" copy={request.deadline_at}>
            {formatAbsolute(request.deadline_at)}
          </DescriptionItem>
          {routing?.escalation_path_key ? (
            <DescriptionItem term="Escalation path" mono>
              {routing.escalation_path_key}
            </DescriptionItem>
          ) : null}
          {decision?.decided_at ? (
            <DescriptionItem term="Decided at" copy={decision.decided_at}>
              {formatAbsolute(decision.decided_at)}
            </DescriptionItem>
          ) : null}
          <DescriptionItem term="Request id" mono>
            {request.id}
          </DescriptionItem>
        </DescriptionList>
        {request.context !== undefined && request.context !== null ? (
          <CodeBlock
            label="Runtime context from the adapter"
            code={JSON.stringify(request.context, null, 2)}
            raised
          />
        ) : null}
      </div>
    </Disclosure>
  );
}

/* The request's name for a toast or a tab: its summary when it has one,
 * else the presentation's title with its values, else the humanised
 * tool name. */
export function requestTitle(request: ApprovalRequest): string {
  if (request.summary?.text) return request.summary.text;
  const segments = renderTitle(request.presentation?.title, request.arguments);
  return segments ? titleText(segments) : humanizeTool(request.tool);
}

/* A presentation title with its argument values set in mono. */
function TitleSegments({
  segments,
  valueClassName,
}: {
  segments: TitleSegment[];
  valueClassName?: string;
}) {
  return segments.map((segment, index) =>
    segment.kind === "value" ? (
      <span key={index} className={cn("font-mono text-[0.82em]", valueClassName)}>
        {segment.text}
      </span>
    ) : (
      <Fragment key={index}>{segment.text}</Fragment>
    ),
  );
}

/* Where the call came from, for the presentation editor to bind to: the
 * runtime the adapter recorded and the server that defines the tool. */
function requestSelector(request: ApprovalRequest): { runtime: string; server: string } {
  const context = request.context && typeof request.context === "object" ? (request.context as Record<string, unknown>) : {};
  const runtime = typeof context.runtime === "string" && context.runtime ? context.runtime : "";
  return { runtime, server: request.server ?? "" };
}

/* For people who can change the organization's settings: which
 * presentation drew the arguments, and the way to change it. A quiet
 * strip under the payload, since it is about the page, not the request. */
function PresentationFooter({ request }: { request: ApprovalRequest }) {
  const presentation = request.presentation;
  const { runtime, server } = requestSelector(request);
  const linkClass = "text-ink-secondary underline decoration-edge-strong underline-offset-2 hover:text-ink hover:decoration-ink";
  return (
    <p className="flex flex-wrap items-center gap-x-1 border-t-hairline border-edge px-4 py-2 text-micro text-ink-faint">
      {presentation?.source === "organization" ? (
        <>
          Shown with your organization's presentation.
          <Link to="/settings/tool-presentations/$presentationId" params={{ presentationId: presentation.id ?? "" }} search={{ request: request.id }} className={linkClass}>
            Edit it
          </Link>
        </>
      ) : presentation ? (
        <>
          Shown with the built-in presentation.
          <Link
            to="/settings/tool-presentations/new"
            search={{ tool: request.tool, runtime, server, request: request.id }}
            className={linkClass}
          >
            Customize it
          </Link>
        </>
      ) : (
        <>
          No presentation covers this tool.
          <Link
            to="/settings/tool-presentations/new"
            search={{ tool: request.tool, runtime, server, request: request.id }}
            className={linkClass}
          >
            Add one
          </Link>
        </>
      )}
    </p>
  );
}

/* Why a route that asks the agent's owner is at its fallback path, as the
 * line under the path chip says it; null when the owner route is not
 * involved. */
function agentOwnerFallbackNote(routing: ApprovalRouting): string | null {
  if (routing.agent_owner_handed_off_at) return "The agent's owner was deactivated";
  switch (routing.agent_owner_fallback) {
    case "no_agent_owner":
      return "This agent has no owner";
    case "agent_owner_inactive":
      return "The agent's owner is deactivated";
    case "agent_owner_cannot_decide":
      return "The agent's owner cannot decide requests";
    default:
      return null;
  }
}
