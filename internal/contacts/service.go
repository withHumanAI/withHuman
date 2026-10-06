// Package contacts owns the addresses a person adds beyond the email their
// account already carries: adding one, proving it is theirs, and removing
// it. Everything here is self-service, so the only authorization is that
// you are acting on your own account.
package contacts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"math/big"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/withHumanAI/withHuman/application"
	"github.com/withHumanAI/withHuman/domain"
)

const (
	// DefaultChallengeTTL is how long a verification code stays usable.
	// Long enough to switch to a mail client, short enough that a code
	// left in an inbox stops working.
	DefaultChallengeTTL = 15 * time.Minute
	// MaxVerificationAttempts bounds guessing against one code. A wrong
	// answer costs an attempt rather than the whole challenge, so a
	// mistyped digit does not force a new email.
	MaxVerificationAttempts = 5
	codeDigits              = 6
)

type Config struct {
	ChallengeTTL time.Duration
}

type Service struct {
	contacts application.ContactMethodRepository
	// adapters are the edition's kinds, keyed by kind.
	adapters      map[domain.ContactMethodKind]application.ContactKindAdapter
	rules         application.NotificationRuleRepository
	identities    application.IdentityRepository
	notifications application.NotificationDispatcher
	clock         application.Clock
	logger        *slog.Logger
	config        Config
}

// RegisterKind installs an edition kind's adapter; the kind itself must
// be registered with domain.RegisterContactKinds.
func (s *Service) RegisterKind(adapter application.ContactKindAdapter) {
	if s.adapters == nil {
		s.adapters = map[domain.ContactMethodKind]application.ContactKindAdapter{}
	}
	s.adapters[adapter.Kind] = adapter
}

func NewService(contacts application.ContactMethodRepository, rules application.NotificationRuleRepository, identities application.IdentityRepository, notifications application.NotificationDispatcher, clock application.Clock, logger *slog.Logger, config Config) *Service {
	if config.ChallengeTTL <= 0 {
		config.ChallengeTTL = DefaultChallengeTTL
	}
	return &Service{contacts: contacts, rules: rules, identities: identities, notifications: notifications, clock: clock, logger: logger, config: config}
}

// Ladders is a person's two notification ladders as the page shows them:
// their own rungs, or the computed defaults when they have not shaped one.
type Ladders struct {
	Standard   []application.NotificationRung
	Interrupt  []application.NotificationRung
	Customized map[domain.NotificationUrgency]bool
}

// Rules returns both ladders for the calling person in this organization.
// Defaults are computed from the methods they hold here, so what the page
// shows is exactly what a request would do.
func (s *Service) Rules(ctx context.Context, principal domain.Principal) (Ladders, error) {
	if principal.ActorType != domain.ActorHuman {
		return Ladders{}, domain.ErrForbidden
	}
	methods, err := s.List(ctx, principal)
	if err != nil {
		return Ladders{}, err
	}
	rules, err := s.rules.ListNotificationRules(ctx, application.ListNotificationRulesInput{UserID: principal.ActorID})
	if err != nil {
		return Ladders{}, err
	}
	ladders := Ladders{Customized: map[domain.NotificationUrgency]bool{}}
	for _, urgency := range []domain.NotificationUrgency{domain.NotificationStandard, domain.NotificationInterrupt} {
		var own []application.NotificationRung
		for _, rule := range rules {
			if rule.Urgency == urgency {
				own = append(own, application.NotificationRung{Delay: rule.Delay, ContactMethodID: rule.ContactMethodID})
			}
		}
		ladders.Customized[urgency] = len(own) > 0
		if len(own) == 0 {
			own = defaultRungs(urgency, methods)
		}
		if urgency == domain.NotificationStandard {
			ladders.Standard = own
		} else {
			ladders.Interrupt = own
		}
	}
	return ladders, nil
}

// SaveRules replaces one ladder. Every rung must name a method the
// person holds; the store enforces it so a rule can never route through
// an address that is not theirs.
func (s *Service) SaveRules(ctx context.Context, principal domain.Principal, urgency domain.NotificationUrgency, rungs []application.NotificationRung) (Ladders, error) {
	if principal.ActorType != domain.ActorHuman {
		return Ladders{}, domain.ErrForbidden
	}
	if _, err := s.rules.ReplaceNotificationRules(ctx, application.ReplaceNotificationRulesInput{
		UserID: principal.ActorID, Urgency: urgency, Rungs: rungs, Now: s.clock.Now(),
	}); err != nil {
		return Ladders{}, err
	}
	return s.Rules(ctx, principal)
}

