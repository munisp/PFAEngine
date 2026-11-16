package main

import (
	"context"
	"fmt"
	"time"

	pfawf "github.com/munisp/pfaengine/internal/workflow"
	"go.temporal.io/sdk/client"
)

func main() {
	c, err := client.Dial(client.Options{HostPort: "localhost:7233"})
	if err != nil {
		panic(err)
	}
	defer c.Close()
	ctx := context.Background()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: "live-transfer-1", TaskQueue: "pfaengine-core",
	}, "TransferWorkflow", pfawf.TransferInput{
		TenantID: "t1", TransferID: "tr-live-1", RSAPIN: "PEN123", ValueKobo: 14400000, Quarter: "2026-Q4",
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("started:", run.GetID())

	time.Sleep(3 * time.Second)
	qr, err := c.QueryWorkflow(ctx, run.GetID(), "", pfawf.QueryState)
	if err != nil {
		panic(err)
	}
	var state string
	_ = qr.Get(&state)
	fmt.Println("state after start:", state)

	if err := c.SignalWorkflow(ctx, run.GetID(), "", pfawf.SignalRTSSettlement, nil); err != nil {
		panic(err)
	}
	var final string
	if err := run.Get(ctx, &final); err != nil {
		panic(err)
	}
	fmt.Println("LIVE RESULT:", final)
}
