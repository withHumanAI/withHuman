import { useState } from "react";
import { cn } from "@/ds/lib/cn";
import { Link, type LinkProps, useRouter } from "@tanstack/react-router";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { Checkbox } from "@/ds/components/checkbox";
import { ConfirmDialog } from "@/ds/components/confirm-dialog";
import { EmptyState } from "@/ds/components/empty-state";
import { PageBackLink } from "@/ds/components/page-header";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  MultiSelectTrigger,
} from "@/ds/components/select";
import { SettingsPage } from "@/ds/components/settings-page";
import { SettingsSection } from "@/ds/components/settings-section";
import { Table, type TableColumn } from "@/ds/components/table";
import { TextField } from "@/ds/components/text-field";
import { useToast } from "@/ds/components/toast";
import { UnsavedGuard } from "@/ds/components/unsaved-guard";
import { api } from "../api";
import type {
  PermissionDescriptor,
  PermissionPoliciesPage,
  PermissionPolicy,
  PermissionPolicyCollection,
  PermissionPolicyInput,
  PermissionResource,
  Principal,
  ScopeKind,
} from "../types";
import { areaLabel } from "./permission-labels";
import { can } from "./authorization";
import { usePrincipal } from "./principal";
import { ReferenceChip } from "./reference-chip";
import { useCustomPermissions } from "./roles";
import { refreshSession } from "./session-store";
import { useTitle } from "./use-title";

export const SCOPE_LABELS: Record<ScopeKind, string> = {
  organization: "Organization",
  team: "Teams",
  agent: "Agents",
  pipeline: "Pipelines",
};
export type PermissionPolicyDraft = Omit<
  PermissionPolicyInput,
  "scope_kind"
> & { scope_kind: ScopeKind | "" };
export const emptyPolicy = (): PermissionPolicyDraft => ({
  scope_kind: "",
  resource_ids: [],
  permissions: [],
});

export function appliesAt(
  permission: PermissionDescriptor,
  scope: ScopeKind | "",
): boolean {
  return (
    !!scope &&
    (scope === "organization" ||
      permission.resource === "scope" ||
      permission.resource === scope)
  );
}
export function mayDelegate(
  principal: Principal,
  permission: string,
  draft: PermissionPolicyDraft,
): boolean {
  if (!draft.scope_kind) return false;
  if (draft.scope_kind === "organization")
    return (
      can(principal, permission) && can(principal, "permission_policy.write")
    );
  return (
    draft.resource_ids.length > 0 &&
    draft.resource_ids.every(
      (id) =>
        can(principal, permission, {
          kind: draft.scope_kind as Exclude<ScopeKind, "organization">,
          id,
        }) &&
        can(principal, "permission_policy.write", {
          kind: draft.scope_kind as Exclude<ScopeKind, "organization">,
          id,
        }),
    )
  );
}
export function policyValid(
  draft: PermissionPolicyDraft,
): draft is PermissionPolicyInput {
  return (
    !!draft.scope_kind &&
    draft.permissions.length > 0 &&
    (draft.scope_kind === "organization" || draft.resource_ids.length > 0)
  );
}

