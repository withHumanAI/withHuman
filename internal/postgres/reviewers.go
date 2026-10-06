package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
	"github.com/withHumanAI/withHuman/internal/authorization"
)

// addedReviewerIDs lists the memberships added to a request's routing
// after it was pinned, for the decide and claim kernels.
func addedReviewerIDs(ctx context.Context, q routingQuerier, organizationID, requestID domain.ID) ([]domain.ID, error) {
	var members []string
	if err := q.QueryRow(ctx, `SELECT COALESCE(array_agg(membership_id::text ORDER BY created_at), '{}')
        FROM approval_request_reviewers WHERE organization_id = $1 AND request_id = $2`,
		organizationID, requestID).Scan(&members); err != nil {
		return nil, err
	}
	ids := make([]domain.ID, 0, len(members))
	for _, membershipID := range members {
		ids = append(ids, domain.ID(membershipID))
	}
	return ids, nil
}

// listAddedReviewers reads a request's added reviewers with both display
// names resolved, oldest first.
func listAddedReviewers(ctx context.Context, q querier, organizationID, requestID domain.ID) ([]domain.ApprovalReviewer, error) {
	rows, err := q.Query(ctx, `SELECT arr.membership_id::text, u.display_name, arr.added_by_membership_id::text, au.display_name, arr.created_at
        FROM approval_request_reviewers arr
        JOIN memberships m ON m.id = arr.membership_id AND m.organization_id = arr.organization_id
        JOIN users u ON u.id = m.user_id
        JOIN memberships am ON am.id = arr.added_by_membership_id AND am.organization_id = arr.organization_id
        JOIN users au ON au.id = am.user_id
        WHERE arr.organization_id = $1 AND arr.request_id = $2
        ORDER BY arr.created_at, arr.membership_id`, organizationID, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reviewers := make([]domain.ApprovalReviewer, 0)
	for rows.Next() {
		var membershipID, addedBy string
		reviewer := domain.ApprovalReviewer{RequestID: requestID}
		if err := rows.Scan(&membershipID, &reviewer.DisplayName, &addedBy, &reviewer.AddedByDisplayName, &reviewer.CreatedAt); err != nil {
			return nil, err
		}
		reviewer.MembershipID, reviewer.AddedByMembershipID = domain.ID(membershipID), domain.ID(addedBy)
		reviewers = append(reviewers, reviewer)
	}
	return reviewers, rows.Err()
}

// routingTargetsMembership reports whether a pinned routing snapshot names
// the membership: directly, through one of its teams, or as an added
// reviewer. The same rule the kernels apply, for the handoff check and the
// read model.
func routingTargetsMembership(membershipID domain.ID, userIDs, teamIDs, addedIDs, membershipTeams []domain.ID) bool {
	if slices.Contains(userIDs, membershipID) || slices.Contains(addedIDs, membershipID) {
		return true
	}
	for _, teamID := range teamIDs {
		if slices.Contains(membershipTeams, teamID) {
			return true
		}
	}
	return false
}

// AddApprovalReviewer widens a pending, routed request's routing by one
// membership (design doc 10, "Handoff"). Only someone the request was
// routed to, or a break-glass holder, may pull a colleague in, and the
// colleague must be an active member who may decide requests; the
// snapshot narrows who decides, it never grants. The row is append-only
// and the act is audited as request.reviewer_added.
func (s *Store) AddApprovalReviewer(ctx context.Context, input application.AddApprovalReviewerInput) (domain.ApprovalReviewer, error) {
	if err := input.Validate(); err != nil {
		return domain.ApprovalReviewer{}, err
	}
	var reviewer domain.ApprovalReviewer
	err := retrySerializable(func() error {
		var attemptError error
		reviewer, attemptError = s.addApprovalReviewer(ctx, input)
		return attemptError
	})
	if err != nil {
		return domain.ApprovalReviewer{}, err
	}
	return reviewer, nil
}

func (s *Store) addApprovalReviewer(ctx context.Context, input application.AddApprovalReviewerInput) (domain.ApprovalReviewer, error) {
	organizationID, requestID, actor := input.OrganizationID, input.RequestID, input.Actor
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return domain.ApprovalReviewer{}, err
	}
	defer tx.Rollback(ctx)

	var requestStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM approval_requests WHERE organization_id = $1 AND id = $2 FOR UPDATE`,
		organizationID, requestID).Scan(&requestStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ApprovalReviewer{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ApprovalReviewer{}, err
	}
	if domain.ApprovalStatus(requestStatus) != domain.ApprovalPending {
		return domain.ApprovalReviewer{}, fmt.Errorf("this request is no longer pending: %w", domain.ErrConflict)
	}
	pinned, escalated, err := readPinnedRouting(ctx, tx, organizationID, requestID)
	if err != nil {
		return domain.ApprovalReviewer{}, err
	}
	if !escalated && pinned.agentOwnerID == "" {
		return domain.ApprovalReviewer{}, fmt.Errorf("this request was not sent to anyone yet: %w", domain.ErrConflict)
	}
	// A path that reaches nobody for this request still scopes it: only a
	// break-glass holder passes the check below, and pulling someone in
	// is exactly what they need to do.
	userIDs, teamIDs, err := pinnedTargets(pinned)
	if err != nil {
		return domain.ApprovalReviewer{}, err
	}
	added, err := addedReviewerIDs(ctx, tx, organizationID, requestID)
	if err != nil {
		return domain.ApprovalReviewer{}, err
	}
	actorTeams, err := membershipTeamIDs(ctx, tx, organizationID, actor.MembershipID)
	if err != nil {
		return domain.ApprovalReviewer{}, err
	}
	resource := domain.RequestResource(requestID)
	if !routingTargetsMembership(actor.MembershipID, userIDs, teamIDs, added, actorTeams) &&
		authorization.Check(actor, domain.PermissionRequestDecideUnrouted, resource) != nil {
		return domain.ApprovalReviewer{}, fmt.Errorf("only reviewers this request was sent to can add reviewers: %w", domain.ErrForbidden)
	}

	var memberStatus, memberName string
	err = tx.QueryRow(ctx, `SELECT m.status, u.display_name FROM memberships m JOIN users u ON u.id = m.user_id
        WHERE m.organization_id = $1 AND m.id = $2`, organizationID, input.MembershipID).Scan(&memberStatus, &memberName)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && domain.MembershipStatus(memberStatus) != domain.MembershipActive) {
		return domain.ApprovalReviewer{}, fmt.Errorf("this member is not active: %w", domain.ErrInvalid)
	}
	if err != nil {
		return domain.ApprovalReviewer{}, err
	}
	grants, err := loadGrants(ctx, tx, organizationID, domain.PrincipalMembership, input.MembershipID)
	if err != nil {
		return domain.ApprovalReviewer{}, err
	}
	if authorization.Check(domain.Principal{Grants: grants}, domain.PermissionRequestDecide, resource) != nil {
		return domain.ApprovalReviewer{}, fmt.Errorf("this member cannot decide requests: %w", domain.ErrInvalid)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO approval_request_reviewers
        (request_id, organization_id, membership_id, added_by_membership_id, created_at)
        VALUES ($1, $2, $3, $4, $5)`,
		requestID, organizationID, input.MembershipID, actor.MembershipID, input.Now); err != nil {
		if isUniqueViolation(err) {
			return domain.ApprovalReviewer{}, fmt.Errorf("this member is already a reviewer: %w", domain.ErrConflict)
		}
		return domain.ApprovalReviewer{}, err
	}
	// A handoff is the request brought to the colleague's attention: it
	// lands in their queue's default layer at once, ahead of any channel.
	if err := recordApprovalNotifications(ctx, tx, organizationID, requestID, []domain.ID{input.MembershipID}, input.Now); err != nil {
		return domain.ApprovalReviewer{}, err
	}
	var actorName string
	if err := tx.QueryRow(ctx, `SELECT u.display_name FROM memberships m JOIN users u ON u.id = m.user_id
        WHERE m.organization_id = $1 AND m.id = $2`, organizationID, actor.MembershipID).Scan(&actorName); err != nil {
		return domain.ApprovalReviewer{}, err
	}
	actorID, err := membershipActorID(ctx, tx, organizationID, actor.MembershipID)
	if err != nil {
		return domain.ApprovalReviewer{}, err
	}
	data, _ := json.Marshal(map[string]any{
		"membership_id": input.MembershipID, "added_by_membership_id": actor.MembershipID,
	})
	if err := s.appendAuditEvent(ctx, tx, domain.AuditEvent{
		OrganizationID: organizationID, EventType: "request.reviewer_added",
		ActorType: domain.ActorHuman, ActorID: actorID,
		SubjectType: "approval_request", SubjectID: requestID,
		Data: data, OccurredAt: input.Now,
	}); err != nil {
		return domain.ApprovalReviewer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ApprovalReviewer{}, err
	}
	return domain.ApprovalReviewer{
		RequestID: requestID, MembershipID: input.MembershipID, DisplayName: memberName,
		AddedByMembershipID: actor.MembershipID, AddedByDisplayName: actorName, CreatedAt: input.Now,
	}, nil
}

