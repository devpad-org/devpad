package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devpad-org/devpad/internal/database"
)

type inviteFixture struct {
	db      *sql.DB
	svc     Service
	users   UserRepository
	invites *inviteService
	handler *InviteHandler
	admin   *User
	clock   time.Time
}

// setupInvites runs against the real schema, every migration applied, so the
// tests cover what the migration does when an admin is demoted or deleted.
func setupInvites(t *testing.T) *inviteFixture {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), "devpad.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	conn := db.Conn()
	f := &inviteFixture{db: conn, users: NewUserRepository(conn), clock: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	f.svc = NewService(f.users, NewSessionRepository(conn))
	f.admin, err = f.svc.Setup(context.Background(), "admin", "admin@test.com", "password123")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	inv := NewInviteService(NewInviteRepository(conn), NewSessionRepository(conn)).(*inviteService)
	inv.now = func() time.Time { return f.clock }
	f.invites = inv
	f.handler = NewInviteHandler(inv, NewHandler(f.svc, false))
	return f
}

func (f *inviteFixture) create(t *testing.T, by *User, isAdmin bool) (*Invite, string) {
	t.Helper()
	inv, token, err := f.invites.Create(context.Background(), by.ID, isAdmin, 0)
	if err != nil {
		t.Fatalf("creating invite: %v", err)
	}
	return inv, token
}

// accept redeems a fresh invite from by, failing the test if it can't.
func (f *inviteFixture) accept(t *testing.T, by *User, isAdmin bool, username string) *User {
	t.Helper()
	_, token := f.create(t, by, isAdmin)
	user, _, err := f.invites.Accept(context.Background(), token, username, username+"@test.com", "password123")
	if err != nil {
		t.Fatalf("accepting invite as %s: %v", username, err)
	}
	return user
}

func (f *inviteFixture) setAdmin(t *testing.T, user *User, isAdmin bool) {
	t.Helper()
	user.IsAdmin = isAdmin
	if err := f.users.Update(context.Background(), user); err != nil {
		t.Fatalf("updating user: %v", err)
	}
}

func TestInvite_AcceptCreatesUserAndSignsIn(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()
	_, token := f.create(t, f.admin, false)

	user, session, err := f.invites.Accept(ctx, token, " alice ", " alice@test.com ", "password123")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if user.Username != "alice" || user.Email != "alice@test.com" || user.IsAdmin {
		t.Fatalf("unexpected user %+v", user)
	}
	if session == nil || session.Token == "" || session.UserID != user.ID {
		t.Fatalf("unexpected session %+v", session)
	}
	if _, err := f.svc.Login(ctx, "alice", "password123", ""); err != nil {
		t.Fatalf("new user cannot log in: %v", err)
	}

	pending, err := f.invites.ListPending(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("used invite still pending: %+v", pending)
	}
}

func TestInvite_IsSingleUse(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()
	_, token := f.create(t, f.admin, false)

	if _, _, err := f.invites.Accept(ctx, token, "alice", "alice@test.com", "password123"); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	if _, _, err := f.invites.Accept(ctx, token, "bob", "bob@test.com", "password123"); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("second accept: got %v, want ErrInviteInvalid", err)
	}
	if _, err := f.invites.Check(ctx, token); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("check after use: got %v, want ErrInviteInvalid", err)
	}
}

func TestInvite_ConcurrentAcceptsMakeOneUser(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()
	_, token := f.create(t, f.admin, false)

	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := string(rune('a' + i))
			_, _, errs[i] = f.invites.Accept(ctx, token, "user-"+name, name+"@test.com", "password123")
		}(i)
	}
	wg.Wait()

	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ErrInviteInvalid):
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d accepts succeeded, want exactly 1", ok)
	}
}

func TestInvite_Expires(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()
	_, token := f.create(t, f.admin, false)

	f.clock = f.clock.AddDate(0, 0, DefaultInviteDays)
	if _, err := f.invites.Check(ctx, token); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("check: got %v, want ErrInviteInvalid", err)
	}
	if _, _, err := f.invites.Accept(ctx, token, "alice", "alice@test.com", "password123"); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("accept: got %v, want ErrInviteInvalid", err)
	}

	pending, err := f.invites.ListPending(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pending) != 1 || !pending[0].Expired(f.clock) {
		t.Fatalf("expected one expired pending invite, got %+v", pending)
	}
}

