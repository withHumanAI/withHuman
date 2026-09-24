package domain

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"sync"
)

// Permission is one key of the code-owned vocabulary, shaped
// resource.action with a third segment only when a sub-resource is a real
// gate. Keys are static per build: a handler names one, the OpenAPI
// operation carries the same key, and the registry below describes it.
// A key is never constructed from user data; the instance a grant applies
// to lives on the assignment's scope, never in the key.
type Permission string

// PermissionHolder is the kind of actor a permission can be granted to.
type PermissionHolder string

const (
	HolderHuman   PermissionHolder = "human"
	HolderAgent   PermissionHolder = "agent"
	HolderGateway PermissionHolder = "gateway"
	// HolderAPIKey marks a permission a personal API key may carry. Every
	// human permission has it except the api_key.* family, so minting and
	// revoking keys stays a browser act.
	HolderAPIKey             PermissionHolder = "api_key"
	HolderOrganizationAPIKey PermissionHolder = "organization_api_key"
)

// ResourceKind is what a Check on a permission is evaluated against.
// The kind decides which scopes can cover the resource: an organization
// resource is covered only by an organization-scope grant, an agent_slug
// resource by an organization or matching agent_slug grant, and so on.
type ResourceKind string

const (
	ResourceOrganization ResourceKind = "organization"
	ResourceTeam         ResourceKind = "team"
	ResourceAgent        ResourceKind = "agent"
	ResourceRequest      ResourceKind = "request"
	// ResourceScope is for permissions that act on a scope itself, such
	// as assigning roles at it: a check may pass any scope kind's resource
	// and is satisfied by a grant at that scope or wider.
	ResourceScope ResourceKind = "scope"
)

// ScopeKind is where a role assignment applies. Scope kinds are
// structural: things with an identity that exist before any request does.
// Attributes of a request (its tool, its target) are routing conditions,
// never scopes.
type ScopeKind string

const (
	ScopeOrganization ScopeKind = "organization"
	ScopeTeam         ScopeKind = "team"
	ScopeAgent        ScopeKind = "agent"
)

// ValidScopeKind reports whether the kind is one an assignment may carry.
func ValidScopeKind(kind ScopeKind) bool {
	return kind == ScopeOrganization || kind == ScopeTeam || kind == ScopeAgent
}

// ScopeKinds lists every scope kind, widest first.
func ScopeKinds() []ScopeKind {
	return []ScopeKind{ScopeOrganization, ScopeTeam, ScopeAgent}
}

// PermissionDefinition is one registry entry: the key, what it means, who
// may hold it, what kind of resource a check passes, and whether granting
// or using it deserves ceremony (confirmation, freshness, louder audit,
// never through unattended admission).
type PermissionDefinition struct {
	Key         Permission
	Description string
	Holders     []PermissionHolder
	Resource    ResourceKind
	Dangerous   bool
	// BuiltInRoles names the built-in roles, other than owner, that hold
	// an edition-registered key: the hosted audit permission joins the
	// admin and auditor roles this way. Core's own keys list their roles
	// in roles.go instead. Owner holds every human key regardless.
	BuiltInRoles []Role
}

// Area is the first segment of the key, used to group the permission
// matrix.
func (d PermissionDefinition) Area() string {
	for index, char := range d.Key {
		if char == '.' {
			return string(d.Key[:index])
		}
	}
	return string(d.Key)
}

// HeldBy reports whether the holder kind may be granted the permission.
func (d PermissionDefinition) HeldBy(holder PermissionHolder) bool {
	return slices.Contains(d.Holders, holder)
}

