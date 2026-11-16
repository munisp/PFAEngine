package vendors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/munisp/pfaengine/internal/kyc"
)

// Dojah implements kyc.Provider (NIN + BVN via Dojah KYC APIs).
type Dojah struct {
	AppID     string
	SecretKey string
	BaseURL   string // https://api.dojah.io
	HTTP      *http.Client
}

func (d *Dojah) Name() string { return "dojah" }

func (d *Dojah) client() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (d *Dojah) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", d.BaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("AppId", d.AppID)
	req.Header.Set("Authorization", d.SecretKey)
	resp, err := d.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("dojah: %s: %s", resp.Status, string(raw))
	}
	return json.Unmarshal(raw, out)
}

type dojahIDResp struct {
	Entity struct {
		FirstName string `json:"first_name"`
	} `json:"entity"`
}

func (d *Dojah) VerifyNIN(nin, name, dob string) (kyc.VerifyResult, error) {
	var out dojahIDResp
	if err := d.get(context.Background(), "/api/v1/kyc/nin/full?nin="+nin, &out); err != nil {
		return kyc.VerifyResult{}, err
	}
	return kyc.VerifyResult{
		ProviderRef: fmt.Sprintf("dojah-nin-%d", time.Now().UnixNano()),
		Token:       hexHash(d.SecretKey + "|" + nin),
		Matched:     out.Entity.FirstName != "",
		EvidenceRef: "ev://dojah/nin/" + nin[len(nin)-4:],
	}, nil
}

func (d *Dojah) ValidateBVN(bvn, name, dob, phone string) (kyc.VerifyResult, error) {
	var out dojahIDResp
	if err := d.get(context.Background(), "/api/v1/kyc/bvn/full?bvn="+bvn, &out); err != nil {
		return kyc.VerifyResult{}, err
	}
	return kyc.VerifyResult{
		ProviderRef: fmt.Sprintf("dojah-bvn-%d", time.Now().UnixNano()),
		Matched:     out.Entity.FirstName != "",
		EvidenceRef: "ev://dojah/bvn/" + bvn[:4],
	}, nil
}

func (d *Dojah) LivenessFaceMatch(selfieRef, ninToken string) (float64, string, error) {
	return 0, "", fmt.Errorf("dojah: liveness via widget flow; selfieRef=%s queued", selfieRef)
}
