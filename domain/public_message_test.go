package domain

import "testing"

func TestPublicMessage(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"enter the code we sent you: invalid", "Enter the code we sent you."},
		{"invitation was already accepted: conflict", "Invitation was already accepted."},
		{"not found", "Not found."},
		{"agent slug is already taken", "Agent slug is already taken."},
		{"An internal error occurred.", "An internal error occurred."},
		{"withhuman_scope is required: invalid", "withhuman_scope is required."},
		{"Idempotency-Key header is required: invalid", "Idempotency-Key header is required."},
		{"wait a minute before requesting another code: limit reached", "Wait a minute before requesting another code."},
		{"limit reached", "Limit reached."},
		{"", ""},
	} {
		if got := PublicMessage(tc.in); got != tc.want {
			t.Errorf("PublicMessage(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
