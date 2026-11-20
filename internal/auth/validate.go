package auth

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MinPasswordLength is the fewest characters a password may have.
	MinPasswordLength = 8
	// MaxPasswordLength is bcrypt's limit in bytes: it refuses anything longer.
	MaxPasswordLength = 72
	// MaxUsernameLength is the most characters a username may have.
	MaxUsernameLength = 64
	// MaxEmailLength is the most bytes an email address may have.
	MaxEmailLength = 254
)

// validationError is a username, email or password the user must change. Its
// message is fit to show them.
type validationError string

func (e validationError) Error() string { return string(e) }

// IsValidationError reports whether err, or an error it wraps, is one of the
// errors below, which a handler answers with 400 and the error's message.
func IsValidationError(err error) bool {
	var v validationError
	return errors.As(err, &v)
}

var (
	ErrPasswordTooShort = validationError(fmt.Sprintf("password must be at least %d characters", MinPasswordLength))
	ErrPasswordTooLong  = validationError(fmt.Sprintf("password must be at most %d bytes (letters outside plain ASCII take 2 to 4 bytes each)", MaxPasswordLength))
	ErrUsernameRequired = validationError("username is required")
	ErrEmailRequired    = validationError("email is required")
	ErrUsernameInvalid  = validationError(fmt.Sprintf("username must be at most %d characters, must not start with '-', and must not contain '<', '>', or invisible or control characters other than plain spaces", MaxUsernameLength))
	ErrEmailInvalid     = validationError("email must look like name@example.com, without spaces, '<', '>', or invisible characters")
	ErrGitNameInvalid   = validationError(fmt.Sprintf("Git name must be at most %d characters, must not start with '-', must not contain '<', '>', or invisible or control characters other than plain spaces, and must have at least one letter or number", MaxUsernameLength))
	ErrGitEmailInvalid  = validationError("Git email must look like name@example.com, without spaces, '<', '>', or invisible characters")
)

// ValidatePassword returns ErrPasswordTooShort or ErrPasswordTooLong when
// password can't be used, and nil when it can. The minimum counts
// characters, as people do; the maximum counts bytes, as bcrypt does.
func ValidatePassword(password string) error {
	switch {
	case utf8.RuneCountInString(password) < MinPasswordLength:
		return ErrPasswordTooShort
	case len(password) > MaxPasswordLength:
		return ErrPasswordTooLong
	}
	return nil
}

// CleanIdentity trims a username and email someone chose and checks them,
// returning the trimmed values. Both are the user's git identity in their
// workspaces unless they choose another, where the agent refuses a value
// starting with '-' and git drops '<' and '>', so neither may have them.
func CleanIdentity(username, email string) (string, string, error) {
	username, email = strings.TrimSpace(username), strings.TrimSpace(email)
	switch {
	case username == "":
		return "", "", ErrUsernameRequired
	case email == "":
		return "", "", ErrEmailRequired
	case !validUsername(username):
		return "", "", ErrUsernameInvalid
	case !validEmail(email):
		return "", "", ErrEmailInvalid
	}
	return username, email, nil
}

// CleanGitIdentity trims the name and email someone chose to commit as and
// checks them by the same rules as a username and email, returning the
// trimmed values. Either may be empty, which means the account's own.
func CleanGitIdentity(name, email string) (string, string, error) {
	name, email = strings.TrimSpace(name), strings.TrimSpace(email)
	if name != "" && (!validUsername(name) || !strings.ContainsFunc(name, isGitNameChar)) {
		return "", "", ErrGitNameInvalid
	}
	if email != "" && !validEmail(email) {
		return "", "", ErrGitEmailInvalid
	}
	return name, email, nil
}

// isGitNameChar reports whether git keeps r in a name. Git strips these
// from both ends and refuses to commit when nothing is left.
func isGitNameChar(r rune) bool {
	return r > ' ' && !strings.ContainsRune(".,:;<>\"\\'", r)
}

// hidden reports whether r is a control character or an invisible format
// character, such as a bidi override or a zero-width space. People choose
// their own usernames and emails through invites, and others see them in
// devpad and in git history, so neither may hide or reorder what they show.
func hidden(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

func validUsername(s string) bool {
	return utf8.ValidString(s) &&
		utf8.RuneCountInString(s) <= MaxUsernameLength &&
		!strings.HasPrefix(s, "-") &&
		!strings.ContainsFunc(s, func(r rune) bool {
			// Only a plain space: others, like U+00A0 or U+2028, look like
			// it or break lines.
			return hidden(r) || (unicode.IsSpace(r) && r != ' ') || r == '<' || r == '>'
		})
}

func validEmail(s string) bool {
	at := strings.LastIndexByte(s, '@')
	return utf8.ValidString(s) &&
		len(s) <= MaxEmailLength &&
		at > 0 && at < len(s)-1 &&
		!strings.HasPrefix(s, "-") &&
		!strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || hidden(r) || r == '<' || r == '>' })
}