// Redeem checks expiry itself, so an invite that expires between Accept's
// check and its claim still can't be used.
func TestInvite_RedeemRefusesExpiredInvite(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()
	inv, token := f.create(t, f.admin, false)

	user := &User{Username: "alice", Email: "alice@test.com", Password: "x"}
	err := f.invites.invites.Redeem(ctx, hashInviteToken(token), user, inv.ExpiresAt)
	if !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("redeem at expiry: got %v, want ErrInviteInvalid", err)
	}
	if got, err := f.users.GetByUsername(ctx, "alice"); err != nil || got != nil {
		t.Fatalf("expected no user made, got %+v (%v)", got, err)
	}
	if err := f.invites.invites.Redeem(ctx, hashInviteToken(token), user, inv.ExpiresAt.Add(-time.Second)); err != nil {
		t.Fatalf("redeem just before expiry: %v", err)
	}
}

func TestInvite_AdminInviteMakesAnAdmin(t *testing.T) {
	f := setupInvites(t)
	if user := f.accept(t, f.admin, true, "carol"); !user.IsAdmin {
		t.Error("expected an admin, as the invite says")
	}
	if user := f.accept(t, f.admin, false, "dave"); user.IsAdmin {
		t.Error("expected a plain user, as the invite says")
	}
}

func TestInvite_AcceptValidatesInput(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()
	_, token := f.create(t, f.admin, false)

	tests := []struct {
		name, username, email, password string
		want                            error
	}{
		{"no username", "  ", "e@test.com", "password123", ErrUsernameRequired},
		{"no email", "erin", "  ", "password123", ErrEmailRequired},
		{"short password", "erin", "e@test.com", "short", ErrPasswordTooShort},
		{"long password", "erin", "e@test.com", strings.Repeat("x", MaxPasswordLength+1), ErrPasswordTooLong},
		{"username like a flag", "-erin", "e@test.com", "password123", ErrUsernameInvalid},
		{"email like a flag", "erin", "-e@test.com", "password123", ErrEmailInvalid},
		{"email without a domain", "erin", "erin@", "password123", ErrEmailInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := f.invites.Accept(ctx, token, tt.username, tt.email, tt.password); !errors.Is(err, tt.want) {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
	if _, err := f.invites.Check(ctx, token); err != nil {
		t.Fatalf("refused accepts used the invite: %v", err)
	}
}

func TestInvite_TakenUsernameLeavesInviteUsable(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()
	_, token := f.create(t, f.admin, false)

	if _, _, err := f.invites.Accept(ctx, token, "admin", "new@test.com", "password123"); !errors.Is(err, ErrUserExists) {
		t.Fatalf("got %v, want ErrUserExists", err)
	}
	if _, _, err := f.invites.Accept(ctx, token, "erin", "new@test.com", "password123"); err != nil {
		t.Fatalf("retry with a free username: %v", err)
	}
}

func TestInvite_CreateRefusesBadDays(t *testing.T) {
	f := setupInvites(t)
	for _, days := range []int{-1, MaxInviteDays + 1} {
		if _, _, err := f.invites.Create(context.Background(), f.admin.ID, false, days); !errors.Is(err, ErrInviteDays) {
			t.Errorf("%d days: got %v, want ErrInviteDays", days, err)
		}
	}
}

func TestInvite_OnlyAnAdminCanCreate(t *testing.T) {
	f := setupInvites(t)
	user := f.accept(t, f.admin, false, "frank")
	if _, _, err := f.invites.Create(context.Background(), user.ID, false, 0); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("got %v, want ErrNotAdmin", err)
	}
}

func TestInvite_RevokeAndStoreOnlyHash(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()
	inv, token := f.create(t, f.admin, false)

	if len(token) < 40 {
		t.Fatalf("token %q is too short", token)
	}
	stored, err := f.invites.invites.GetByTokenHash(ctx, token)
	if err != nil || stored != nil {
		t.Fatalf("raw token must not be stored; lookup by it found %+v (%v)", stored, err)
	}

	if err := f.invites.Revoke(ctx, inv.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := f.invites.Check(ctx, token); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("check after revoke: got %v", err)
	}
	if err := f.invites.Revoke(ctx, inv.ID); !errors.Is(err, ErrInviteNotFound) {
		t.Fatalf("second revoke: got %v, want ErrInviteNotFound", err)
	}
}

func TestInvite_DemotingAdminRevokesTheirPendingInvites(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()
	other := f.accept(t, f.admin, true, "gina")
	_, theirs := f.create(t, other, true)
	_, mine := f.create(t, f.admin, false)
	invitee := f.accept(t, other, false, "hal")

	f.setAdmin(t, other, false)
	if _, err := f.invites.Check(ctx, theirs); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("demoted admin's invite: got %v, want ErrInviteInvalid", err)
	}
	// Promoting them again doesn't bring it back.
	f.setAdmin(t, other, true)
	if _, err := f.invites.Check(ctx, theirs); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("after promoting again: got %v, want ErrInviteInvalid", err)
	}

	if _, err := f.invites.Check(ctx, mine); err != nil {
		t.Fatalf("another admin's invite went too: %v", err)
	}
	var usedBy sql.NullInt64
	if err := f.db.QueryRow(`SELECT used_by FROM user_invites WHERE created_by = ? AND used_at IS NOT NULL`, other.ID).Scan(&usedBy); err != nil {
		t.Fatalf("their used invite went too: %v", err)
	}
	if usedBy.Int64 != invitee.ID {
		t.Fatalf("used invite records %v, want %d", usedBy, invitee.ID)
	}
}