const (
	PermissionOrganizationRead          Permission = "organization.read"
	PermissionOrganizationSettingsWrite Permission = "organization.settings.write"
	PermissionOrganizationDelete        Permission = "organization.delete"
	PermissionMembershipRead            Permission = "membership.read"
	PermissionMembershipInvite          Permission = "membership.invite"
	PermissionMembershipWrite           Permission = "membership.write"
	PermissionPolicyWrite               Permission = "permission_policy.write"
	PermissionPolicyRead                Permission = "permission_policy.read"
	PermissionTeamRead                  Permission = "team.read"
	PermissionTeamWrite                 Permission = "team.write"
	PermissionTeamMemberWrite           Permission = "team.member.write"
	PermissionTeamEscalationWrite       Permission = "team.escalation.write"
	PermissionTeamEscalationActivate    Permission = "team.escalation.activate"
	PermissionAgentRead                 Permission = "agent.read"
	PermissionAgentWrite                Permission = "agent.write"
	PermissionAgentCredentialIssue      Permission = "agent.credential.issue"
	PermissionAgentCredentialRevoke     Permission = "agent.credential.revoke"
	PermissionPipelineRead              Permission = "pipeline.read"
	PermissionPipelineWrite             Permission = "pipeline.write"
	PermissionPipelineActivate          Permission = "pipeline.activate"
	PermissionEscalationPathRead        Permission = "escalation_path.read"
	PermissionEscalationPathWrite       Permission = "escalation_path.write"
	PermissionEscalationPathActivate    Permission = "escalation_path.activate"
	PermissionRequestCreate             Permission = "request.create"
	PermissionRequestRead               Permission = "request.read"
	PermissionRequestClaim              Permission = "request.claim"
	PermissionRequestDecide             Permission = "request.decide"
	PermissionRequestDecideUnrouted     Permission = "request.decide.unrouted"
	PermissionRequestReadUnrouted       Permission = "request.read.unrouted"
	PermissionWebhookRead               Permission = "webhook.read"
	PermissionWebhookWrite              Permission = "webhook.write"
	PermissionConnectionRead            Permission = "connection.read"
	PermissionConnectionWrite           Permission = "connection.write"
	// PermissionAPIKeyOrganizationIssue is the hosted edition's: it registers
	// the definition (issuing organization keys and delegating access to
	// them); core keeps the key as vocabulary for the store's delegation
	// checks, which deny by construction where nothing registered it.
	PermissionAPIKeyOrganizationIssue Permission = "api_key.organization.issue"
	PermissionAPIKeyIssue             Permission = "api_key.issue"
	PermissionAPIKeyRead              Permission = "api_key.read"
	PermissionAPIKeyRevoke            Permission = "api_key.revoke"
)

// humans is what a person holds and may place on a personal API key;
// humansOnly is the handful a key can never carry.
var humans = []PermissionHolder{HolderHuman, HolderAPIKey}
var managementHolders = []PermissionHolder{HolderHuman, HolderAPIKey, HolderOrganizationAPIKey}
var humansOnly = []PermissionHolder{HolderHuman}

