package workflow

import (
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
)

// ActivityRegistry maps every activity name referenced by the workflows to
// its implementation. Workflows invoke activities by string name, so each
// method must be registered with a matching explicit name — registering the
// Activities struct directly panics in SDK v1.29.
func ActivityRegistry(a Activities) map[string]any {
	return map[string]any{
		"ValidateTransfer": a.ValidateTransfer, "AwaitRTSWindow": a.AwaitRTSWindow,
		"SubmitToRTS": a.SubmitToRTS, "RaiseSLAAlarm": a.RaiseSLAAlarm,
		"SettleTransferValue": a.SettleTransferValue, "NotifyMember": a.NotifyMember,
		"EscalateClaim": a.EscalateClaim, "InstructCustodianPayment": a.InstructCustodianPayment,
		"CompensateClaimApproval": a.CompensateClaimApproval,
		"SendLivenessReminder":    a.SendLivenessReminder, "SuspendRetiree": a.SuspendRetiree,
		"ReinstateRetiree":   a.ReinstateRetiree,
		"EscalateDeathClaim": a.EscalateDeathClaim, "PayDeathBenefit": a.PayDeathBenefit,
		"NotifyNOK": a.NotifyNOK, "InitiateFCYReturn": a.InitiateFCYReturn,
		"RaiseFCYSLAAlarm": a.RaiseFCYSLAAlarm,
	}
}

// RegisterAll registers all workflows and activities on a live worker.
func RegisterAll(w worker.Worker) {
	w.RegisterWorkflow(TransferWorkflow)
	w.RegisterWorkflow(ClaimWorkflow)
	w.RegisterWorkflow(LivenessCampaignWorkflow)
	w.RegisterWorkflow(DeathClaimWorkflow)
	w.RegisterWorkflow(FCYReturnWorkflow)
	for name, fn := range ActivityRegistry(Activities{}) {
		w.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}
}
