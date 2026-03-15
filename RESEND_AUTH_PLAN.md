# Resend Auth Pipeline Plan

## Overview

Polish the authentication pipeline using Resend for transactional emails. Fix forgot password, wire up email verification, add confirmation emails.

## Current State

- Resend API integration exists (`RESEND_API_KEY` env var, `internal/server/verification.go`)
- `email_verifications` table exists (migration v4) but is **never used**
- Password change works but has no recovery path
- Email invites already send via Resend
- `accountRequired()` returns true when `RESEND_API_KEY` is set

## Changes

### 1. Forgot Password Flow (Priority)

**Backend** (`internal/server/auth.go`):

New table (migration):
```sql
CREATE TABLE IF NOT EXISTS password_resets (
  id TEXT PRIMARY KEY,
  email TEXT NOT NULL,
  token TEXT NOT NULL UNIQUE,
  expires_at TEXT NOT NULL,
  used BOOLEAN DEFAULT FALSE,
  created_at TEXT NOT NULL
);
```

New endpoints:
- `POST /api/auth/forgot-password` — accepts `{ email }`, generates token, sends Resend email
  - Generate 32-byte random token
  - Store in `password_resets` with 1hr expiry
  - Send email via Resend with link: `https://nexus-workspace.fly.dev/reset?token=...`
  - Always return 200 (don't leak whether email exists)

- `POST /api/auth/reset-password` — accepts `{ token, new_password }`
  - Validate token exists, not expired, not used
  - Update password hash in `accounts`
  - Mark token as used
  - Send confirmation email ("Your password was changed")
  - Return 200

**Frontend** (`web/src/routes/(app)/+page.svelte`):
- Add "Forgot password?" link below password field on login form
- Click shows email input + "Send reset link" button
- New route or modal for `/reset?token=...` — new password + confirm form

**Email template:**
```html
Subject: Reset your Nexus password

<h2>Password Reset</h2>
<p>Click the link below to reset your password. This link expires in 1 hour.</p>
<a href="{{resetURL}}">Reset Password</a>
<p>If you didn't request this, you can safely ignore this email.</p>
```

### 2. Email Verification on Signup

**Backend** (`internal/server/auth.go`):

Wire up existing `email_verifications` table:

- In `handleRegister()`: after creating account, generate 6-digit code, insert into `email_verifications`, send via Resend
- New endpoint: `POST /api/auth/verify-email` — accepts `{ email, code }`
  - Validate code matches, not expired
  - Mark as verified
  - Add `email_verified` column to `accounts` table (migration)
- In `handleLogin()`: if `accountRequired()` and `!email_verified`, return error with message to check email

**Frontend:**
- After signup, show verification code input screen
- "Didn't receive it? Resend" button
- Auto-redirect to workspace after verification

**Email template:**
```html
Subject: Verify your Nexus account

<h2>Welcome to Nexus</h2>
<p>Your verification code is:</p>
<h1 style="letter-spacing: 8px; font-size: 32px;">{{code}}</h1>
<p>This code expires in 15 minutes.</p>
```

### 3. Password Change Confirmation Email

**Backend** (`internal/server/auth.go`):

- In `handleChangePassword()`: after successful change, send confirmation email via Resend
- No new endpoints needed

**Email template:**
```html
Subject: Your Nexus password was changed

<h2>Password Changed</h2>
<p>Your password was successfully changed on {{date}}.</p>
<p>If you didn't make this change, contact your workspace admin immediately.</p>
```

### 4. Shared Email Sending Helper

Consolidate email sending into a clean helper:

```go
// internal/server/email_transactional.go
func (s *Server) sendTransactionalEmail(to, subject, html string) error {
    // Uses Resend API (existing code in verification.go)
    // Consistent from address: "Nexus <noreply@nexus-workspace.fly.dev>"
    // Wraps in HTML template with Nexus branding
}
```

### 5. Email HTML Template

Shared wrapper for all transactional emails:
```html
<!DOCTYPE html>
<html>
<body style="font-family: -apple-system, sans-serif; max-width: 480px; margin: 0 auto; padding: 40px 20px;">
  <div style="text-align: center; margin-bottom: 32px;">
    <svg><!-- Nexus logo --></svg>
  </div>
  {{content}}
  <hr style="margin: 32px 0; border: none; border-top: 1px solid #333;">
  <p style="font-size: 12px; color: #888;">Nexus Workspace · nexus-workspace.fly.dev</p>
</body>
</html>
```

## Database Migrations

Add to next migration version:
1. `password_resets` table (new)
2. `ALTER TABLE accounts ADD COLUMN email_verified BOOLEAN DEFAULT FALSE`
3. Mark existing accounts as verified (they were created before this feature)

## Testing

1. `make dev` with `RESEND_API_KEY` set
2. Register new account → check email for verification code → enter code → login works
3. Login → "Forgot password?" → enter email → check email for reset link → reset → login with new password
4. Login → change password → check email for confirmation
5. Try forgot password with non-existent email → still returns 200 (no leak)
6. Try expired/used reset token → returns error
