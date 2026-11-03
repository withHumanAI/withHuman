package authn

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"

	"github.com/withHumanAI/withHuman/domain"
)

// Native login. A native app cannot take a browser cookie from a
// provider round trip, so an edition's browser-based login (the hosted
// enterprise SSO) hands the app a single-use exchange code on a
// registered callback instead, guarded by PKCE. Core keeps the allowlist
// and the PKCE helpers every such hand-off shares; the flows themselves
// are the edition's.

// NativeClients is the allowlist of client kinds that may ask for the
// native hand-off and the callbacks each may name. A custom URL scheme can
// be claimed by any app on the device, which is why the exchange also
// requires PKCE; the allowlist keeps the callback from being an open
// redirect. The desktop app has its own scheme so it never competes with
// a phone app installed on the same Mac.
var NativeClients = map[string][]string{
	"ios":     {"withhuman://auth/callback"},
	"android": {"withhuman://auth/callback"},
	"desktop": {"withhuman-desktop://auth/callback"},
}

// ErrNativeClientUnknown is returned when the client kind or its callback
// is not on the allowlist.
var ErrNativeClientUnknown = errors.New("native client is not registered")

// ValidateNativeRequest checks the begin parameters against the allowlist
// and the PKCE shape before anything is stored.
func ValidateNativeRequest(client, callback, codeChallenge string) error {
	callbacks, ok := NativeClients[client]
	if !ok {
		return fmt.Errorf("%w: %w", ErrNativeClientUnknown, domain.ErrInvalid)
	}
	if !slices.Contains(callbacks, callback) {
		return fmt.Errorf("callback is not registered for client %s: %w: %w", client, ErrNativeClientUnknown, domain.ErrInvalid)
	}
	if !validCodeChallenge(codeChallenge) {
		return fmt.Errorf("code_challenge must be the base64url SHA-256 of the verifier: %w", domain.ErrInvalid)
	}
	return nil
}

// validCodeChallenge accepts exactly the unpadded base64url form of a
// 32-byte digest.
func validCodeChallenge(challenge string) bool {
	if len(challenge) != 43 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(challenge)
	return err == nil
}

// CodeChallenge derives the PKCE S256 challenge for a verifier. Exported
// for tests and clients written in Go.
func CodeChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
