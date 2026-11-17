package guardauth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/bakhod1r/guard"
)

// Options tune Guard's password hashing and audit privacy.
type Options struct {
	// Argon2id parameters (memory in KiB). All zero keeps Guard's defaults
	// (64 MiB, 3, 2). Changing them rehashes each password on its next login.
	HashMemoryKiB uint32
	HashTime      uint32
	HashThreads   uint8
	// AuditEmailKey is AUDIT_EMAIL_KEY: hex, at least 32 bytes once decoded.
	// Empty leaves audit events with an unkeyed email fingerprint.
	AuditEmailKey string
	// SessionIdle and SessionAbsolute are SESSION_IDLE_TIMEOUT and
	// SESSION_ABSOLUTE_TIMEOUT. Zero keeps Guard's defaults (30 minutes, 7 days).
	SessionIdle     time.Duration
	SessionAbsolute time.Duration
	// AccessSecret (ACCESS_TOKEN_SECRET, >= 32 bytes) turns on access tokens
	// (HS256 JWT, AccessTTL long); the session becomes the refresh token.
	// AccessPreviousSecret still verifies tokens signed before a rotation.
	// Empty AccessSecret: sessions only.
	AccessSecret         string
	AccessPreviousSecret string
	AccessTTL            time.Duration
	// Migrate applies Guard's migrations and seeds permissions, roles and
	// policies (Seed, SeedAccessRules) when Open runs. MIGRATE_ON_START.
	Migrate bool
	// AccessRules are the config-driven moderator gates seeded with Migrate.
	AccessRules AccessRules
}

// Argon2id floor Guard enforces (OWASP minimum).
const (
	minHashMemoryKiB = 19456
	minHashTime      = 2
	minHashThreads   = 1
	minAuditEmailKey = 32
	hashKeyLen       = 32
	hashSaltLen      = 16
)

// hashParams returns Guard's argon2id override, nil for defaults, or an
// error naming the env vars when the values are below the floor.
func (o Options) hashParams() (*guard.PasswordHashParams, error) {
	if o.HashMemoryKiB == 0 && o.HashTime == 0 && o.HashThreads == 0 {
		return nil, nil
	}
	if o.HashMemoryKiB < minHashMemoryKiB || o.HashTime < minHashTime || o.HashThreads < minHashThreads {
		return nil, fmt.Errorf("PASSWORD_HASH_MEMORY_KIB/TIME/THREADS = %d/%d/%d: below the argon2id minimum %d/%d/%d",
			o.HashMemoryKiB, o.HashTime, o.HashThreads, minHashMemoryKiB, minHashTime, minHashThreads)
	}
	return &guard.PasswordHashParams{
		Memory: o.HashMemoryKiB, Time: o.HashTime, Threads: o.HashThreads,
		KeyLen: hashKeyLen, SaltLen: hashSaltLen,
	}, nil
}

// auditEmailKey decodes AUDIT_EMAIL_KEY; empty returns nil.
func (o Options) auditEmailKey() ([]byte, error) {
	if o.AuditEmailKey == "" {
		return nil, nil
	}
	key, err := hex.DecodeString(o.AuditEmailKey)
	if err != nil {
		return nil, fmt.Errorf("AUDIT_EMAIL_KEY: not hex: %w", err)
	}
	if len(key) < minAuditEmailKey {
		return nil, fmt.Errorf("AUDIT_EMAIL_KEY: %d bytes, need at least %d (openssl rand -hex 32)", len(key), minAuditEmailKey)
	}
	return key, nil
}

// accessToken builds Guard's access-token options; nil without a secret.
// Key ids derive from the secret, so rotating needs no extra setting.
func (o Options) accessToken() (*guard.AccessTokenOptions, error) {
	if o.AccessSecret == "" {
		return nil, nil
	}
	a := &guard.AccessTokenOptions{TTL: o.AccessTTL, Issuer: "iwillbegoogler", Audience: "iwillbegoogler"}
	for _, s := range []string{o.AccessSecret, o.AccessPreviousSecret} {
		if s == "" {
			continue
		}
		if len(s) < 32 {
			return nil, fmt.Errorf("ACCESS_TOKEN_SECRET: %d bytes, need at least 32 (openssl rand -hex 32)", len(s))
		}
		sum := sha256.Sum256([]byte(s))
		a.Keys = append(a.Keys, guard.AccessKey{ID: hex.EncodeToString(sum[:4]), Secret: []byte(s)})
	}
	return a, nil
}
