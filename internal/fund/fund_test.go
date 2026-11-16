package fund

import (
	"errors"
	"testing"
	"time"
)

func TestDefaultFund(t *testing.T) {
	cases := []struct {
		name    string
		age     int
		retired bool
		micro   bool
		want    string
	}{
		{"young active", 30, false, false, II},
		{"boundary 49", 49, false, false, II},
		{"boundary 50", 50, false, false, III},
		{"older active", 62, false, false, III},
		{"retiree", 55, true, false, IV},
		{"retiree beats micro", 60, true, true, IV},
		{"micro young", 25, false, true, VConservative},
		{"micro older", 55, false, true, VConservative},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DefaultFund(tc.age, tc.retired, tc.micro); got != tc.want {
				t.Fatalf("DefaultFund(%d,%v,%v) = %s, want %s", tc.age, tc.retired, tc.micro, got, tc.want)
			}
		})
	}
}

func TestValidateMoveProhibitions(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	never := time.Time{}
	cases := []struct {
		name       string
		from, to   string
		age        int
		retired    bool
		micro      bool
		returnPath bool
		wantErr    error
	}{
		{"III to I prohibited", III, I, 30, false, false, false, ErrProhibitedMove},
		{"II to III prohibited", II, III, 40, false, false, false, ErrProhibitedMove},
		{"II to III return path allowed", II, III, 40, false, false, true, nil},
		{"age 50 cannot enter I", III, I, 50, false, false, false, ErrProhibitedMove}, // III->I first
		{"age 50 II to I barred", II, I, 50, false, false, false, ErrAgeRestricted},
		{"retired cannot enter I", IV, I, 60, true, false, false, ErrAgeRestricted},
		{"non-micro into V barred", II, VConservative, 30, false, false, false, ErrMicroFundOnly},
		{"non-micro into V-Growth barred", II, VGrowth, 30, false, false, false, ErrMicroFundOnly},
		{"micro V to V allowed", VConservative, VGrowth, 30, false, true, false, nil},
		{"same fund", II, II, 30, false, false, false, ErrProhibitedMove},
		{"unknown from", "IX", II, 30, false, false, false, ErrUnknownFund},
		{"unknown to", II, "IX", 30, false, false, false, ErrUnknownFund},
		{"normal II to I young", II, I, 30, false, false, false, nil},
		{"III to IV at retirement", III, IV, 55, true, false, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateMove(tc.from, tc.to, tc.age, tc.retired, tc.micro, tc.returnPath, never, now)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateMoveFeeWindow(t *testing.T) {
	last := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		now     time.Time
		wantFee bool
	}{
		{"within 365 days charged", last.Add(364 * 24 * time.Hour), true},
		{"boundary exactly 365 days free", last.Add(365 * 24 * time.Hour), false},
		{"after 365 days free", last.Add(400 * 24 * time.Hour), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fee, err := ValidateMove(II, I, 30, false, false, false, last, tc.now)
			if err != nil {
				t.Fatal(err)
			}
			if fee != tc.wantFee {
				t.Fatalf("fee = %v, want %v", fee, tc.wantFee)
			}
		})
	}
	// First-ever move is free.
	fee, err := ValidateMove(II, I, 30, false, false, false, time.Time{}, last)
	if err != nil || fee {
		t.Fatalf("first move: fee=%v err=%v", fee, err)
	}
}
