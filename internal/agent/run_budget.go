package agent

import (
	"context"
	"fmt"
	"sync"
)

type modelBudgetKey struct{}
type modelBudget struct {
	mu        sync.Mutex
	remaining int
}

func withModelBudget(ctx context.Context, limit int) context.Context {
	if ctx.Value(modelBudgetKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, modelBudgetKey{}, &modelBudget{remaining: limit})
}
func consumeModelCall(ctx context.Context) error {
	if b, ok := ctx.Value(modelBudgetKey{}).(*modelBudget); ok {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.remaining <= 0 {
			return fmt.Errorf("shared model-call budget exhausted (parent and subagents)")
		}
		b.remaining--
	}
	return nil
}

// ChildLimits inherits behavior; descendants share the parent's context budget.
func (a *Agent) ChildLimits() Limits {
	max, thinking := a.effectiveLimits()
	return Limits{MaxIterations: max, Thinking: &thinking, PromptProfile: a.promptProfile(), ReasoningEffort: a.reasoningEffort()}
}
