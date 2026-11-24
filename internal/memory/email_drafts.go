package memory

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrEmailDraftConflict = errors.New("draft changed or is being sent; reopen it before saving")

// Email drafts belong to the mailbox owner, never to the currently open board.
// Attachments are part of the same atomic snapshot as the text.
type EmailDraftAttachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Data        []byte `json:"data"`
}

type EmailDraft struct {
	ID              int64                  `json:"id"`
	Revision        int64                  `json:"revision"`
	ClientID        string                 `json:"client_id"`
	To              string                 `json:"to"`
	CC              string                 `json:"cc"`
	BCC             string                 `json:"bcc"`
	Subject         string                 `json:"subject"`
	Body            string                 `json:"body"`
	UID             uint32                 `json:"uid"`
	Folder          string                 `json:"folder"`
	Attachments     []EmailDraftAttachment `json:"attachments,omitempty"`
	AttachmentCount int                    `json:"attachmentCount"`
	Status          string                 `json:"status"`
	UpdatedAt       time.Time              `json:"updatedAt"`
	Account         string                 `json:"-"`
	Replayed        bool                   `json:"-"`
}

func scanEmailDraft(row pgx.Row) (EmailDraft, error) {
	var d EmailDraft
	var payload []byte
	err := row.Scan(&d.ID, &d.Revision, &d.ClientID, &d.Account, &d.Status, &d.UpdatedAt, &payload)
	if err != nil {
		return d, err
	}
	var content EmailDraft
	if err := json.Unmarshal(payload, &content); err != nil {
		return d, err
	}
	d.To, d.CC, d.BCC, d.Subject, d.Body = content.To, content.CC, content.BCC, content.Subject, content.Body
	d.UID, d.Folder, d.Attachments = content.UID, content.Folder, content.Attachments
	d.AttachmentCount = content.AttachmentCount
	if d.Attachments != nil {
		d.AttachmentCount = len(d.Attachments)
	}
	return d, nil
}

const emailDraftColumns = `id, revision, client_id, account, status, updated_at, content`
const emailDraftMetadataColumns = `id, revision, client_id, account, status, updated_at, (content - 'attachments') || jsonb_build_object('attachmentCount',jsonb_array_length(COALESCE(NULLIF(content->'attachments','null'::jsonb),'[]'::jsonb)))`
const emailDraftListColumns = `id, revision, client_id, account, status, updated_at, (content - 'body' - 'attachments') || jsonb_build_object('attachmentCount',jsonb_array_length(COALESCE(NULLIF(content->'attachments','null'::jsonb),'[]'::jsonb)))`

func (s *Store) GetEmailDraft(ctx context.Context, id int64) (EmailDraft, error) {
	return scanEmailDraft(s.pool.QueryRow(ctx, `SELECT `+emailDraftColumns+` FROM email_drafts WHERE owner_scope=$1 AND id=$2`, s.cfgScope, id))
}

