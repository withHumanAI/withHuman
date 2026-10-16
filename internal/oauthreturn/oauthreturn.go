// Package oauthreturn names the page a browser comes back to after a
// provider's OAuth round trip (a Slack or Discord install, a Discord
// link). The guided setup starts the same flows as the settings pages
// and must get the person back to the setup, not to a settings page.
//
// The page travels in the OAuth state itself, as a prefix in front of the
// random part, so a callback can send the browser back even when the
// state is never redeemed: the person declined, or the state expired.
// The prefix is not a secret and needs no integrity. The whole state is
// hashed and stored, so a forged prefix cannot redeem anything, and on a
// refused callback the worst a forged prefix can do is choose between
// two pages of the product.
package oauthreturn

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/withHumanAI/withHuman/domain"
)

// Page is where the browser lands after the round trip.
type Page string

const (
	// Settings is the page the flow was started from: Integrations for
	// an install, Notifications for a link.
	Settings Page = "settings"
	// Onboarding is the guided setup at /welcome.
	Onboarding Page = "onboarding"
)

// separator cannot occur in either the page or the URL-safe base64
// random part.
const separator = "."

// Parse reads a page named by a request. An empty value is Settings.
func Parse(value string) (Page, error) {
	switch Page(value) {
	case "", Settings:
		return Settings, nil
	case Onboarding:
		return Onboarding, nil
	}
	return "", fmt.Errorf("unknown return page %q: %w", value, domain.ErrInvalid)
}

// NewState mints an OAuth state that returns the browser to page.
func NewState(page Page) (string, error) {
	if _, err := Parse(string(page)); err != nil {
		return "", err
	}
	if page == "" {
		page = Settings
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return string(page) + separator + base64.RawURLEncoding.EncodeToString(raw), nil
}

// FromState reads the page a state names. Anything it cannot read,
// including a missing state, is Settings.
func FromState(state string) Page {
	prefix, _, found := strings.Cut(state, separator)
	if !found {
		return Settings
	}
	page, err := Parse(prefix)
	if err != nil {
		return Settings
	}
	return page
}
