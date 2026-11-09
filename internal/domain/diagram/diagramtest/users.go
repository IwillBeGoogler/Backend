package diagramtest

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

// Users is an in-memory account lookup (application.DiagramUsers).
type Users struct {
	mu sync.Mutex
	by map[int]user.User
}

func NewUsers() *Users { return &Users{by: map[int]user.User{}} }

// Add registers an account.
func (u *Users) Add(id int, username, email, displayName string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.by[id] = user.User{ID: id, Username: username, Email: email, DisplayName: displayName}
}

// Delete marks an account as pending deletion.
func (u *Users) Delete(id int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	x := u.by[id]
	at := time.Now()
	x.DeletedAt = &at
	u.by[id] = x
}

func (u *Users) GetByID(_ context.Context, id int) (user.User, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	x, ok := u.by[id]
	if !ok {
		return x, domain.ErrNotFound
	}
	return x, nil
}

func (u *Users) GetByLogin(_ context.Context, login string) (user.User, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, x := range u.by {
		if strings.EqualFold(x.Username, login) || strings.EqualFold(x.Email, login) {
			return x, nil
		}
	}
	return user.User{}, domain.ErrNotFound
}

func (u *Users) GetByUsername(_ context.Context, username string) (user.User, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, x := range u.by {
		if strings.EqualFold(x.Username, username) {
			return x, nil
		}
	}
	return user.User{}, domain.ErrNotFound
}