func (s *Store) ListEmailDrafts(ctx context.Context) ([]EmailDraft, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+emailDraftListColumns+` FROM email_drafts WHERE owner_scope=$1 ORDER BY updated_at DESC, id DESC`, s.cfgScope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EmailDraft{}
	for rows.Next() {
		d, err := scanEmailDraft(rows)
		if err != nil {
			return nil, err
		}
		d.Body, d.Attachments = "", nil
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) SaveEmailDraft(ctx context.Context, d EmailDraft, replaceAttachments bool) (EmailDraft, error) {
	if d.Attachments == nil {
		d.Attachments = []EmailDraftAttachment{}
	}
	// Payload excludes all authority metadata; those columns come from SQL only.
	payload, err := json.Marshal(struct {
		To          string                 `json:"to"`
		CC          string                 `json:"cc"`
		BCC         string                 `json:"bcc"`
		Subject     string                 `json:"subject"`
		Body        string                 `json:"body"`
		UID         uint32                 `json:"uid"`
		Folder      string                 `json:"folder"`
		Attachments []EmailDraftAttachment `json:"attachments"`
	}{d.To, d.CC, d.BCC, d.Subject, d.Body, d.UID, d.Folder, d.Attachments})
	if err != nil {
		return EmailDraft{}, err
	}
	if d.ID == 0 {
		created, err := scanEmailDraft(s.pool.QueryRow(ctx, `INSERT INTO email_drafts(owner_scope,client_id,account,content) VALUES($1,$2,$3,$4) ON CONFLICT(owner_scope,client_id) DO NOTHING RETURNING `+emailDraftMetadataColumns, s.cfgScope, d.ClientID, d.Account, payload))
		if errors.Is(err, pgx.ErrNoRows) {
			// A lost create response is retried using the same client token. Never
			// overwrite edits already made to that draft from another tab.
			existing, err := scanEmailDraft(s.pool.QueryRow(ctx, `SELECT `+emailDraftMetadataColumns+` FROM email_drafts WHERE owner_scope=$1 AND client_id=$2`, s.cfgScope, d.ClientID))
			if err == nil && existing.Account != d.Account {
				return EmailDraft{}, ErrEmailDraftConflict
			}
			existing.Replayed = true
			return existing, err
		}
		return created, err
	}
	query := `UPDATE email_drafts SET content=$1,revision=revision+1,updated_at=NOW() WHERE owner_scope=$2 AND id=$3 AND revision=$4 AND status='draft' AND account=$5 RETURNING ` + emailDraftMetadataColumns
	if !replaceAttachments {
		query = `UPDATE email_drafts SET content=$1::jsonb || jsonb_build_object('attachments',content->'attachments'),revision=revision+1,updated_at=NOW() WHERE owner_scope=$2 AND id=$3 AND revision=$4 AND status='draft' AND account=$5 RETURNING ` + emailDraftMetadataColumns
	}
	updated, err := scanEmailDraft(s.pool.QueryRow(ctx, query, payload, s.cfgScope, d.ID, d.Revision, d.Account))
	if errors.Is(err, pgx.ErrNoRows) {
		if _, getErr := s.GetEmailDraft(ctx, d.ID); getErr != nil {
			return EmailDraft{}, getErr
		}
		return EmailDraft{}, ErrEmailDraftConflict
	}
	return updated, err
}

func (s *Store) DeleteEmailDraft(ctx context.Context, id, revision int64) error {
	cmd, err := s.pool.Exec(ctx, `DELETE FROM email_drafts WHERE owner_scope=$1 AND id=$2 AND revision=$3 AND status='draft'`, s.cfgScope, id, revision)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		if _, err := s.GetEmailDraft(ctx, id); err != nil {
			return err
		}
		return ErrEmailDraftConflict
	}
	return nil
}

// Claim persists the in-flight state before touching SMTP. Concurrent requests
// cannot send the same snapshot twice. A process interruption leaves it marked
// sending: delivery must be checked before a person explicitly retries.
func (s *Store) ClaimEmailDraft(ctx context.Context, id, revision int64, account string) (EmailDraft, error) {
	d, err := scanEmailDraft(s.pool.QueryRow(ctx, `UPDATE email_drafts SET status='sending' WHERE owner_scope=$1 AND id=$2 AND revision=$3 AND account=$4 AND status='draft' RETURNING `+emailDraftColumns, s.cfgScope, id, revision, account))
	if errors.Is(err, pgx.ErrNoRows) {
		if _, e := s.GetEmailDraft(ctx, id); e != nil {
			return d, e
		}
		return d, ErrEmailDraftConflict
	}
	return d, err
}

func (s *Store) FinishEmailDraftSend(ctx context.Context, id, revision int64, sent bool) error {
	q := `UPDATE email_drafts SET status='draft' WHERE owner_scope=$1 AND id=$2 AND revision=$3 AND status='sending'`
	if sent {
		q = `DELETE FROM email_drafts WHERE owner_scope=$1 AND id=$2 AND revision=$3 AND status='sending'`
	}
	_, err := s.pool.Exec(ctx, q, s.cfgScope, id, revision)
	return err
}
