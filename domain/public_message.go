package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// categorySuffixes are the generic sentinels that wrap a specific
// message only so callers can classify it. They tell a person nothing
// the message and the response's code do not already say.
var categorySuffixes = []error{
	ErrInvalid,
	ErrConflict,
	ErrNotFound,
	ErrForbidden,
	ErrUnauthenticated,
	ErrPreconditionFailed,
	ErrPreconditionRequired,
	ErrLimitExceeded,
}

// PublicMessage turns an error message into the sentence an API error
// response shows a person: the trailing category sentinel is dropped,
// the first letter is capitalised and the sentence ends with a full stop.
// A message that starts with an identifier (one holding an underscore or
// a dot) keeps its case.
func PublicMessage(message string) string {
	message = strings.TrimSpace(message)
	for _, category := range categorySuffixes {
		suffix := ": " + category.Error()
		if trimmed, ok := strings.CutSuffix(message, suffix); ok && trimmed != "" {
			message = trimmed
			break
		}
	}
	if message == "" {
		return message
	}
	first, size := utf8.DecodeRuneInString(message)
	firstWord, _, _ := strings.Cut(message, " ")
	if unicode.IsLower(first) && !strings.ContainsAny(firstWord, "_.") {
		message = string(unicode.ToUpper(first)) + message[size:]
	}
	if last, _ := utf8.DecodeLastRuneInString(message); !strings.ContainsRune(".!?", last) {
		message += "."
	}
	return message
}
