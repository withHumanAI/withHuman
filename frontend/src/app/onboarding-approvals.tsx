import { useMemo, type ReactNode } from "react";
import { Callout } from "@/ds/components/callout";
import { Card, CardDescription, CardHeader, CardTitle } from "@/ds/components/card";
import { EmptyState } from "@/ds/components/empty-state";
import { Spinner } from "@/ds/components/spinner";
import { CheckIcon, XIcon } from "@/ds/icons";
import { STATUS_LABEL } from "./approval-status";
import { RequestDetail } from "./request-detail";
import { refreshQueue, useQueue } from "./queue-store";
import type { ApprovalRequest } from "../types";

/* The test step of the guided setup, live: the newest pending request
 * from the agents just enrolled renders inline for the person to
 * decide, and every request decided since the step began stays on screen
 * as a closed loop, so repeated test runs each show their outcome. */
export interface TestActivity {
  /* The newest pending request from the enrolled agents, to decide inline. */
  pending: ApprovalRequest | null;
  /* Requests decided since the step began, newest first: loops closed here. */
  closed: ApprovalRequest[];
  /* Whether the queue has failed to load at all. A queue that loaded
   * and then lost its stream says nothing while it reconnects. */
  loadFailed: boolean;
  /* Whether the test has run: something arrived since the step began. */
  tested: boolean;
}

/* What the test step knows about the agents just enrolled, from the
 * live queue. Only requests created after since count, so the test sees
 * the calls made after the person left the rules step; that survives a
 * reload, because the boundary is the session's, not the page's. One
 * caller derives it and hands it down, so the pane and the flow's way
 * out agree. */
export function useTestActivity(agentSlugs: string[], since: string | null): TestActivity {
  /* The whole visible queue, not the notified layer: the test request
   * must show the instant it exists, before any plan has addressed it. */
  const { requests, loadFailed, loaded } = useQueue("visible");
  const slugKey = agentSlugs.join("\n");
  const slugs = useMemo(() => new Set(slugKey.split("\n").filter(Boolean)), [slugKey]);
  const from = since === null ? 0 : Date.parse(since);

  const mine = useMemo(
    () =>
      requests
        .filter((request) => slugs.has(request.agent_slug) && Date.parse(request.created_at) >= from)
        .sort((a, b) => b.created_at.localeCompare(a.created_at)),
    [requests, slugs, from],
  );

  const pending = mine.find((request) => request.status === "pending") ?? null;
  const closed = mine.filter((request) => request.status !== "pending");
  return { pending, closed, loadFailed, tested: loaded && (pending !== null || closed.length > 0) };
}

export function OnboardingApprovals({
  activity: { pending, closed, loadFailed },
  waitingDescription,
  next,
}: {
  activity: TestActivity;
  /* What the empty state says while nothing has arrived. */
  waitingDescription: string;
  /* The way forward, once the flow offers one: a full-width row under
   * the closed loop, or under the card while waiting. */
  next?: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-3">
      {loadFailed ? (
        <Callout tone="warn">
          withHuman could not be reached to watch for the test request. Trying again automatically.
        </Callout>
      ) : null}
      {closed.map((request) => (
        <Callout
          key={request.id}
          tone={request.status === "approved" ? "approve" : "deny"}
          icon={
            request.status === "approved" ? (
              <CheckIcon width={15} height={15} />
            ) : (
              <XIcon width={15} height={15} />
            )
          }
          title={`Loop closed: ${request.tool} was ${STATUS_LABEL[request.status].toLowerCase()}`}
        >
          {request.agent_name} received your decision and carried on.
        </Callout>
      ))}
      {pending || closed.length === 0 ? (
        <>
          {pending ? (
            <Card>
              <CardHeader>
                <div>
                  <CardTitle>The agent is waiting for your approval</CardTitle>
                  <CardDescription>
                    The agent is currently paused on your decision, check it, decide what to do
                    then check back.
                  </CardDescription>
                </div>
              </CardHeader>
              <RequestDetail request={pending} onDecided={() => refreshQueue("visible")} />
            </Card>
          ) : (
            /* Nothing has happened yet: the spinner says the page is listening. */
            <Card>
              <EmptyState
                icon={<Spinner size={16} />}
                title="Waiting for the agent to call a gated tool"
                description={waitingDescription}
              />
            </Card>
          )}
          {next ? <div className="flex justify-end">{next}</div> : null}
        </>
      ) : (
        /* A loop is closed; the page still listens, quietly. The way
         * forward takes the whole row under the callout. */
        next ? <div className="flex">{next}</div> : null
      )}
    </div>
  );
}