// Shared by policy editing and invitation creation. Scope compatibility is
// registry driven; delegation is checked for every selected resource.
export function PermissionPolicyFields({
  value,
  onChange,
  permissions,
  resources,
  disabled = false,
  holder = "human",
  className,
}: {
  value: PermissionPolicyDraft;
  onChange: (value: PermissionPolicyDraft) => void;
  permissions: PermissionDescriptor[];
  resources: PermissionResource[];
  disabled?: boolean;
  holder?: "human" | "organization_api_key";
  /* Given flex-1 inside a fixed-height column, the empty state centres
   * in the space left under the scope pickers. */
  className?: string;
}) {
  const principal = usePrincipal();
  const [query, setQuery] = useState("");
  const options = resources.filter((r) => r.kind === value.scope_kind);
  const scopeOptions = Object.keys(SCOPE_LABELS) as ScopeKind[];
  const needsResources =
    !!value.scope_kind &&
    value.scope_kind !== "organization" &&
    value.resource_ids.length === 0;
  const visible = permissions.filter(
    (p) =>
      p.holders.includes(holder) &&
      appliesAt(p, value.scope_kind) &&
      `${p.key} ${p.description}`.toLowerCase().includes(query.toLowerCase()),
  );
  const groups = [...new Set(visible.map((p) => p.area))];
  function toggle(keys: string[], checked: boolean) {
    onChange({
      ...value,
      permissions: checked
        ? [...new Set([...value.permissions, ...keys])]
        : value.permissions.filter((key) => !keys.includes(key)),
    });
  }
  return (
    <div className={cn("flex flex-col gap-4", className)}>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-[1fr_2fr]">
        <div className="flex min-w-0 flex-col gap-1.5">
          <span className="text-label text-ink">Scope</span>
          <Select
            items={scopeOptions}
            value={value.scope_kind || null}
            disabled={disabled}
            itemToStringLabel={(item) => SCOPE_LABELS[item as ScopeKind]}
            onValueChange={(next) => {
              if (next) {
                onChange({
                  scope_kind: next as ScopeKind,
                  resource_ids: [],
                  permissions: [],
                });
                setQuery("");
              }
            }}
          >
            <SelectTrigger
              aria-label="Scope"
              placeholder="Select a scope"
              className="w-full min-w-0"
            />
            <SelectContent<ScopeKind>>
              {(item) => (
                <SelectItem key={item} value={item}>
                  {SCOPE_LABELS[item]}
                </SelectItem>
              )}
            </SelectContent>
          </Select>
        </div>
        <div className="flex min-w-0 flex-col gap-1.5">
          <span className="text-label text-ink">Applies to</span>
          {value.scope_kind === "organization" || !value.scope_kind ? (
            <TextField
              aria-label="Applies to"
              disabled
              value={
                value.scope_kind
                  ? "Entire organization"
                  : "Select a scope first"
              }
            />
          ) : (
            <>
              <Select
                multiple
                items={options.map((r) => r.id)}
                value={value.resource_ids}
                disabled={disabled}
                itemToStringLabel={(item) =>
                  options.find((r) => r.id === item)?.name ?? String(item)
                }
                onValueChange={(next) =>
                  onChange({ ...value, resource_ids: next as string[] })
                }
              >
                <MultiSelectTrigger<string>
                  display="pills"
                  aria-label="Applies to"
                  placeholder={`Select ${SCOPE_LABELS[value.scope_kind].toLowerCase()}`}
                  itemLabel={(id) =>
                    options.find((r) => r.id === id)?.name ?? id
                  }
                  className="w-full min-w-0"
                />
                <SelectContent<string> filterPlaceholder="Search resources">
                  {(id) => (
                    <SelectItem key={id} value={id}>
                      {options.find((r) => r.id === id)?.name ?? id}
                    </SelectItem>
                  )}
                </SelectContent>
              </Select>
              {options.length === 0 ? (
                <p className="text-label text-ink-muted">
                  There are no resources you can grant access to.
                </p>
              ) : null}
            </>
          )}
        </div>
      </div>
      {!value.scope_kind ? (
        <EmptyState
          className="flex-1 justify-center border-t-hairline border-edge px-0 py-6"
          title="Select a scope"
          description="Choose a scope to see its available permissions."
        />
      ) : (
        <>
          <Callout tone="accent">
            {value.scope_kind === "organization" ? (
              "These permissions apply across the whole organization."
            ) : (
              <>
                {needsResources ? (
                  <span className="font-medium text-ink">
                    Select {SCOPE_LABELS[value.scope_kind].toLowerCase()} above
                    to enable permissions.{" "}
                  </span>
                ) : null}
                These permissions apply only to the selected{" "}
                {SCOPE_LABELS[value.scope_kind].toLowerCase()}.
              </>
            )}
          </Callout>
          <TextField
            aria-label="Search permissions"
            placeholder="Search permissions"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          {groups.length === 0 ? (
            <p className="text-body text-ink-muted">
              No permissions match this search.
            </p>
          ) : null}
          {groups.map((group) => {
            const entries = visible.filter((p) => p.area === group);
            const allowed = entries
              .filter((p) => mayDelegate(principal, p.key, value))
              .map((p) => p.key);
            const selected = allowed.filter((key) =>
              value.permissions.includes(key),
            ).length;
            return (
              <div
                key={group}
                className="flex flex-col border-t-hairline border-edge pt-3"
              >
                <div className="flex items-center justify-between gap-3 pb-2 pl-[27px]">
                  <h3 className="text-label font-medium leading-5 text-ink">
                    {areaLabel(group)}
                  </h3>
                  <Checkbox
                    aria-label={`Select all ${group} permissions`}
                    label="Select all"
                    checked={allowed.length > 0 && selected === allowed.length}
                    indeterminate={selected > 0 && selected < allowed.length}
                    disabled={disabled || allowed.length === 0}
                    onCheckedChange={(checked) => toggle(allowed, checked)}
                  />
                </div>
                {entries.map((p) => {
                  const allowed = mayDelegate(principal, p.key, value);
                  return (
                    <div
                      key={p.key}
                      className="grid grid-cols-[15px_minmax(0,1fr)_auto] items-start gap-x-3 gap-y-0.5 py-2.5"
                    >
                      <span className="col-start-1 row-start-1 flex h-5 items-center">
                        <Checkbox
                          aria-label={p.description}
                          checked={value.permissions.includes(p.key)}
                          disabled={
                            disabled ||
                            (!allowed && !value.permissions.includes(p.key))
                          }
                          onCheckedChange={(checked) =>
                            toggle([p.key], checked)
                          }
                        />
                      </span>
                      <p className="col-start-2 row-start-1 text-body leading-5 text-ink [overflow-wrap:anywhere]">
                        {p.description}
                      </p>
                      {p.dangerous ? (
                        <Badge
                          tone="warn"
                          className="col-start-3 row-start-1 h-5 px-0 leading-5"
                        >
                          Sensitive
                        </Badge>
                      ) : null}
                      <div className="col-start-2 row-start-2 flex min-w-0 flex-col gap-1">
                        <code className="block text-micro font-normal leading-4 text-ink-muted [overflow-wrap:anywhere]">
                          {p.key}
                        </code>
                        {!allowed && !needsResources ? (
                          <p className="text-label text-ink-muted">
                            You cannot grant this permission at this scope.
                          </p>
                        ) : null}
                      </div>
                    </div>
                  );
                })}
              </div>
            );
          })}
        </>
      )}
    </div>
  );
}

