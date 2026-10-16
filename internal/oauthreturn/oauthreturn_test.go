package oauthreturn

import (
	"errors"
	"testing"

	"github.com/withHumanAI/withHuman/domain"
)

func TestAStateCarriesItsPage(t *testing.T) {
	for _, page := range []Page{Settings, Onboarding} {
		state, err := NewState(page)
		if err != nil {
			t.Fatalf("NewState(%q): %v", page, err)
		}
		if got := FromState(state); got != page {
			t.Fatalf("FromState(%q) = %q, want %q", state, got, page)
		}
	}
}

func TestStatesAreUnique(t *testing.T) {
	first, _ := NewState(Onboarding)
	second, _ := NewState(Onboarding)
	if first == second {
		t.Fatalf("two states were equal: %q", first)
	}
}

func TestAnUnreadableStateReturnsToSettings(t *testing.T) {
	for _, state := range []string{"", "no-separator", "elsewhere.abc", ".abc", "https://evil.example.abc"} {
		if got := FromState(state); got != Settings {
			t.Fatalf("FromState(%q) = %q, want settings", state, got)
		}
	}
}

func TestParse(t *testing.T) {
	if page, err := Parse(""); err != nil || page != Settings {
		t.Fatalf("Parse(\"\") = %q, %v", page, err)
	}
	if page, err := Parse("onboarding"); err != nil || page != Onboarding {
		t.Fatalf("Parse(onboarding) = %q, %v", page, err)
	}
	if _, err := Parse("/welcome"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("Parse(/welcome) = %v, want ErrInvalid", err)
	}
	if _, err := NewState("elsewhere"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("NewState(elsewhere) = %v, want ErrInvalid", err)
	}
}
