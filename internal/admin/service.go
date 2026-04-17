package admin

import (
	"context"
	"errors"
	"fmt"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/workspace"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrUserNotFound = errors.New("user not found")
	ErrSelfDelete   = errors.New("cannot delete your own account")
	ErrSelfDemote   = errors.New("cannot remove your own admin status")
)

// Service defines admin business logic.
type Service interface {
	ListUsers(ctx context.Context) ([]*auth.User, error)
	CreateUser(ctx context.Context, username, email, password string, isAdmin bool) (*auth.User, error)
	UpdateUser(ctx context.Context, id int64, username, email string, isAdmin bool) (*auth.User, error)
	ResetPassword(ctx context.Context, id int64, password string) error
	DeleteUser(ctx context.Context, callerID, targetID int64) error

	ListWorkspaces(ctx context.Context) ([]*workspace.Workspace, error)
	UpdateWorkspaceLimits(ctx context.Context, workspaceID, memoryLimit, nanoCPUs int64) (*workspace.Workspace, error)
}

type service struct {
	users      auth.UserRepository
	workspaces workspace.Service
}

// NewService creates a new admin Service.
func NewService(users auth.UserRepository, workspaces workspace.Service) Service {
	return &service{users: users, workspaces: workspaces}
}

func (s *service) ListUsers(ctx context.Context) ([]*auth.User, error) {
	users, err := s.users.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	return users, nil
}

func (s *service) CreateUser(ctx context.Context, username, email, password string, isAdmin bool) (*auth.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hashing password: %w", err)
	}

	user := &auth.User{
		Username: username,
		Email:    email,
		Password: string(hash),
		IsAdmin:  isAdmin,
	}

	if err := s.users.Create(ctx, user); err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}

	return user, nil
}

func (s *service) UpdateUser(ctx context.Context, id int64, username, email string, isAdmin bool) (*auth.User, error) {
	user, err := s.users.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return nil, ErrUserNotFound
	}

	user.Username = username
	user.Email = email
	user.IsAdmin = isAdmin

	if err := s.users.Update(ctx, user); err != nil {
		return nil, fmt.Errorf("updating user: %w", err)
	}

	return user, nil
}

func (s *service) ResetPassword(ctx context.Context, id int64, password string) error {
	user, err := s.users.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return ErrUserNotFound
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}

	user.Password = string(hash)
	if err := s.users.Update(ctx, user); err != nil {
		return fmt.Errorf("updating password: %w", err)
	}

	return nil
}

func (s *service) DeleteUser(ctx context.Context, callerID, targetID int64) error {
	if callerID == targetID {
		return ErrSelfDelete
	}

	user, err := s.users.GetByID(ctx, targetID)
	if err != nil {
		return fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return ErrUserNotFound
	}

	if err := s.users.Delete(ctx, targetID); err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}

	return nil
}

func (s *service) ListWorkspaces(ctx context.Context) ([]*workspace.Workspace, error) {
	return s.workspaces.ListAll(ctx)
}

func (s *service) UpdateWorkspaceLimits(ctx context.Context, workspaceID, memoryLimit, nanoCPUs int64) (*workspace.Workspace, error) {
	return s.workspaces.UpdateResourceLimits(ctx, workspaceID, memoryLimit, nanoCPUs)
}
