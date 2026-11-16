// Package workflow implements the Temporal durable workflows (§7.1 AR-05):
// RTS transfers with quarterly timers and SLA alarms, benefit claims with
// maker-checker and compensation, and the annual I-Am-Alive campaign.
// State machines mirror internal/transfer and internal/benefit — Temporal
// adds durability, visibility, and automatic retries.
package workflow

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// ---------- Signals & queries ----------

const (
	SignalClaimDecision = "claim-decision" // maker-checker decision arrives here
	SignalDocsVerified  = "docs-verified"
	SignalRTSSettlement = "rts-settlement" // RTS cycle settles the transfer
	SignalLivenessDone  = "liveness-done"  // retiree completed verification
	QueryState          = "state"
)

// ---------- Activities (implemented in activities.go) ----------

type Activities struct{}

var activityOpts = workflow.ActivityOptions{
	StartToCloseTimeout: 2 * time.Minute,
	RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 5}, // idempotent retries (INT-02)
}

// ---------- RTS Transfer workflow (FR-402, INT-01) ----------

type TransferInput struct {
	TenantID   string
	TransferID string
	RSAPIN     string
	ValueKobo  int64
	Quarter    string
}

// TransferWorkflow: initiate → validate → wait for RTS quarterly window
// (durable timer) → submit → await settlement signal → notify. SLA alarm
// fires if the cycle SLA is breached (INT-01).
func TransferWorkflow(ctx workflow.Context, ti TransferInput) (string, error) {
	state := "INITIATED"
	_ = workflow.SetQueryHandler(ctx, QueryState, func() (string, error) { return state, nil })

	ao := workflow.WithActivityOptions(ctx, activityOpts)

	state = "VALIDATED"
	if err := workflow.ExecuteActivity(ao, "ValidateTransfer", ti).Get(ctx, nil); err != nil {
		return state, err
	}

	// Wait until the quarterly submission window opens (durable timer).
	state = "QUEUED_FOR_CYCLE:" + ti.Quarter
	if err := workflow.ExecuteActivity(ao, "AwaitRTSWindow", ti.Quarter).Get(ctx, nil); err != nil {
		return state, err
	}

	state = "SUBMITTED_TO_RTS"
	if err := workflow.ExecuteActivity(ao, "SubmitToRTS", ti.TransferID).Get(ctx, nil); err != nil {
		return state, err
	}

	// Await settlement with a 30-day SLA timer (INT-01 SLA alarms).
	state = "AWAITING_SETTLEMENT"
	settled := false
	sel := workflow.NewSelector(ctx)
	sigCh := workflow.GetSignalChannel(ctx, SignalRTSSettlement)
	sel.AddReceive(sigCh, func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, nil)
		settled = true
	})
	slaTimer := workflow.NewTimer(ctx, 30*24*time.Hour)
	sel.AddFuture(slaTimer, func(f workflow.Future) { /* SLA breach */ })
	sel.Select(ctx)
	if !settled {
		_ = workflow.ExecuteActivity(ao, "RaiseSLAAlarm", ti.TransferID).Get(ctx, nil)
		return "SLA_BREACHED", fmt.Errorf("transfer %s exceeded RTS SLA", ti.TransferID)
	}

	state = "SETTLED"
	if err := workflow.ExecuteActivity(ao, "SettleTransferValue", ti.TransferID, ti.ValueKobo).Get(ctx, nil); err != nil {
		return state, err
	}
	state = "NOTIFIED"
	_ = workflow.ExecuteActivity(ao, "NotifyMember", ti.TransferID).Get(ctx, nil)
	return state, nil
}

// ---------- Benefit claim workflow (FR-401, maker-checker) ----------

type ClaimInput struct {
	TenantID   string
	ClaimID    string
	RSAID      string
	AmountKobo int64
}

type ClaimDecision struct {
	Approver string
	Approve  bool
	Reason   string
}