// defaultRungs mirrors the plan builder's defaults for display: every
// kind the edition marked immediate (an installed app) is told at once;
// standard also goes to the account email
// at once; interrupt goes to the person's chat method at once (Slack
// before Discord, in domain.ChatContactKinds order) and adds email after
// five minutes, or straight to email when unlinked.
func defaultRungs(urgency domain.NotificationUrgency, methods []domain.ContactMethod) []application.NotificationRung {
	var rungs []application.NotificationRung
	for _, method := range methods {
		if method.Kind.Immediate() && method.Available() {
			rungs = append(rungs, application.NotificationRung{Delay: 0, ContactMethodID: method.ID})
		}
	}
	if urgency == domain.NotificationInterrupt {
		for _, kind := range domain.ChatContactKinds {
			for _, method := range methods {
				if method.Kind == kind && method.Available() {
					return append(rungs,
						application.NotificationRung{Delay: 0, ContactMethodID: method.ID},
						application.NotificationRung{Delay: 5 * time.Minute})
				}
			}
		}
	}
	return append(rungs, application.NotificationRung{Delay: 0})
}

// List returns the account's own address first, marked as primary, then
// every address the person added. The primary is synthesized rather than
// stored: it is the sign-in identity, so duplicating it here would give it
// two sources of truth.
func (s *Service) List(ctx context.Context, principal domain.Principal) ([]domain.ContactMethod, error) {
	if principal.ActorType != domain.ActorHuman {
		return nil, domain.ErrForbidden
	}
	user, err := s.identities.GetUser(ctx, application.GetUserInput{UserID: principal.ActorID})
	if err != nil {
		return nil, err
	}
	added, err := s.contacts.ListContactMethods(ctx, application.ListContactMethodsInput{UserID: principal.ActorID, OrganizationID: principal.OrganizationID})
	if err != nil {
		return nil, err
	}
	verifiedAt := user.CreatedAt
	methods := []domain.ContactMethod{{
		ID: "primary", UserID: user.ID, Kind: domain.ContactEmail, Value: user.PrimaryEmail,
		VerifiedAt: &verifiedAt, Primary: true, CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt,
	}}
	return append(methods, added...), nil
}

// Add records a new address or number and sends it a code. It is not
// delivered to until somebody answers that code, so adding a typo costs
// one message to whoever owns it and nothing more. Phone kinds also
// answer to the code-send budget, since every text costs money before
// anything is proved, and require the person's express consent to be
// messaged at the number: carriers audit that consent was asked for,
// unchecked by default, before a single message goes out.
// Challenge is an unverified method together with the way its code
// travels. Email and text message codes are delivered and the URL is
// empty; a WhatsApp number's code is carried by the person themselves,
// in a message the link prefills, so the URL is the only copy of it.
type Challenge struct {
	Method domain.ContactMethod
	// Extras are what the kind's adapter hands the person beside the
	// method, carried to the response as they are (the hosted WhatsApp
	// kind's confirmation link and its QR code).
	Extras map[string]string
}

func (s *Service) Add(ctx context.Context, principal domain.Principal, kind domain.ContactMethodKind, value string, consented bool) (Challenge, error) {
	if principal.ActorType != domain.ActorHuman {
		return Challenge{}, domain.ErrForbidden
	}
	if kind.RequiresConsent() && !consented {
		return Challenge{}, fmt.Errorf("agree to receive messages at this address first: %w", domain.ErrInvalid)
	}
	address, err := s.normalize(kind, value)
	if err != nil {
		return Challenge{}, err
	}
	now := s.clock.Now()
	if kind == domain.ContactEmail {
		user, err := s.identities.GetUser(ctx, application.GetUserInput{UserID: principal.ActorID})
		if err != nil {
			return Challenge{}, err
		}
		if strings.EqualFold(address, user.PrimaryEmail) {
			return Challenge{}, fmt.Errorf("that is already your account address: %w", domain.ErrConflict)
		}
	} else if err := s.codeBudget(ctx, kind, principal.ActorID, "", now); err != nil {
		return Challenge{}, err
	}
	code, codeHash, err := mintCode()
	if err != nil {
		return Challenge{}, err
	}
	method, err := s.contacts.CreateContactMethod(ctx, application.CreateContactMethodInput{
		MethodID: domain.ID(uuid.NewString()), UserID: principal.ActorID,
		Kind: kind, Value: address, CodeHash: codeHash,
		ExpiresAt: now.Add(s.config.ChallengeTTL), Now: now,
	})
	if err != nil {
		return Challenge{}, err
	}
	return s.challenge(ctx, method, code, now.Add(s.config.ChallengeTTL)), nil
}

