package main

import (
	"context"
	"fmt"
	"time"

	"github.com/cucumber/godog"
	"github.com/slidebolt/sdk-types"
)

func registerCommandSteps(sc *godog.ScenarioContext) {
	sc.Step(`^I send command "([^"]*)" to entity "([^"]*)" on plugin "([^"]*)" device "([^"]*)"$`, stepSendCommand)
	sc.Step(`^the command state is "([^"]*)"$`, stepCommandStateIs)
	sc.Step(`^the command has an ID$`, stepCommandHasID)
	sc.Step(`^I poll the command status$`, stepPollCommandStatus)
}

func stepSendCommand(ctx context.Context, action, entityID, pluginID, deviceID string) error {
	w := worldFrom(ctx)
	path := fmt.Sprintf("/api/plugins/%s/devices/%s/entities/%s/commands", pluginID, deviceID, entityID)
	if err := w.do(w.Harness.Post(path, map[string]any{"type": action})); err != nil {
		return err
	}
	// Only decode on success
	if w.LastResp.StatusCode == 202 {
		var status types.CommandStatus
		if err := w.decodeLastBody(&status); err != nil {
			return err
		}
		w.CommandID = status.CommandID
		w.CommandState = status.State
	}
	return nil
}

func stepCommandStateIs(ctx context.Context, wantState string) error {
	w := worldFrom(ctx)
	want := types.CommandState(wantState)
	if w.CommandState != want {
		return fmt.Errorf("expected command state %q, got %q", want, w.CommandState)
	}
	return nil
}

func stepCommandHasID(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.CommandID == "" {
		return fmt.Errorf("command ID is empty")
	}
	return nil
}

func stepPollCommandStatus(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.CommandID == "" {
		return fmt.Errorf("no command ID to poll; run a command first")
	}
	// Retry briefly to handle async command execution completing after the poll.
	for attempt := 0; attempt < 5; attempt++ {
		for pluginID := range w.Plugins {
			path := fmt.Sprintf("/api/plugins/%s/commands/%s", pluginID, w.CommandID)
			resp, err := w.Harness.Get(path)
			if err != nil {
				continue
			}
			if resp.StatusCode == 200 {
				if err := w.do(resp, nil); err != nil {
					return err
				}
				var status types.CommandStatus
				if err := w.decodeLastBody(&status); err != nil {
					return err
				}
				w.CommandID = status.CommandID
				w.CommandState = status.State
				if status.State != types.CommandPending {
					return nil
				}
				// Still pending — wait and retry.
				time.Sleep(10 * time.Millisecond)
				break
			}
			_ = resp.Body.Close()
		}
	}
	return nil
}
