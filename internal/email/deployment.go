package email

import (
	"fmt"
	"strings"
)

// ValidateBridgePreset rejects only the bundled local service when absent.
// Custom IMAP/SMTP endpoints remain available in every deployment.
func ValidateBridgePreset(disabled bool, hosts ...string) error {
	if disabled {
		for _, host := range hosts {
			if strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(host), "."), "protonmail-bridge") {
				return fmt.Errorf("the bundled Proton Mail Bridge is unavailable in this deployment; a working personal Bridge connection must be configured separately")
			}
		}
	}
	return nil
}
