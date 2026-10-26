package application

import (
	"errors"
	"testing"
	"time"

	"github.com/withHumanAI/withHuman/domain"
)

// A personal API key acts as its member but never signs in, so its principal
// carries the key's id (CredentialID) and no session id. The documented
// contract is that such a key may decide where its member may.
func decisionCommandWithAuth(auth domain.AuthContext) domain.DecisionCommand {
	return domain.DecisionCommand{
		OperationID:    "operation-1",
		RequestID:      "request-1",
		OrganizationID: "organization-1",
		Principal: domain.Principal{
			ActorType:      domain.ActorHuman,
			ActorID:        "user-1",
			OrganizationID: "organization-1",
			MembershipID:   "membership-1",
			Auth:           auth,
		},
		Status:         domain.ApprovalApproved,
		Channel:        "web",
		IdempotencyKey: "decision-key-1",
	}
}

func TestSubmitDecisionAcceptsPersonalAPIKey(t *testing.T) {
	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	command := decisionCommandWithAuth(domain.AuthContext{
		Method: domain.AuthMethodAPIKey, Assurance: "single_factor", AuthenticatedAt: now, CredentialID: "key-1",
	})
	if err := (SubmitDecisionInput{Command: command}).Validate(); err != nil {
		t.Fatalf("personal API key decision rejected: %v", err)
	}
}

func TestSubmitDecisionRejectsAPIKeyWithoutKeyID(t *testing.T) {
	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	command := decisionCommandWithAuth(domain.AuthContext{
		Method: domain.AuthMethodAPIKey, Assurance: "single_factor", AuthenticatedAt: now,
	})
	if err := (SubmitDecisionInput{Command: command}).Validate(); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("API key decision without a key id: got %v, want ErrInvalid", err)
	}
}

func TestSubmitDecisionStillRequiresSessionForSignedInMember(t *testing.T) {
	now := time.Date(2026, time.October, 26, 0, 0, 0, 0, time.UTC)
	command := decisionCommandWithAuth(domain.AuthContext{
		Method: "local_password", Assurance: "single_factor", AuthenticatedAt: now, CredentialID: "credential-1",
	})
	if err := (SubmitDecisionInput{Command: command}).Validate(); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("session decision without a session id: got %v, want ErrInvalid", err)
	}
}