/* A team by id, an agent by slug, a pipeline by key (its id under this
 * scope kind); the organization scope never lists resources. */
function ResourceLabel({ resource }: { resource: PermissionResource }) {
  if (resource.kind === "team")
    return (
      <ReferenceChip
        reference={{ kind: "team", id: resource.id, name: resource.name }}
      />
    );
  if (resource.kind === "agent")
    return (
      <ReferenceChip
        reference={{ kind: "agent", slug: resource.id, name: resource.name }}
      />
    );
  if (resource.kind === "pipeline")
    return (
      <ReferenceChip
        reference={{ kind: "pipeline", pipelineKey: resource.id, name: resource.name }}
      />
    );
  return <Badge>{resource.name}</Badge>;
}

/* Whose policies the screen edits, fixed by the route: the collection
 * the principal is addressed in under /api/v1, whose permissions the
 * picker offers (a person's, or what an organization key may carry),
 * the sentence under the title, and the list the back link returns to.
 * Core knows members and teams; an edition describes its own. */
export interface PermissionPolicySubject {
  collection: PermissionPolicyCollection;
  holder: "human" | "organization_api_key";
  description: string;
  back: { to: LinkProps["to"]; label: string };
}

export const MEMBER_POLICIES: PermissionPolicySubject = {
  collection: "members",
  holder: "human",
  description: "Direct permissions and access inherited from teams.",
  back: { to: "/settings/members", label: "Members" },
};

export const TEAM_POLICIES: PermissionPolicySubject = {
  collection: "teams",
  holder: "human",
  description: "Every team member gets these permissions.",
  back: { to: "/settings/teams", label: "Teams" },
};

