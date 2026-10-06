import type { ComponentProps } from "react";
import { Badge } from "./badge";
import { BlockCard, type BlockMenuHandlers, type BlockPosition } from "./block-card";
import { BlockSummaryCard, SummaryChoices, SummaryField, SummaryText } from "./block-summary";
import { Button } from "./button";
import { Checkbox } from "./checkbox";
import { InfoLabel } from "./info-label";
import { Referenced } from "./reference";
import { TargetBadge, TargetSelect, type ReviewerTarget } from "./reviewer-target";
import { Select, SelectContent, SelectItem, SelectTrigger } from "./select";
import { Switch } from "./switch";
import { TextField } from "./text-field";
import { ToggleGroup, ToggleGroupItem } from "./toggle-group";
import { Tooltip } from "./tooltip";
import { ArrowUpRightIcon } from "@/ds/icons";

/*
 * One webhook block of an approval pipeline: the request is signed and
 * posted to an endpoint the organization runs, and the endpoint answers
 * with an outcome. The block decides which outcomes the endpoint may
 * return; anything else fails closed to a human. Same card anatomy as
 * every other kind (BlockCard), so they all sit on one rail.
 */

export type WebhookOutcome = "next" | "human" | "approve" | "deny";

export interface WebhookBlockDraft {
  id: string;
  name: string;
  enabled: boolean;
  /* The key of the organization endpoint (URL and signing secret) this
   * block posts to; deliveries go to the key's active revision. */
  endpointKey: string;
  allowedOutcomes: WebhookOutcome[];
  /* Per-attempt budget as a duration string; the server caps it at 30s. */
  timeout: string;
  maxAttempts: 1 | 2 | 3;
  include: { agentReasoning: boolean; context: boolean; previousMetadata: boolean };
  reason: string;
  /* Who is asked when the endpoint answers human, or a failure falls
   * closed to human. null inherits the pipeline default. */
  target?: ReviewerTarget | null;
}

/* What the picker needs to know about an endpoint: its key, the latest
 * revision's name and URL, and whether a revision is active. */
export interface WebhookEndpointOption {
  key: string;
  name: string;
  url: string;
  active: boolean;
}

const TIMEOUTS = ["2s", "5s", "10s", "30s"] as const;
const ATTEMPTS = [1, 2, 3] as const;

const OUTCOME_ROWS: { value: WebhookOutcome; label: string; hint: string }[] = [
  { value: "next", label: "Continue", hint: "Pass the request to the next block." },
  { value: "human", label: "Ask a human", hint: "Send the request to a human." },
  { value: "approve", label: "Approve", hint: "Approve the request automatically." },
  { value: "deny", label: "Deny", hint: "Deny the request automatically." },
];

export function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return "";
  }
}

export function findEndpoint(
  endpoints: WebhookEndpointOption[],
  endpointKey: string,
): WebhookEndpointOption | undefined {
  return endpoints.find((endpoint) => endpoint.key === endpointKey);
}

/* The rail exits a webhook draws: one branch per allowed outcome that
 * decides or asks a human; "next" continues down the rail when allowed. */
export function webhookExits(block: WebhookBlockDraft): {
  exits: {
    key: string;
    label: string;
    outcome: "approve" | "deny" | "human";
    target?: ReviewerTarget | null;
  }[];
  continueLabel: string;
} {
  const order: ("approve" | "deny" | "human")[] = ["approve", "deny", "human"];
  const exits = order
    .filter((outcome) => block.allowedOutcomes.includes(outcome))
    .map((outcome) => ({
      key: outcome,
      label: `says ${outcome}`,
      outcome,
      target: outcome === "human" ? (block.target ?? null) : undefined,
    }));
  return {
    exits,
    continueLabel: block.allowedOutcomes.includes("next") ? "says next: continue" : "cannot continue",
  };
}

/* The subtitle identifies the type and destination. Its hover card and
 * the expanded editor carry the endpoint and delivery details. */
