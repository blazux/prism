package email

import "testing"

func TestBridgePresetAvailability(t *testing.T) {
	for _, host := range []string{"protonmail-bridge", " ProtonMail-Bridge. "} {
		if err := ValidateBridgePreset(false, host); err != nil {
			t.Fatal("local preset changed", err)
		}
		if err := ValidateBridgePreset(true, host); err == nil {
			t.Fatal("missing hosted bridge accepted")
		}
	}
	if err := ValidateBridgePreset(true, "imap.gmail.com", "smtp.example.test"); err != nil {
		t.Fatal("custom endpoints affected", err)
	}
}
