package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// QueuedCall reports an accepted mission, never a claimed successful outcome.
// Endpoint and scope are captured from the request, independently of tool args.
type QueuedCall struct {
	ID                                int64
	VoxURL, Scope                     string
	PhoneNumber, ContactName, Mission string
}

func (e *ToolExecutor) SetCallQueuedCallback(fn func(context.Context, QueuedCall) error) {
	e.onCallQueued = fn
}

type voxRequestResult struct {
	data       []byte
	url, scope string
}

type voxRequestError struct {
	status int
	body   string
}

func (e *voxRequestError) Error() string {
	return fmt.Sprintf("phone service returned HTTP %d: %s", e.status, e.body)
}

func (e *ToolExecutor) currentVoxScope() string {
	if e.voxScopeProvider != nil {
		return e.voxScopeProvider()
	}
	return e.voxScope
}

func (e *ToolExecutor) voxRequest(ctx context.Context, method, path string, payload any) (voxRequestResult, error) {
	base, user, password := e.voxConnection()
	result := voxRequestResult{url: strings.TrimRight(base, "/"), scope: e.currentVoxScope()}
	if result.url == "" {
		return result, fmt.Errorf("no telephony stack is available")
	}
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return result, err
		}
		body = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, result.url+path, body)
	if err != nil {
		return result, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if result.scope != "" {
		req.Header.Set("X-Prism-Scope", result.scope)
	}
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return result, fmt.Errorf("telephony request could not be confirmed: %w", err)
	}
	defer response.Body.Close()
	const maxBytes = 2 << 20
	result.data, err = io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return result, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, &voxRequestError{response.StatusCode, strings.TrimSpace(string(result.data))}
	}
	if len(result.data) > maxBytes {
		return result, fmt.Errorf("phone-service response is too large; request a smaller page")
	}
	return result, nil
}

func phoneInteger(args map[string]any, name string, fallback, min, max int64) (int64, error) {
	value, exists := args[name]
	if !exists {
		return fallback, nil
	}
	var n int64
	var err error
	switch v := value.(type) {
	case float64:
		if math.Trunc(v) != v || math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1<<53-1 {
			return 0, fmt.Errorf("%s must be an integer", name)
		}
		n = int64(v)
	case int:
		n = int64(v)
	case int64:
		n = v
	case json.Number:
		n, err = v.Int64()
	case string:
		n, err = strconv.ParseInt(v, 10, 64)
	default:
		err = fmt.Errorf("invalid integer")
	}
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, min, max)
	}
	return n, nil
}

func (e *ToolExecutor) placeCall(ctx context.Context, args map[string]any) (string, error) {
	phone, _ := args["phone_number"].(string)
	name, _ := args["contact_name"].(string)
	mission, _ := args["mission"].(string)
	phone, name, mission = strings.TrimSpace(phone), strings.TrimSpace(name), strings.TrimSpace(mission)
	if mission == "" || len(mission) > 16000 {
		return "", fmt.Errorf("mission is required and must be at most 16000 bytes")
	}
	attempts, err := phoneInteger(args, "max_attempts", 3, 1, 10)
	if err != nil {
		return "", err
	}
	payload := map[string]any{"mission": mission, "max_attempts": attempts, "request_id": "prism-" + rand.Text()}
	if value, exists := args["scheduled_at"]; exists {
		when, ok := value.(string)
		date, parseErr := time.Parse(time.RFC3339, when)
		if !ok || parseErr != nil || !date.After(time.Now()) {
			return "", fmt.Errorf("scheduled_at must be a future ISO8601 date/time with timezone; omit it for an immediate call")
		}
		payload["scheduled_at"] = date.Format(time.RFC3339)
	}
	if phone == "" {
		if name == "" {
			return "", fmt.Errorf("give a contact_name from the outbound directory or a phone_number")
		}
		phone, name, err = e.outboundContact(ctx, name)
		if err != nil {
			return "", err
		}
	}
	payload["phone_number"], payload["contact_name"] = phone, name
	response, err := e.voxRequest(ctx, http.MethodPost, "/api/calls", payload)
	if err != nil {
		var rejected *voxRequestError
		if errors.As(err, &rejected) && rejected.status < 500 {
			return "", err
		}
		return "", fmt.Errorf("call queuing outcome is unknown; inspect list_calls before placing another call: %w", err)
	}
	var created struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	if json.Unmarshal(response.data, &created) != nil || created.ID <= 0 {
		return "The phone service accepted the request without a usable mission ID. Queuing is unconfirmed: inspect list_calls before placing another call.", nil
	}
	tracked := false
	if e.onCallQueued != nil {
		trackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err := e.onCallQueued(trackCtx, QueuedCall{ID: created.ID, VoxURL: response.url, Scope: response.scope, PhoneNumber: phone, ContactName: name, Mission: mission})
		cancel()
		tracked = err == nil
	}
	result := map[string]any{"id": created.ID, "status": created.Status, "phone_number": phone, "contact_name": name,
		"queued": true, "outcome": "not yet known", "automatic_result_tracking": tracked}
	if tracked {
		destination := "this conversation"
		if e.telephonyRelay != nil {
			destination = "your personal Prism assistant conversation"
		}
		result["follow_up"] = "The result will be saved in " + destination + " and its notifications. Read the summary before claiming that the mission succeeded."
	} else {
		result["follow_up"] = "The call is queued, but automatic result tracking is unavailable. Use list_calls with this call_id; do not place it again."
	}
	return jsonResult(result), nil
}