func TestInvite_PurgeExpiredKeepsRecentAndUsedInvites(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()
	f.create(t, f.admin, false) // expires first, unused
	invitee := f.accept(t, f.admin, false, "kim")
	f.clock = f.clock.AddDate(0, 0, DefaultInviteDays)
	recent, _ := f.create(t, f.admin, false)
	f.clock = f.clock.AddDate(0, 0, DefaultInviteDays)
	live, _ := f.create(t, f.admin, false)

	// The first invite expired exactly ExpiredInviteRetention ago: it stays
	// until it is older than that.
	if err := f.invites.PurgeExpired(ctx); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if got := pendingIDs(t, f); len(got) != 3 {
		t.Fatalf("at the retention boundary: pending %v, want all three", got)
	}

	f.clock = f.clock.Add(time.Second)
	if err := f.invites.PurgeExpired(ctx); err != nil {
		t.Fatalf("purge: %v", err)
	}
	got := pendingIDs(t, f)
	if len(got) != 2 || got[0] != live.ID || got[1] != recent.ID {
		t.Fatalf("pending %v, want [%d %d]", got, live.ID, recent.ID)
	}
	var usedBy int64
	if err := f.db.QueryRow(`SELECT used_by FROM user_invites WHERE used_at IS NOT NULL`).Scan(&usedBy); err != nil || usedBy != invitee.ID {
		t.Fatalf("used invite: used_by %d, err %v; want it kept for %d", usedBy, err, invitee.ID)
	}
}

func pendingIDs(t *testing.T, f *inviteFixture) []int64 {
	t.Helper()
	invites, err := f.invites.ListPending(context.Background())
	if err != nil {
		t.Fatalf("listing invites: %v", err)
	}
	ids := make([]int64, len(invites))
	for i, inv := range invites {
		ids[i] = inv.ID
	}
	return ids
}

func TestInvite_UpdatingAnAdminKeepsTheirInvites(t *testing.T) {
	f := setupInvites(t)
	_, token := f.create(t, f.admin, false)
	f.admin.Email = "new-admin@test.com"
	f.setAdmin(t, f.admin, true)
	if _, err := f.invites.Check(context.Background(), token); err != nil {
		t.Fatalf("an update that kept admin revoked the invite: %v", err)
	}
}

func TestInvite_DeletingAdminRevokesTheirInvites(t *testing.T) {
	f := setupInvites(t)
	other := f.accept(t, f.admin, true, "jo")
	_, token := f.create(t, other, false)
	if err := f.users.Delete(context.Background(), other.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.invites.Check(context.Background(), token); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("got %v, want the deleted admin's invite gone", err)
	}
}

