package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"prism/internal/customtools"
	"prism/internal/resources"
)

func TestCancelledResourceRequestsDoNotExecuteAfterLockWait(t *testing.T) {
	e := resourceFixture(t)
	unlock := resources.Lock(e.workspaceDir)
	defer unlock()
	for _, operation := range []string{"inventory", "cleanup", "delete", "bind"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "inventory":
					_, err = e.ResourceInventory(ctx)
				case "cleanup":
					_, err = e.CleanupResources(ctx, "", true, nil, false)
				case "delete":
					_, err = e.DeleteDashboard(ctx, nil, func() error { return errors.New("commit must not run") })
				case "bind":
					err = e.BindWidgetResources(ctx, "widget", nil)
				}
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("request did not stop at deadline: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("resource request could not leave lock queue")
			}
		})
	}
}

// Measure the actual inventory path with synthetic files. Keep cleanup on its
// fresh snapshot; display caching should only be added if this cost warrants it.
func BenchmarkResourceInventory(b *testing.B) {
	for _, count := range []int{100, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			root := b.TempDir()
			plugins := filepath.Join(root, "plugins", "fixture")
			if err := os.MkdirAll(plugins, 0700); err != nil {
				b.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "data"), 0700); err != nil {
				b.Fatal(err)
			}
			for i := range count {
				if err := os.WriteFile(filepath.Join(root, "data", fmt.Sprintf("file-%04d.json", i)), []byte("{}"), 0600); err != nil {
					b.Fatal(err)
				}
			}
			e := NewToolExecutor(nil, root, plugins, "", "")
			e.SetSessionID("fixture")
			b.ResetTimer()
			for range b.N {
				nodes, err := e.ResourceInventory(b.Context())
				if err != nil || len(nodes) != count {
					b.Fatalf("inventory nodes=%d err=%v", len(nodes), err)
				}
			}
		})
	}
}

func BenchmarkResourceInventoryTools(b *testing.B) {
	for _, count := range []int{100, 500} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			root := b.TempDir()
			plugins := filepath.Join(root, "plugins", "fixture")
			if err := os.MkdirAll(plugins, 0700); err != nil {
				b.Fatal(err)
			}
			tools := filepath.Join(root, "agent_tools")
			if err := os.MkdirAll(tools, 0700); err != nil {
				b.Fatal(err)
			}
			for i := range count {
				name := fmt.Sprintf("tool_%04d", i)
				content := fmt.Sprintf("# TOOL: {\"name\":%q,\"description\":\"fixture\",\"parameters\":{\"type\":\"object\"}}\nprint('ok')\n", name)
				if err := os.WriteFile(filepath.Join(tools, name+".py"), []byte(content), 0600); err != nil {
					b.Fatal(err)
				}
			}
			e := NewToolExecutor(nil, root, plugins, "", "")
			e.SetSessionID("fixture")
			e.SetCustomTools(customtools.LoadManager(tools), nil)
			b.ResetTimer()
			for range b.N {
				nodes, err := e.ResourceInventory(b.Context())
				if err != nil || len(nodes) != count {
					b.Fatalf("inventory nodes=%d err=%v", len(nodes), err)
				}
			}
		})
	}
}
