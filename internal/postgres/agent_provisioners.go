package postgres

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

func (s *Store) CreateAgentProvisioner(ctx context.Context, input application.CreateAgentProvisionerInput) (domain.AgentProvisioner, error) {
	if err := input.Validate(); err != nil {
		return domain.AgentProvisioner{}, err
	}
	organizationID, agentID, membershipID := input.OrganizationID, input.AgentID, input.ActorID
	record, now := input.Provisioner, input.Now
	provisioner := domain.AgentProvisioner{
		ID: record.ID, OrganizationID: organizationID, AgentID: agentID,
		CreatedByActorID: membershipID, IssuedAt: record.IssuedAt, ExpiresAt: record.ExpiresAt,
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentProvisioner{}, err
	}
	defer tx.Rollback(ctx)
	var status, kind string
	if err := tx.QueryRow(ctx, `SELECT status, slug, kind FROM agents WHERE organization_id = $1 AND id = $2 FOR SHARE`, organizationID, agentID).Scan(&status, &provisioner.AgentSlug, &kind); errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentProvisioner{}, domain.ErrNotFound
	} else if err != nil {
		return domain.AgentProvisioner{}, err
	} else if domain.AgentStatus(status) != domain.AgentActive {
		return domain.AgentProvisioner{}, domain.ErrAgentInactive
	}
	// A personal agent's instances belong to people, so there is no fleet
	// to mint for and no token that could name an owner.
	if domain.AgentKind(kind) == domain.AgentPersonal {
		return domain.AgentProvisioner{}, domain.ErrPersonalAgent
	}
	if _, err := tx.Exec(ctx, `INSERT INTO agent_provisioners
        (id, organization_id, agent_id, created_by_actor_id, secret_hash, issued_at, expires_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7)`, record.ID, organizationID, agentID, membershipID,
		record.SecretHash, record.IssuedAt, record.ExpiresAt); err != nil {
		return domain.AgentProvisioner{}, err
	}
	data, _ := json.Marshal(map[string]any{"agent_slug": provisioner.AgentSlug, "expires_at": record.ExpiresAt})
	if err := s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{OrganizationID: organizationID, EventType: "agent_provisioner.created", ActorType: domain.ActorHuman, ActorID: membershipID, SubjectType: "agent_provisioner", SubjectID: provisioner.ID, Data: data, OccurredAt: now}); err != nil {
		return domain.AgentProvisioner{}, err
	}

	return provisioner, tx.Commit(ctx)
}

func (s *Store) MintAgentInstance(ctx context.Context, input application.MintAgentInstanceInput) (domain.AgentInstance, error) {
	if err := input.Validate(); err != nil {
		return domain.AgentInstance{}, err
	}
	provisionerID, secretHash, credential, now := input.ProvisionerID, input.SecretHash, input.Credential, input.Now
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.AgentInstance{}, err
	}
	defer tx.Rollback(ctx)
	var organizationID, agentID, agentSlug, agentStatus, agentKind string
	var storedHash []byte
	err = tx.QueryRow(ctx, `SELECT p.organization_id::text, p.agent_id::text, a.slug, p.secret_hash, a.status, a.kind
      FROM agent_provisioners p
      JOIN agents a ON a.id = p.agent_id AND a.organization_id = p.organization_id
      JOIN organizations o ON o.id = p.organization_id
     WHERE p.id = $1 AND p.revoked_at IS NULL AND p.expires_at > $2 AND o.status = 'active'
       FOR SHARE OF p, a`, provisionerID, now).Scan(&organizationID, &agentID, &agentSlug, &storedHash, &agentStatus, &agentKind)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentInstance{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return domain.AgentInstance{}, err
	}
	if subtle.ConstantTimeCompare(storedHash, secretHash) != 1 {
		return domain.AgentInstance{}, domain.ErrUnauthenticated
	}
	if domain.AgentStatus(agentStatus) != domain.AgentActive {
		return domain.AgentInstance{}, domain.ErrAgentInactive
	}
	if domain.AgentKind(agentKind) == domain.AgentPersonal {
		return domain.AgentInstance{}, domain.ErrPersonalAgent
	}
	instance := domain.AgentInstance{
		ID: input.InstanceID, OrganizationID: domain.ID(organizationID), AgentID: domain.ID(agentID),
		AgentSlug: domain.AgentSlug(agentSlug),
		Name:      input.InstanceName, Status: domain.AgentActive, Metadata: input.Metadata, CreatedAt: now, LastSeenAt: now,
	}
	if instance.Name == "" {
		instance.Name = "instance-" + string(instance.ID)[:8]
	}
	command, err := tx.Exec(ctx, `INSERT INTO agent_instances
        (id, organization_id, agent_id, name, status, metadata, created_at, last_seen_at)
        VALUES ($1, $2, $3, $4, 'active', $5, $6, $6)
        ON CONFLICT (agent_id, name) DO NOTHING`, instance.ID, instance.OrganizationID, instance.AgentID,
		instance.Name, instance.Metadata, now)
	if err != nil {
		return domain.AgentInstance{}, err
	}
	if command.RowsAffected() == 0 {
		return domain.AgentInstance{}, fmt.Errorf("agent instance name already exists: %w", domain.ErrConflict)
	}
	if err := insertAgentCredential(ctx, tx, instance.OrganizationID, instance.ID, credential); err != nil {
		return domain.AgentInstance{}, err
	}
	if delivery := input.Delivery; delivery != nil {
		if _, err := tx.Exec(ctx, `INSERT INTO agent_instance_deliveries
			(agent_instance_id, organization_id, type, url, registration_id, sealed_signing_secret, verified_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, instance.ID, instance.OrganizationID, application.InstanceDeliveryWebhook,
			delivery.URL, delivery.RegistrationID, delivery.SealedSigningSecret, delivery.VerifiedAt); err != nil {
			return domain.AgentInstance{}, err
		}
		instance.Delivery = &domain.AgentInstanceDelivery{
			Type: application.InstanceDeliveryWebhook, URL: delivery.URL, RegistrationID: delivery.RegistrationID,
		}
	}
	if err := completeProvisioningKey(ctx, tx, provisionerID, input.IdempotencyKey, instance.ID, now); err != nil {
		return domain.AgentInstance{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_provisioners SET last_used_at = $2 WHERE id = $1`, provisionerID, now); err != nil {
		return domain.AgentInstance{}, err
	}
	data, _ := json.Marshal(map[string]any{"provisioner_id": provisionerID, "name": instance.Name, "delivery": instance.Delivery})
	if err := s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: instance.OrganizationID, EventType: "agent_instance.minted",
		ActorType: domain.ActorAgent, ActorID: instance.AgentID,
		SubjectType: "agent_instance", SubjectID: instance.ID,
		Data: data, OccurredAt: now,
	}); err != nil {
		return domain.AgentInstance{}, err
	}
	return instance, tx.Commit(ctx)
}