function EndpointSummary({ block, endpoint }: { block: WebhookBlockDraft; endpoint?: WebhookEndpointOption }) {
  const summary = summarizeWebhook(block, endpoint);
  if (!endpoint) return <>{summary}</>;
  return (
    <>
      Webhook ·{" "}
      <Referenced
        reference={{ kind: "webhook_endpoint", key: endpoint.key, name: endpoint.name }}
        inline
      >
        {endpoint.name}
      </Referenced>
      {!endpoint.active ? " · archived" : null}
    </>
  );
}

export function summarizeWebhook(block: WebhookBlockDraft, endpoint?: WebhookEndpointOption): string {
  const destination = endpoint
    ? [endpoint.name, endpoint.active ? "" : "archived"].filter(Boolean).join(" · ")
    : block.endpointKey || "No endpoint selected";
  return `Webhook · ${destination}`;
}

export interface WebhookBlockCardProps extends BlockMenuHandlers {
  block: WebhookBlockDraft;
  endpoints: WebhookEndpointOption[];
  position: BlockPosition;
  expanded: boolean;
  onExpandedChange: (expanded: boolean) => void;
  onChange: (next: WebhookBlockDraft) => void;
  /* Takes the user to the endpoints page. */
  onManageEndpoints?: () => void;
  handleProps?: ComponentProps<"div">;
  dragging?: boolean;
  /* Teams and people the human answer may name. Without it the block
   * offers no reviewer choice and every human exit uses the default. */
  directory?: ReviewerTarget[];
  /* The resolved reviewer destination, included in the field help. */
  describeRoute?: (target: ReviewerTarget | null) => string;
}

