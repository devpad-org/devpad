package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name, password string
		want           error
	}{
		{"eight ASCII characters", "abcdefgh", nil},
		{"seven ASCII characters", "abcdefg", ErrPasswordTooShort},
		// Eight characters but 32 bytes: the minimum counts characters.
		{"eight emoji", strings.Repeat("🔑", 8), nil},
		// Four characters but 16 bytes: still too few characters.
		{"four emoji", strings.Repeat("🔑", 4), ErrPasswordTooShort},
		{"72 bytes", strings.Repeat("x", MaxPasswordLength), nil},
		{"73 bytes", strings.Repeat("x", MaxPasswordLength+1), ErrPasswordTooLong},
		// 19 characters but 76 bytes: the maximum counts bytes, as bcrypt does.
		{"nineteen emoji", strings.Repeat("🔑", 19), ErrPasswordTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidatePassword(tt.password); !errors.Is(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCleanIdentity(t *testing.T) {
	tests := []struct {
		name, username, email string
		wantUsername          string
		wantEmail             string
		want                  error
	}{
		{"plain", "alice", "alice@example.com", "alice", "alice@example.com", nil},
		{"trimmed", "  alice ", " alice@example.com\t", "alice", "alice@example.com", nil},
		{"space and accents in username", "Zoë Smith", "zoe@example.com", "Zoë Smith", "zoe@example.com", nil},
		{"longest username", strings.Repeat("é", MaxUsernameLength), "a@b", strings.Repeat("é", MaxUsernameLength), "a@b", nil},
		{"blank username", "   ", "a@b", "", "", ErrUsernameRequired},
		{"blank email", "alice", "", "", "", ErrEmailRequired},
		{"username too long", strings.Repeat("a", MaxUsernameLength+1), "a@b", "", "", ErrUsernameInvalid},
		{"username like a flag", "-alice", "a@b", "", "", ErrUsernameInvalid},
		{"username with angle bracket", "alice<", "a@b", "", "", ErrUsernameInvalid},
		{"username with newline", "ali\nce", "a@b", "", "", ErrUsernameInvalid},
		{"username not UTF-8", "ali\xffce", "a@b", "", "", ErrUsernameInvalid},
		{"username with bidi override", "ali\u202ecb", "a@b", "", "", ErrUsernameInvalid},
		{"username with zero-width space", "ad\u200bmin", "a@b", "", "", ErrUsernameInvalid},
		{"username with no-break space", "ali\u00a0ce", "a@b", "", "", ErrUsernameInvalid},
		{"username with line separator", "ali\u2028ce", "a@b", "", "", ErrUsernameInvalid},
		{"username with tab", "ali\tce", "a@b", "", "", ErrUsernameInvalid},
		{"email with bidi isolate", "alice", "alice\u2066@example.com", "", "", ErrEmailInvalid},
		{"email with zero-width joiner", "alice", "al\u200dice@example.com", "", "", ErrEmailInvalid},
		{"email without at", "alice", "alice.example.com", "", "", ErrEmailInvalid},
		{"email without local part", "alice", "@example.com", "", "", ErrEmailInvalid},
		{"email without domain", "alice", "alice@", "", "", ErrEmailInvalid},
		{"email with space", "alice", "al ice@example.com", "", "", ErrEmailInvalid},
		{"email like a flag", "alice", "-alice@example.com", "", "", ErrEmailInvalid},
		{"email with angle bracket", "alice", "<alice@example.com>", "", "", ErrEmailInvalid},
		{"email too long", "alice", strings.Repeat("a", MaxEmailLength) + "@b", "", "", ErrEmailInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			username, email, err := CleanIdentity(tt.username, tt.email)
			if !errors.Is(err, tt.want) {
				t.Fatalf("got error %v, want %v", err, tt.want)
			}
			if username != tt.wantUsername || email != tt.wantEmail {
				t.Fatalf("got %q, %q; want %q, %q", username, email, tt.wantUsername, tt.wantEmail)
			}
		})
	}
}

// Handlers answer any validation error with 400 by asking IsValidationError,
// so every error the validators return must be one.
func TestValidatorsReturnOnlyValidationErrors(t *testing.T) {
	errs := []error{
		ValidatePassword("short"),
		ValidatePassword(strings.Repeat("x", MaxPasswordLength+1)),
	}
	for _, in := range [][2]string{{"", "a@b"}, {"a", ""}, {"-a", "a@b"}, {"a", "a.b"}} {
		_, _, err := CleanIdentity(in[0], in[1])
		errs = append(errs, err)
	}
	for _, err := range errs {
		if err == nil || !IsValidationError(fmt.Errorf("wrapped: %w", err)) {
			t.Errorf("%v: want a validation error", err)
		}
	}
	for _, err := range []error{nil, ErrUserExists, ErrInviteInvalid, errors.New(ErrEmailRequired.Error())} {
		if IsValidationError(err) {
			t.Errorf("%v: not a validation error", err)
		}
	}
}

func TestUserRepository_UpdateToTakenUsernameIsUserExists(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()
	alice := f.accept(t, f.admin, false, "alice")

	alice.Username = "admin"
	if err := f.users.Update(ctx, alice); !errors.Is(err, ErrUserExists) {
		t.Fatalf("got %v, want ErrUserExists", err)
	}
}
