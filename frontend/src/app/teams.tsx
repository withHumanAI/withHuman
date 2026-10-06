import { useEffect, useState, type FormEvent } from "react";
import { PermissionsSummary } from "./permission-summary";
import { useCustomPermissions } from "./roles";
import { Badge } from "@/ds/components/badge";
import { Referenced } from "@/ds/components/reference";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { ConfirmDialog } from "@/ds/components/confirm-dialog";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@/ds/components/dialog";
import { EmptyState } from "@/ds/components/empty-state";
import type { MenuAction } from "@/ds/components/menu";
import {
  MultiSelectTrigger,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
} from "@/ds/components/select";
import { SettingsPage } from "@/ds/components/settings-page";
import { SettingsSection } from "@/ds/components/settings-section";
import { Table, type TableColumn } from "@/ds/components/table";
import { TextField } from "@/ds/components/text-field";
import { useToast } from "@/ds/components/toast";
import { BroadcastIcon, PeopleIcon, PersonIcon, ShieldIcon } from "@/ds/icons";
import { Link, useNavigate, useRouter } from "@tanstack/react-router";
import { api } from "../api";
import type { Member, Team, TeamListStatus, TeamMember } from "../types";

import { useAccess } from "./authorization";
import { useTitle } from "./use-title";

const TEAM_STATUS_LABEL: Record<TeamListStatus, string> = {
  live: "Live teams",
  archived: "Archived teams",
  all: "All teams",
};

/* What the teams route loader delivers. */
export interface TeamsPage {
  teams: Team[];
  error: string;
}

