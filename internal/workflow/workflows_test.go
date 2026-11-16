package workflow

import (
	"testing"
	"time"

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

// registerAll registers every activity the workflows invoke.
func registerAll(env *testsuite.TestWorkflowEnvironment) {
	for name, fn := range ActivityRegistry(Activities{}) {
		env.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}
}

// DeathClaimWorkflow: docs + approval reach payout and NOK notification.
func TestDeathClaimWorkflowHappy(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestWorkflowEnvironment()
	registerAll(env)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalDocsVerified, nil) }, 0)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalClaimDecision, ClaimDecision{Approver: "checker1", Approve: true})
	}, 0)
	env.ExecuteWorkflow(DeathClaimWorkflow, DeathClaimInput{
		TenantID: "t1", ClaimID: "dc-1", RSAID: "rsa-1", NOKID: "nok-1", AmountKobo: 500000000})
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
	if state != "NOK_NOTIFIED" {
		t.Fatalf("state %s", state)
	}
}

// DeathClaimWorkflow: docs SLA breach escalates after 7 days.
func TestDeathClaimWorkflowDocsSLA(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestWorkflowEnvironment()
	registerAll(env)
	env.ExecuteWorkflow(DeathClaimWorkflow, DeathClaimInput{TenantID: "t1", ClaimID: "dc-2", NOKID: "nok-2"})
	var state string
	if err := env.GetWorkflowResult(&state); err != nil {
		t.Fatal(err)
	}
	if state != "ESCALATED_DOCS_PENDING" {
		t.Fatalf("state %s", state)
	}
}

// DeathClaimWorkflow: rejection notifies the NOK and stops before payout.
func TestDeathClaimWorkflowReject(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestWorkflowEnvironment()
	registerAll(env)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalDocsVerified, nil) }, 0)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalClaimDecision, ClaimDecision{Approver: "checker1", Approve: false, Reason: "probate invalid"})
	}, 0)
	env.ExecuteWorkflow(DeathClaimWorkflow, DeathClaimInput{TenantID: "t1", ClaimID: "dc-3", NOKID: "nok-3"})
	var state string
	if err := env.GetWorkflowResult(&state); err != nil {
		t.Fatal(err)
	}
	if state != "REJECTED" {
		t.Fatalf("state %s", state)
	}
}

// FCYReturnWorkflow: return confirmed within 48 hours.
func TestFCYReturnWorkflowHappy(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestWorkflowEnvironment()
	registerAll(env)
	env.RegisterDelayedCallback(func() { env.SignalWorkflow(SignalFCYReturned, nil) }, time.Hour)
	env.ExecuteWorkflow(FCYReturnWorkflow, FCYReturnInput{TenantID: "t1", Ref: "fcy-1", AmountCents: 250000})
	var state string
	if err := env.GetWorkflowResult(&state); err != nil {
		t.Fatal(err)
	}
	if state != "RETURNED" {
		t.Fatalf("state %s", state)
	}
}

// FCYReturnWorkflow: 48-hour SLA breach raises the alarm.
func TestFCYReturnWorkflowSLABreach(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestWorkflowEnvironment()
	registerAll(env)
	env.ExecuteWorkflow(FCYReturnWorkflow, FCYReturnInput{TenantID: "t1", Ref: "fcy-2", AmountCents: 99000})
	var state string
	if err := env.GetWorkflowResult(&state); err != nil {
		t.Fatal(err)
	}
	if state != "SLA_BREACHED" {
		t.Fatalf("state %s", state)
	}
}

// ClaimWorkflow: docs SLA breach escalates.
func TestClaimWorkflowDocsSLA(t *testing.T) {
	ts := &testsuite.WorkflowTestSuite{}
	env := ts.NewTestWorkflowEnvironment()
	registerAll(env)
	env.ExecuteWorkflow(ClaimWorkflow, ClaimInput{TenantID: "t1", ClaimID: "clm-3"})
	var state string
	if err := env.GetWorkflowResult(&state); err != nil {
		t.Fatal(err)
	}
	if state != "ESCALATED_DOCS_PENDING" {
		t.Fatalf("state %s", state)
	}
}
