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

func actlog(ctx context.Context, format string, args ...any) {
	log.Printf("[activity] "+format, args...)
}

var _ = fmt.Sprintf // keep fmt import
