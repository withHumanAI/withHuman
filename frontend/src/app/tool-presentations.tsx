import { useMemo, useState } from "react";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { ConfirmDialog } from "@/ds/components/confirm-dialog";
import { EmptyState } from "@/ds/components/empty-state";
import { SettingsPage } from "@/ds/components/settings-page";
import { SettingsSection } from "@/ds/components/settings-section";
import { Table, type TableColumn } from "@/ds/components/table";
import { TextField } from "@/ds/components/text-field";
import { useToast } from "@/ds/components/toast";
import { AgentIcon, ToolIcon } from "@/ds/icons";
import { api } from "../api";
import type { ToolPresentation, ToolPresentationBinding, ToolPresentationInput, ToolPresentationsPage } from "../types";
import { formatElapsed, plural } from "./format";
import { matchesBinding } from "./tool-presentation";
import { useTitle } from "./use-title";

/*
 * Settings: tool presentations. How a tool call's arguments are shown
 * to reviewers: which argument is an email body to render, which is a
 * command, which pair is a diff, and what the request is called. The
 * product ships a catalog; the organization's own entries layer over
 * it and always win. Nothing here changes what is sent to a human,
 * only how it reads.
 */

export interface ToolPresentationsPageData {
  page: ToolPresentationsPage | null;
  error: string;
}

export function loadToolPresentationsPage(): Promise<ToolPresentationsPageData> {
  return api.toolPresentations().then(
    (page) => ({ page, error: "" }),
    (reason: unknown) => ({
      page: null,
      error: reason instanceof Error ? reason.message : "Could not load tool presentations",
    }),
  );
}

const DESCRIPTION = "Choose how each tool's arguments are shown to reviewers.";

/* Where a binding applies, as badges: the runtime, the MCP server, or
 * nothing when it binds to the tool name alone. */
function BindingBadges({ binding }: { binding: ToolPresentationBinding }) {
  if (!binding.runtime && !binding.server) {
    return <span className="text-label text-ink-muted">Any</span>;
  }
  return (
    <span className="flex flex-wrap items-center gap-1">
      {binding.server ? (
        <Badge icon={<ToolIcon width={13} height={13} />} tone="neutral">
          {binding.server}
        </Badge>
      ) : null}
      {binding.runtime ? (
        <Badge icon={<AgentIcon width={13} height={13} />} tone="neutral">
          {binding.runtime}
        </Badge>
      ) : null}
    </span>
  );
}

function ToolCell({ binding, title }: { binding: ToolPresentationBinding; title?: string }) {
  return (
    <div className="min-w-0 py-0.5">
      <p className="truncate font-mono text-[12px] text-ink">{binding.tool}</p>
      <p className="truncate text-label font-normal text-ink-muted">{title || "Untitled"}</p>
    </div>
  );
}

function filterEntries<Entry extends ToolPresentationInput>(entries: Entry[], query: string): Entry[] {
  const needle = query.trim().toLowerCase();
  if (!needle) return entries;
  return entries.filter(
    (entry) =>
      entry.tool.toLowerCase().includes(needle) ||
      (entry.runtime ?? "").toLowerCase().includes(needle) ||
      (entry.server ?? "").toLowerCase().includes(needle) ||
      (entry.spec.title ?? "").toLowerCase().includes(needle),
  );
}

