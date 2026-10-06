package authorization

import (
	"fmt"

	"github.com/withHumanAI/withHuman/domain"
)

// GrantInput is one proposed assignment: the actor granting it, the role
// row, and the scope it would apply at.
type GrantInput struct {
	Actor domain.Principal
	// ActorIsOwner is whether the actor holds the built-in owner role at
	// organization scope; only owners grant owner.
	ActorIsOwner bool
	Role         domain.RoleRow
	Scope        domain.Scope
	// Unattended marks grants no person decides at the moment they take
	// effect: a JIT default role or a directory group mapping. Those may
	// never carry a dangerous permission.
	Unattended bool
}

// EvaluateGrant is the no-escalation kernel: an assignment is allowed only
// when the role is bindable at the scope, only owners grant owner, no
// dangerous role arrives unattended, and the actor may assign at that
// scope. A custom role additionally requires the actor to hold every
// permission in it at that scope or wider, so nobody composes a role
// wider than themselves. Built-in roles other than owner are the
// product's fixed vocabulary, meant to be handed out by administrators
// who do not hold them (an admin invites reviewers without being one),
// so the per-permission rule does not apply to them. It is pure so every
// refusal is cheap to test.
func EvaluateGrant(input GrantInput) *Rejection {
	if !input.Role.BindableAt(input.Scope.Kind) {
		return &Rejection{Code: "scope_not_bindable", Err: fmt.Errorf("role %s cannot be assigned at %s scope: %w", input.Role.Key, input.Scope.Kind, domain.ErrInvalid)}
	}
	if input.Role.BuiltIn && input.Role.Key == domain.RoleOwner && !input.ActorIsOwner {
		return &Rejection{Code: "owner_required", Err: fmt.Errorf("only an owner can grant or remove the owner role: %w", domain.ErrForbidden)}
	}
	if input.Unattended && input.Role.Dangerous() {
		return &Rejection{Code: "dangerous_role_unattended", Err: fmt.Errorf("roles with dangerous permissions cannot be granted automatically: %w", domain.ErrInvalid)}
	}
	resource := input.Scope.Resource()
	if !holds(input.Actor.Grants, domain.PermissionPolicyWrite, resource) {
		return &Rejection{Code: "forbidden", Err: Denied{Permission: domain.PermissionPolicyWrite, Resource: resource}}
	}
	if input.Role.BuiltIn {
		return nil
	}
	for _, permission := range input.Role.Permissions {
		def, ok := domain.LookupPermission(permission)
		if !ok {
			return &Rejection{Code: "grant_exceeds_actor", Err: fmt.Errorf("role %s names unregistered permission %s: %w", input.Role.Key, permission, domain.ErrForbidden)}
		}
		target := resource
		// A permission whose resource is the organization is only ever
		// checked against the organization, so an actor scoped narrower
		// cannot delegate it, which is the point.
		if def.Resource == domain.ResourceOrganization || def.Resource == domain.ResourceRequest {
			target = domain.OrganizationResource()
		}
		if !holds(input.Actor.Grants, permission, target) {
			return &Rejection{Code: "grant_exceeds_actor", Err: fmt.Errorf("you cannot grant %s at this scope without holding it yourself: %w", permission, Denied{Permission: permission, Resource: target})}
		}
	}
	return nil
}
