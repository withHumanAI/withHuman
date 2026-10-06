package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

// ActivateOrganization is the shared organization-creation transaction: the
// bootstrap ceremony and the hosted signup flow both end here. It finds or
// creates the founding owner by email, then creates the organization, its
// policy row, the owner membership, and the audit record.
func (s *Store) ActivateOrganization(ctx context.Context, input application.ActivateOrganizationInput) (application.ActivationResult, error) {
	if err := input.Validate(); err != nil {
		return application.ActivationResult{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return application.ActivationResult{}, err
	}
	defer tx.Rollback(ctx)

	// The founder is an existing user when their provider identity is
	// already linked (the triple is the identity, so a changed email at the
	// provider still finds them), else when their email is known; otherwise
	// they are created with the identity the caller verified.
	userID := input.UserID
	var existingID string
	err = pgx.ErrNoRows
	if input.HasProviderIdentity() {
		err = tx.QueryRow(ctx, `SELECT u.id::text FROM external_identities e JOIN users u ON u.id = e.user_id
            WHERE e.provider_key = $1 AND e.issuer = $2 AND e.subject = $3 AND u.status = 'active'`,
			input.ProviderKey, input.Issuer, input.Subject).Scan(&existingID)
		if err == nil {
			if _, err := tx.Exec(ctx, `UPDATE external_identities SET last_observed_email = $4, last_authenticated_at = $5
                WHERE provider_key = $1 AND issuer = $2 AND subject = $3`,
				input.ProviderKey, input.Issuer, input.Subject, input.Email, input.Now); err != nil {
				return application.ActivationResult{}, err
			}
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id::text FROM users
            WHERE lower(primary_email) = lower($1) AND status = 'active'`, input.Email).Scan(&existingID)
		if err == nil && input.HasProviderIdentity() {
			if err := linkExternalIdentity(ctx, tx, domain.ID(existingID), input.ProviderKey, input.Issuer, input.Subject, input.Email, input.Now); err != nil {
				return application.ActivationResult{}, err
			}
		}
	}
	switch {
	case err == nil:
		userID = domain.ID(existingID)
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := tx.Exec(ctx, `INSERT INTO users (id, display_name, primary_email, status, created_at, updated_at)
            VALUES ($1, $2, $3, 'active', $4, $4)`, userID, input.DisplayName, input.Email, input.Now); err != nil {
			return application.ActivationResult{}, err
		}
		providerKey, issuer, subject := "local", "withhuman:self-hosted", string(userID)
		if input.HasProviderIdentity() {
			providerKey, issuer, subject = input.ProviderKey, input.Issuer, input.Subject
		}
		if _, err := tx.Exec(ctx, `INSERT INTO external_identities
            (user_id, provider_key, issuer, subject, last_observed_email, email_verified, linked_at, last_authenticated_at)
            VALUES ($1, $2, $3, $4, $5, true, $6, $6)`,
			userID, providerKey, issuer, subject, input.Email, input.Now); err != nil {
			if isUniqueViolation(err) {
				return application.ActivationResult{}, fmt.Errorf("this sign-in is linked to another account: %w", domain.ErrConflict)
			}
			return application.ActivationResult{}, err
		}
		if input.PasswordHash != "" {
			if _, err := tx.Exec(ctx, `INSERT INTO local_credentials (user_id, password_hash, created_at, updated_at)
                VALUES ($1, $2, $3, $3)`, userID, input.PasswordHash, input.Now); err != nil {
				return application.ActivationResult{}, err
			}
		}
	default:
		return application.ActivationResult{}, err
	}

	if _, err := tx.Exec(ctx, `INSERT INTO organizations
        (id, slug, name, status, status_reason, created_at, updated_at)
        VALUES ($1, $2, $3, 'active', '', $4, $4)`,
		input.OrganizationID, input.OrganizationSlug, input.OrganizationName, input.Now); err != nil {
		if isUniqueViolation(err) {
			return application.ActivationResult{}, fmt.Errorf("organization slug is taken: %w", domain.ErrConflict)
		}
		return application.ActivationResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO organization_policies
        (organization_id, session_max_age_seconds, approval_reauth_window_seconds) VALUES ($1, $2, $3)`,
		input.OrganizationID, int64(domain.DefaultSessionMaxAge.Seconds()),
		int64(domain.DefaultApprovalReauthWindow.Seconds())); err != nil {
		return application.ActivationResult{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO memberships
        (id, user_id, organization_id, status, admission_source, created_at, updated_at)
        VALUES ($1, $2, $3, 'active', $4, $5, $5)`,
		input.MembershipID, userID, input.OrganizationID, string(input.AdmissionSource), input.Now); err != nil {
		return application.ActivationResult{}, err
	}
	// The founder owns the organization by construction; nobody granted
	// it, so the row records no granting membership.
	if err := s.grantSpecs(ctx, tx, input.OrganizationID, assignmentWrite{
		Principal: domain.PrincipalMembership, PrincipalID: input.MembershipID,
		Source: domain.AssignmentManual, ActorType: domain.ActorHuman, ActorID: userID,
	}, []domain.AssignmentSpec{{RoleID: domain.RoleOwnerID, ScopeKind: domain.ScopeOrganization}}, input.Now); err != nil {
		return application.ActivationResult{}, err
	}
	// The entry pipeline exists from the first moment, empty, so every
	// request has a pipeline to start in.
	if err := s.ensureEntryPipeline(ctx, tx, input.OrganizationID, input.MembershipID, input.Now); err != nil {
		return application.ActivationResult{}, err
	}
	activated := map[string]string{
		"display_name": input.DisplayName, "email": input.Email,
		"admission_source": string(input.AdmissionSource),
	}
	if input.HasProviderIdentity() {
		activated["identity_provider"] = input.ProviderKey
	}
	data, _ := json.Marshal(activated)
	if err := s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: "organization.activated",
		ActorType: domain.ActorHuman, ActorID: userID,
		SubjectType: "organization", SubjectID: input.OrganizationID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return application.ActivationResult{}, err
	}
	grants, err := loadGrants(ctx, tx, input.OrganizationID, domain.PrincipalMembership, input.MembershipID)
	if err != nil {
		return application.ActivationResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return application.ActivationResult{}, err
	}
	return application.ActivationResult{
		Organization: domain.Organization{
			ID: input.OrganizationID, Slug: input.OrganizationSlug, Name: input.OrganizationName,
			Status: domain.OrganizationActive, CreatedAt: input.Now, UpdatedAt: input.Now,
		},
		Principal: domain.Principal{
			ActorType: domain.ActorHuman, ActorID: userID, OrganizationID: input.OrganizationID,
			MembershipID: input.MembershipID,
		}.WithGrants(grants),
	}, nil
}

// allowedStatusTransitions maps a target status to the statuses it may be
// reached from.
var allowedStatusTransitions = map[domain.OrganizationStatus][]string{
	domain.OrganizationActive:          {"suspended", "pending_deletion"},
	domain.OrganizationSuspended:       {"active"},
	domain.OrganizationPendingDeletion: {"active", "suspended"},
}

func (s *Store) SetOrganizationStatus(ctx context.Context, input application.SetOrganizationStatusInput) (domain.Organization, error) {
	if err := input.Validate(); err != nil {
		return domain.Organization{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domain.Organization{}, err
	}
	defer tx.Rollback(ctx)

	var current string
	err = tx.QueryRow(ctx, `SELECT status FROM organizations WHERE id = $1 FOR UPDATE`, input.OrganizationID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Organization{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Organization{}, err
	}
	allowed := false
	for _, from := range allowedStatusTransitions[input.Status] {
		if from == current {
			allowed = true
		}
	}
	if !allowed {
		return domain.Organization{}, fmt.Errorf("organization is %s, cannot become %s: %w", current, input.Status, domain.ErrConflict)
	}

	var query string
	switch input.Status {
	case domain.OrganizationSuspended:
		query = `UPDATE organizations SET status = 'suspended', status_reason = $2,
            suspended_at = $3, deletion_scheduled_for = NULL, updated_at = $3
            WHERE id = $1`
		if _, err := tx.Exec(ctx, query, input.OrganizationID, input.Reason, input.Now); err != nil {
			return domain.Organization{}, err
		}
	case domain.OrganizationPendingDeletion:
		query = `UPDATE organizations SET status = 'pending_deletion', status_reason = $2,
            deletion_scheduled_for = $3, updated_at = $4
            WHERE id = $1`
		if _, err := tx.Exec(ctx, query, input.OrganizationID, input.Reason, *input.DeletionScheduledFor, input.Now); err != nil {
			return domain.Organization{}, err
		}
	case domain.OrganizationActive:
		query = `UPDATE organizations SET status = 'active', status_reason = '',
            suspended_at = NULL, deletion_scheduled_for = NULL, updated_at = $2
            WHERE id = $1`
		if _, err := tx.Exec(ctx, query, input.OrganizationID, input.Now); err != nil {
			return domain.Organization{}, err
		}
	}

	eventType := map[domain.OrganizationStatus]string{
		domain.OrganizationSuspended:       "organization.suspended",
		domain.OrganizationPendingDeletion: "organization.deletion_scheduled",
	}[input.Status]
	if input.Status == domain.OrganizationActive {
		eventType = "organization.resumed"
		if current == "pending_deletion" {
			eventType = "organization.deletion_canceled"
		}
	}
	data, _ := json.Marshal(map[string]string{"reason": input.Reason, "previous_status": current})
	if err := s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: input.OrganizationID, EventType: eventType,
		ActorType: domain.ActorType(input.ActorType), ActorID: input.ActorID,
		SubjectType: "organization", SubjectID: input.OrganizationID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return domain.Organization{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Organization{}, err
	}
	return s.GetOrganization(ctx, application.GetOrganizationInput{OrganizationID: input.OrganizationID})
}

// purgeProtectiveTriggers are the append-only and last-owner triggers that
// would otherwise refuse the purge's deletes. They are disabled inside the
// purge transaction only; disabling takes an exclusive lock on each table
// for the duration of the purge, which is acceptable for a rare operation.
var purgeProtectiveTriggers = [][2]string{
	{"decisions", "decisions_append_only"},
	{"approval_request_agent_owner_handoffs", "approval_request_agent_owner_handoffs_append_only"},
	{"approval_request_routing", "approval_request_routing_append_only"},
	{"approval_request_reviewers", "approval_request_reviewers_append_only"},
	{"approval_request_notifications", "approval_request_notifications_append_only"},
	{"approval_request_routing_team_policies", "approval_request_routing_team_policies_append_only"},
	{"escalation_paths", "escalation_paths_protect_revision"},
	{"team_escalation_policies", "team_escalation_policies_protect_revision"},
	{"webhook_endpoint_revisions", "webhook_endpoint_revisions_protect_revision"},
	{"request_approval_blocks", "request_approval_blocks_append_only"},
	{"request_approval_pipelines", "request_approval_pipelines_immutable_revision"},
	{"role_assignments", "role_assignments_last_owner"},
	// membership_roles still exists (0025 only expands) and its copied
	// rows still reference memberships, so the legacy pair stays until
	// the table is dropped.
	{"membership_roles", "membership_roles_last_owner"},
}

// purgeStatements delete organization-scoped rows children-first. Users,
// external identities, and local credentials are global and survive.
var purgeStatements = []string{
	`DELETE FROM tool_catalog_tools WHERE organization_id = $1`,
	`DELETE FROM tool_catalog_sources WHERE organization_id = $1`,
	`DELETE FROM decision_operations WHERE organization_id = $1`,
	`DELETE FROM request_resolved_events WHERE organization_id = $1`,
	`DELETE FROM decisions WHERE organization_id = $1`,
	`DELETE FROM approval_request_pipeline_suspensions WHERE organization_id = $1`,
	`DELETE FROM approval_request_claims WHERE organization_id = $1`,
	`DELETE FROM approval_request_escalation_team_state WHERE organization_id = $1`,
	`DELETE FROM approval_request_escalation_state WHERE organization_id = $1`,
	`DELETE FROM approval_request_routing_team_policies WHERE organization_id = $1`,
	`DELETE FROM approval_request_agent_owner_handoffs WHERE organization_id = $1`,
	`DELETE FROM approval_request_routing WHERE organization_id = $1`,
	`DELETE FROM approval_request_reviewers WHERE organization_id = $1`,
	`DELETE FROM approval_request_notifications WHERE organization_id = $1`,
	`DELETE FROM slack_email_fallbacks WHERE organization_id = $1`,
	`DELETE FROM notification_holds WHERE organization_id = $1`,
	`DELETE FROM notification_windows WHERE organization_id = $1`,
	`DELETE FROM approval_requests WHERE organization_id = $1`,
	`DELETE FROM outbox_events WHERE organization_id = $1`,
	`DELETE FROM agent_enrollments WHERE organization_id = $1`,
	`DELETE FROM agent_credentials WHERE organization_id = $1`,
	`DELETE FROM agent_instance_provisioning_keys WHERE organization_id = $1`,
	`DELETE FROM agent_instance_update_keys WHERE organization_id = $1`,
	`DELETE FROM agent_provisioners WHERE organization_id = $1`,
	`DELETE FROM agent_instance_deliveries WHERE organization_id = $1`,
	`DELETE FROM agent_instances WHERE organization_id = $1`,
	`DELETE FROM agents WHERE organization_id = $1`,
	`DELETE FROM approval_request_pipeline_suspension_pins WHERE organization_id = $1`,
	`DELETE FROM request_approval_pipelines WHERE organization_id = $1`,
	`DELETE FROM request_approval_blocks WHERE organization_id = $1`,
	`DELETE FROM escalation_paths WHERE organization_id = $1`,
	`DELETE FROM team_escalation_policies WHERE organization_id = $1`,
	`DELETE FROM onboarding_sessions WHERE organization_id = $1`,
	`DELETE FROM sessions WHERE organization_id = $1`,
	`DELETE FROM invitations WHERE organization_id = $1`,
	`DELETE FROM directory_object_links WHERE organization_id = $1`,
	// Person-wide contact methods carry no organization and survive; only
	// the organization-scoped links, such as Slack identities, go.
	`DELETE FROM contact_methods WHERE organization_id = $1`,
	`DELETE FROM slack_install_states WHERE organization_id = $1`,
	`DELETE FROM slack_connections WHERE organization_id = $1`,
	`DELETE FROM discord_oauth_states WHERE organization_id = $1`,
	`DELETE FROM discord_connections WHERE organization_id = $1`,
	`DELETE FROM webhook_endpoint_revisions WHERE organization_id = $1`,
	`DELETE FROM webhook_endpoint_secrets WHERE organization_id = $1`,
	`DELETE FROM role_assignments WHERE organization_id = $1`,
	`DELETE FROM permission_policies WHERE organization_id = $1`,
	`DELETE FROM permission_policy_commands WHERE organization_id = $1`,
	`DELETE FROM membership_teams WHERE organization_id = $1`,
	`DELETE FROM membership_roles WHERE membership_id IN
        (SELECT id FROM memberships WHERE organization_id = $1)`,
	`DELETE FROM teams WHERE organization_id = $1`,
	`DELETE FROM management_actors WHERE organization_id = $1`,
	`DELETE FROM oauth_authorization_codes WHERE organization_id = $1`,
	`DELETE FROM api_key_creations WHERE organization_id = $1`,
	`DELETE FROM api_keys WHERE organization_id = $1`,
	`DELETE FROM memberships WHERE organization_id = $1`,
	`DELETE FROM roles WHERE organization_id = $1`,
	`DELETE FROM directory_connections WHERE organization_id = $1`,
	`DELETE FROM tool_presentations WHERE organization_id = $1`,
	`DELETE FROM organization_policies WHERE organization_id = $1`,
	`DELETE FROM organizations WHERE id = $1`,
}

// PurgeOrganization deletes every organization-scoped row in one
// transaction. It reports false without deleting anything when the
// organization is missing (already purged) or is not a pending deletion
// whose schedule has arrived, so a raced cancellation wins.
func (s *Store) PurgeOrganization(ctx context.Context, input application.PurgeOrganizationInput) (bool, error) {
	if err := input.Validate(); err != nil {
		return false, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	var eligible bool
	err = tx.QueryRow(ctx, `SELECT status = 'pending_deletion' AND deletion_scheduled_for <= $2
        FROM organizations WHERE id = $1 FOR UPDATE`, input.OrganizationID, input.Now).Scan(&eligible)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !eligible {
		return false, nil
	}
	for _, trigger := range purgeProtectiveTriggers {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s DISABLE TRIGGER %s`, trigger[0], trigger[1])); err != nil {
			return false, err
		}
	}
	// The edition's own rows go first: they reference the organization
	// and may sit behind protective triggers of their own.
	for _, hook := range s.purgeHooks {
		if err := hook.PurgeOrganization(ctx, tx, input.OrganizationID); err != nil {
			return false, err
		}
	}
	for _, statement := range purgeStatements {
		if _, err := tx.Exec(ctx, statement, input.OrganizationID); err != nil {
			return false, err
		}
	}
	for _, trigger := range purgeProtectiveTriggers {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`ALTER TABLE %s ENABLE TRIGGER %s`, trigger[0], trigger[1])); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