// GetApprovalRoutingState answers how a request's routing relates to one
// membership, for the review view: whether the pinned snapshot names
// targets, whether this membership is among them, and who was added.
func (s *Store) GetApprovalRoutingState(ctx context.Context, input application.GetApprovalRoutingStateInput) (application.ApprovalRoutingState, error) {
	if err := input.Validate(); err != nil {
		return application.ApprovalRoutingState{}, err
	}
	reviewers, err := listAddedReviewers(ctx, s.pool, input.OrganizationID, input.RequestID)
	if err != nil {
		return application.ApprovalRoutingState{}, err
	}
	state := application.ApprovalRoutingState{AddedReviewers: reviewers}
	pinned, escalated, err := readPinnedRouting(ctx, s.pool, input.OrganizationID, input.RequestID)
	if err != nil || (!escalated && pinned.agentOwnerID == "") {
		return state, err
	}
	userIDs, teamIDs, err := pinnedTargets(pinned)
	if err != nil {
		return application.ApprovalRoutingState{}, err
	}
	if input.MembershipID == "" {
		state.HasTargets = true
		return state, nil
	}
	teams, err := membershipTeamIDs(ctx, s.pool, input.OrganizationID, input.MembershipID)
	if err != nil {
		return application.ApprovalRoutingState{}, err
	}
	added := make([]domain.ID, 0, len(reviewers))
	for _, reviewer := range reviewers {
		added = append(added, reviewer.MembershipID)
	}
	state.HasTargets = true
	state.Targeted = routingTargetsMembership(input.MembershipID, userIDs, teamIDs, added, teams)
	return state, nil
}

