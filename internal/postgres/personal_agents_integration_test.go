package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/db"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/embeddedpg/embeddedpgtest"
)

// Exercises personal agents against real PostgreSQL: an instance of a
// personal agent belongs to the member who registers or enrolls it, its
// credential resolves to a principal that names that member and refuses
// while the member is inactive, its requests carry the member as the
// requester, provisioner tokens are refused, and deprovisioning the member
// disables their instances in the same transaction.
func TestPersonalAgentsAgainstRealPostgres(t *testing.T) {
	server := embeddedpgtest.Start(t)
	ctx := context.Background()
	store, err := Open(ctx, server.URL("postgres"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := db.Apply(ctx, store.Pool(), db.Migrations()); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	recordAudit(t, store)
	now := time.Now().UTC().Truncate(time.Microsecond)
	orgID, ownerUserID, ownerMembershipID := domain.ID(uuid.NewString()), domain.ID(uuid.NewString()), domain.ID(uuid.NewString())
	if _, err := store.ActivateOrganization(ctx, application.ActivateOrganizationInput{
		OrganizationID: orgID, OrganizationSlug: "acme", OrganizationName: "Acme",
		UserID: ownerUserID, MembershipID: ownerMembershipID, DisplayName: "Jordan Lee",
		Email: "owner@example.com", PasswordHash: "argon2id$test",
		AdmissionSource: domain.AdmissionManual, Now: now,
	}); err != nil {
		t.Fatalf("ActivateOrganization: %v", err)
	}
	_, memberID := apiKeyTestMember(t, ctx, store, orgID, ownerMembershipID, domain.RoleReviewerID, "chris", now)

	createAgent := func(name string, kind domain.AgentKind) domain.Agent {
		agent, err := store.CreateAgent(ctx, application.CreateAgentInput{Kind: kind,
			OrganizationID: orgID, ActorID: ownerMembershipID, Name: name, AgentSlug: domain.AgentSlug(strings.ReplaceAll(name, " ", "-")),
			AgentQuota: application.UnlimitedQuota(), Now: now,
		})
		if err != nil {
			t.Fatalf("CreateAgent %s: %v", name, err)
		}
		if agent.Kind != kind {
			t.Fatalf("CreateAgent %s: kind = %q, want %q", name, agent.Kind, kind)
		}
		return agent
	}
	coding := createAgent("claude code", domain.AgentPersonal)
	support := createAgent("support bot", domain.AgentService)
	if got, _ := store.GetAgentBySlug(ctx, application.GetAgentBySlugInput{OrganizationID: orgID, AgentSlug: coding.AgentSlug}); got.Kind != domain.AgentPersonal {
		t.Fatalf("GetAgentBySlug kind = %q", got.Kind)
	}

	register := func(agent domain.Agent, owner domain.ID, name string) (domain.AgentInstance, domain.ID, []byte, error) {
		secret := sha256.Sum256([]byte(name))
		credentialID := domain.ID(uuid.NewString())
		instance, err := store.CreateAgentInstanceWithCredential(ctx, application.CreateAgentInstanceWithCredentialInput{ActorType: domain.ActorHuman,
			OrganizationID: orgID, AgentID: agent.ID, ActorID: ownerUserID, MembershipID: owner, Name: name, Metadata: json.RawMessage(`{}`),
			Credential: application.AgentCredentialRecord{ID: credentialID, SecretHash: secret[:], IssuedAt: now, ExpiresAt: now.Add(time.Hour)},
			Now:        now,
		})
		return instance, credentialID, secret[:], err
	}
	// The owner rule holds on both sides.
	if _, _, _, err := register(coding, "", "ownerless"); !errors.Is(err, domain.ErrAgentOwnerRequired) {
		t.Fatalf("personal instance without an owner: err = %v, want ErrAgentOwnerRequired", err)
	}
	if _, _, _, err := register(support, memberID, "owned"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("service instance with an owner: err = %v, want ErrInvalid", err)
	}
	laptop, laptopCredential, laptopSecret, err := register(coding, memberID, "laptop")
	if err != nil || laptop.MembershipID != memberID {
		t.Fatalf("personal instance: %+v %v", laptop, err)
	}
	runner, runnerCredential, runnerSecret, err := register(support, "", "runner")
	if err != nil || runner.MembershipID != "" {
		t.Fatalf("service instance: %+v %v", runner, err)
	}

	// Enrollment writes the authorizing member as the owner of a personal
	// instance and nobody for a service one; the retry and the exchange
	// carry it.
	begin := func(name string) string {
		code := uuid.NewString()
		if err := store.CreateAgentEnrollment(ctx, application.CreateAgentEnrollmentInput{Enrollment: domain.AgentEnrollment{
			ID: domain.ID(uuid.NewString()), EnrollmentCodeHash: tokenHashOf(code), UserCode: code, Runtime: "claude-code",
			InstanceName: name, InstanceMetadata: json.RawMessage(`{}`), Status: "pending", CreatedAt: now, ExpiresAt: now.Add(time.Minute),
		}}); err != nil {
			t.Fatal(err)
		}
		return code
	}
	code := begin("desktop")
	desktop, err := store.AuthorizeAgentEnrollment(ctx, application.AuthorizeAgentEnrollmentInput{OrganizationID: orgID, MembershipID: memberID, UserCode: code, AgentID: coding.ID, Now: now})
	if err != nil || desktop.MembershipID != memberID {
		t.Fatalf("enrolled personal instance: %+v %v", desktop, err)
	}
	if again, err := store.AuthorizeAgentEnrollment(ctx, application.AuthorizeAgentEnrollmentInput{OrganizationID: orgID, MembershipID: memberID, UserCode: code, AgentID: coding.ID, Now: now}); err != nil || again.ID != desktop.ID || again.MembershipID != memberID {
		t.Fatalf("enrollment retry: %+v %v", again, err)
	}
	enrolled := auditEvents(t, ctx, store, `organization_id = $1 AND event_type = 'agent_instance.enrolled' AND subject_id = $2`, orgID, desktop.ID)
	if len(enrolled) != 1 || auditField(t, enrolled[0], "membership_id") != string(memberID) {
		t.Fatalf("enrollment audit = %+v", enrolled)
	}
	exchanged, err := store.ExchangeAgentEnrollment(ctx, application.ExchangeAgentEnrollmentInput{EnrollmentCodeHash: tokenHashOf(code),
		Credential: application.AgentCredentialRecord{ID: domain.ID(uuid.NewString()), SecretHash: tokenHashOf("desktop-secret"), IssuedAt: now, ExpiresAt: now.Add(time.Hour)}, Now: now})
	if err != nil || exchanged.AgentInstance.MembershipID != memberID || exchanged.Agent.Kind != domain.AgentPersonal {
		t.Fatalf("exchange: %+v %v", exchanged, err)
	}
	serviceCode := begin("ci")
	if ci, err := store.AuthorizeAgentEnrollment(ctx, application.AuthorizeAgentEnrollmentInput{OrganizationID: orgID, MembershipID: ownerMembershipID, UserCode: serviceCode, AgentID: support.ID, Now: now}); err != nil || ci.MembershipID != "" {
		t.Fatalf("enrolled service instance: %+v %v", ci, err)
	}

	// The enrollment roster: personal agents for everyone, service agents
	// by the person's issuance scope.
	page, err := store.ListEnrollmentAgents(ctx, application.ListEnrollmentAgentsInput{OrganizationID: orgID, Scope: domain.ScopeSet{}, Query: "", Offset: 0, Limit: 25})
	if err != nil || len(page.Agents) != 1 || page.Agents[0].ID != coding.ID {
		t.Fatalf("enrollment agents without a scope: %+v %v", page, err)
	}
	page, err = store.ListEnrollmentAgents(ctx, application.ListEnrollmentAgentsInput{OrganizationID: orgID, Scope: domain.ScopeSet{All: true}, Query: "", Offset: 0, Limit: 25})
	if err != nil || len(page.Agents) != 2 {
		t.Fatalf("enrollment agents everywhere: %+v %v", page, err)
	}

	// Resolution names the owner; a service instance names nobody.
	resolve := func(credentialID domain.ID, secret []byte, at time.Time) (domain.Principal, error) {
		return store.ResolveAgentCredential(ctx, application.ResolveAgentCredentialInput{CredentialID: credentialID, SecretHash: secret, Now: at})
	}
	principal, err := resolve(laptopCredential, laptopSecret, now)
	if err != nil || principal.MembershipID != memberID || principal.ActorType != domain.ActorAgent || principal.AgentInstanceID != laptop.ID {
		t.Fatalf("personal principal: %+v %v", principal, err)
	}
	if principal, err := resolve(runnerCredential, runnerSecret, now); err != nil || principal.MembershipID != "" {
		t.Fatalf("service principal: %+v %v", principal, err)
	}

	// A request made by a personal instance carries the member it acts
	// for, and product reads name them.
	request := domain.ApprovalRequest{
		Timeout: "1h", ID: domain.ID(uuid.NewString()), OrganizationID: orgID,
		AgentID: coding.ID, AgentInstanceID: laptop.ID, AgentSlug: coding.AgentSlug,
		Tool: "deploy", Arguments: json.RawMessage(`{"env":"prod"}`), IdempotencyKey: "laptop-1", SemanticHash: "hash-laptop-1",
		Status: domain.ApprovalPending, DeadlineAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	}
	stored, err := store.InitializeApproval(ctx, pendingApprovalInitialization(request))
	if err != nil || stored.RequesterMembershipID != memberID || stored.RequesterDisplayName != "chris" {
		t.Fatalf("personal request: %+v %v", stored, err)
	}
	serviceRequest := request
	serviceRequest.ID, serviceRequest.AgentID, serviceRequest.AgentInstanceID, serviceRequest.AgentSlug, serviceRequest.IdempotencyKey = domain.ID(uuid.NewString()), support.ID, runner.ID, support.AgentSlug, "runner-1"
	if stored, err := store.InitializeApproval(ctx, pendingApprovalInitialization(serviceRequest)); err != nil || stored.RequesterMembershipID != "" || stored.RequesterDisplayName != "" {
		t.Fatalf("service request: %+v %v", stored, err)
	}

	// The agent page names the owner of each personal instance.
	detail, err := store.GetAgentDetail(ctx, application.GetAgentDetailInput{OrganizationID: orgID, AgentID: coding.ID, InstanceLimit: 25, Now: now})
	if err != nil {
		t.Fatalf("GetAgentDetail: %v", err)
	}
	for _, instance := range detail.Instances {
		if instance.MembershipID != memberID || instance.OwnerDisplayName != "chris" {
			t.Fatalf("instance %s owner = %q %q", instance.Name, instance.MembershipID, instance.OwnerDisplayName)
		}
	}
	renamed, err := store.RenameAgentInstance(ctx, application.RenameAgentInstanceInput{OrganizationID: orgID, InstanceID: laptop.ID, Name: "macbook", ActorID: ownerMembershipID, Now: now})
	if err != nil || renamed.MembershipID != memberID {
		t.Fatalf("renamed instance owner: %+v %v", renamed, err)
	}

	// No provisioner tokens for a personal agent.
	if _, err := store.CreateAgentProvisioner(ctx, application.CreateAgentProvisionerInput{OrganizationID: orgID, AgentID: coding.ID, ActorID: ownerMembershipID,
		Provisioner: application.AgentProvisionerRecord{ID: domain.ID(uuid.NewString()), SecretHash: tokenHashOf("provisioner"), IssuedAt: now, ExpiresAt: now.Add(time.Hour)}, Now: now}); !errors.Is(err, domain.ErrPersonalAgent) {
		t.Fatalf("provisioner for a personal agent: err = %v, want ErrPersonalAgent", err)
	}

	// The instance follows its owner's membership: refused while
	// suspended, back on reactivation, disabled for good on removal.
	setStatus := func(status domain.MembershipStatus) {
		if _, err := store.SetMembershipStatus(ctx, application.SetMembershipStatusInput{OrganizationID: orgID, MembershipID: memberID, Status: status, RevokeSessions: true, ActorMembershipID: ownerMembershipID, Now: now}); err != nil {
			t.Fatalf("SetMembershipStatus %s: %v", status, err)
		}
	}
	setStatus(domain.MembershipSuspended)
	if _, err := resolve(laptopCredential, laptopSecret, now); !errors.Is(err, domain.ErrAgentInactive) {
		t.Fatalf("suspended owner: err = %v, want ErrAgentInactive", err)
	}
	setStatus(domain.MembershipActive)
	if _, err := resolve(laptopCredential, laptopSecret, now); err != nil {
		t.Fatalf("reactivated owner: %v", err)
	}
	setStatus(domain.MembershipDeprovisioned)
	if _, err := resolve(laptopCredential, laptopSecret, now); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("deprovisioned owner's credential: err = %v, want ErrUnauthenticated", err)
	}
	if _, err := resolve(runnerCredential, runnerSecret, now); err != nil {
		t.Fatalf("service instance after an unrelated removal: %v", err)
	}
	detail, err = store.GetAgentDetail(ctx, application.GetAgentDetailInput{OrganizationID: orgID, AgentID: coding.ID, InstanceLimit: 25, Now: now})
	if err != nil || detail.InstanceCount != 2 || detail.LiveInstanceCount != 0 {
		t.Fatalf("detail after removal: %+v %v", detail, err)
	}
	for _, instance := range detail.Instances {
		if instance.Status != domain.AgentDisabled {
			t.Fatalf("instance %s status = %s after the owner left", instance.Name, instance.Status)
		}
		for _, credential := range instance.Credentials {
			if credential.RevokedAt == nil {
				t.Fatalf("instance %s kept a live credential after the owner left", instance.Name)
			}
		}
	}
	disabled := auditEvents(t, ctx, store, `organization_id = $1 AND event_type = 'agent_instance.disabled'`, orgID)
	if len(disabled) != 2 {
		t.Fatalf("agent_instance.disabled events = %d, want 2", len(disabled))
	}
	for _, event := range disabled {
		if event.ActorType != domain.ActorHuman || event.ActorID != ownerUserID || auditField(t, event, "reason") != "membership_deprovisioned" {
			t.Fatalf("disable audit = %+v", event)
		}
	}
	// A removed member's instance stays disabled: the switch is not a way
	// back in, and the trigger keeps the kind and owner rule.
	if _, err := store.Pool().Exec(ctx, `UPDATE agents SET kind = 'service' WHERE id = $1`, coding.ID); err == nil {
		t.Fatal("agent kind changed")
	}
	if _, err := store.Pool().Exec(ctx, `UPDATE agent_instances SET membership_id = NULL WHERE id = $1`, laptop.ID); err == nil {
		t.Fatal("personal instance lost its owner")
	}
}

// auditField reads one string field of a recorded event's data.
func auditField(t *testing.T, event domain.AuditEvent, key string) string {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatalf("decode audit data %s: %v", event.Data, err)
	}
	value, _ := data[key].(string)
	return value
}
