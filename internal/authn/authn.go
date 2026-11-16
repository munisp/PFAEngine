// Package authn implements the identity-service core: accounts, RBAC,
// TOTP MFA (RFC 6238, pure Go), and HMAC-signed session tokens.
package authn

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrBadCredentials  = errors.New("authn: invalid credentials")
	ErrMFARequired     = errors.New("authn: MFA code required")
	ErrBadMFA          = errors.New("authn: invalid TOTP code")
	ErrForbidden       = errors.New("authn: insufficient role")
	ErrBadToken        = errors.New("authn: invalid token")
	ErrAccountDisabled = errors.New("authn: account disabled pending onboarding approval")
)

type Role string

const (
	RoleTenantAdmin Role = "TENANT_ADMIN"
	RoleOps         Role = "OPS"
	RoleCompliance  Role = "COMPLIANCE"
	RoleMember      Role = "MEMBER"
	RoleEmployer    Role = "EMPLOYER"
	RoleAuditor     Role = "AUDITOR"
)

type Account struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenant_id"`
	Email       string `json:"email"`
	PassHash    string `json:"-"`
	Roles       []Role `json:"roles"`
	TOTPSecret  string `json:"-"`
	MFAEnrolled bool   `json:"mfa_enrolled"`
	// Disabled gates login until the staff onboarding case (FR-520) is
	// approved; existing flows create enabled accounts (zero value).
	Disabled  bool      `json:"disabled"`
	CreatedAt time.Time `json:"created_at"`
}

type Service struct {
	mu       sync.Mutex
	accounts map[string]*Account // email -> account
	secret   []byte
	seq      int
}

func NewService(signingSecret string) *Service {
	return &Service{accounts: map[string]*Account{}, secret: []byte(signingSecret)}
}

func hashPassword(pw, salt string) string {
	h := sha256.Sum256([]byte(salt + ":" + pw))
	return base64.StdEncoding.EncodeToString(h[:])
}

func (s *Service) CreateAccount(tenantID, email, password string, roles ...Role) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[email]; ok {
		return nil, errors.New("authn: account exists")
	}
	s.seq++
	a := &Account{
		ID: fmt.Sprintf("acct-%d", s.seq), TenantID: tenantID, Email: email,
		PassHash: hashPassword(password, s.IDSalt()), Roles: roles, CreatedAt: time.Now().UTC(),
	}
	s.accounts[email] = a
	return a, nil
}

func (s *Service) IDSalt() string { return string(s.secret)[:min(16, len(s.secret))] }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// EnrollMFA generates a TOTP secret (base32, RFC 6238).
func (s *Service) EnrollMFA(email string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[email]
	if !ok {
		return "", ErrBadCredentials
	}
	raw := make([]byte, 20)
	_, _ = rand.Read(raw)
	a.TOTPSecret = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	a.MFAEnrolled = true
	return a.TOTPSecret, nil
}

// SetDisabled toggles the login gate (FR-520 staff onboarding: the account
// is created disabled and activated when the onboarding case is approved).
func (s *Service) SetDisabled(email string, disabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[email]
	if !ok {
		return ErrBadCredentials
	}
	a.Disabled = disabled
	return nil
}

// TOTP computes the 6-digit code for a secret at a time (RFC 6238, SHA-1, 30s).
func TOTP(secretB32 string, at time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secretB32))
	if err != nil {
		return "", err
	}
	counter := uint64(at.Unix() / 30)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	h := hmac.New(sha1.New, key)
	h.Write(buf[:])
	sum := h.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[off])&0x7f)<<24 | uint32(sum[off+1])<<16 | uint32(sum[off+2])<<8 | uint32(sum[off+3])
	return fmt.Sprintf("%06d", code%1000000), nil
}

// Login verifies credentials + TOTP, and issues a signed session token.
func (s *Service) Login(email, password, totpCode string, now time.Time) (string, error) {
	s.mu.Lock()
	a, ok := s.accounts[email]
	s.mu.Unlock()
	if !ok || a.PassHash != hashPassword(password, s.IDSalt()) {
		return "", ErrBadCredentials
	}
	if a.Disabled {
		return "", ErrAccountDisabled
	}
	if a.MFAEnrolled {
		if totpCode == "" {
			return "", ErrMFARequired
		}
		want, err := TOTP(a.TOTPSecret, now)
		if err != nil || totpCode != want {
			return "", ErrBadMFA
		}
	}
	return s.issueToken(a)
}

type claims struct {
	Sub      string `json:"sub"`
	TenantID string `json:"tid"`
	Roles    []Role `json:"roles"`
	Exp      int64  `json:"exp"`
}

func (s *Service) issueToken(a *Account) (string, error) {
	c := claims{Sub: a.ID, TenantID: a.TenantID, Roles: a.Roles, Exp: time.Now().Add(8 * time.Hour).Unix()}
	body, _ := json.Marshal(c)
	sig := hmac.New(sha256.New, s.secret)
	sig.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(sig.Sum(nil)), nil
}

// VerifyToken validates signature + expiry.
func (s *Service) VerifyToken(token string) (tenantID string, roles []Role, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", nil, ErrBadToken
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", nil, ErrBadToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", nil, ErrBadToken
	}
	want := hmac.New(sha256.New, s.secret)
	want.Write(body)
	if !hmac.Equal(sig, want.Sum(nil)) {
		return "", nil, ErrBadToken
	}
	var c claims
	if err := json.Unmarshal(body, &c); err != nil {
		return "", nil, ErrBadToken
	}
	if time.Now().Unix() > c.Exp {
		return "", nil, ErrBadToken
	}
	return c.TenantID, c.Roles, nil
}

// RequireRole enforces RBAC.
func RequireRole(have []Role, need Role) error {
	for _, r := range have {
		if r == need || r == RoleTenantAdmin {
			return nil
		}
	}
	return ErrForbidden
}