var corePermissions = []PermissionDefinition{
	{Key: PermissionOrganizationRead, Description: "Read organization settings and policy", Holders: managementHolders, Resource: ResourceOrganization},
	{Key: PermissionOrganizationSettingsWrite, Description: "Change organization settings and policy", Holders: humans, Resource: ResourceOrganization},
	{Key: PermissionOrganizationDelete, Description: "Schedule the organization for deletion", Holders: humans, Resource: ResourceOrganization, Dangerous: true},
	{Key: PermissionMembershipRead, Description: "List members and pending invitations", Holders: managementHolders, Resource: ResourceOrganization},
	{Key: PermissionMembershipInvite, Description: "Invite people and manage pending invitations", Holders: humans, Resource: ResourceOrganization},
	{Key: PermissionMembershipWrite, Description: "Suspend, reactivate, and remove members", Holders: humans, Resource: ResourceOrganization},
	{Key: PermissionPolicyWrite, Description: "Grant and revoke permission policies for people and teams", Holders: humans, Resource: ResourceScope, Dangerous: true},
	{Key: PermissionPolicyRead, Description: "Read permission policies and available permissions", Holders: humans, Resource: ResourceScope},
	{Key: PermissionTeamRead, Description: "List teams and their rosters", Holders: managementHolders, Resource: ResourceTeam},
	{Key: PermissionTeamWrite, Description: "Create, rename, and archive teams", Holders: humans, Resource: ResourceTeam},
	{Key: PermissionTeamMemberWrite, Description: "Change who is on a team", Holders: humans, Resource: ResourceTeam},
	{Key: PermissionTeamEscalationWrite, Description: "Create team escalation policy revisions", Holders: managementHolders, Resource: ResourceTeam},
	{Key: PermissionTeamEscalationActivate, Description: "Activate, roll back, and deactivate team escalation policies", Holders: managementHolders, Resource: ResourceTeam},
	{Key: PermissionAgentRead, Description: "View agents, their instances, and credential state", Holders: managementHolders, Resource: ResourceAgent},
	{Key: PermissionAgentWrite, Description: "Create agents and change their status", Holders: managementHolders, Resource: ResourceAgent},
	{Key: PermissionAgentCredentialIssue, Description: "Issue instance credentials and provisioner tokens", Holders: managementHolders, Resource: ResourceAgent, Dangerous: true},
	{Key: PermissionAgentCredentialRevoke, Description: "Revoke instance credentials and provisioner tokens", Holders: managementHolders, Resource: ResourceAgent},
	{Key: PermissionPipelineRead, Description: "Read request approval pipelines and their revisions", Holders: managementHolders, Resource: ResourceAgent},
	{Key: PermissionPipelineWrite, Description: "Create and preview request approval pipeline revisions", Holders: managementHolders, Resource: ResourceAgent},
	{Key: PermissionPipelineActivate, Description: "Activate and roll back pipeline revisions", Holders: managementHolders, Resource: ResourceAgent},
	{Key: PermissionEscalationPathRead, Description: "Read escalation paths and their revisions", Holders: managementHolders, Resource: ResourceOrganization},
	{Key: PermissionEscalationPathWrite, Description: "Create escalation paths and their revisions", Holders: managementHolders, Resource: ResourceOrganization},
	{Key: PermissionEscalationPathActivate, Description: "Activate, roll back, and archive escalation paths", Holders: managementHolders, Resource: ResourceOrganization},
	{Key: PermissionRequestCreate, Description: "Create approval requests", Holders: []PermissionHolder{HolderAgent}, Resource: ResourceOrganization},
	{Key: PermissionRequestRead, Description: "Read approval requests and their outcomes", Holders: []PermissionHolder{HolderHuman, HolderAPIKey, HolderOrganizationAPIKey, HolderAgent}, Resource: ResourceRequest},
	{Key: PermissionRequestClaim, Description: "Claim a pending request while reviewing it", Holders: humans, Resource: ResourceRequest},
	{Key: PermissionRequestDecide, Description: "Approve or deny requests routed to you", Holders: humans, Resource: ResourceRequest},
	{Key: PermissionRequestDecideUnrouted, Description: "Decide requests that were routed to other people (break glass)", Holders: humans, Resource: ResourceRequest, Dangerous: true},
	{Key: PermissionRequestReadUnrouted, Description: "Read requests that were routed to other people, whatever the visibility policy", Holders: managementHolders, Resource: ResourceRequest},
	{Key: PermissionWebhookRead, Description: "View webhook endpoints", Holders: managementHolders, Resource: ResourceOrganization},
	{Key: PermissionWebhookWrite, Description: "Create, edit, rotate, and delete webhook endpoints", Holders: managementHolders, Resource: ResourceOrganization},
	{Key: PermissionConnectionRead, Description: "View SSO, directory, chat, and domain connections", Holders: humans, Resource: ResourceOrganization},
	{Key: PermissionConnectionWrite, Description: "Connect and disconnect SSO, directories, chat apps, and domains", Holders: humans, Resource: ResourceOrganization, Dangerous: true},
	{Key: PermissionAPIKeyIssue, Description: "Mint personal API keys and manage your own", Holders: humansOnly, Resource: ResourceOrganization},
	{Key: PermissionAPIKeyRead, Description: "List every API key in the organization", Holders: humansOnly, Resource: ResourceOrganization},
	{Key: PermissionAPIKeyRevoke, Description: "Revoke any API key in the organization", Holders: humansOnly, Resource: ResourceOrganization},
}

