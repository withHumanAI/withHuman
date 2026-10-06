import { useState } from "react";
import { useNavigate, useRouter } from "@tanstack/react-router";
import { AssignmentBadge } from "./permission-summary";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import { ConfirmDialog } from "@/ds/components/confirm-dialog";
import { EmptyState } from "@/ds/components/empty-state";
import { IconButton } from "@/ds/components/icon-button";
import { SettingRow } from "@/ds/components/setting-row";
import { SettingsPage } from "@/ds/components/settings-page";
import { SettingsSection } from "@/ds/components/settings-section";
import { useToast } from "@/ds/components/toast";
import { PageBackLink } from "@/ds/components/page-header";
import { BroadcastIcon, PersonIcon, ShieldIcon, TrashIcon } from "@/ds/icons";
import { api } from "../api";
import type { Team, TeamMember } from "../types";

import { useAccess } from "./authorization";
import { useCustomPermissions } from "./roles";
import { ReferenceChip } from "./reference-chip";
import { NameDialog, TeamMembersDialog } from "./teams";
import { useTitle } from "./use-title";

/*
 * Settings: one team. Everything about it on one page: who is on it,
 * the permissions it holds, its escalation policy, and the team itself.
 */

/* What the team route loader delivers. A failed load leaves team null
 * and says why. */
export interface TeamPage {
  team: Team | null;
  members: TeamMember[];
  error: string;
}

export async function loadTeamPage(teamId: string): Promise<TeamPage> {
  try {
    const result = await api.getTeam(teamId);
    return { team: result.team, members: result.members, error: "" };
  } catch (reason: unknown) {
    return {
      team: null,
      members: [],
      error: reason instanceof Error ? reason.message : "Unable to load the team",
    };
  }
}

