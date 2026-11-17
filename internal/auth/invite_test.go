package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type inviteFixture struct {
	svc     Service
	invites *inviteService
	handler *InviteHandler
	admin   *User
	clock   time.Time
}

func setupInvites(t *testing.T) *inviteFixture {
	t.Helper()
	svc, db := setupTestService(t)
	admin, err := svc.Setup(context.Background(), "admin", "admin@test.com", "password123")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	f := &inviteFixture{svc: svc, admin: admin, clock: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	inv := NewInviteService(NewInviteRepository(db), NewSessionRepository(db)).(*inviteService)
	inv.now = func() time.Time { return f.clock }
	f.invites = inv
	f.handler = NewInviteHandler(inv, NewHandler(svc, false))
	return f
}

func (f *inviteFixture) create(t *testing.T, email string, isAdmin bool) (*Invite, string) {
	t.Helper()
	inv, token, err := f.invites.Create(context.Background(), f.admin.ID, email, isAdmin, 0)
	if err != nil {
		t.Fatalf("creating invite: %v", err)
	}
	return inv, token
}

func TestInvite_AcceptCreatesUserAndSignsIn(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()
	_, token := f.create(t, "", false)

	user, session, err := f.invites.Accept(ctx, token, " alice ", "alice@test.com", "password123")
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
	_, token := f.create(t, "", false)

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
	_, token := f.create(t, "", false)

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
	_, token := f.create(t, "", false)

	f.clock = f.clock.Add(DefaultInviteTTL)
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

func TestInvite_FixesEmailAndAdmin(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()
	_, token := f.create(t, "carol@test.com", true)

	user, _, err := f.invites.Accept(ctx, token, "carol", "someone-else@test.com", "password123")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if user.Email != "carol@test.com" {
		t.Errorf("email = %q, want the invite's", user.Email)
	}
	if !user.IsAdmin {
		t.Error("expected an admin, as the invite says")
	}
}

func TestInvite_RequiresEmailWhenInviteHasNone(t *testing.T) {
	f := setupInvites(t)
	_, token := f.create(t, "", false)

	if _, _, err := f.invites.Accept(context.Background(), token, "dave", "  ", "password123"); !errors.Is(err, ErrEmailRequired) {
		t.Fatalf("got %v, want ErrEmailRequired", err)
	}
}

func TestInvite_TakenUsernameLeavesInviteUsable(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()
	_, token := f.create(t, "", false)

	if _, _, err := f.invites.Accept(ctx, token, "admin", "new@test.com", "password123"); !errors.Is(err, ErrUserExists) {
		t.Fatalf("got %v, want ErrUserExists", err)
	}
	if _, _, err := f.invites.Accept(ctx, token, "erin", "new@test.com", "password123"); err != nil {
		t.Fatalf("retry with a free username: %v", err)
	}
}

func TestInvite_CreateRefusesTakenEmailAndBadTTL(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()

	if _, _, err := f.invites.Create(ctx, f.admin.ID, "admin@test.com", false, 0); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("taken email: got %v, want ErrEmailTaken", err)
	}
	for _, ttl := range []time.Duration{-time.Hour, time.Minute, MaxInviteTTL + time.Hour} {
		if _, _, err := f.invites.Create(ctx, f.admin.ID, "", false, ttl); !errors.Is(err, ErrInviteTTL) {
			t.Errorf("ttl %v: got %v, want ErrInviteTTL", ttl, err)
		}
	}
}

func TestInvite_RevokeAndStoreOnlyHash(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()
	inv, token := f.create(t, "", false)

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

func TestInviteHandler_AcceptSetsSessionCookie(t *testing.T) {
	f := setupInvites(t)
	_, token := f.create(t, "", false)

	body := `{"token":"` + token + `","username":"frank","email":"frank@test.com","password":"password123"}`
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
	if err != nil || user == nil || user.Username != "frank" {
		t.Fatalf("session belongs to %+v (%v)", user, err)
	}
}

func TestInviteHandler_AcceptRefusesSignedInUser(t *testing.T) {
	f := setupInvites(t)
	_, token := f.create(t, "", false)
	session, err := f.svc.Login(context.Background(), "admin", "password123", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	body := `{"token":"` + token + `","username":"gina","email":"gina@test.com","password":"password123"}`
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

func TestInviteHandler_Validation(t *testing.T) {
	f := setupInvites(t)
	_, token := f.create(t, "", false)

	tests := []struct {
		name string
		body string
		want int
	}{
		{"bad token", `{"token":"nope","username":"h","email":"h@test.com","password":"password123"}`, http.StatusNotFound},
		{"no username", `{"token":"` + token + `","username":" ","email":"h@test.com","password":"password123"}`, http.StatusBadRequest},
		{"short password", `{"token":"` + token + `","username":"h","email":"h@test.com","password":"short"}`, http.StatusBadRequest},
		{"long password", `{"token":"` + token + `","username":"h","email":"h@test.com","password":"` + strings.Repeat("x", 73) + `"}`, http.StatusBadRequest},
		{"taken username", `{"token":"` + token + `","username":"admin","email":"h@test.com","password":"password123"}`, http.StatusConflict},
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
	req := httptest.NewRequest(http.MethodPost, "/api/admin/invites", strings.NewReader(`{"email":"ivy@test.com","validDays":3}`)).WithContext(ctx)
	f.handler.HandleCreate(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Invite struct {
			Token     string    `json:"token"`
			Email     string    `json:"email"`
			CreatedBy string    `json:"createdBy"`
			ExpiresAt time.Time `json:"expiresAt"`
		} `json:"invite"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if created.Invite.Token == "" || created.Invite.Email != "ivy@test.com" || created.Invite.CreatedBy != "admin" {
		t.Fatalf("unexpected invite %+v", created.Invite)
	}
	if want := f.clock.Add(3 * 24 * time.Hour); !created.Invite.ExpiresAt.Equal(want) {
		t.Errorf("expires %v, want %v", created.Invite.ExpiresAt, want)
	}

	rec = httptest.NewRecorder()
	f.handler.HandleList(rec, httptest.NewRequest(http.MethodGet, "/api/admin/invites", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ivy@test.com") || strings.Contains(rec.Body.String(), created.Invite.Token) {
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

func TestInvite_DeletingAdminRevokesTheirInvites(t *testing.T) {
	f := setupInvites(t)
	ctx := context.Background()
	other, _, err := f.invites.Accept(ctx, mustToken(t, f), "jo", "jo@test.com", "password123")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	_, token, err := f.invites.Create(ctx, other.ID, "", false, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := NewUserRepository(f.invites.invites.(*inviteRepository).db).Delete(ctx, other.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := f.invites.Check(ctx, token); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("got %v, want the deleted admin's invite gone", err)
	}
}

func mustToken(t *testing.T, f *inviteFixture) string {
	t.Helper()
	_, token := f.create(t, "", true)
	return token
}
