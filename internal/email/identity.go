package email

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

// ErrAccountChanged identifies requests made from a view of another mailbox.
var ErrAccountChanged = errors.New("the mailbox connection changed; reload Email before continuing")

// AccountFingerprint identifies a mailbox without including its password.
// Rotating credentials or changing transport security keeps drafts and views
// attached to the same mailbox; changing the actual connection does not.
func (c Config) AccountFingerprint() string {
	h := sha256.Sum256([]byte(strings.Join([]string{c.IMAPHost, strconv.Itoa(c.IMAPPort), c.SMTPHost, strconv.Itoa(c.SMTPPort), c.User, c.From}, "\x00")))
	return hex.EncodeToString(h[:])
}

// CheckAccount keeps the guard optional for existing tool and API callers.
func (c Config) CheckAccount(expected string) error {
	if expected != "" && expected != c.AccountFingerprint() {
		return ErrAccountChanged
	}
	return nil
}