export function TeamScreen({
  page,
  canWrite: canWriteTeam,
  canEditMembers: canEditTeamMembers,
}: {
  page: TeamPage;
  /* Rename and archive the team. */
  canWrite: boolean;
  /* Change who is on the team. */
  canEditMembers: boolean;
}) {
  const { team, members, error } = page;
  const archived = !!team?.archived_at;
  const canWrite = canWriteTeam && !archived;
  const canEditMembers = canEditTeamMembers && !archived;
  useTitle(team?.name ?? "Team");
  const toast = useToast();
  const router = useRouter();
  const navigate = useNavigate();
  /* Granting a team a role is the same checked action as granting a
   * person one, so it gates on the same permission. */
  const { holdsAnywhere, isOwner } = useAccess();
  /* Team permissions are custom permission policies, which only an
   * edition that switches them on offers; predefined roles are held by
   * people. */
  const customPermissions = useCustomPermissions();
  const canAssign =
    customPermissions && !archived && holdsAnywhere("permission_policy.write");
  const [editingMembers, setEditingMembers] = useState(false);

  const [renaming, setRenaming] = useState(false);
  const [archiving, setArchiving] = useState(false);

  if (!team) {
    return (
      <SettingsPage title="Team" back={<PageBackLink to="/settings/teams">Teams</PageBackLink>}>
        <Callout tone="warn" title="Unable to load the team">
          {error}
        </Callout>
      </SettingsPage>
    );
  }

  /* Every change is followed by a reload of the loader's page, so the
   * page always shows what the server holds. */
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

  /* Removal rewrites the manual set without the member. Directory
   * assignments never pass through here; sync owns them. */
  function removeMember(member: TeamMember) {
    const manualIDs = members
      .filter((existing) => existing.sources.includes("manual"))
      .map((existing) => existing.membership_id)
      .filter((id) => id !== member.membership_id);
    void act(() => api.setTeamMembers(team!.id, manualIDs), "Member removal failed");
  }

  const policy = team.escalation_policy;

  return (
    <SettingsPage
      title={team.name}
      description={
        team.directory_managed
          ? "Managed by your identity provider."
          : members.length === 1
            ? "1 member."
            : `${members.length} members.`
      }
      back={<PageBackLink to="/settings/teams">Teams</PageBackLink>}
    >
      {archived ? (
        <div className="flex flex-col items-start gap-2">
          <Badge>Archived</Badge>
          <p className="text-body text-ink-muted">
            This team is read-only.
          </p>
        </div>
      ) : null}
      <SettingsSection
        title="Members"
        description="Everyone on this team."
        inset={members.length === 0 ? "none" : "rows"}
        action={
          canEditMembers ? (
            <Button size="sm" variant="neutral" onClick={() => setEditingMembers(true)}>
              Add members
            </Button>
          ) : undefined
        }
      >
        {members.length === 0 ? (
          <EmptyState
            icon={<PersonIcon width={18} height={18} />}
            title="No members yet"
            description={canEditMembers ? "Add members to bring people onto this team." : undefined}
          />
        ) : (
          members.map((member) => (
            <SettingRow
              key={member.membership_id}
              label={
                <span className="-ml-2 flex">
                  <ReferenceChip
                    reference={{
                      kind: "person",
                      id: member.membership_id,
                      name: member.display_name || member.email,
                    }}
                  />
                </span>
              }
            >
              {member.sources.includes("directory") ? (
                <Badge tone="accent" icon={<ShieldIcon width={14} height={14} />}>
                  Directory
                </Badge>
              ) : null}
              {canEditMembers && member.sources.includes("manual") ? (
                <IconButton
                  aria-label={`Remove ${member.display_name || member.email} from team`}
                  variant="ghost"
                  size="sm"
                  onClick={() => removeMember(member)}
                >
                  <TrashIcon width={14} height={14} />
                </IconButton>
              ) : null}
            </SettingRow>
          ))
        )}
      </SettingsSection>

      {customPermissions || team.assignments.length > 0 ? (
        <SettingsSection
          title="Permissions"
          description="Permissions every member of this team gets."
          inset={team.assignments.length === 0 ? "none" : "rows"}
          action={
            canAssign ? (
              <Button size="sm" variant="neutral" onClick={() => void navigate({ to: "/settings/permissions/$kind/$principalId", params: { kind: "teams", principalId: team.id } })}>
                Edit permissions
              </Button>
            ) : undefined
          }
        >
          {team.assignments.length === 0 ? (
            <EmptyState title="No permissions yet" />
          ) : (
            team.assignments.map((assignment) => (
              <SettingRow
                key={assignment.id}
                label={<AssignmentBadge assignment={assignment} holder="team" />}
                description={
                  assignment.scope_kind === "organization"
                    ? "Across the organization."
                    : assignment.scope_name
                      ? `Scoped to ${assignment.scope_name}.`
                      : undefined
                }
              />
            ))
          )}
        </SettingsSection>
      ) : null}

      <SettingsSection
        title="Escalation policy"
        description={archived ? "This policy is read-only." : "Choose who on the team gets notified, and in what order."}
      >
        <SettingRow
          icon={<BroadcastIcon />}
          label={
            policy.active_revision !== null
              ? `Revision ${policy.active_revision} active`
              : policy.latest_revision !== null
                ? "No active revision"
                : "No policy yet"
          }
          description={
            archived
              ? "This team is archived and cannot receive requests."
              : policy.active_revision !== null
              ? "Notifies members in the order the policy sets."
              : "Every member is notified at once."
          }
        >
          <Button
            size="sm"
            variant="neutral"
            onClick={() =>
              void navigate({
                to: "/settings/teams/$teamId/escalation-policy",
                params: { teamId: team.id },
              })
            }
          >
            {archived ? "View policy" : policy.latest_revision !== null ? "Edit policy" : "Create policy"}
          </Button>
        </SettingRow>
      </SettingsSection>

      {canWrite && !team.directory_managed ? (
        <>
          <SettingsSection title="Team">
            <SettingRow
              label="Name"
              description="Changing the name won't affect who gets notified."
            >
              <Button size="sm" variant="neutral" onClick={() => setRenaming(true)}>
                Rename
              </Button>
            </SettingRow>
          </SettingsSection>
          <SettingsSection
            tone="danger"
            title="Danger zone"
            description="These actions are dangerous."
          >
            <SettingRow
              label="Archive team"
              description="Make this team read-only and remove its permissions."
            >
              <Button size="sm" variant="danger" onClick={() => setArchiving(true)}>
                Archive team
              </Button>
            </SettingRow>
          </SettingsSection>
        </>
      ) : null}

      {editingMembers && canEditMembers ? (
        <TeamMembersDialog
          team={team}
          onClose={() => setEditingMembers(false)}
          onSave={async (membershipIds) => {
            const ok = await act(
              () => api.setTeamMembers(team.id, membershipIds),
              "Member change failed",
            );
            if (ok) setEditingMembers(false);
          }}
        />
      ) : null}

      {renaming && canWrite ? (
        <NameDialog
          open
          title={`Rename ${team.name}`}
          description="Changing the name won't affect who gets notified."
          confirmLabel="Rename team"
          initialName={team.name}
          onOpenChange={(open) => !open && setRenaming(false)}
          onSubmit={async (name) => {
            const ok = await act(() => api.renameTeam(team.id, name), "Rename failed");
            if (ok) setRenaming(false);
          }}
        />
      ) : null}
      {archiving && canWrite ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => !open && setArchiving(false)}
          title={`Archive ${team.name}?`}
          description="Its members lose the team's permissions. This can't be undone."
          confirmLabel="Archive team"
          typeToConfirm={members.length > 0 ? team.name : undefined}
          onConfirm={async () => {
            try {
              await api.archiveTeam(team.id);
              toast.add({ type: "approve", title: `${team.name} archived` });
              await router.invalidate();
              await navigate({ to: "/settings/teams", search: { status: "archived" } });
            } catch (reason) {
              toast.add({
                type: "deny",
                title: "Archiving failed",
                description: reason instanceof Error ? reason.message : undefined,
              });
            }
          }}
        />
      ) : null}
    </SettingsPage>
  );
}