export function PermissionPoliciesScreen({
  page,
  error,
  subject,
  principalId,
}: {
  page: PermissionPoliciesPage | null;
  error: string;
  subject: PermissionPolicySubject;
  principalId: string;
}) {
  useTitle(page ? `Permissions for ${page.name}` : "Permissions");
  const router = useRouter();
  const toast = useToast();
  const principal = usePrincipal();
  /* Without custom permissions the edition offers predefined roles alone:
   * the page still explains what someone holds but edits nothing. */
  const customPermissions = useCustomPermissions();
  const [draft, setDraft] = useState<PermissionPolicyDraft | null>(null);
  const [existing, setExisting] = useState<PermissionPolicy | undefined>();
  const [baseline, setBaseline] = useState("");
  const [command, setCommand] = useState("");
  const [saving, setSaving] = useState(false);
  const [failure, setFailure] = useState("");
  const [query, setQuery] = useState("");
  const [removing, setRemoving] = useState<PermissionPolicy | null>(null);
  const [discarding, setDiscarding] = useState(false);
  const dirty = draft !== null && JSON.stringify(draft) !== baseline;
  function start(policy?: PermissionPolicy) {
    const initial = policy
      ? {
          scope_kind: policy.scope_kind,
          resource_ids: [...policy.resource_ids],
          permissions: [...policy.permissions],
        }
      : emptyPolicy();
    setDraft(initial);
    setExisting(policy);
    setBaseline(JSON.stringify(initial));
    setCommand(crypto.randomUUID());
    setFailure("");
  }
  function update(value: PermissionPolicyDraft) {
    setDraft(value);
    setCommand(crypto.randomUUID());
    setFailure("");
  }
  function close() {
    setDraft(null);
    setFailure("");
  }
  async function save() {
    if (!draft || !policyValid(draft))
      throw new Error(
        "Choose a scope, resources, and permissions before saving.",
      );
    setSaving(true);
    setFailure("");
    try {
      await api.savePermissionPolicy(
        subject.collection,
        principalId,
        draft,
        command,
        existing,
      );
      close();
      await refreshSession();
      await router.invalidate();
      toast.add({ type: "approve", title: "Permissions updated" });
    } catch (reason) {
      setFailure(
        reason instanceof Error ? reason.message : "Unable to save permissions",
      );
      throw reason;
    } finally {
      setSaving(false);
    }
  }
  const columns: TableColumn<PermissionPolicy>[] = [
    {
      id: "scope",
      label: "Scope",
      width: 125,
      cell: (p) => SCOPE_LABELS[p.scope_kind],
    },
    {
      id: "resources",
      label: "Applies to",
      width: 190,
      cell: (p) =>
        p.scope_kind === "organization" ? (
          "Entire organization"
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {p.resources.map((r) => (
              <ResourceLabel key={r.id} resource={r} />
            ))}
          </div>
        ),
    },
    {
      id: "permissions",
      label: "Permissions",
      grow: true,
      cell: (p) => (
        <div className="flex flex-col gap-1">
          {p.permissions.slice(0, 3).map((key) => (
            <span key={key} className="text-body">
              {page?.permissions.find((d) => d.key === key)?.description ?? key}
            </span>
          ))}
          {p.permissions.length > 3 ? (
            <details>
              <summary className="cursor-pointer text-label text-ink-muted">
                {p.permissions.length - 3} more permissions
              </summary>
              {p.permissions.slice(3).map((key) => (
                <p key={key} className="text-body">
                  {page?.permissions.find((d) => d.key === key)?.description ??
                    key}
                </p>
              ))}
            </details>
          ) : null}
          {p.via_team_id ? (
            <Link
              to="/settings/permissions/$kind/$principalId"
              params={{ kind: "teams", principalId: p.via_team_id }}
              className="text-label text-ink-muted underline"
            >
              Inherited from {p.via_team_name}
            </Link>
          ) : p.read_only_reason ? (
            <p className="text-label text-ink-muted">{p.read_only_reason}</p>
          ) : null}
        </div>
      ),
    },
  ];
  const filtered =
    page?.policies.filter((p) =>
      [
        SCOPE_LABELS[p.scope_kind],
        ...p.resources.map((r) => r.name),
        ...p.permissions.flatMap((key) => [
          key,
          page.permissions.find((d) => d.key === key)?.description ?? "",
        ]),
      ]
        .join(" ")
        .toLowerCase()
        .includes(query.toLowerCase()),
    ) ?? [];
  return (
    <SettingsPage
      title={
        draft
          ? `${existing ? "Edit" : "Add"} permission policy`
          : `Permissions${page ? ` for ${page.name}` : ""}`
      }
      description={
        draft
          ? `Choose where ${page?.name} has access and what they can do.`
          : subject.description
      }
      back={<PageBackLink to={subject.back.to}>{subject.back.label}</PageBackLink>}
    >
      {!page ? (
        <Callout tone="warn" title="Unable to load permissions">
          {error}
          <Button variant="ghost" onClick={() => void router.invalidate()}>
            Retry
          </Button>
        </Callout>
      ) : (
        <>
          {page.is_owner ? (
            <Callout title="Organization ownership">
              Permission policies do not change ownership.
            </Callout>
          ) : null}
          {!customPermissions ? (
            <Callout title="Predefined roles">
              This deployment offers predefined roles only. Change a
              member's role from Members.
            </Callout>
          ) : page.read_only_reason ? (
            <Callout>{page.read_only_reason}</Callout>
          ) : null}
          {failure ? (
            <Callout tone="warn" title="Change could not be saved">
              {failure}
            </Callout>
          ) : null}
          {draft ? (
            <div className="flex flex-col gap-3">
              <SettingsSection inset="none">
                <div className="p-4">
                  <PermissionPolicyFields
                    value={draft}
                    onChange={update}
                    permissions={page.permissions}
                    resources={page.resources}
                    disabled={saving}
                    holder={subject.holder}
                  />
                </div>
              </SettingsSection>
              {subject.collection === "teams" && principal.team_ids?.includes(principalId) ? (
                <Callout>
                  This change also affects your own access.
                </Callout>
              ) : null}
              <div className="flex flex-wrap items-center justify-between gap-3">
                <span className="text-body text-ink-muted">
                  {draft.permissions.length} permissions selected
                </span>
                <div className="flex gap-2">
                  <Button
                    variant="ghost"
                    disabled={saving}
                    onClick={() => (dirty ? setDiscarding(true) : close())}
                  >
                    Cancel
                  </Button>
                  <Button
                    variant="primary"
                    loading={saving}
                    disabled={
                      !policyValid(draft) ||
                      draft.permissions.some(
                        (key) => !mayDelegate(principal, key, draft),
                      )
                    }
                    onClick={() => void save().catch(() => undefined)}
                  >
                    Save policy
                  </Button>
                </div>
              </div>
              <UnsavedGuard
                dirty={dirty}
                saving={saving}
                onSave={save}
                onDiscard={close}
              />
            </div>
          ) : (
            <div className="flex flex-col gap-3">
              <div className="flex flex-wrap items-center gap-3">
                <div className="min-w-0 flex-1 basis-[240px]">
                  <TextField
                    aria-label="Search permission policies"
                    placeholder="Search policies"
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                  />
                </div>
                {page.can_create && customPermissions ? (
                  <Button variant="primary" onClick={() => start()}>
                    Add permission policy
                  </Button>
                ) : null}
              </div>
              <SettingsSection id="permission-policies" inset="none">
                {filtered.length ? (
                  <Table<PermissionPolicy>
                    aria-label="Permission policies"
                    items={filtered}
                    selectable={false}
                    selected={new Set()}
                    onSelectedChange={() => undefined}
                    columns={columns}
                    getId={(p) => p.id}
                    rowLabel={(p) =>
                      `${SCOPE_LABELS[p.scope_kind]} permission policy`
                    }
                    rowActions={(p) =>
                      p.can_edit && customPermissions
                        ? [
                            {
                              id: "edit",
                              label: "Edit",
                              action: () => start(p),
                            },
                            {
                              id: "remove",
                              label: "Remove",
                              action: () => {
                                setRemoving(p);
                                setCommand(crypto.randomUUID());
                              },
                            },
                          ]
                        : []
                    }
                  />
                ) : (
                  <EmptyState
                    title={
                      query ? "No matching policies" : "No permission policies"
                    }
                    description={
                      query
                        ? "Try another resource or permission."
                        : "Add a policy to grant access."
                    }
                  />
                )}
              </SettingsSection>
            </div>
          )}
        </>
      )}
      {discarding ? (
        <ConfirmDialog
          open
          onOpenChange={setDiscarding}
          title="Discard these changes?"
          description="The saved permissions will stay as they are."
          confirmLabel="Discard changes"
          onConfirm={() => {
            close();
            setDiscarding(false);
          }}
        />
      ) : null}
      {removing ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => !open && setRemoving(null)}
          title="Remove this permission policy?"
          description="Access granted by other policies and teams will remain."
          confirmLabel="Remove policy"
          onConfirm={async () => {
            try {
              await api.deletePermissionPolicy(
                subject.collection,
                principalId,
                removing,
                command,
              );
              setRemoving(null);
              await refreshSession();
              await router.invalidate();
              toast.add({
                type: "approve",
                title: "Permission policy removed",
              });
            } catch (reason) {
              setFailure(
                reason instanceof Error
                  ? reason.message
                  : "Unable to remove policy",
              );
              setRemoving(null);
            }
          }}
        />
      ) : null}
    </SettingsPage>
  );
}