// visibleApprovals keeps the candidates a membership may see under the
// routed_only visibility policy, which is also the set it may decide
// outright: requests no path matched (unless hideUnrouted says the
// membership may not take those), requests it was added to, and requests
// whose pinned snapshot targets it directly or through one of its teams.
// The snapshots are read in one query and the document test runs in Go,
// since the queue is capped at 200 rows.
func (s *Store) visibleApprovals(ctx context.Context, organizationID, membershipID domain.ID, hideUnrouted bool, candidates []domain.ApprovalRequest) ([]domain.ApprovalRequest, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}
	teams, err := membershipTeamIDs(ctx, s.pool, organizationID, membershipID)
	if err != nil {
		return nil, err
	}
	var addedTo []string
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(array_agg(request_id::text), '{}')
        FROM approval_request_reviewers WHERE organization_id = $1 AND membership_id = $2`,
		organizationID, membershipID).Scan(&addedTo); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT arr.request_id::text, arr.outcome, COALESCE(ep.document, 'null'::jsonb), arr.facts,
        COALESCE(arr.agent_owner_membership_id::text, ''), h.request_id IS NOT NULL
        FROM approval_request_routing arr
        LEFT JOIN escalation_paths ep ON ep.id = arr.escalation_path_revision_id AND ep.organization_id = arr.organization_id
        LEFT JOIN approval_request_agent_owner_handoffs h ON h.request_id = arr.request_id AND h.organization_id = arr.organization_id
        WHERE arr.organization_id = $1 AND arr.request_id = ANY($2::uuid[])`,
		organizationID, idStrings(approvalIDsOf(candidates)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	snapshots := make(map[domain.ID]pinnedRouting)
	for rows.Next() {
		var requestID, outcome, ownerID string
		var handedOff bool
		var pinned pinnedRouting
		if err := rows.Scan(&requestID, &outcome, &pinned.document, &pinned.facts, &ownerID, &handedOff); err != nil {
			return nil, err
		}
		owner, escalated := effectiveRouting(outcome, ownerID, handedOff, pinned.document)
		if !escalated && owner == "" {
			continue
		}
		pinned.agentOwnerID = owner
		snapshots[domain.ID(requestID)] = pinned
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	visible := make([]domain.ApprovalRequest, 0, len(candidates))
	for _, candidate := range candidates {
		pinned, routed := snapshots[candidate.ID]
		if slices.Contains(addedTo, string(candidate.ID)) || (!routed && !hideUnrouted) {
			visible = append(visible, candidate)
			continue
		}
		if !routed {
			continue
		}
		userIDs, teamIDs, err := pinnedTargets(pinned)
		if err != nil {
			return nil, err
		}
		if routingTargetsMembership(membershipID, userIDs, teamIDs, nil, teams) {
			visible = append(visible, candidate)
		}
	}
	return visible, nil
}

func approvalIDsOf(requests []domain.ApprovalRequest) []domain.ID {
	ids := make([]domain.ID, len(requests))
	for index, request := range requests {
		ids[index] = request.ID
	}
	return ids
}

// ListReviewerCandidates lists active members holding request.decide who
// are not yet reviewers of the request, for the handoff picker. It is
// gated by request.decide on the request, not by membership.read, because
// a targeted reviewer without the members page still needs to hand off.
func (s *Store) ListReviewerCandidates(ctx context.Context, input application.ListReviewerCandidatesInput) ([]application.ReviewerCandidate, error) {
	if err := input.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT m.id::text, u.display_name, u.primary_email
        FROM memberships m JOIN users u ON u.id = m.user_id
        WHERE m.organization_id = $1 AND m.status = 'active' AND u.status = 'active'
          AND EXISTS (SELECT 1 FROM role_assignments ra JOIN roles r ON r.id = ra.role_id
              WHERE ra.organization_id = m.organization_id AND ra.scope_kind = 'organization'
                AND $3 = ANY(r.permissions) AND `+membershipPrincipalSQL("m.id")+`)
          AND NOT EXISTS (SELECT 1 FROM approval_request_reviewers arr
              WHERE arr.organization_id = m.organization_id AND arr.request_id = $2 AND arr.membership_id = m.id)
        ORDER BY u.display_name, u.primary_email`, input.OrganizationID, input.RequestID, string(domain.PermissionRequestDecide))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := make([]application.ReviewerCandidate, 0)
	for rows.Next() {
		var candidate application.ReviewerCandidate
		var membershipID string
		if err := rows.Scan(&membershipID, &candidate.DisplayName, &candidate.Email); err != nil {
			return nil, err
		}
		candidate.MembershipID = domain.ID(membershipID)
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}
