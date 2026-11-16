package kyc

import (
	"errors"
	"testing"
)

func TestValidateBVNFormat(t *testing.T) {
	tests := []struct {
		bvn  string
		want error
	}{
		{"12345678901", nil},
		{"00000000000", nil},
		{"1234567890", ErrBVNFormat},   // 10 digits
		{"123456789012", ErrBVNFormat}, // 12 digits
		{"1234567890a", ErrBVNFormat},  // non-numeric
		{"", ErrBVNFormat},
		{"12345 78901", ErrBVNFormat}, // space
	}
	for _, tt := range tests {
		if err := ValidateBVNFormat(tt.bvn); !errors.Is(err, tt.want) {
			t.Fatalf("BVN %q: got %v, want %v", tt.bvn, err, tt.want)
		}
	}
}

func TestValidateNINFormat(t *testing.T) {
	tests := []struct {
		nin  string
		want error
	}{
		{"12345678901", nil},
		{"1234567890", ErrNINFormat},
		{"123456789012", ErrNINFormat},
		{"abcdefghijk", ErrNINFormat},
		{"", ErrNINFormat},
	}
	for _, tt := range tests {
		if err := ValidateNINFormat(tt.nin); !errors.Is(err, tt.want) {
			t.Fatalf("NIN %q: got %v, want %v", tt.nin, err, tt.want)
		}
	}
}

// FR-514: format validation is wired into registration verification.
func TestServiceRejectsBadFormats(t *testing.T) {
	s := NewService("salt", MockProvider{ProviderName: "mock"})
	if _, _, err := s.VerifyNIN("t1", "p1", "12345", "A", "1992-01-01"); !errors.Is(err, ErrNINFormat) {
		t.Fatalf("short NIN: %v", err)
	}
	if _, _, err := s.VerifyNIN("t1", "p1", "1234567890a", "A", "1992-01-01"); !errors.Is(err, ErrNINFormat) {
		t.Fatalf("alpha NIN: %v", err)
	}
	if _, _, err := s.ValidateBVN("t1", "p1", "123", "A", "1992-01-01", "0803"); !errors.Is(err, ErrBVNFormat) {
		t.Fatalf("short BVN: %v", err)
	}
	if _, _, err := s.ValidateBVN("t1", "p1", "98765432109", "A", "1992-01-01", "0803"); err != nil {
		t.Fatalf("valid BVN rejected: %v", err)
	}
}
