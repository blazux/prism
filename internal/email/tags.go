package email

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

const legacyTagsOwnerKey = "email_tags_legacy_account"

// Like rules, categorization runs in this deployment's single server process.
// Serialize read/merge/write and the one-time binding of pre-account tag keys.
var tagsMu sync.Mutex

// BindLegacyTags must run before changing an existing mailbox configuration.
// Original keys remain intact; their owner is fixed once and never reassigned.
func BindLegacyTags(ctx context.Context, store RuleStore, account string) error {
	tagsMu.Lock()
	defer tagsMu.Unlock()
	_, err := bindLegacyTags(ctx, store, account)
	return err
}

func bindLegacyTags(ctx context.Context, store RuleStore, account string) (string, error) {
	owner, exists, err := store.GetConfig(ctx, legacyTagsOwnerKey)
	if err != nil {
		return "", err
	}
	if !exists {
		if err = store.SetConfig(ctx, legacyTagsOwnerKey, account); err != nil {
			return "", err
		}
		owner = account
	}
	return owner, nil
}

func tagKeys(account, folder string) (string, string) {
	legacy := "email_tags"
	if folder != "" && !strings.EqualFold(folder, "INBOX") {
		legacy += ":" + folder
	}
	return "email_tags:" + account + strings.TrimPrefix(legacy, "email_tags"), legacy
}

func loadTags(ctx context.Context, store RuleStore, account, folder string) (map[string]json.RawMessage, error) {
	owner, err := bindLegacyTags(ctx, store, account)
	if err != nil {
		return nil, err
	}
	key, legacy := tagKeys(account, folder)
	raw, exists, err := store.GetConfig(ctx, key)
	if err != nil {
		return nil, err
	}
	if !exists && owner == account {
		raw, exists, err = store.GetConfig(ctx, legacy)
		if err != nil {
			return nil, err
		}
		if exists {
			if err = store.SetConfig(ctx, key, raw); err != nil {
				return nil, err
			}
		}
	}
	tags := map[string]json.RawMessage{}
	if raw != "" {
		if err = json.Unmarshal([]byte(raw), &tags); err != nil || tags == nil {
			return nil, fmt.Errorf("saved email categories are invalid; their stored copy has been kept")
		}
	}
	return tags, nil
}

func LoadTags(ctx context.Context, store RuleStore, account, folder string) (map[string]json.RawMessage, error) {
	tagsMu.Lock()
	defer tagsMu.Unlock()
	return loadTags(ctx, store, account, folder)
}

func MergeTags(ctx context.Context, store RuleStore, account, folder string, incoming map[string]json.RawMessage) error {
	tagsMu.Lock()
	defer tagsMu.Unlock()
	tags, err := loadTags(ctx, store, account, folder)
	if err != nil {
		return err
	}
	for uid, value := range incoming {
		if string(value) == "null" {
			delete(tags, uid)
		} else {
			tags[uid] = value
		}
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return err
	}
	key, _ := tagKeys(account, folder)
	return store.SetConfig(ctx, key, string(raw))
}
