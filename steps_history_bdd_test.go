//go:build bdd

package main

import (
	"context"
	"fmt"

	"github.com/cucumber/godog"
)

func registerHistorySteps(sc *godog.ScenarioContext) {
	sc.Step(`^I get history stats$`, stepGetHistoryStats)
	sc.Step(`^history event count is (\d+)$`, stepHistoryEventCount)
	sc.Step(`^history command count is (\d+)$`, stepHistoryCommandCount)
	sc.Step(`^I check health$`, stepCheckHealth)
}

func stepGetHistoryStats(ctx context.Context) error {
	w := worldFrom(ctx)
	return w.do(w.Harness.Get("/_internal/history/stats"))
}

func stepHistoryEventCount(ctx context.Context, want int) error {
	w := worldFrom(ctx)
	var body struct {
		EventCount   int64 `json:"event_count"`
		CommandCount int64 `json:"command_count"`
	}
	if err := w.decodeLastBody(&body); err != nil {
		return err
	}
	if body.EventCount != int64(want) {
		return fmt.Errorf("expected event_count %d, got %d", want, body.EventCount)
	}
	return nil
}

func stepHistoryCommandCount(ctx context.Context, want int) error {
	w := worldFrom(ctx)
	var body struct {
		EventCount   int64 `json:"event_count"`
		CommandCount int64 `json:"command_count"`
	}
	if err := w.decodeLastBody(&body); err != nil {
		return err
	}
	if body.CommandCount != int64(want) {
		return fmt.Errorf("expected command_count %d, got %d", want, body.CommandCount)
	}
	return nil
}

func stepCheckHealth(ctx context.Context) error {
	w := worldFrom(ctx)
	return w.do(w.Harness.Get("/_internal/health"))
}