func TestInviteHandler_AcceptSetsSessionCookie(t *testing.T) {
	f := setupInvites(t)
	_, token := f.create(t, f.admin, false)

	body := `{"token":"` + token + `","username":"kim","email":"kim@test.com","password":"password123"}`
	rec := httptest.NewRecorder()
	f.handler.HandleAccept(rec, httptest.NewRequest(http.MethodPost, "/api/auth/invites/accept", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	cookie := findCookie(rec.Result().Cookies(), cookieName)
	if cookie == nil || cookie.Value == "" {
		t.Fatal("expected a session cookie")
	}
	user, err := f.svc.ValidateSession(context.Background(), cookie.Value)
	if err != nil || user == nil || user.Username != "kim" {
		t.Fatalf("session belongs to %+v (%v)", user, err)
	}
}

func TestInviteHandler_AcceptRefusesSignedInUser(t *testing.T) {
	f := setupInvites(t)
	_, token := f.create(t, f.admin, false)
	session, err := f.svc.Login(context.Background(), "admin", "password123", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	body := `{"token":"` + token + `","username":"lee","email":"lee@test.com","password":"password123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/invites/accept", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: cookieName, Value: session.Token})
	rec := httptest.NewRecorder()
	f.handler.HandleAccept(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "sign out") {
		t.Errorf("body %s should tell them to sign out", rec.Body.String())
	}
	if _, err := f.invites.Check(context.Background(), token); err != nil {
		t.Fatalf("refused accept used the invite: %v", err)
	}
}

func TestInviteHandler_AcceptStatuses(t *testing.T) {
	f := setupInvites(t)
	_, token := f.create(t, f.admin, false)

	tests := []struct {
		name string
		body string
		want int
	}{
		{"bad token", `{"token":"nope","username":"h","email":"h@test.com","password":"password123"}`, http.StatusNotFound},
		{"no username", `{"token":"` + token + `","username":" ","email":"h@test.com","password":"password123"}`, http.StatusBadRequest},
		{"no email", `{"token":"` + token + `","username":"h","email":"","password":"password123"}`, http.StatusBadRequest},
		{"short password", `{"token":"` + token + `","username":"h","email":"h@test.com","password":"short"}`, http.StatusBadRequest},
		{"long password", `{"token":"` + token + `","username":"h","email":"h@test.com","password":"` + strings.Repeat("x", 73) + `"}`, http.StatusBadRequest},
		{"taken username", `{"token":"` + token + `","username":"admin","email":"h@test.com","password":"password123"}`, http.StatusConflict},
		{"taken email", `{"token":"` + token + `","username":"h","email":"admin@test.com","password":"password123"}`, http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			f.handler.HandleAccept(rec, httptest.NewRequest(http.MethodPost, "/api/auth/invites/accept", strings.NewReader(tt.body)))
			if rec.Code != tt.want {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}
}

func TestInviteHandler_CreateListCheck(t *testing.T) {
	f := setupInvites(t)
	ctx := context.WithValue(context.Background(), UserContextKey, f.admin)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/invites", strings.NewReader(`{"isAdmin":true,"validDays":3}`)).WithContext(ctx)
	f.handler.HandleCreate(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Invite struct {
			ID        int64     `json:"id"`
			Token     string    `json:"token"`
			IsAdmin   bool      `json:"isAdmin"`
			InvitedBy string    `json:"invitedBy"`
			ExpiresAt time.Time `json:"expiresAt"`
		} `json:"invite"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if created.Invite.Token == "" || !created.Invite.IsAdmin || created.Invite.InvitedBy != "admin" {
		t.Fatalf("unexpected invite %+v", created.Invite)
	}
	if want := f.clock.AddDate(0, 0, 3); !created.Invite.ExpiresAt.Equal(want) {
		t.Errorf("expires %v, want %v", created.Invite.ExpiresAt, want)
	}

	rec = httptest.NewRecorder()
	f.handler.HandleList(rec, httptest.NewRequest(http.MethodGet, "/api/admin/invites", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"invitedBy":"admin"`) || strings.Contains(rec.Body.String(), created.Invite.Token) {
		t.Fatalf("list: status %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	f.handler.HandleCheck(rec, httptest.NewRequest(http.MethodPost, "/api/auth/invites/check", strings.NewReader(`{"token":"`+created.Invite.Token+`"}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"invitedBy":"admin"`) {
		t.Fatalf("check: status %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/admin/invites", strings.NewReader(`{"validDays":31}`)).WithContext(ctx)
	f.handler.HandleCreate(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("31 days: status %d, want 400", rec.Code)
	}
}