var permissionKeyPattern = regexp.MustCompile(`^[a-z_]+(\.[a-z_]+){1,2}$`)

var registry = struct {
	sync.RWMutex
	entries map[Permission]PermissionDefinition
}{entries: indexPermissions(corePermissions)}

func indexPermissions(defs []PermissionDefinition) map[Permission]PermissionDefinition {
	entries := make(map[Permission]PermissionDefinition, len(defs))
	for _, def := range defs {
		entries[def.Key] = def
	}
	return entries
}

// RegisterPermissions adds edition-owned keys (the hosted edition's
// billing and audit permissions) to the registry. It runs once at
// composition time, before the built-in roles are reconciled, so the
// owner role and any role a key names pick it up. Duplicates, malformed
// keys, unknown holders or resource kinds, and roles a key may not name
// are refused.
func RegisterPermissions(defs ...PermissionDefinition) error {
	registry.Lock()
	defer registry.Unlock()
	for _, def := range defs {
		if !permissionKeyPattern.MatchString(string(def.Key)) {
			return fmt.Errorf("permission %q: key must be resource.action", def.Key)
		}
		if _, exists := registry.entries[def.Key]; exists {
			return fmt.Errorf("permission %q: already registered", def.Key)
		}
		if len(def.Holders) == 0 {
			return fmt.Errorf("permission %q: at least one holder is required", def.Key)
		}
		for _, holder := range def.Holders {
			if holder != HolderHuman && holder != HolderAgent && holder != HolderGateway && holder != HolderAPIKey && holder != HolderOrganizationAPIKey {
				return fmt.Errorf("permission %q: unknown holder %q", def.Key, holder)
			}
		}
		switch def.Resource {
		case ResourceOrganization, ResourceTeam, ResourceAgent, ResourceRequest, ResourceScope:
		default:
			return fmt.Errorf("permission %q: unknown resource kind %q", def.Key, def.Resource)
		}
		for _, role := range def.BuiltInRoles {
			switch role {
			case RoleAdmin, RoleReviewer, RoleAuditor:
			default:
				return fmt.Errorf("permission %q: built-in role %q cannot be named (owner holds every human key; agent holds none)", def.Key, role)
			}
			if !def.HeldBy(HolderHuman) {
				return fmt.Errorf("permission %q: only a human permission joins a built-in role", def.Key)
			}
		}
		registry.entries[def.Key] = def
	}
	return nil
}

// Permissions lists every registered definition sorted by key.
func Permissions() []PermissionDefinition {
	registry.RLock()
	defer registry.RUnlock()
	defs := make([]PermissionDefinition, 0, len(registry.entries))
	for _, def := range registry.entries {
		defs = append(defs, def)
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Key < defs[j].Key })
	return defs
}

// PermissionKeys lists every registered key sorted.
func PermissionKeys() []Permission {
	defs := Permissions()
	keys := make([]Permission, len(defs))
	for index, def := range defs {
		keys[index] = def.Key
	}
	return keys
}

// LookupPermission finds a registered definition.
func LookupPermission(key Permission) (PermissionDefinition, bool) {
	registry.RLock()
	defer registry.RUnlock()
	def, ok := registry.entries[key]
	return def, ok
}

// HumanPermissions lists every key a human may hold, sorted. The owner
// role is exactly this list, computed at reconcile time so edition keys
// are included.
func HumanPermissions() []Permission {
	var keys []Permission
	for _, def := range Permissions() {
		if def.HeldBy(HolderHuman) {
			keys = append(keys, def.Key)
		}
	}
	return keys
}

// PermissionIsDangerous reports the registry's dangerous flag; unknown
// keys are treated as dangerous so a stale key never slips through
// quietly.
func PermissionIsDangerous(key Permission) bool {
	def, ok := LookupPermission(key)
	return !ok || def.Dangerous
}

// Scope is one place a grant applies: the organization, one team, or one
// agent.
type Scope struct {
	Kind ScopeKind `json:"kind"`
	ID   string    `json:"id,omitempty"`
}

