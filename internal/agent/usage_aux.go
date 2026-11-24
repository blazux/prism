package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"prism/internal/ollama"
)

var auxiliaryUsageSequence atomic.Uint64

// AuxiliaryTaskID keeps work inside its originating task. Standalone utilities
// receive their own content-free identity rather than a legacy session total.
func AuxiliaryTaskID(ctx context.Context, scope string, started time.Time) string {
	if id := taskIDFromContext(ctx); id != "" {
		return id
	}
	return fmt.Sprintf("%s-%d-%d", scope, started.UnixNano(), auxiliaryUsageSequence.Add(1))
}

// MeasureModelUsage records sizes and provider counters, never request content.
// Complete means the stream ended normally, not that its answer passed a grader.
func MeasureModelUsage(req ollama.ChatRequest, taskID, scope, profile string, started time.Time, usage *ollama.Usage, complete bool) *ModelUsage {
	if taskID == "" {
		taskID = req.TaskID
	}
	if taskID == "" {
		taskID = AuxiliaryTaskID(context.Background(), scope, started)
	}
	content, results, arguments, images := historyFootprint(req.Messages)
	var systemBytes, toolBytes int
	if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
		systemBytes = len(req.Messages[0].Content)
	}
	if len(req.Tools) > 0 {
		encoded, _ := json.Marshal(req.Tools)
		toolBytes = len(encoded)
	}
	return &ModelUsage{
		TaskID: taskID, Scope: scope, Model: req.Model, Profile: profile,
		DurationMS: time.Since(started).Milliseconds(), SystemBytes: systemBytes,
		ToolBytes: toolBytes, MessageCount: len(req.Messages), HistoryContentBytes: content,
		HistoryToolResultBytes: results, HistoryToolArgumentBytes: arguments,
		HistoryImageBytes: images, Complete: complete, Usage: usage,
	}
}

// RecordModelUsage survives cancellation of the feature request. Recording is
// best-effort and bounded; telemetry cannot extend a cancelled task indefinitely.
func RecordModelUsage(recorder UsageRecorder, userID int64, sessionID string, record *ModelUsage) {
	if recorder == nil || record == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	recorder.AddUsage(ctx, userID, sessionID, "model_request", record.Model, 1,
		map[string]interface{}{"measurement": record})
}

type auxiliaryUsageKey struct{}
type auxiliaryUsageTarget struct {
	recorder UsageRecorder
	session  string
	profile  string
}

// Parent attribution follows tools and parallel research calls, including
// ephemeral subagents whose executors intentionally have no history store.
func withAuxiliaryUsage(ctx context.Context, recorder UsageRecorder, sessionID, profile string) context.Context {
	return context.WithValue(ctx, auxiliaryUsageKey{}, auxiliaryUsageTarget{recorder, sessionID, profile})
}

func (a *Agent) recordAuxiliaryModelUsage(ctx context.Context, req ollama.ChatRequest, scope string, started time.Time, usage *ollama.Usage, complete bool) {
	taskID := req.TaskID
	if taskID == "" {
		taskID = AuxiliaryTaskID(ctx, scope, started)
	}
	record := MeasureModelUsage(req, taskID, scope, a.promptProfile(), started, usage, complete)
	RecordModelUsage(a.usageRecorder, 0, a.sessionID, record)
}

func (e *ToolExecutor) recordAuxiliaryModelUsage(ctx context.Context, req ollama.ChatRequest, scope string, started time.Time, usage *ollama.Usage, complete bool) {
	target, found := ctx.Value(auxiliaryUsageKey{}).(auxiliaryUsageTarget)
	if !found {
		target = auxiliaryUsageTarget{recorder: e.memStore, session: e.sessionID}
	} else if target.recorder == nil {
		target.recorder = e.memStore
	}
	record := MeasureModelUsage(req, req.TaskID, scope, target.profile, started, usage, complete)
	RecordModelUsage(target.recorder, 0, target.session, record)
}