// AddVouched records a person-wide method that needs no code because
// something other than its owner vouched for it: the hosted phone apps
// register the installed app this way, the session on the phone being
// the proof. The same value again refreshes the label.
func (s *Service) AddVouched(ctx context.Context, principal domain.Principal, kind domain.ContactMethodKind, value, label string) (domain.ContactMethod, error) {
	if principal.ActorType != domain.ActorHuman {
		return domain.ContactMethod{}, domain.ErrForbidden
	}
	return s.contacts.CreateVouchedContactMethod(ctx, application.CreateVouchedContactMethodInput{
		MethodID: domain.ID(uuid.NewString()), UserID: principal.ActorID, Kind: kind,
		Value: strings.TrimSpace(value), Label: strings.TrimSpace(label), Now: s.clock.Now(),
	})
}

// RemoveByValue removes the person's method of a kind by its value, for
// a caller that knows the value rather than the id (a phone signing out
// names its installation identifier). An absent value removes nothing.
func (s *Service) RemoveByValue(ctx context.Context, principal domain.Principal, kind domain.ContactMethodKind, value string) error {
	if principal.ActorType != domain.ActorHuman {
		return domain.ErrForbidden
	}
	methods, err := s.contacts.ListContactMethods(ctx, application.ListContactMethodsInput{UserID: principal.ActorID, OrganizationID: principal.OrganizationID})
	if err != nil {
		return err
	}
	for _, method := range methods {
		if method.Kind == kind && strings.EqualFold(method.Value, strings.TrimSpace(value)) {
			return s.Remove(ctx, principal, method.ID)
		}
	}
	return nil
}

// Resend rotates the challenge, which invalidates the previous code and
// restores the attempt budget.
func (s *Service) Resend(ctx context.Context, principal domain.Principal, methodID domain.ID) (Challenge, error) {
	if principal.ActorType != domain.ActorHuman {
		return Challenge{}, domain.ErrForbidden
	}
	now := s.clock.Now()
	// The budget is checked before the challenge rotates, so a refused
	// resend leaves the code already on its way still valid.
	if kind, err := s.methodKind(ctx, principal, methodID); err != nil {
		return Challenge{}, err
	} else if err := s.codeBudget(ctx, kind, principal.ActorID, methodID, now); err != nil {
		return Challenge{}, err
	}
	code, codeHash, err := mintCode()
	if err != nil {
		return Challenge{}, err
	}
	method, err := s.contacts.SetContactMethodChallenge(ctx, application.SetContactMethodChallengeInput{
		MethodID: methodID, UserID: principal.ActorID, CodeHash: codeHash,
		ExpiresAt: now.Add(s.config.ChallengeTTL), Now: now,
	})
	if err != nil {
		return Challenge{}, err
	}
	return s.challenge(ctx, method, code, now.Add(s.config.ChallengeTTL)), nil
}

// challenge delivers the code and packages what the person needs next.
func (s *Service) challenge(ctx context.Context, method domain.ContactMethod, code string, expiresAt time.Time) Challenge {
	return Challenge{Method: method, Extras: s.deliverCode(ctx, method, code, expiresAt)}
}

// VerifyByValue answers a challenge from the address side rather than
// the owner's: a message arrives at one of the deployment's numbers
// carrying a code, and whichever pending method of the kind with that
// value holds the matching code is verified.
func (s *Service) VerifyByValue(ctx context.Context, kind domain.ContactMethodKind, value, code string) (domain.ContactMethod, error) {
	value, code = strings.TrimSpace(value), strings.TrimSpace(code)
	if !kind.Challengeable() || value == "" {
		return domain.ContactMethod{}, fmt.Errorf("this address cannot be verified with a code: %w", domain.ErrInvalid)
	}
	if code == "" {
		return domain.ContactMethod{}, fmt.Errorf("the message carries no code: %w", domain.ErrInvalid)
	}
	return s.contacts.VerifyContactMethodByValue(ctx, application.VerifyContactMethodByValueInput{
		Kind: kind, Value: value, CodeHash: hashCode(code),
		MaxAttempts: MaxVerificationAttempts, Now: s.clock.Now(),
	})
}

func (s *Service) Verify(ctx context.Context, principal domain.Principal, methodID domain.ID, code string) (domain.ContactMethod, error) {
	if principal.ActorType != domain.ActorHuman {
		return domain.ContactMethod{}, domain.ErrForbidden
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return domain.ContactMethod{}, fmt.Errorf("enter the code we sent you: %w", domain.ErrInvalid)
	}
	return s.contacts.VerifyContactMethod(ctx, application.VerifyContactMethodInput{
		MethodID: methodID, UserID: principal.ActorID, CodeHash: hashCode(code),
		MaxAttempts: MaxVerificationAttempts, Now: s.clock.Now(),
	})
}

