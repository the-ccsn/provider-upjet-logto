package main

import (
	"fmt"
	"time"
)

// Reject configurations that would panic rate limiters or disable drift detection.
func validateRuntimeOptions(sync, poll, metrics time.Duration, rate, webhookPort int) error {
	for name, value := range map[string]time.Duration{"sync": sync, "poll": poll, "poll-state-metric": metrics} {
		if value <= 0 {
			return fmt.Errorf("%s must be positive", name)
		}
	}
	if rate <= 0 {
		return fmt.Errorf("max-reconcile-rate must be positive")
	}
	if webhookPort < 1 || webhookPort > 65535 {
		return fmt.Errorf("webhook-port must be between 1 and 65535")
	}
	return nil
}
