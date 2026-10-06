package slackconnect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

var ErrCheckUnavailable = errors.New("could not check the Slack connection. Try connecting again")

func (s *Service) activeConnection(ctx context.Context, orgID domain.ID) (domain.SlackConnection, string, error) {
	connection, err := s.connections.GetSlackConnection(ctx, application.GetSlackConnectionInput{OrganizationID: orgID})
	if err != nil {
		return connection, "", err
	}
	if connection.Status != "connected" {
		return connection, "", domain.ErrNotFound
	}
	token, err := s.connections.OpenSlackBotToken(ctx, application.GetSlackConnectionInput{OrganizationID: orgID, InstallationID: connection.InstallationID})
	return connection, token, err
}

func (s *Service) disconnectRevoked(ctx context.Context, connection domain.SlackConnection) (bool, error) {
	changed, err := s.connections.DisconnectSlackConnection(ctx, application.DisconnectSlackConnectionInput{
		SlackConnectionVersion: application.SlackConnectionVersion{OrganizationID: connection.OrganizationID, InstallationID: connection.InstallationID},
		Reason:                 "token_revoked", Now: s.clock.Now(),
	})
	if err == nil && changed {
		s.dispatchEvents(ctx, "disconnection/"+string(connection.InstallationID))
	}
	return changed, err
}

func (s *Service) dispatchEvents(ctx context.Context, key string) {
	if s.dispatcher == nil {
		return
	}
	// Slack expects prompt acknowledgment. Failure here is safe: the accepted
	// event remains in the durable inbox and the minute drain will retry it.
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := s.dispatcher.DispatchSlackEvents(bounded, application.DispatchSlackEventsInput{Key: key}); err != nil {
		s.logger.WarnContext(ctx, "Slack lifecycle dispatch deferred to inbox drain", "error", err)
	}
}

// ReceiveEvent verifies the original bytes before decoding. The only persisted
// fields are normalized lifecycle metadata, never Slack's legacy token field.
func (s *Service) ReceiveEvent(ctx context.Context, timestamp, signature string, body []byte) (string, error) {
	if err := s.verifyRequest(timestamp, signature, body); err != nil {
		return "", err
	}
	var envelope struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		AppID     string `json:"api_app_id"`
		EventID   string `json:"event_id"`
		TeamID    string `json:"team_id"`
		EventTime int64  `json:"event_time"`
		Event     struct {
			Type   string `json:"type"`
			Tokens struct {
				Bot []string `json:"bot"`
			} `json:"tokens"`
		} `json:"event"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", domain.ErrInvalid
	}
	if envelope.Type == "url_verification" {
		if envelope.Challenge == "" {
			return "", domain.ErrInvalid
		}
		return envelope.Challenge, nil
	}
	if envelope.AppID != s.config.AppID {
		return "", domain.ErrUnauthenticated
	}
	if envelope.Type != "event_callback" || (envelope.Event.Type != "app_uninstalled" && envelope.Event.Type != "tokens_revoked") {
		return "", nil
	}
	if envelope.Event.Type == "tokens_revoked" && len(envelope.Event.Tokens.Bot) == 0 {
		return "", nil
	}
	if envelope.EventTime <= 0 {
		return "", domain.ErrInvalid
	}
	err := s.connections.InsertSlackEvent(ctx, application.SlackEvent{
		AppID: envelope.AppID, ExternalEventID: envelope.EventID, TeamID: envelope.TeamID, Type: envelope.Event.Type,
		BotUserIDs: envelope.Event.Tokens.Bot, OccurredAt: time.Unix(envelope.EventTime, 0), ReceivedAt: s.clock.Now(),
	})
	if err != nil {
		return "", err
	}
	s.dispatchEvents(ctx, "event/"+envelope.AppID+"/"+envelope.EventID)
	return "", nil
}

// DrainEvents runs as a retryable Temporal activity. One inconclusive workspace
// never prevents other received events from being applied.
func (s *Service) DrainEvents(ctx context.Context) error {
	events, err := s.connections.ListPendingSlackEvents(ctx, application.ListPendingSlackEventsInput{Limit: 1000})
	if err != nil {
		return err
	}
	var failures []error
	for _, event := range events {
		if err := s.applyEvent(ctx, event); err != nil {
			failures = append(failures, fmt.Errorf("Slack event %s: %w", event.ID, err))
			continue
		}
		if err := s.connections.CompleteSlackEvent(ctx, application.CompleteSlackEventInput{ID: event.ID, Now: s.clock.Now()}); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *Service) applyEvent(ctx context.Context, event application.SlackEvent) error {
	if event.AppID != s.config.AppID {
		return nil
	}
	connection, err := s.connections.FindSlackWorkspace(ctx, application.FindSlackWorkspaceInput{TeamID: event.TeamID})
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if event.Type == "tokens_revoked" && !slices.Contains(event.BotUserIDs, connection.BotUserID) {
		return nil
	}
	// Slack event times have second precision. A newer or same-second install
	// must be checked rather than trusting the event's workspace identity.
	if event.OccurredAt.Before(connection.InstalledAt.Truncate(time.Second)) {
		return nil
	}
	token, err := s.connections.OpenSlackBotToken(ctx, application.GetSlackConnectionInput{OrganizationID: connection.OrganizationID, InstallationID: connection.InstallationID})
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	_, checkErr := s.connector.CheckToken(ctx, application.SlackTokenInput{BotToken: token})
	if checkErr == nil {
		return nil
	}
	// Dispatch timestamps and bot IDs cannot distinguish a delayed removal
	// from a newer grant. An inconclusive check keeps the event pending, even
	// when its timestamp follows the local install, rather than clearing it.
	if !errors.Is(checkErr, application.ErrSlackRevoked) {
		return ErrCheckUnavailable
	}
	_, err = s.connections.DisconnectSlackConnection(ctx, application.DisconnectSlackConnectionInput{
		SlackConnectionVersion: application.SlackConnectionVersion{OrganizationID: connection.OrganizationID, InstallationID: connection.InstallationID},
		Reason:                 event.Type, Now: s.clock.Now(),
	})
	return err
}