func (s *Service) Remove(ctx context.Context, principal domain.Principal, methodID domain.ID) error {
	if principal.ActorType != domain.ActorHuman {
		return domain.ErrForbidden
	}
	return s.contacts.DeleteContactMethod(ctx, application.DeleteContactMethodInput{
		MethodID: methodID, UserID: principal.ActorID,
	})
}

// deliverCode gets the code to the person. Email codes are sent through
// the notification registry, best effort like every other notification:
// the method row is already committed, and somebody who never receives
// the code can ask for another. An edition kind delivers through its
// adapter, which may answer with fields for the person instead (the
// hosted WhatsApp kind's confirmation link).
func (s *Service) deliverCode(ctx context.Context, method domain.ContactMethod, code string, expiresAt time.Time) map[string]string {
	if adapter, ok := s.adapters[method.Kind]; ok && adapter.DeliverCode != nil {
		extras, err := adapter.DeliverCode(ctx, method, code, expiresAt)
		if err != nil {
			s.logger.WarnContext(ctx, "contact verification code not delivered",
				"contact_method_id", method.ID, "kind", string(method.Kind), "error", err)
		}
		return extras
	}
	if err := s.notifications.SendTransactional(ctx, application.SendTransactionalInput{
		Message: application.TransactionalMessage{
			Kind:             application.TransactionalContactVerification,
			VerificationCode: code, ExpiresAt: expiresAt,
		},
		Recipient: application.NotificationRecipient{Emails: []string{method.Value}}, Address: method.Value,
		Kind: method.Kind, ContactMethodID: method.ID,
	}); err != nil {
		s.logger.WarnContext(ctx, "contact verification code not delivered",
			"contact_method_id", method.ID, "kind", string(method.Kind), "error", err)
	}
	return nil
}

// codeBudget asks the kind's adapter whether a code may go out now.
func (s *Service) codeBudget(ctx context.Context, kind domain.ContactMethodKind, userID, methodID domain.ID, now time.Time) error {
	if adapter, ok := s.adapters[kind]; ok && adapter.CodeBudget != nil {
		return adapter.CodeBudget(ctx, userID, methodID, now)
	}
	return nil
}

// normalize turns what the person typed into the stored form for the
// kind, refusing kinds this deployment has no adapter for: a method that
// can never be proved would only ever be a row nobody delivers to.
func (s *Service) normalize(kind domain.ContactMethodKind, value string) (string, error) {
	if kind == domain.ContactEmail {
		return normalizeEmail(value)
	}
	if adapter, ok := s.adapters[kind]; ok && kind.Challengeable() && adapter.Normalize != nil {
		return adapter.Normalize(value)
	}
	return "", fmt.Errorf("that kind of contact method cannot be added here: %w", domain.ErrInvalid)
}

// methodKind finds one of the person's methods by id. The store answers
// absence on the rotate that follows, so an unknown id here is simply
// treated as email and refused there.
func (s *Service) methodKind(ctx context.Context, principal domain.Principal, methodID domain.ID) (domain.ContactMethodKind, error) {
	methods, err := s.contacts.ListContactMethods(ctx, application.ListContactMethodsInput{UserID: principal.ActorID, OrganizationID: principal.OrganizationID})
	if err != nil {
		return "", err
	}
	for _, method := range methods {
		if method.ID == methodID {
			return method.Kind, nil
		}
	}
	return domain.ContactEmail, nil
}

func normalizeEmail(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	parsed, err := mail.ParseAddress(trimmed)
	if err != nil || parsed.Address == "" {
		return "", fmt.Errorf("that is not an email address: %w", domain.ErrInvalid)
	}
	return strings.ToLower(parsed.Address), nil
}

// mintCode returns a short numeric code and the hash stored at rest. Six
// digits is short enough to retype from a phone; the attempt budget, not
// the length, is what makes guessing pointless.
func mintCode() (string, []byte, error) {
	digits := make([]byte, codeDigits)
	for index := range digits {
		value, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", nil, err
		}
		digits[index] = byte('0' + value.Int64())
	}
	code := string(digits)
	return code, hashCode(code), nil
}

func hashCode(code string) []byte {
	sum := sha256.Sum256([]byte(strings.TrimSpace(code)))
	return sum[:]
}