// AuthenticateAgentProvisioner answers the provisioner a live token
// belongs to without recording a use. Minting checks the token again in
// its own transaction; this read only gates work that must happen first.
func (s *Store) AuthenticateAgentProvisioner(ctx context.Context, input application.AuthenticateAgentProvisionerInput) (domain.AgentProvisioner, error) {
	if err := input.Validate(); err != nil {
		return domain.AgentProvisioner{}, err
	}
	var provisioner domain.AgentProvisioner
	var storedHash []byte
	var agentStatus string
	err := s.pool.QueryRow(ctx, `SELECT p.id::text, p.organization_id::text, p.agent_id::text, a.slug, p.secret_hash, a.status
      FROM agent_provisioners p
      JOIN agents a ON a.id = p.agent_id AND a.organization_id = p.organization_id
      JOIN organizations o ON o.id = p.organization_id
     WHERE p.id = $1 AND p.revoked_at IS NULL AND p.expires_at > $2 AND o.status = 'active'`, input.ProvisionerID, input.Now).
		Scan(&provisioner.ID, &provisioner.OrganizationID, &provisioner.AgentID, &provisioner.AgentSlug, &storedHash, &agentStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AgentProvisioner{}, domain.ErrUnauthenticated
	}
	if err != nil {
		return domain.AgentProvisioner{}, err
	}
	if subtle.ConstantTimeCompare(storedHash, input.SecretHash) != 1 {
		return domain.AgentProvisioner{}, domain.ErrUnauthenticated
	}
	if domain.AgentStatus(agentStatus) != domain.AgentActive {
		return domain.AgentProvisioner{}, domain.ErrAgentInactive
	}
	return provisioner, nil
}

func (s *Store) RevokeAgentProvisioner(ctx context.Context, input application.RevokeAgentProvisionerInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var revoked bool
	err = tx.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM agent_provisioners WHERE organization_id=$1 AND id=$2 FOR UPDATE`, input.OrganizationID, input.ProvisionerID).Scan(&revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if revoked {
		return nil
	}
	if _, err = tx.Exec(ctx, `UPDATE agent_provisioners SET revoked_at=$3 WHERE organization_id=$1 AND id=$2`, input.OrganizationID, input.ProvisionerID, input.Now); err != nil {
		return err
	}
	if err = s.appendManagementAuditEvent(ctx, tx, domain.AuditEvent{OrganizationID: input.OrganizationID, EventType: "agent_provisioner.revoked", ActorType: domain.ActorHuman, ActorID: input.ActorID, SubjectType: "agent_provisioner", SubjectID: input.ProvisionerID, Data: json.RawMessage(`{}`), OccurredAt: input.Now}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
