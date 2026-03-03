package roles

import (
	"database/sql"
)

// Checker resolves effective permissions for a member, considering
// their role defaults + any per-member overrides stored in the DB.
type Checker struct {
	db *sql.DB
}

func NewChecker(db *sql.DB) *Checker {
	return &Checker{db: db}
}

// Has checks if a member has a specific permission.
func (c *Checker) Has(memberID string, perm Permission) bool {
	perms := c.Resolve(memberID)
	return perms[perm]
}

// Resolve returns the full effective permission set for a member.
func (c *Checker) Resolve(memberID string) map[Permission]bool {
	// Get the member's role
	var role string
	err := c.db.QueryRow("SELECT role FROM members WHERE id = ?", memberID).Scan(&role)
	if err != nil {
		return map[Permission]bool{}
	}

	// Start with role defaults
	defaults := DefaultPermissions[Role(role)]
	effective := make(map[Permission]bool, len(defaults))
	for k, v := range defaults {
		effective[k] = v
	}

	// Apply per-member overrides
	rows, err := c.db.Query(
		"SELECT permission, granted FROM permission_overrides WHERE member_id = ?",
		memberID,
	)
	if err != nil {
		return effective
	}
	defer rows.Close()

	for rows.Next() {
		var perm string
		var granted bool
		if err := rows.Scan(&perm, &granted); err != nil {
			continue
		}
		if granted {
			effective[Permission(perm)] = true
		} else {
			delete(effective, Permission(perm))
		}
	}

	return effective
}

// SetOverride creates or updates a permission override for a member.
func (c *Checker) SetOverride(memberID string, perm Permission, granted bool) error {
	_, err := c.db.Exec(
		`INSERT INTO permission_overrides (member_id, permission, granted)
		 VALUES (?, ?, ?)
		 ON CONFLICT(member_id, permission) DO UPDATE SET granted = ?`,
		memberID, string(perm), granted, granted,
	)
	return err
}

// ClearOverride removes a permission override, reverting to role default.
func (c *Checker) ClearOverride(memberID string, perm Permission) error {
	_, err := c.db.Exec(
		"DELETE FROM permission_overrides WHERE member_id = ? AND permission = ?",
		memberID, string(perm),
	)
	return err
}

// ClearAllOverrides removes all overrides for a member (e.g. on role change).
func (c *Checker) ClearAllOverrides(memberID string) error {
	_, err := c.db.Exec("DELETE FROM permission_overrides WHERE member_id = ?", memberID)
	return err
}
