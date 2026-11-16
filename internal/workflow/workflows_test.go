package workflow

import (
	"testing"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

// ClaimWorkflow: approve path reaches PAYMENT_INSTRUCTED.
func TestClaimWorkflowApprove(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestWorkflowEnvironment()
	a := Activities{}
	for name, fn := range map[string]any{
		"ValidateTransfer": a.ValidateTransfer, "AwaitRTSWindow": a.AwaitRTSWindow,
		"SubmitToRTS": a.SubmitToRTS, "RaiseSLAAlarm": a.RaiseSLAAlarm,
		"SettleTransferValue": a.SettleTransferValue, "NotifyMember": a.NotifyMember,
		"EscalateClaim": a.EscalateClaim, "InstructCustodianPayment": a.InstructCustodianPayment,
		"CompensateClaimApproval": a.CompensateClaimApproval,
		"SendLivenessReminder":    a.SendLivenessReminder, "SuspendRetiree": a.SuspendRetiree,
		"ReinstateRetiree": a.ReinstateRetiree,
	} {
		env.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}

	// Deliver signals after workflow blocks on them.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalDocsVerified, nil)
	}, 0)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalClaimDecision, ClaimDecision{Approver: "checker1", Approve: true})
	}, 0)

	env.ExecuteWorkflow(ClaimWorkflow, ClaimInput{TenantID: "t1", ClaimID: "clm-1", RSAID: "rsa-1", AmountKobo: 100000000})
	if !env.IsWorkflowCompleted() {
		t.Fatal("workflow did not complete")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := env.GetWorkflowResult(&state); err != nil {
		t.Fatal(err)
	}
	if state != "PAYMENT_INSTRUCTED" {
		t.Fatalf("state %s", state)
	}
}

// ClaimWorkflow: rejection path returns REJECTED.
func TestClaimWorkflowReject(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestWorkflowEnvironment()
	a := Activities{}
	for name, fn := range map[string]any{
		"ValidateTransfer": a.ValidateTransfer, "AwaitRTSWindow": a.AwaitRTSWindow,
		"SubmitToRTS": a.SubmitToRTS, "RaiseSLAAlarm": a.RaiseSLAAlarm,
		"SettleTransferValue": a.SettleTransferValue, "NotifyMember": a.NotifyMember,
		"EscalateClaim": a.EscalateClaim, "InstructCustodianPayment": a.InstructCustodianPayment,
		"CompensateClaimApproval": a.CompensateClaimApproval,
		"SendLivenessReminder":    a.SendLivenessReminder, "SuspendRetiree": a.SuspendRetiree,
		"ReinstateRetiree": a.ReinstateRetiree,
	} {
		env.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalDocsVerified, nil) }, 0)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalClaimDecision, ClaimDecision{Approver: "checker1", Approve: false, Reason: "docs mismatch"})
	}, 0)
	env.ExecuteWorkflow(ClaimWorkflow, ClaimInput{TenantID: "t1", ClaimID: "clm-2", RSAID: "rsa-1", AmountKobo: 5000})
	var state string
	if err := env.GetWorkflowResult(&state); err != nil {
		t.Fatal(err)
	}
	if state != "REJECTED" {
		t.Fatalf("state %s", state)
	}
}

// ClaimWorkflow: docs SLA breach escalates.
func TestClaimWorkflowDocsSLA(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestWorkflowEnvironment()
	a := Activities{}
	for name, fn := range map[string]any{
		"ValidateTransfer": a.ValidateTransfer, "AwaitRTSWindow": a.AwaitRTSWindow,
		"SubmitToRTS": a.SubmitToRTS, "RaiseSLAAlarm": a.RaiseSLAAlarm,
		"SettleTransferValue": a.SettleTransferValue, "NotifyMember": a.NotifyMember,
		"EscalateClaim": a.EscalateClaim, "InstructCustodianPayment": a.InstructCustodianPayment,
		"CompensateClaimApproval": a.CompensateClaimApproval,
		"SendLivenessReminder":    a.SendLivenessReminder, "SuspendRetiree": a.SuspendRetiree,
		"ReinstateRetiree": a.ReinstateRetiree,
	} {
		env.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}
	env.ExecuteWorkflow(ClaimWorkflow, ClaimInput{TenantID: "t1", ClaimID: "clm-3"})
	var state string
	if err := env.GetWorkflowResult(&state); err != nil {
		t.Fatal(err)
	}
	if state != "ESCALATED_DOCS_PENDING" {
		t.Fatalf("state %s", state)
	}
}