export function TeamsScreen({
  page,
  status,
  onStatusChange,
  canRead,
  canWrite,
  canEditMembers,
}: {
  page: TeamsPage;
  status: TeamListStatus;
  onStatusChange: (status: TeamListStatus) => void;
  canRead: boolean;
  /* Create, rename, and archive teams. */
  canWrite: boolean;
  /* Change who is on a team. */
  canEditMembers: boolean;
}) {
  useTitle("Teams");
  const toast = useToast();
  const router = useRouter();
  const navigate = useNavigate();
  /* Granting a team a role is the same checked action as granting a
   * person one, so it gates on the same permission. */
  const { holdsAnywhere, isOwner } = useAccess();
  const customPermissions = useCustomPermissions();
  const canAssign = holdsAnywhere("permission_policy.write");
  const { teams, error } = page;
  const [query, setQuery] = useState("");
  const needle = query.trim().toLowerCase();
  const shown = teams.filter((team) =>
    team.name.toLowerCase().includes(needle) ||
    team.assignments.some((assignment) => assignment.permissions.some(key => key.includes(needle))),
  );
  const [creating, setCreating] = useState(false);
  const [renaming, setRenaming] = useState<Team | null>(null);

  const [archiving, setArchiving] = useState<Team | null>(null);
  if (!canRead) {
    return (
      <SettingsPage title={TITLE} description={DESCRIPTION}>
        <Callout tone="neutral" title="Teams are managed by administrators">
          You need the team.read permission to view this page.
        </Callout>
      </SettingsPage>
    );
  }

  /* Every change is followed by a reload of the loader's page, so the
   * list always shows what the server holds. */
  async function act(action: () => Promise<unknown>, failure: string) {
    try {
      await action();
      await router.invalidate();
      return true;
    } catch (reason) {
      toast.add({
        type: "deny",
        title: failure,
        description: reason instanceof Error ? reason.message : undefined,
      });
      return false;
    }
  }

  /* Plain array, not useMemo: the cells close over the dialog setters
   * and the list is small. */
  const columns: TableColumn<Team>[] = [
    {
      id: "name",
      label: "Team",
      grow: true,
      /* Archived and directory-managed teams carry badges beside the name. */
      cell: (team) => (
        <span className="flex min-w-0 items-center gap-2.5">
          <span className="truncate text-body font-medium text-ink">{team.name}</span>
          {team.archived_at ? <Badge>Archived</Badge> : null}
          {team.directory_managed ? (
            <Badge tone="accent" icon={<ShieldIcon width={14} height={14} />}>
              Directory
            </Badge>
          ) : null}
        </span>
      ),
    },
    ...(customPermissions
      ? [
          {
            id: "permissions",
            label: "Permissions",
            width: 130,
            cell: (team: Team) => <PermissionsSummary assignments={team.assignments} />,
          },
        ]
      : []),
    {
      id: "policy",
      label: "Escalation policy",
      width: 150,
      /* The badge opens the policy page; the row underneath opens the
       * team, so the click must not reach it. */
      cell: (team) => (
        <Referenced
          reference={{ kind: "team_escalation_policy", teamId: team.id, teamName: team.name }}
          focusable={false}
        >
          <Link
            to="/settings/teams/$teamId/escalation-policy"
            params={{ teamId: team.id }}
            onClick={(event) => event.stopPropagation()}
            className="inline-flex rounded-sm"
          >
            {team.escalation_policy.active_revision !== null ? (
              <Badge tone="approve" icon={<BroadcastIcon width={14} height={14} />}>
                Revision {team.escalation_policy.active_revision}
              </Badge>
            ) : team.escalation_policy.latest_revision !== null ? (
              <Badge icon={<BroadcastIcon width={14} height={14} />}>No active revision</Badge>
            ) : (
              <Badge icon={<BroadcastIcon width={14} height={14} />}>No policy</Badge>
            )}
          </Link>
        </Referenced>
      ),
    },
    {
      id: "members",
      label: "Members",
      width: 100,
      cell: (team) => (
        <span className="text-body text-ink-muted">
          {team.member_count === 1 ? "1 member" : `${team.member_count} members`}
        </span>
      ),
    },
  ];

  const teamActions = (team: Team): MenuAction[] => [
    {
      id: "escalation-policy",
      label: "Escalation policy",
      action: () =>
        void navigate({
          to: "/settings/teams/$teamId/escalation-policy",
          params: { teamId: team.id },
        }),
    },
    ...(customPermissions
      ? [{ id: "permissions", label: "Edit permissions", disabled: !canAssign || !!team.archived_at, action: () => void navigate({ to: "/settings/permissions/$kind/$principalId", params: { kind: "teams", principalId: team.id } }) }]
      : []),
    {
      id: "rename",
      label: "Rename",
      disabled: !canWrite || team.directory_managed || !!team.archived_at,
      action: () => setRenaming(team),
    },
    {
      id: "archive",
      label: "Archive",
      danger: true,
      disabled: !canWrite || team.directory_managed || !!team.archived_at,
      action: () => setArchiving(team),
    },
  ];

  return (
    <SettingsPage
      title={TITLE}
      description={DESCRIPTION}
      action={
        canWrite ? (
          <Button variant="primary" onClick={() => setCreating(true)}>
            New team
          </Button>
        ) : undefined
      }
    >
      {error ? (
        <Callout tone="warn" title="Unable to load teams">
          {error}
        </Callout>
      ) : null}

      <SettingsSection
        title={TEAM_STATUS_LABEL[status]}
        description="Open a team to manage its members, permissions, and escalation policy."
        inset="none"
        toolbar={
          <>
            <div className="min-w-0 flex-1">
              <TextField
                aria-label="Search teams by name or permission"
                placeholder="Search teams by name or permission"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
              />
            </div>
            <Select
              items={["live", "archived", "all"] as TeamListStatus[]}
              value={status}
              onValueChange={(value) => value && onStatusChange(value as TeamListStatus)}
              itemToStringLabel={(value) => TEAM_STATUS_LABEL[value as TeamListStatus]}
            >
              <SelectTrigger aria-label="Team status" className="w-[180px]" />
              <SelectContent<TeamListStatus>>
                {(value) => (
                  <SelectItem key={value} value={value}>
                    {TEAM_STATUS_LABEL[value]}
                  </SelectItem>
                )}
              </SelectContent>
            </Select>
          </>
        }
      >
        {shown.length === 0 && needle ? (
          <EmptyState
            icon={<PeopleIcon width={18} height={18} />}
            title="No teams match"
            description="Try a different search or filter."
            action={<Button variant="neutral" onClick={() => setQuery("")}>Clear search</Button>}
          />
        ) : teams.length === 0 ? (
          <EmptyState
            icon={<PeopleIcon width={18} height={18} />}
            title={status === "archived" ? "No archived teams" : "No teams yet"}
            description={status === "archived"
              ? "Archived teams will appear here."
              : "Create a team to notify a group of people together."}
          />
        ) : (
          <div className="overflow-x-auto">
            <Table
              aria-label="Teams"
              items={shown}
              getId={(team) => team.id}
              selected={new Set()}
              onSelectedChange={() => undefined}
              selectable={false}
              onOpen={(team) =>
                void navigate({ to: "/settings/teams/$teamId", params: { teamId: team.id } })
              }
              openOnClick
              openLabel="Open"
              rowLabel={(team) => team.name}
              rowActions={teamActions}
              columns={columns}
              className="min-w-[480px]"
            />
          </div>
        )}
      </SettingsSection>

      <NameDialog
        open={creating}
        title="New team"
        description="Give the team a name."
        confirmLabel="Create team"
        onOpenChange={setCreating}
        onSubmit={async (name) => {
          const ok = await act(() => api.createTeam(name), "Team creation failed");
          if (ok) setCreating(false);
        }}
      />
      {renaming ? (
        <NameDialog
          open
          title={`Rename ${renaming.name}`}
          description="Changing the name won't affect who gets notified."
          confirmLabel="Rename team"
          initialName={renaming.name}
          onOpenChange={(open) => !open && setRenaming(null)}
          onSubmit={async (name) => {
            const ok = await act(() => api.renameTeam(renaming.id, name), "Rename failed");
            if (ok) setRenaming(null);
          }}
        />
      ) : null}

      {archiving ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => !open && setArchiving(null)}
          title={`Archive ${archiving.name}?`}
          description="Its members lose the team's permissions. This can't be undone."
          confirmLabel="Archive team"
          typeToConfirm={archiving.member_count > 0 ? archiving.name : undefined}
          onConfirm={async () => {
            const ok = await act(() => api.archiveTeam(archiving.id), "Archiving failed");
            if (ok) setArchiving(null);
          }}
        />
      ) : null}
    </SettingsPage>
  );
}

