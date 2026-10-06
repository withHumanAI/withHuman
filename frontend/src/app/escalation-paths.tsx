import { useMemo, useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Card } from "@/ds/components/card";
import { EmptyState } from "@/ds/components/empty-state";
import { Page } from "@/ds/components/page";
import { TextField } from "@/ds/components/text-field";
import { ToggleGroup, ToggleGroupItem } from "@/ds/components/toggle-group";
import { ChevronRightIcon, ProgressIcon } from "@/ds/icons";
import type { EscalationPathSummary } from "../types";
import { useTitle } from "./use-title";

/*
 * The escalation path index: every path key with its name and active
 * revision, archived ones included behind a filter, and the way to start
 * a new one (the editor itself, opened on an empty draft). A pipeline's
 * human exit names one of these; the path decides who is asked and when.
 */

/* The list's filter: the active (not archived) paths by default. */
type PathFilter = "all" | "active" | "archived";

/* What the escalation paths route loader delivers. */
export interface EscalationPathsPage {
  paths: EscalationPathSummary[];
  error: string;
}

export function EscalationPathsScreen({
  page,
  canWrite,
}: {
  page: EscalationPathsPage;
  canWrite: boolean;
}) {
  useTitle("Escalation paths");
  const navigate = useNavigate();
  const { paths, error } = page;
  const startNew = () => void navigate({ to: "/escalation-paths/new" });
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<PathFilter>("active");
  const archivedCount = paths.filter((path) => path.archived_at).length;
  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return paths.filter((path) => {
      if (filter === "active" && path.archived_at) return false;
      if (filter === "archived" && !path.archived_at) return false;
      if (!needle) return true;
      return path.name.toLowerCase().includes(needle) || path.path_key.includes(needle);
    });
  }, [paths, filter, query]);

  return (
    <Page>
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-title-lg text-ink">Escalation paths</h1>
          <p className="mt-1.5 max-w-[560px] text-body text-ink-muted">
            Escalation paths define which specific humans get notified to approve requests.
          </p>
        </div>
        {canWrite ? (
          <Button variant="primary" onClick={startNew}>
            New path
          </Button>
        ) : null}
      </div>

      {error ? (
        <Callout tone="warn" title="Unable to load escalation paths">
          {error}
        </Callout>
      ) : paths.length === 0 ? (
        <EmptyState
          icon={<ProgressIcon width={18} height={18} />}
          title="No escalation paths yet"
          description="Add an escalation path to choose who gets notified."
          action={
            canWrite ? (
              <Button variant="primary" onClick={startNew}>
                New path
              </Button>
            ) : undefined
          }
        />
      ) : (
        <>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <TextField
              aria-label="Search escalation paths"
              placeholder="Search by name or key"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              className="w-[280px]"
            />
            <div className="flex items-center gap-4">
              <span className="text-label text-ink-muted">
                Showing {visible.length} of {paths.length}
              </span>
              <ToggleGroup
                aria-label="Filter escalation paths"
                value={filter}
                onValueChange={(value) => setFilter(value as PathFilter)}
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
                title="No escalation paths match"
                description={
                  filter === "archived"
                    ? "Try a different search."
                    : "Try a different search or filter."
                }
              />
            ) : (
              <ul className="flex flex-col">
                {visible.map((path) => (
                  <li key={path.path_key} className="border-b-hairline border-edge last:border-b-0">
                    <Link
                      to="/escalation-paths/$pathKey"
                      params={{ pathKey: path.path_key }}
                      className="flex items-center gap-3 px-5 py-3 transition-colors duration-100 hover:bg-fill-hover"
                    >
                      <div className="min-w-0 flex-1">
                        <p
                          className={
                            path.archived_at
                              ? "text-body font-medium text-ink-muted"
                              : "text-body font-medium text-ink"
                          }
                        >
                          {path.name}
                        </p>
                        <p className="font-mono text-[11px] text-ink-muted">{path.path_key}</p>
                      </div>
                      {path.archived_at ? (
                        <Badge tone="warn">
                          Archived {new Date(path.archived_at).toLocaleDateString()}
                        </Badge>
                      ) : path.active_revision ? (
                        <Badge tone="approve">Revision {path.active_revision} active</Badge>
                      ) : (
                        <Badge>No active revision</Badge>
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

    </Page>
  );
}