// Resource is what a check is evaluated against. The kind comes from the
// permission's registry entry; the ID names the instance for team and
// agent_slug resources.
type Resource struct {
	Kind ResourceKind
	ID   string
}

func OrganizationResource() Resource { return Resource{Kind: ResourceOrganization} }
func TeamResource(id ID) Resource    { return Resource{Kind: ResourceTeam, ID: string(id)} }
func RequestResource(id ID) Resource { return Resource{Kind: ResourceRequest, ID: string(id)} }

// AgentResource names one agent by slug. The empty slug is the
// organization pipeline, an organization resource, so an agent grant can
// never reach it.
func AgentResource(agentSlug AgentSlug) Resource {
	if agentSlug == "" {
		return OrganizationResource()
	}
	return Resource{Kind: ResourceAgent, ID: string(agentSlug)}
}

// CoveringScopes lists the scopes a grant may carry to satisfy a check on
// this resource: the organization always, plus the resource's own scope
// for team and agent_slug resources. A request is an organization
// resource for grants; routing selects who decides a particular one.
func (r Resource) CoveringScopes() []Scope {
	scopes := []Scope{{Kind: ScopeOrganization}}
	switch r.Kind {
	case ResourceTeam:
		scopes = append(scopes, Scope{Kind: ScopeTeam, ID: r.ID})
	case ResourceAgent:
		scopes = append(scopes, Scope{Kind: ScopeAgent, ID: r.ID})
	}
	return scopes
}

// ScopeForKind maps a scope back to the resource a check at that scope
// evaluates: the organization, the team, or the agent itself.
func (s Scope) Resource() Resource {
	switch s.Kind {
	case ScopeTeam:
		return Resource{Kind: ResourceTeam, ID: s.ID}
	case ScopeAgent:
		return Resource{Kind: ResourceAgent, ID: s.ID}
	}
	return OrganizationResource()
}

// Grant is one permission a principal holds at one scope. A principal's
// grants are the flattening of its role assignments, direct and inherited
// through teams, joined to the roles' permission lists.
type Grant struct {
	Permission Permission `json:"permission"`
	ScopeKind  ScopeKind  `json:"scope_kind"`
	ScopeID    string     `json:"scope_id,omitempty"`
}

// Scope returns the grant's scope.
func (g Grant) Scope() Scope { return Scope{Kind: g.ScopeKind, ID: g.ScopeID} }

// Covers reports whether the grant satisfies a check on the resource.
func (g Grant) Covers(permission Permission, resource Resource) bool {
	if g.Permission != permission {
		return false
	}
	return slices.Contains(resource.CoveringScopes(), g.Scope())
}

// OrganizationGrants pins every key at organization scope: the shape of a
// built-in role held everywhere.
func OrganizationGrants(keys []Permission) []Grant {
	grants := make([]Grant, 0, len(keys))
	for _, key := range keys {
		grants = append(grants, Grant{Permission: key, ScopeKind: ScopeOrganization})
	}
	return grants
}

// ProjectPermissions is the "holds it anywhere" projection of a grant
// set: sorted, distinct keys. Clients gate navigation on it; server code
// never reads it.
func ProjectPermissions(grants []Grant) []Permission {
	seen := make(map[Permission]struct{}, len(grants))
	for _, grant := range grants {
		seen[grant.Permission] = struct{}{}
	}
	keys := make([]Permission, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// ScopeSet is the answer to "where does this principal hold a permission":
// everywhere, or a set of scopes. Repositories turn a set into predicates
// and drop the predicate for All.
type ScopeSet struct {
	All    bool
	Scopes []Scope
}

// Empty reports that the principal holds the permission nowhere.
func (s ScopeSet) Empty() bool { return !s.All && len(s.Scopes) == 0 }

// IDs lists the scope IDs of one kind, sorted and distinct.
func (s ScopeSet) IDs(kind ScopeKind) []string {
	var ids []string
	for _, scope := range s.Scopes {
		if scope.Kind == kind && !slices.Contains(ids, scope.ID) {
			ids = append(ids, scope.ID)
		}
	}
	slices.Sort(ids)
	return ids
}