export function WebhookBlockCard({
  block,
  endpoints,
  position,
  expanded,
  onExpandedChange,
  onChange,
  onManageEndpoints,
  handleProps,
  dragging = false,
  directory,
  describeRoute,
  ...menu
}: WebhookBlockCardProps) {
  const endpoint = findEndpoint(endpoints, block.endpointKey);
  const timeouts: string[] = TIMEOUTS.includes(block.timeout as (typeof TIMEOUTS)[number])
    ? [...TIMEOUTS]
    : [block.timeout, ...TIMEOUTS];
  return (
    <BlockCard
      {...menu}
      position={position}
      title={block.name || "Untitled webhook"}
      subtitle={<EndpointSummary block={block} endpoint={endpoint} />}
      enabled={block.enabled}
      expanded={expanded}
      onExpandedChange={onExpandedChange}
      handleProps={handleProps}
      dragging={dragging}
    >
      {/* Reads in evaluation order: what it is called, where it posts,
        * what it sends, what the answer may do, how long it may take,
        * and why. */}
      <TextField
        label="Name"
        value={block.name}
        onChange={(event) => onChange({ ...block, name: event.target.value })}
        className="max-w-[380px]"
      />

      <div className="flex flex-col gap-1.5">
        <InfoLabel
          className="text-label text-ink-secondary"
          info="Choose the webhook endpoint that reviews this request."
          docsLinks={[{ href: "https://withhuman.ai/docs/web-app/webhook-blocks#endpoints-and-allowed-outcomes", label: "Webhook endpoints and outcomes" }]}
        >
          Webhook Endpoint
        </InfoLabel>
        <div className="flex flex-wrap items-center gap-2">
          <Select
            items={endpoints}
            itemToStringLabel={(item) => (item as WebhookEndpointOption).name}
            value={endpoint ?? null}
            onValueChange={(next) => {
              if (next) onChange({ ...block, endpointKey: (next as WebhookEndpointOption).key });
            }}
          >
            <SelectTrigger
              aria-label="Webhook Endpoint"
              className="w-72"
              placeholder={endpoints.length ? "Choose an endpoint" : "No endpoints yet"}
            />
            <SelectContent<WebhookEndpointOption>>
              {(item) => (
                <SelectItem key={item.key} value={item}>
                  {item.active ? item.name : `${item.name} (archived)`}
                </SelectItem>
              )}
            </SelectContent>
          </Select>
          {onManageEndpoints ? (
            <Button variant="ghost" size="sm" onClick={onManageEndpoints}>
              Manage Webhook Endpoint
              <ArrowUpRightIcon width={13} height={13} />
            </Button>
          ) : null}
        </div>
        {(endpoint && !endpoint.active) || (!endpoint && block.endpointKey) ? (
          <p role="alert" className="text-micro text-deny">
            {endpoint
              ? "This endpoint is archived. Choose another one."
              : "This endpoint is no longer available. Choose another one."}
          </p>
        ) : null}
      </div>

      <div className="flex flex-col gap-1.5">
        <InfoLabel
          className="text-label text-ink-secondary"
          info="Choose which answers the endpoint can give."
          docsLinks={[{ href: "https://withhuman.ai/docs/web-app/webhook-blocks#endpoints-and-allowed-outcomes", label: "Allowed webhook outcomes" }]}
        >
          The endpoint may answer
        </InfoLabel>
        <div className="flex flex-wrap gap-x-6 gap-y-2">
          {OUTCOME_ROWS.map((row) => (
            <Tooltip key={row.value} content={row.hint}>
              <span className="inline-flex">
                <Checkbox
                  label={row.label}
                  checked={block.allowedOutcomes.includes(row.value)}
                  onCheckedChange={(checked) =>
                    onChange({
                      ...block,
                      allowedOutcomes: checked
                        ? [...block.allowedOutcomes, row.value]
                        : block.allowedOutcomes.filter((outcome) => outcome !== row.value),
                    })
                  }
                />
              </span>
            </Tooltip>
          ))}
        </div>
      </div>

      <div className="flex flex-col gap-1.5">
        <InfoLabel
          className="text-label text-ink-secondary"
          info="The endpoint always sees the tool call, its arguments and the agent."
          docsLinks={[{ href: "https://withhuman.ai/docs/web-app/webhook-blocks#inputs-and-delivery-settings", label: "Webhook inputs and delivery" }]}
        >
          The endpoint also sees
        </InfoLabel>
        <div className="flex flex-wrap gap-x-6 gap-y-2">
          <Switch
            label="Agent reasoning"
            checked={block.include.agentReasoning}
            onCheckedChange={(checked) =>
              onChange({ ...block, include: { ...block.include, agentReasoning: checked } })
            }
          />
          <Switch
            label="Request context"
            checked={block.include.context}
            onCheckedChange={(checked) =>
              onChange({ ...block, include: { ...block.include, context: checked } })
            }
          />
          <Switch
            label="Earlier block results"
            checked={block.include.previousMetadata}
            onCheckedChange={(checked) =>
              onChange({ ...block, include: { ...block.include, previousMetadata: checked } })
            }
          />
        </div>
      </div>

      <div className="flex flex-wrap gap-6">
        <div className="flex flex-col gap-1.5">
          <InfoLabel
            className="text-label text-ink-secondary"
            info="How long to wait for each attempt before retrying or asking a human."
            docsLinks={[{ href: "https://withhuman.ai/docs/web-app/webhook-blocks#inputs-and-delivery-settings", label: "Timeouts and retries" }]}
          >
            Attempt timeout
          </InfoLabel>
          <ToggleGroup
            aria-label="Attempt timeout"
            className="self-start"
            value={block.timeout}
            onValueChange={(timeout) => onChange({ ...block, timeout: String(timeout) })}
          >
            {timeouts.map((timeout) => (
              <ToggleGroupItem key={timeout} value={timeout} className="px-2">
                {timeout}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </div>
        <div className="flex flex-col gap-1.5">
          <InfoLabel
            className="text-label text-ink-secondary"
            info="How many times to call the endpoint before asking a human."
            docsLinks={[{ href: "https://withhuman.ai/docs/web-app/webhook-blocks#inputs-and-delivery-settings", label: "Timeouts and retries" }]}
          >
            Attempts
          </InfoLabel>
          <ToggleGroup
            aria-label="Attempts"
            className="self-start"
            value={String(block.maxAttempts)}
            onValueChange={(attempts) =>
              onChange({ ...block, maxAttempts: Number(attempts) as 1 | 2 | 3 })
            }
          >
            {ATTEMPTS.map((attempts) => (
              <ToggleGroupItem key={attempts} value={String(attempts)} className="px-2">
                {attempts}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </div>
      </div>

      {directory ? (
        <div className="flex flex-col gap-1.5">
          <InfoLabel
            className="text-label text-ink-secondary"
            info={["Who reviews the request when the endpoint asks a human or fails.", describeRoute?.(block.target ?? null)].filter(Boolean).join(" ")}
            docsLinks={[{ href: "https://withhuman.ai/docs/web-app/approval-pipelines#human-review", label: "Routing requests to reviewers" }]}
          >
            Escalation path
          </InfoLabel>
          <TargetSelect
            directory={directory}
            value={block.target ?? null}
            onChange={(target) => onChange({ ...block, target })}
            fallbackLabel="Pipeline default"
          />
        </div>
      ) : null}

      <TextField
        label="Reason"
        value={block.reason}
        onChange={(event) => onChange({ ...block, reason: event.target.value })}
        placeholder="Fraud service reviewed the request."
        info="The explanation the agent receives for an automatic decision."
        docsLinks={[{ href: "https://withhuman.ai/docs/web-app/webhook-blocks#webhook-responses", label: "Webhook decision reasons" }]}
      />
    </BlockCard>
  );
}

/* The read-only face of a webhook block. Expanded, it lays out the
 * editing card's fields (endpoint, allowed answers, inputs, timing,
 * path, reason) as text. */
export function WebhookBlockSummaryCard({
  block,
  endpoints,
  position,
  onNavigate,
  expanded,
  onExpandedChange,
  describeRoute,
}: {
  block: WebhookBlockDraft;
  endpoints: WebhookEndpointOption[];
  position: BlockPosition;
  onNavigate?: () => void;
  expanded?: boolean;
  /* Present when the card opens to its details. */
  onExpandedChange?: (expanded: boolean) => void;
  /* The resolved reviewer destination, shown under the path. */
  describeRoute?: (target: ReviewerTarget | null) => string;
}) {
  const endpoint = findEndpoint(endpoints, block.endpointKey);
  const route = describeRoute?.(block.target ?? null);
  const answers = OUTCOME_ROWS.filter((row) => block.allowedOutcomes.includes(row.value)).map((row) => row.label);
  const sees = [
    block.include.agentReasoning ? "Agent reasoning" : "",
    block.include.context ? "Request context" : "",
    block.include.previousMetadata ? "Earlier block results" : "",
  ].filter(Boolean);
  return (
    <BlockSummaryCard
      position={position}
      title={block.name || "Untitled webhook"}
      subtitle={<EndpointSummary block={block} endpoint={endpoint} />}
      enabled={block.enabled}
      onNavigate={onNavigate}
      expanded={expanded}
      onExpandedChange={onExpandedChange}
    >
      <SummaryField label="Webhook Endpoint">
        {endpoint ? (
          <p className="flex flex-wrap items-center gap-x-2 text-body text-ink">
            <Referenced
              reference={{ kind: "webhook_endpoint", key: endpoint.key, name: endpoint.name }}
              inline
            >
              {endpoint.name}
            </Referenced>
            <span className="font-mono text-[12px] text-ink-muted">{hostOf(endpoint.url)}</span>
            {!endpoint.active ? <Badge>Archived</Badge> : null}
          </p>
        ) : (
          <p className="text-body text-ink-muted">
            {block.endpointKey ? "This endpoint is no longer available." : "No endpoint selected."}
          </p>
        )}
      </SummaryField>

      <SummaryField label="The endpoint may answer">
        <SummaryChoices chosen={answers} empty="Nothing. Every answer goes to a human." />
      </SummaryField>

      <SummaryField label="The endpoint also sees">
        <SummaryChoices chosen={sees} empty="Only the action, its arguments, and the agent." />
      </SummaryField>

      <div className="flex flex-wrap gap-6">
        <SummaryField label="Attempt timeout">
          <p className="font-mono text-[12px] text-ink">{block.timeout}</p>
        </SummaryField>
        <SummaryField label="Attempts">
          <p className="font-mono text-[12px] text-ink">{block.maxAttempts}</p>
        </SummaryField>
      </div>

      <SummaryField label="Escalation path">
        <TargetBadge target={block.target ?? null} fallbackLabel="Pipeline default" className="self-start" />
        {route ? <p className="text-micro text-ink-muted">{route}</p> : null}
      </SummaryField>

      <SummaryField label="Reason">
        <SummaryText empty="No reason given.">{block.reason}</SummaryText>
      </SummaryField>
    </BlockSummaryCard>
  );
}
