// worker hosts the Temporal durable workers (§7.1): transfer, claim, and
// liveness-campaign workflows with activity retries.
package main

import (
	"log"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/munisp/pfaengine/internal/appx"
	"github.com/munisp/pfaengine/internal/workflow"
)

func main() {
	hostPort := appx.Env("TEMPORAL_HOST_PORT", "localhost:7233")
	c, err := client.Dial(client.Options{HostPort: hostPort, Namespace: appx.Env("TEMPORAL_NAMESPACE", "default")})
	if err != nil {
		log.Fatalf("[worker] temporal dial: %v", err)
	}
	defer c.Close()

	w := worker.New(c, "pfaengine-core", worker.Options{})
	wf := workflow.Activities{}
	w.RegisterWorkflow(workflow.TransferWorkflow)
	w.RegisterWorkflow(workflow.ClaimWorkflow)
	w.RegisterWorkflow(workflow.LivenessCampaignWorkflow)
	w.RegisterWorkflow(workflow.DeathClaimWorkflow)
	w.RegisterWorkflow(workflow.FCYReturnWorkflow)
	w.RegisterActivity(wf)

	log.Printf("[worker] task queue pfaengine-core on %s", hostPort)
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatalf("[worker] %v", err)
	}
}
