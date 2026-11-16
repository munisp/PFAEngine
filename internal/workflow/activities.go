package workflow

import (
	"context"
	"fmt"
	"log"
)

// Activities bridge durable workflows to the domain services.
// In production these call the HTTP services / Postgres; the default
// implementations are structured logs so workers run without side effects.

func (Activities) ValidateTransfer(ctx context.Context, ti TransferInput) error {
	actlog(ctx, "transfer.validated id=%s pin=%s", ti.TransferID, ti.RSAPIN)
	return nil
}

func (Activities) AwaitRTSWindow(ctx context.Context, quarter string) error {
	actlog(ctx, "rts.window.await quarter=%s", quarter)
	return nil
}

func (Activities) SubmitToRTS(ctx context.Context, transferID string) error {
	actlog(ctx, "rts.submitted id=%s", transferID)
	return nil
}

func (Activities) RaiseSLAAlarm(ctx context.Context, transferID string) error {
	actlog(ctx, "rts.sla.alarm id=%s", transferID)
	return nil
}

func (Activities) SettleTransferValue(ctx context.Context, transferID string, valueKobo int64) error {
	actlog(ctx, "rts.settled id=%s value=%d", transferID, valueKobo)
	return nil
}

func (Activities) NotifyMember(ctx context.Context, ref string) error {
	actlog(ctx, "member.notified ref=%s", ref)
	return nil
}

func (Activities) EscalateClaim(ctx context.Context, claimID string) error {
	actlog(ctx, "claim.escalated id=%s", claimID)
	return nil
}

func (Activities) InstructCustodianPayment(ctx context.Context, claimID string, amountKobo int64) error {
	actlog(ctx, "custodian.instructed id=%s amount=%d", claimID, amountKobo)
	return nil
}

func (Activities) CompensateClaimApproval(ctx context.Context, claimID string) error {
	actlog(ctx, "claim.compensated id=%s", claimID)
	return nil
}

func (Activities) SendLivenessReminder(ctx context.Context, rsaID string) error {
	actlog(ctx, "liveness.reminder rsa=%s", rsaID)
	return nil
}

func (Activities) SuspendRetiree(ctx context.Context, rsaID string) error {
	actlog(ctx, "retiree.suspended rsa=%s", rsaID)
	return nil
}

func (Activities) ReinstateRetiree(ctx context.Context, rsaID string) error {
	actlog(ctx, "retiree.reinstated rsa=%s", rsaID)
	return nil
}

// EscalateDeathClaim flags a death-benefit claim whose documentation is
// still pending after the 7-day SLA (FR-512).
func (Activities) EscalateDeathClaim(ctx context.Context, claimID string) error {
	actlog(ctx, "death.escalated id=%s", claimID)
	return nil
}

// PayDeathBenefit instructs the custodian payout to beneficiaries (FR-512).
func (Activities) PayDeathBenefit(ctx context.Context, claimID string, amountKobo int64) error {
	actlog(ctx, "death.payout id=%s amount=%d", claimID, amountKobo)
	return nil
}

// NotifyNOK notifies the next-of-kin of a death-claim outcome (FR-512/513).
func (Activities) NotifyNOK(ctx context.Context, nokID, claimID string) error {
	actlog(ctx, "nok.notified nok=%s claim=%s", nokID, claimID)
	return nil
}

// InitiateFCYReturn starts the return of an uncreditable FCY contribution
// (BR-22 48-hour window).
func (Activities) InitiateFCYReturn(ctx context.Context, ref string, amountCents int64) error {
	actlog(ctx, "fcy.return.initiated ref=%s cents=%d", ref, amountCents)
	return nil
}

// RaiseFCYSLAAlarm raises the 48-hour FCY-return SLA breach alarm.
func (Activities) RaiseFCYSLAAlarm(ctx context.Context, ref string) error {
	actlog(ctx, "fcy.return.sla.alarm ref=%s", ref)
	return nil
}

func actlog(ctx context.Context, format string, args ...any) {
	log.Printf("[activity] "+format, args...)
}

var _ = fmt.Sprintf // keep fmt import
