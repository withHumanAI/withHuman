import { useState } from "react";
import { Badge } from "@/ds/components/badge";
import { Button } from "@/ds/components/button";
import { Callout } from "@/ds/components/callout";
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogTitle,
} from "@/ds/components/dialog";
import { RadioGroup, RadioOption } from "@/ds/components/radio-group";
import { PersonIcon, ShieldIcon, SettingsIcon } from "@/ds/icons";
import type { RoleAssignment, RoleDescriptor, RoleKey } from "../types";
import { useRuntimeConfig } from "./use-runtime-config";

/* Predefined roles are what every edition offers: owner, admin, reviewer
 * and auditor, the same in every organization. An edition that switches
 * custom permissions on (runtime-config custom_permissions_enabled) also
 * offers permission policies beside them. */

export function useCustomPermissions(): boolean {
  return useRuntimeConfig().config?.custom_permissions_enabled ?? false;
}

/* The predefined role a person holds directly, when their direct access
 * is exactly one role everywhere; otherwise undefined (custom
 * permissions, or several grants). Inherited and directory grants are
 * not the member's own and do not count. */
export function directRole(
  assignments: RoleAssignment[],
): RoleKey | undefined {
  const direct = assignments.filter(
    (assignment) => !assignment.via_team_id && assignment.source === "manual",
  );
  if (direct.length !== 1) return undefined;
  const [only] = direct;
  return only.scope_kind === "organization" ? only.built_in_role : undefined;
}

/* The badges that name a member's access: one per predefined role they
 * hold at organization scope, however they hold it, and "Custom" when
 * anything else grants them permissions. */
export function RoleBadges({
  assignments,
  roles,
}: {
  assignments: RoleAssignment[];
  roles: RoleDescriptor[];
}) {
  const names = new Map(roles.map((role) => [role.key, role.name]));
  const held = [
    ...new Set(
      assignments
        .filter(
          (assignment) =>
            assignment.built_in_role && assignment.scope_kind === "organization",
        )
        .map((assignment) => assignment.built_in_role as RoleKey),
    ),
  ];
  const custom = assignments.some(
    (assignment) =>
      !assignment.built_in_role || assignment.scope_kind !== "organization",
  );
  if (held.length === 0 && !custom)
    return <span className="text-label text-ink-muted">No role</span>;
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {held.map((key) => (
        <Badge
          key={key}
          icon={
            key === "owner" ? (
              <ShieldIcon width={14} height={14} />
            ) : (
              <PersonIcon width={14} height={14} />
            )
          }
        >
          {names.get(key) ?? key}
        </Badge>
      ))}
      {custom ? (
        <Badge icon={<SettingsIcon width={14} height={14} />}>Custom</Badge>
      ) : null}
    </div>
  );
}

/* One predefined role, explained, as a single bordered list. Owner is
 * offered only to owners: only an owner may grant it. */
export function RolePicker({
  roles,
  value,
  onChange,
  canGrantOwner,
  disabled = false,
}: {
  roles: RoleDescriptor[];
  value: RoleKey | null;
  onChange: (role: RoleKey) => void;
  canGrantOwner: boolean;
  disabled?: boolean;
}) {
  return (
    <RadioGroup
      aria-label="Role"
      value={value}
      onValueChange={(next) => onChange(next as RoleKey)}
      disabled={disabled}
      className="gap-0 overflow-hidden rounded-lg border-hairline border-edge"
    >
      {roles.map((role) => (
        <RadioOption
          key={role.key}
          value={role.key}
          disabled={role.key === "owner" && !canGrantOwner}
          className="rounded-none border-t-hairline border-edge px-3 py-2.5 first:border-t-0"
          label={role.name}
          description={
            role.key === "owner" && !canGrantOwner
              ? `${role.description} Only an owner can grant it.`
              : role.description
          }
        />
      ))}
    </RadioGroup>
  );
}

/* Change a member's predefined role. Saving replaces every permission
 * they hold directly, so a member with custom permissions is warned. */
export function ChangeRoleDialog({
  name,
  assignments,
  roles,
  canGrantOwner,
  onClose,
  onSave,
}: {
  name: string;
  assignments: RoleAssignment[];
  roles: RoleDescriptor[];
  canGrantOwner: boolean;
  onClose: () => void;
  onSave: (role: RoleKey) => Promise<boolean>;
}) {
  const current = directRole(assignments);
  const [role, setRole] = useState<RoleKey | null>(current ?? null);
  const [saving, setSaving] = useState(false);
  const replacesCustom = assignments.some(
    (assignment) =>
      !assignment.via_team_id &&
      assignment.source === "manual" &&
      (!assignment.built_in_role || assignment.scope_kind !== "organization"),
  );
  const inherited = assignments.some((assignment) => assignment.via_team_id);
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="w-[min(520px,calc(100vw-32px))]">
        <DialogTitle>Change role for {name}</DialogTitle>
        <DialogDescription>
          The role applies across the whole organization.
        </DialogDescription>
        <DialogBody className="flex flex-col gap-4">
          {replacesCustom ? (
            <Callout tone="warn">
              {name} has custom permissions. Choosing a role replaces them.
            </Callout>
          ) : null}
          <RolePicker
            roles={roles}
            value={role}
            onChange={setRole}
            canGrantOwner={canGrantOwner}
            disabled={saving}
          />
          {inherited ? (
            <p className="text-label text-ink-muted">
              {name} keeps the access their teams give them.
            </p>
          ) : null}
        </DialogBody>
        <DialogFooter>
          <Button variant="ghost" disabled={saving} onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="primary"
            loading={saving}
            disabled={!role || (role === current && !replacesCustom)}
            onClick={() => {
              if (!role) return;
              setSaving(true);
              void onSave(role).then((saved) => {
                setSaving(false);
                if (saved) onClose();
              });
            }}
          >
            Save role
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
