import { useMemo, useState, type FormEvent } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Card } from "@/ds/components/card";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@/ds/components/dialog";
import { EmptyState } from "@/ds/components/empty-state";
import { TextField } from "@/ds/components/text-field";
import { ToggleGroup, ToggleGroupItem } from "@/ds/components/toggle-group";
import { Page } from "@/ds/components/page";
import { ChevronRightIcon, SettingsIcon } from "@/ds/icons";
import type { WebhookEndpointSummary } from "../types";
import { useRuntimeConfig } from "./use-runtime-config";
import { useTitle } from "./use-title";

/*
 * The webhook endpoint index: every endpoint key with its latest name,
 * host, and active revision, and the way to start a new one. Endpoints
 * are revisioned like escalation paths; a pipeline webhook block names a
 * key and deliveries go to the key's active revision.
 */

const ENDPOINT_KEY_PATTERN = /^[a-z0-9][a-z0-9._-]{0,62}$/;

/* The list's filter: the live (not archived) endpoints by default, which
 * includes an endpoint saved as a draft and never activated. An archived
 * endpoint keeps its revisions and secret but delivers nowhere until a
 * revision is active again. */
type EndpointFilter = "all" | "active" | "archived";

export function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

/* What the webhooks route loader delivers. */
export interface WebhooksPage {
  endpoints: WebhookEndpointSummary[];
  error: string;
}