func normalizeContactName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("-", " ", "'", " ", "  ", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

func (e *ToolExecutor) outboundContact(ctx context.Context, query string) (phone, name string, err error) {
	response, err := e.voxRequest(ctx, http.MethodGet, "/api/contacts", nil)
	if err != nil {
		return "", "", fmt.Errorf("could not read the outbound directory: %w", err)
	}
	type contact struct {
		Name        string `json:"name"`
		PhoneNumber string `json:"phone_number"`
		Enabled     *bool  `json:"enabled"`
	}
	var contacts []contact
	if json.Unmarshal(response.data, &contacts) != nil {
		var wrapped struct {
			Items []contact `json:"items"`
		}
		if err := json.Unmarshal(response.data, &wrapped); err != nil {
			return "", "", err
		}
		contacts = wrapped.Items
	}
	want := normalizeContactName(query)
	for _, exact := range []bool{true, false} {
		matches := []contact{}
		for _, c := range contacts {
			if c.Enabled != nil && !*c.Enabled {
				continue
			}
			n := normalizeContactName(c.Name)
			if n == want && exact || !exact && strings.Contains(" "+n+" ", " "+want+" ") {
				matches = append(matches, c)
			}
		}
		if len(matches) > 1 {
			return "", "", fmt.Errorf("%q matches several outbound contacts; ask for the full name", query)
		}
		if len(matches) == 1 && strings.TrimSpace(matches[0].PhoneNumber) != "" {
			return matches[0].PhoneNumber, matches[0].Name, nil
		}
	}
	return "", "", fmt.Errorf("%q is not in the outbound directory; add a contact in Telephony settings or give a phone number", query)
}

func (e *ToolExecutor) listCalls(ctx context.Context, args map[string]any) (string, error) {
	if _, exists := args["call_id"]; exists {
		id, err := phoneInteger(args, "call_id", 0, 1, math.MaxInt64)
		if err != nil {
			return "", err
		}
		response, err := e.voxRequest(ctx, http.MethodGet, fmt.Sprintf("/api/calls/%d", id), nil)
		if err != nil {
			return "", err
		}
		var item struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(response.data, &item) != nil || item.ID != id {
			return "", fmt.Errorf("the phone service returned a different or invalid mission")
		}
		return string(response.data), nil
	}
	limit, err := phoneInteger(args, "limit", 25, 1, 50)
	if err != nil {
		return "", err
	}
	offset, err := phoneInteger(args, "offset", 0, 0, math.MaxInt32)
	if err != nil {
		return "", err
	}
	response, err := e.voxRequest(ctx, http.MethodGet, fmt.Sprintf("/api/calls?limit=%d&offset=%d", limit, offset), nil)
	if err != nil {
		return "", err
	}
	var page map[string]json.RawMessage
	if json.Unmarshal(response.data, &page) != nil {
		return "", fmt.Errorf("invalid outbound mission page")
	}
	var total int64
	var items []json.RawMessage
	if json.Unmarshal(page["total"], &total) != nil || json.Unmarshal(page["items"], &items) != nil {
		return "", fmt.Errorf("invalid outbound mission pagination")
	}
	var next any
	if offset+int64(len(items)) < total && len(items) > 0 {
		next = offset + int64(len(items))
	}
	page["next_offset"], _ = json.Marshal(next)
	return jsonResult(page), nil
}

func (e *ToolExecutor) cancelCall(ctx context.Context, args map[string]any) (string, error) {
	id, err := phoneInteger(args, "call_id", 0, 1, math.MaxInt64)
	if err != nil || id == 0 {
		return "", fmt.Errorf("call_id must be a positive mission ID from place_call or list_calls")
	}
	if _, err := e.voxRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/calls/%d", id), nil); err != nil {
		return "", err
	}
	return fmt.Sprintf("Outbound call #%d has been cancelled; it will not be placed.", id), nil
}

