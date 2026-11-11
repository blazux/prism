package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"prism/internal/memory"
)

var chatRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{16,80}$`)

func chatRequestDigest(msg WSMessage) string {
	b, _ := json.Marshal(msg)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func (c *Client) sendReceipt(id, status string, replayed bool) {
	b, _ := json.Marshal(map[string]any{"type": "chat_receipt", "requestId": id, "status": status, "replayed": replayed})
	c.sendRaw(b) // receipt is transport metadata, never part of conversation replay
}

func (s *Server) chatRequestStatus(c *Client, id, digest string) bool {
	if !chatRequestIDPattern.MatchString(id) {
		c.sendReceipt(id, "invalid", false)
		return true
	}
	ctx, cancel := context.WithTimeout(s.runtimeContext(), 5*time.Second)
	defer cancel()
	var receipt *memory.ChatReceipt
	var err error
	if ms := s.store(); ms != nil {
		receipt, err = ms.ChatReceipt(ctx, c.sessionID, id)
	} else if v, ok := s.chatReceipts.Load(c.sessionID + ":" + id); ok {
		r := v.(memory.ChatReceipt)
		receipt = &r
	}
	if err != nil {
		c.sendReceipt(id, "unavailable", false)
		return true
	}
	if receipt == nil {
		if digest == "" {
			c.sendReceipt(id, "unknown", false)
		}
		return false
	}
	if digest != "" && receipt.Digest != digest {
		c.sendReceipt(id, "conflict", false)
		return true
	}
	status := receipt.Status
	if status == "accepted" && !s.runActive(c) {
		status = "interrupted"
	}
	c.sendReceipt(id, status, true)
	if digest != "" {
		s.attachRun(c)
	}
	return true
}

func (s *Server) claimChatRequest(session, id, digest, content string) (int64, error) {
	if !chatRequestIDPattern.MatchString(id) {
		return 0, fmt.Errorf("invalid message identifier")
	}
	if ms := s.store(); ms != nil {
		ctx, cancel := context.WithTimeout(s.runtimeContext(), 5*time.Second)
		defer cancel()
		messageID, err := ms.ClaimChatRequest(ctx, session, id, digest, content)
		if err != nil {
			return 0, fmt.Errorf("could not record message receipt")
		}
		if messageID == 0 {
			return 0, fmt.Errorf("message already received; reconnect to retrieve its status")
		}
		return messageID, nil
	} else {
		if _, loaded := s.chatReceipts.LoadOrStore(session+":"+id, memory.ChatReceipt{Digest: digest, Status: "accepted"}); loaded {
			return 0, fmt.Errorf("message already received")
		}
	}
	return 0, nil
}

func (c *Client) rejectChat(id, message string) {
	if id != "" {
		c.sendReceipt(id, "rejected", false)
	}
	b, _ := json.Marshal(map[string]any{"type": "error", "content": message})
	c.sendRaw(b)
}

func (s *Server) finishChatRequest(session, id, status string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if ms := s.store(); ms != nil {
		_ = ms.FinishChatRequest(ctx, session, id, status)
	} else if v, ok := s.chatReceipts.Load(session + ":" + id); ok {
		r := v.(memory.ChatReceipt)
		r.Status = status
		s.chatReceipts.Store(session+":"+id, r)
	}
}
