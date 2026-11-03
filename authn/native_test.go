package authn

import (
	"errors"
	"testing"
)

func TestDesktopNativeClientUsesItsOwnCallback(t *testing.T) {
	challenge := CodeChallenge("desktop-verifier-0123456789-0123456789-0123456789")
	if err := ValidateNativeRequest("desktop", "withhuman-desktop://auth/callback", challenge); err != nil {
		t.Fatalf("desktop callback = %v, want accepted", err)
	}
	if err := ValidateNativeRequest("desktop", "withhuman://auth/callback", challenge); !errors.Is(err, ErrNativeClientUnknown) {
		t.Fatalf("phone callback for desktop = %v, want unknown", err)
	}
}