func (e *ToolExecutor) callHistory(ctx context.Context, args map[string]any) (string, error) {
	var scope struct {
		Instance string `json:"instance"`
		Owner    *int64 `json:"owner"`
		Standard bool   `json:"standard"`
	}
	if json.Unmarshal([]byte(e.currentVoxScope()), &scope) != nil || scope.Instance == "" || scope.Instance == "unavailable" {
		return "", fmt.Errorf("call archive identity is unavailable")
	}
	if !scope.Standard && scope.Owner == nil {
		return "", fmt.Errorf("reception archive access is not enabled for this group; a global administrator can enable it in Telephony settings")
	}
	query := url.Values{"kind": {"calls"}}
	for _, key := range []string{"since", "until", "number", "direction", "query", "section"} {
		if value, exists := args[key]; exists {
			s, ok := value.(string)
			if !ok {
				return "", fmt.Errorf("%s must be text", key)
			}
			query.Set(key, s)
		}
	}
	for _, key := range []string{"since", "until"} {
		if value := query.Get(key); value != "" {
			if _, err := time.Parse(time.RFC3339, value); err != nil {
				return "", fmt.Errorf("%s must be an ISO8601 date/time with timezone", key)
			}
		}
	}
	if value := query.Get("direction"); value != "" && value != "inbound" && value != "outbound" {
		return "", fmt.Errorf("direction must be inbound or outbound")
	}
	if value := query.Get("section"); value != "" && value != "turns" && value != "tools" && value != "summary" {
		return "", fmt.Errorf("section must be turns, tools or summary")
	}
	if utf8.RuneCountInString(query.Get("query")) > 500 {
		return "", fmt.Errorf("query must be at most 500 characters")
	}
	if number := query.Get("number"); number != "" {
		digits := 0
		for _, c := range number {
			if c >= '0' && c <= '9' {
				digits++
			}
		}
		if digits < 6 {
			return "", fmt.Errorf("number must contain at least six digits")
		}
	}
	for _, option := range []struct {
		name               string
		fallback, min, max int64
	}{
		{"limit", 20, 1, 50}, {"offset", 0, 0, math.MaxInt32},
		{"id", 0, 1, math.MaxInt64}, {"text_offset", 0, 0, math.MaxInt32},
	} {
		_, exists := args[option.name]
		if !exists && option.name == "id" {
			continue
		}
		n, err := phoneInteger(args, option.name, option.fallback, option.min, option.max)
		if err != nil {
			return "", err
		}
		query.Set(option.name, strconv.FormatInt(n, 10))
	}
	response, err := e.voxRequest(ctx, http.MethodGet, "/api/history?"+query.Encode(), nil)
	if err != nil {
		return "", err
	}
	if !json.Valid(response.data) {
		return "", fmt.Errorf("invalid call archive response")
	}
	return string(response.data), nil
}