export function ToolPresentationsScreen({
  data,
  canRead,
  canEdit,
}: {
  data: ToolPresentationsPageData;
  canRead: boolean;
  canEdit: boolean;
}) {
  useTitle("Tool presentations");
  const router = useRouter();
  const navigate = useNavigate();
  const toast = useToast();
  const [query, setQuery] = useState("");
  const [deleting, setDeleting] = useState<ToolPresentation | null>(null);
  const { page, error } = data;
  const own = useMemo(() => filterEntries(page?.presentations ?? [], query), [page, query]);
  const builtIn = useMemo(() => filterEntries(page?.built_in ?? [], query), [page, query]);
  /* A shipped entry an organization entry shadows for every call it
   * would have presented: the same tool with a binding at least as
   * wide. */
  const overridden = useMemo(() => {
    const own = page?.presentations ?? [];
    return new Set(
      (page?.built_in ?? [])
        .filter((entry) => own.some((mine) => matchesBinding(mine, entry)))
        .map((entry) => `${entry.runtime ?? ""}/${entry.server ?? ""}/${entry.tool}`),
    );
  }, [page]);

  async function remove(target: ToolPresentation) {
    try {
      await api.deleteToolPresentation(target.id);
      toast.add({ type: "approve", title: `Removed the presentation for ${target.tool}` });
      await router.invalidate();
    } catch (reason) {
      toast.add({
        type: "deny",
        title: "Could not remove the presentation",
        description: reason instanceof Error ? reason.message : undefined,
      });
    }
  }

  const ownColumns: TableColumn<ToolPresentation>[] = [
    { id: "tool", label: "Tool", grow: true, cell: (entry) => <ToolCell binding={entry} title={entry.spec.title} /> },
    { id: "binding", label: "Applies to", info: "The runtime and MCP server this presentation applies to.", docsLinks: [{ href: "https://withhuman.ai/docs/web-app/tool-presentations#matching-tools", label: "Matching tools and runtimes" }], width: 200, cell: (entry) => <BindingBadges binding={entry} /> },
    { id: "fields", label: "Fields", width: 72, align: "right", cell: (entry) => <span className="text-label text-ink-muted">{entry.spec.fields.length}</span> },
    { id: "updated", label: "Updated", width: 96, cell: (entry) => <span className="text-label text-ink-muted">{formatElapsed(entry.updated_at)} ago</span> },
  ];

  const builtInColumns: TableColumn<ToolPresentationInput>[] = [
    { id: "tool", label: "Tool", grow: true, cell: (entry) => <ToolCell binding={entry} title={entry.spec.title} /> },
    { id: "binding", label: "Applies to", width: 200, cell: (entry) => <BindingBadges binding={entry} /> },
    {
      id: "state",
      label: "State",
      width: 110,
      cell: (entry) =>
        overridden.has(`${entry.runtime ?? ""}/${entry.server ?? ""}/${entry.tool}`) ? (
          <Badge tone="neutral">Overridden</Badge>
        ) : (
          <Badge tone="approve">In use</Badge>
        ),
    },
  ];

  const newButton = (
    <Button variant="primary" onClick={() => void navigate({ to: "/settings/tool-presentations/new", search: {} })}>
      New presentation
    </Button>
  );

  if (!canRead) {
    return (
      <SettingsPage title="Tool presentations" description={DESCRIPTION}>
        <Callout tone="warn" title="You cannot read this page">
          You need the organization.read permission to view this page.
        </Callout>
      </SettingsPage>
    );
  }

  return (
    <SettingsPage title="Tool presentations" description={DESCRIPTION} action={canEdit ? newButton : undefined}>
      {error ? (
        <Callout tone="warn" title="Unable to load tool presentations">
          {error}
        </Callout>
      ) : null}
      {page ? (
        <>
          <TextField
            aria-label="Search presentations by tool, server, runtime, or title"
            placeholder="Search by tool, server, runtime, or title"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          <SettingsSection
            title="Your presentations"
            description="Presentations your organization added."
            inset="none"
          >
            {page.presentations.length === 0 ? (
              <EmptyState
                icon={<ToolIcon width={18} height={18} />}
                title="No presentations of your own yet"
                description="Add a presentation to change how a tool's arguments are shown."
                action={canEdit ? <Button onClick={() => void navigate({ to: "/settings/tool-presentations/new", search: {} })}>New presentation</Button> : undefined}
              />
            ) : own.length === 0 ? (
              <EmptyState title="No presentations match" description="Try a different search." />
            ) : (
              <div className="overflow-x-auto">
                <Table
                  aria-label="Your tool presentations"
                  items={own}
                  getId={(entry) => entry.id}
                  selected={new Set()}
                  onSelectedChange={() => undefined}
                  selectable={false}
                  columns={ownColumns}
                  rowLabel={(entry) => entry.tool}
                  onOpen={(entry) => void navigate({ to: "/settings/tool-presentations/$presentationId", params: { presentationId: entry.id } })}
                  rowActions={(entry) => [
                    { id: "delete", label: "Remove", danger: true, disabled: !canEdit, action: () => setDeleting(entry) },
                  ]}
                  className="min-w-[560px]"
                />
              </div>
            )}
          </SettingsSection>
          <SettingsSection
            title="Built-in catalog"
            description={`Customize one of these ${plural(page.built_in.length, "entry").replace("entrys", "entries")} to start your own.`}
            inset="none"
          >
            {builtIn.length === 0 ? (
              <EmptyState title="No built-in entries match" description="Try a different search." />
            ) : (
              <div className="overflow-x-auto">
                <Table
                  aria-label="Built-in tool presentations"
                  items={builtIn}
                  getId={(entry) => `${entry.runtime ?? ""}/${entry.server ?? ""}/${entry.tool}`}
                  selected={new Set()}
                  onSelectedChange={() => undefined}
                  selectable={false}
                  columns={builtInColumns}
                  rowLabel={(entry) => entry.tool}
                  onOpen={(entry) =>
                    void navigate({
                      to: "/settings/tool-presentations/new",
                      search: { tool: entry.tool, runtime: entry.runtime, server: entry.server },
                    })
                  }
                  rowActions={(entry) => [
                    {
                      id: "customize",
                      label: "Customize",
                      disabled: !canEdit,
                      action: () =>
                        void navigate({
                          to: "/settings/tool-presentations/new",
                          search: { tool: entry.tool, runtime: entry.runtime, server: entry.server },
                        }),
                    },
                  ]}
                  className="min-w-[560px]"
                />
              </div>
            )}
          </SettingsSection>
        </>
      ) : null}
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open) setDeleting(null);
        }}
        title={deleting ? `Remove the presentation for ${deleting.tool}?` : "Remove presentation?"}
        description="Requests for this tool will be shown the default way."
        confirmLabel="Remove presentation"
        onConfirm={() => (deleting ? remove(deleting) : undefined)}
      />
    </SettingsPage>
  );
}
