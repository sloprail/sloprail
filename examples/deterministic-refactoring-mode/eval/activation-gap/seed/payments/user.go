package payments

import (
	"errors"
	"strings"
)

// User is a payments account holder.
type User struct {
	ID    string
	Email string
	Plan  string
}

// GetUser looks up a user by id.
func GetUser(store map[string]*User, id string) (*User, error) {
	u, ok := store[id]
	if !ok {
		return nil, errors.New("user not found")
	}
	return u, nil
}

// ListActiveUsers returns every user whose plan is not "canceled".
func ListActiveUsers(store map[string]*User) []*User {
	var out []*User
	for _, u := range store {
		if u.Plan != "canceled" {
			out = append(out, u)
		}
	}
	return out
}

// UpdateEmail changes a user's email address, validating it is non-empty and
// contains an "@".
func UpdateEmail(store map[string]*User, id, newEmail string) error {
	u, ok := store[id]
	if !ok {
		return errors.New("user not found")
	}
	if newEmail == "" || !strings.Contains(newEmail, "@") {
		return errors.New("invalid email")
	}
	u.Email = newEmail
	return nil
}

// CancelPlan marks a user's plan as canceled.
func CancelPlan(store map[string]*User, id string) error {
	u, ok := store[id]
	if !ok {
		return errors.New("user not found")
	}
	u.Plan = "canceled"
	return nil
}