const TITLE = "Teams";
const DESCRIPTION =
  "Group people so you can notify them and give them permissions together.";

export function NameDialog({
  open,
  title,
  description,
  confirmLabel,
  initialName = "",
  onOpenChange,
  onSubmit,
}: {
  open: boolean;
  title: string;
  description: string;
  confirmLabel: string;
  initialName?: string;
  onOpenChange: (open: boolean) => void;
  onSubmit: (name: string) => Promise<void>;
}) {
  const [name, setName] = useState(initialName);
  const [submitting, setSubmitting] = useState(false);
  /* Each opening starts from the initial name; a refused submit keeps
   * what was typed so it can be corrected. */
  useEffect(() => {
    if (open) setName(initialName);
  }, [open, initialName]);

  function close(next: boolean) {
    onOpenChange(next);
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    setSubmitting(true);
    try {
      await onSubmit(name);
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <form onSubmit={submit} className="flex min-h-0 flex-col">
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
          <DialogBody>
            <TextField
              label="Name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              autoFocus
              required
            />
          </DialogBody>
          <DialogFooter>
            <Button variant="ghost" type="button" onClick={() => close(false)}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" loading={submitting}>
              {confirmLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

interface MemberOption {
  id: string;
  name: string;
}

export function TeamMembersDialog({
  team,
  onClose,
  onSave,
}: {
  team: Team;
  onClose: () => void;
  onSave: (membershipIds: string[]) => Promise<void>;
}) {
  const toast = useToast();
  const [options, setOptions] = useState<MemberOption[]>([]);
  const [selected, setSelected] = useState<MemberOption[]>([]);
  const [directoryMembers, setDirectoryMembers] = useState<TeamMember[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const [memberResult, teamResult] = await Promise.all([
          api.members(),
          api.getTeam(team.id),
        ]);
        if (cancelled) return;
        const roster = memberResult.members
          .filter((member: Member) => member.status === "active")
          .map((member: Member) => ({
            id: member.id,
            name: member.display_name || member.email,
          }));
        setOptions(roster);
        setSelected(
          teamResult.members
            .filter((member) => member.sources.includes("manual"))
            .map(
              (member) =>
                roster.find((option: MemberOption) => option.id === member.membership_id) ?? {
                  id: member.membership_id,
                  name: member.display_name || member.email,
                },
            ),
        );
        setDirectoryMembers(
          teamResult.members.filter((member) => member.sources.includes("directory")),
        );
      } catch (reason) {
        toast.add({
          type: "deny",
          title: "Unable to load the team",
          description: reason instanceof Error ? reason.message : undefined,
        });
        onClose();
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [team.id, toast, onClose]);

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogTitle>Members of {team.name}</DialogTitle>
        <DialogDescription>
          Choose who belongs to this team.
        </DialogDescription>
        <DialogBody className="flex flex-col gap-4">
          {loading ? null : (
            <>
              <Select
                multiple
                items={options}
                itemToStringLabel={(item) => (item as MemberOption).name}
                value={selected}
                onValueChange={(next) => setSelected(next as MemberOption[])}
              >
                <MultiSelectTrigger<MemberOption>
                  placeholder="Add members"
                  itemLabel={(item) => item.name}
                  className="w-full"
                />
                <SelectContent<MemberOption>>
                  {(item) => (
                    <SelectItem key={item.id} value={item}>
                      <span className="flex items-center gap-2">
                        <PersonIcon width={13} height={13} className="text-ink-muted" />
                        {item.name}
                      </span>
                    </SelectItem>
                  )}
                </SelectContent>
              </Select>
              {directoryMembers.length > 0 ? (
                <div className="flex flex-col gap-2">
                  <p className="text-label font-normal text-ink-muted">From your directory</p>
                  <div className="flex flex-wrap items-center gap-1.5">
                    {directoryMembers.map((member) => (
                      <Badge
                        key={member.membership_id}
                        tone="accent"
                        icon={<PersonIcon width={14} height={14} />}
                      >
                        {member.display_name || member.email}
                      </Badge>
                    ))}
                  </div>
                </div>
              ) : null}
            </>
          )}
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            loading={saving}
            disabled={loading}
            onClick={() => {
              setSaving(true);
              void onSave(selected.map((option) => option.id)).finally(() =>
                setSaving(false),
              );
            }}
          >
            Save members
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