export function WebhooksScreen({
  page,
  canManage,
  canRead,
}: {
  page: WebhooksPage;
  canManage: boolean;
  canRead: boolean;
}) {
  useTitle("Webhook endpoints");
  const { config } = useRuntimeConfig();
  const enabled = config?.webhook_enabled ?? true;
  const { endpoints, error } = page;
  const [creating, setCreating] = useState(false);
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<EndpointFilter>("active");
  const archivedCount = endpoints.filter((endpoint) => endpoint.archived_at).length;
  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return endpoints.filter((endpoint) => {
      if (filter === "active" && endpoint.archived_at) return false;
      if (filter === "archived" && !endpoint.archived_at) return false;
      if (!needle) return true;
      return (
        endpoint.name.toLowerCase().includes(needle) ||
        endpoint.endpoint_key.includes(needle) ||
        hostOf(endpoint.url).toLowerCase().includes(needle)
      );
    });
  }, [endpoints, filter, query]);

  if (!canRead) {
    return (
      <Page>
        <ScreenHeader />
        <EmptyState
          icon={<SettingsIcon width={18} height={18} />}
          title="Webhook endpoints are managed by administrators"
          description="You need the webhook.read permission to see endpoints, or pipeline.read to see the ones your pipelines use."
        />
      </Page>
    );
  }

  if (!enabled) {
    return (
      <Page>
        <ScreenHeader />
        <EmptyState
          icon={<SettingsIcon width={18} height={18} />}
          title="Webhook endpoints are not configured"
          description="Signing secrets are sealed under the deployment secrets key. Set WITHHUMAN_SECRETS_KEY on the server and the worker to enable endpoints and webhook blocks."
        />
      </Page>
    );
  }

  return (
    <Page>
      <div className="flex items-start justify-between gap-4">
        <ScreenHeader />
        {canManage ? (
          <Button variant="primary" onClick={() => setCreating(true)}>
            New webhook
          </Button>
        ) : null}
      </div>

      {error ? (
        <Callout tone="warn" title="Unable to load webhook endpoints">
          {error}
        </Callout>
      ) : endpoints.length === 0 ? (
        <EmptyState
          icon={<SettingsIcon width={18} height={18} />}
          title="No webhook endpoints yet"
          description="Add webhooks here to use in your pipelines."
          action={
            canManage ? (
              <Button variant="primary" onClick={() => setCreating(true)}>
                New webhook
              </Button>
            ) : undefined
          }
        />
      ) : (
        <>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <TextField
              aria-label="Search webhook endpoints"
              placeholder="Search by name, key, or host"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              className="w-[280px]"
            />
            <div className="flex items-center gap-4">
              <span className="text-label text-ink-muted">
                Showing {visible.length} of {endpoints.length}
              </span>
              <ToggleGroup
                aria-label="Filter webhook endpoints"
                value={filter}
                onValueChange={(value) => setFilter(value as EndpointFilter)}
              >
                <ToggleGroupItem value="all">All</ToggleGroupItem>
                <ToggleGroupItem value="active">Active</ToggleGroupItem>
                <ToggleGroupItem value="archived">
                  Archived{archivedCount > 0 ? ` (${archivedCount})` : ""}
                </ToggleGroupItem>
              </ToggleGroup>
            </div>
          </div>

          <Card>
            {visible.length === 0 ? (
              <EmptyState
                title="No webhook endpoints match"
                description={
                  filter === "archived"
                    ? "No archived endpoint matches. An archived endpoint keeps its revisions and secret until a revision is active again."
                    : "Try a different search, or another filter."
                }
              />
            ) : (
              <ul className="flex flex-col">
                {visible.map((endpoint) => (
                  <li key={endpoint.endpoint_key} className="border-b-hairline border-edge last:border-b-0">
                    <Link
                      to="/webhooks/$endpointKey"
                      params={{ endpointKey: endpoint.endpoint_key }}
                      className="flex items-center gap-3 px-5 py-3 transition-colors duration-100 hover:bg-fill-hover"
                    >
                      <div className="min-w-0 flex-1">
                        <p
                          className={
                            endpoint.archived_at
                              ? "truncate text-body font-medium text-ink-muted"
                              : "truncate text-body font-medium text-ink"
                          }
                        >
                          {endpoint.name}
                        </p>
                        <p className="truncate font-mono text-[11px] text-ink-muted">
                          {endpoint.endpoint_key} · {hostOf(endpoint.url)}
                        </p>
                      </div>
                      {endpoint.uses.length > 0 ? (
                        <Badge tone="accent">
                          {endpoint.uses.length === 1 ? "1 pipeline use" : `${endpoint.uses.length} pipeline uses`}
                        </Badge>
                      ) : null}
                      {endpoint.archived_at ? (
                        <Badge tone="warn">
                          Archived {new Date(endpoint.archived_at).toLocaleDateString()}
                        </Badge>
                      ) : endpoint.active_revision ? (
                        <Badge tone="approve">Revision {endpoint.active_revision} active</Badge>
                      ) : (
                        <Badge>Draft</Badge>
                      )}
                      <span className="text-ink-muted">
                        <ChevronRightIcon width={14} height={14} />
                      </span>
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </Card>
        </>
      )}

      {canManage ? (
        <NewEndpointDialog open={creating} onOpenChange={setCreating} existing={endpoints} />
      ) : null}
    </Page>
  );
}

function ScreenHeader() {
  return (
    <div>
      <h1 className="text-title-lg text-ink">Webhook endpoints</h1>
      <p className="mt-1.5 max-w-[560px] text-body text-ink-muted">
        Configure webhooks to use in your approval pipelines. These webhooks can be called to
        decide whether to approve or deny agent approval requests.
      </p>
    </div>
  );
}

function NewEndpointDialog({
  open,
  onOpenChange,
  existing,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  existing: WebhookEndpointSummary[];
}) {
  const navigate = useNavigate();
  const [key, setKey] = useState("");
  const valid = ENDPOINT_KEY_PATTERN.test(key);
  const taken = existing.some((endpoint) => endpoint.endpoint_key === key);

  function submit(event: FormEvent) {
    event.preventDefault();
    if (!valid || taken) return;
    onOpenChange(false);
    void navigate({ to: "/webhooks/$endpointKey", params: { endpointKey: key } });
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form onSubmit={submit} className="flex min-h-0 flex-col">
          <DialogTitle>New webhook endpoint</DialogTitle>
          <DialogDescription>
            Name the webhook, it must be globally unique. Try to make it descriptive.
          </DialogDescription>
          <DialogBody>
            <TextField
              label="Key"
              mono
              value={key}
              onChange={(event) => setKey(event.target.value.trim().toLowerCase())}
              placeholder="fraud-service"
              autoFocus
              description={
                taken
                  ? "An endpoint with this key already exists."
                  : "Lowercase letters, digits, dots, dashes, and underscores."
              }
            />
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" type="button" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={!valid || taken}>
              Create endpoint
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