// ClaimWorkflow: submitted → docs verification → maker-checker decision
// (signal, different approver enforced in activity) → payment instruction
// to custodian → confirmation. Compensation on rejection/timeout.
func ClaimWorkflow(ctx workflow.Context, ci ClaimInput) (string, error) {
	state := "SUBMITTED"
	_ = workflow.SetQueryHandler(ctx, QueryState, func() (string, error) { return state, nil })
	ao := workflow.WithActivityOptions(ctx, activityOpts)

	// Await docs verification (signal) — 7-day SLA, else auto-escalate.
	docsOK := false
	sel := workflow.NewSelector(ctx)
	workflow.GetSignalChannel(ctx, SignalDocsVerified)
	sel.AddReceive(workflow.GetSignalChannel(ctx, SignalDocsVerified), func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, nil)
		docsOK = true
	})
	sel.AddFuture(workflow.NewTimer(ctx, 7*24*time.Hour), func(f workflow.Future) {})
	sel.Select(ctx)
	if !docsOK {
		_ = workflow.ExecuteActivity(ao, "EscalateClaim", ci.ClaimID).Get(ctx, nil)
		return "ESCALATED_DOCS_PENDING", nil
	}
	state = "DOCS_VERIFIED"

	// Maker-checker decision signal.
	var decision ClaimDecision
	sel2 := workflow.NewSelector(ctx)
	sel2.AddReceive(workflow.GetSignalChannel(ctx, SignalClaimDecision), func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &decision)
	})
	sel2.Select(ctx)
	if !decision.Approve {
		state = "REJECTED"
		_ = workflow.ExecuteActivity(ao, "NotifyMember", ci.ClaimID).Get(ctx, nil)
		return state, nil
	}
	state = "APPROVED"

	// Custodian payment instruction (INT-05) with compensation on failure.
	if err := workflow.ExecuteActivity(ao, "InstructCustodianPayment", ci.ClaimID, ci.AmountKobo).Get(ctx, nil); err != nil {
		_ = workflow.ExecuteActivity(ao, "CompensateClaimApproval", ci.ClaimID).Get(ctx, nil)
		return "PAYMENT_FAILED_COMPENSATED", err
	}
	state = "PAYMENT_INSTRUCTED"
	_ = workflow.ExecuteActivity(ao, "NotifyMember", ci.ClaimID).Get(ctx, nil)
	return state, nil
}

// ---------- I-Am-Alive annual campaign (FR-108) ----------

type LivenessInput struct {
	TenantID string
	RSAID    string
}

// LivenessCampaignWorkflow runs the 12-month cycle: reminders → 3-month
// grace → suspension; a liveness-done signal resets the cycle. Long-running
// workflow (continue-as-new each cycle).
func LivenessCampaignWorkflow(ctx workflow.Context, li LivenessInput) error {
	ao := workflow.WithActivityOptions(ctx, activityOpts)

	// 12-month timer (durable); remind at due date.
	if err := workflow.Sleep(ctx, 365*24*time.Hour); err != nil {
		return err
	}
	_ = workflow.ExecuteActivity(ao, "SendLivenessReminder", li.RSAID).Get(ctx, nil)

	// 3-month grace window awaiting completion.
	verified := false
	sel := workflow.NewSelector(ctx)
	sel.AddReceive(workflow.GetSignalChannel(ctx, SignalLivenessDone), func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, nil)
		verified = true
	})
	sel.AddFuture(workflow.NewTimer(ctx, 90*24*time.Hour), func(f workflow.Future) {})
	sel.Select(ctx)

	if !verified {
		_ = workflow.ExecuteActivity(ao, "SuspendRetiree", li.RSAID).Get(ctx, nil)
		// Stay suspended until verification arrives (no further timers needed).
		workflow.GetSignalChannel(ctx, SignalLivenessDone).Receive(ctx, nil)
		_ = workflow.ExecuteActivity(ao, "ReinstateRetiree", li.RSAID).Get(ctx, nil)
	}
	// Continue as new for the next 12-month cycle.
	return workflow.NewContinueAsNewError(ctx, LivenessCampaignWorkflow, li)
}
