// Package workspaceexec supervises foreground commands inside an already-bound
// workspace. It grants no capability to select or administer containers.
package workspaceexec

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

//go:embed supervisor.py
var supervisor string

var ErrUnconfirmed = errors.New("workspace command stop could not be confirmed")

type scopeKey struct{}

// WithScope associates internal execution bookkeeping with a dashboard. It is
// not an authorization boundary and is never supplied by the model.
func WithScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, scopeKey{}, scope)
}

func Scope(ctx context.Context) string { v, _ := ctx.Value(scopeKey{}).(string); return v }

func ID() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}

// Arguments must be appended to a docker exec -i invocation, after the bound
// container name. The absolute interpreter ignores user PATH/PYTHON* overrides.
func Arguments(id string) []string { return []string{"/usr/bin/python3", "-I", "-c", supervisor, id} }

// Run holds stdin open as a private execution lease. On cancellation it closes
// the lease and waits for the supervisor's acknowledgement, rather than killing
// only the Docker client. A dead transport is reported as unconfirmed.
func Run(ctx context.Context, cmd *exec.Cmd, id, shell, command string, input []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	payload, err := json.Marshal(struct {
		Shell   string `json:"shell"`
		Command string `json:"command"`
		Input   []byte `json:"input"`
	}{shell, command, input})
	if err != nil {
		return err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	defer r.Close()
	defer w.Close()
	cmd.Stdin = r
	var markers markerWriter
	markers.id, markers.dst = id, cmd.Stderr
	defer markers.flush()
	cmd.Stderr = &markers
	if err = cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	go func() {
		_, e := w.Write(append(payload, '\n'))
		if e != nil {
			w.Close()
		}
	}()
	select {
	case err = <-done:
		// A transport failure while a command is live has no stop acknowledgement.
		if !markers.finished && !markers.stopped {
			return errors.Join(err, ErrUnconfirmed)
		}
		return err
	case <-ctx.Done():
		w.Close()
		select {
		case err = <-done:
		case <-time.After(12 * time.Second):
			cmd.Process.Kill()
			<-done
		}
		if !markers.stopped && !markers.finished {
			return errors.Join(ctx.Err(), ErrUnconfirmed)
		}
		return ctx.Err()
	}
}

// The acknowledgement is removed from stderr without buffering normal output.
type markerWriter struct {
	id                string
	dst               io.Writer
	buf               []byte
	stopped, finished bool
}

func (w *markerWriter) Write(p []byte) (int, error) {
	n := len(p)
	w.buf = append(w.buf, p...)
	markers := []string{"\x1ePRISM_EXEC_STOPPED:" + w.id + "\x1f", "\x1ePRISM_EXEC_FINISHED:" + w.id + "\x1f", "\x1ePRISM_EXEC_UNCONFIRMED:" + w.id + "\x1f"}
	for {
		first, kind := -1, -1
		for k, m := range markers {
			if i := bytes.Index(w.buf, []byte(m)); i >= 0 && (first < 0 || i < first) {
				first, kind = i, k
			}
		}
		if first < 0 {
			break
		}
		if w.dst != nil {
			if _, err := w.dst.Write(w.buf[:first]); err != nil {
				return n, err
			}
		}
		if kind == 0 {
			w.stopped = true
		}
		if kind == 1 {
			w.finished = true
		}
		w.buf = w.buf[first+len(markers[kind]):]
	}
	// Retain only a possible split acknowledgement; normal stderr is unchanged.
	keep := 0
	for _, m := range markers {
		limit := len(m) - 1
		if limit > len(w.buf) {
			limit = len(w.buf)
		}
		for k := limit; k > keep; k-- {
			if bytes.Equal(w.buf[len(w.buf)-k:], []byte(m[:k])) {
				keep = k
				break
			}
		}
	}
	flush := len(w.buf) - keep
	if flush > 0 {
		if w.dst != nil {
			if _, err := w.dst.Write(w.buf[:flush]); err != nil {
				return n, err
			}
		}
		w.buf = append([]byte(nil), w.buf[flush:]...)
	}
	return n, nil
}

func (w *markerWriter) flush() {
	if w.dst != nil && len(w.buf) > 0 {
		w.dst.Write(w.buf)
		w.buf = nil
	}
}

// Fence retains only unconfirmed executions. Deletion can retry verification
// once the runtime is reachable again; it must never assume a lost CLI stopped
// the process. The probe is bound to the same container/account generation.
type Fence struct {
	mu      sync.Mutex
	pending map[string]map[string]func(context.Context) error
}

func (f *Fence) Remember(scope, id string, probe func(context.Context) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pending == nil {
		f.pending = make(map[string]map[string]func(context.Context) error)
	}
	if f.pending[scope] == nil {
		f.pending[scope] = make(map[string]func(context.Context) error)
	}
	f.pending[scope][id] = probe
}
func (f *Fence) Check(ctx context.Context, scope string) error {
	f.mu.Lock()
	probes := make(map[string]func(context.Context) error)
	for id, p := range f.pending[scope] {
		probes[id] = p
	}
	f.mu.Unlock()
	for id, probe := range probes {
		if err := probe(ctx); err != nil {
			return fmt.Errorf("%w; restore workspace execution and retry deletion", ErrUnconfirmed)
		}
		f.mu.Lock()
		delete(f.pending[scope], id)
		if len(f.pending[scope]) == 0 {
			delete(f.pending, scope)
		}
		f.mu.Unlock()
	}
	return nil
}

// ProbeArguments checks for this supervisor or its inherited command marker.
// It never signals arbitrary PIDs. A lingering process blocks deletion until
// it has stopped, including after an interrupted Docker transport.
func ProbeArguments(id string) []string {
	return []string{"/usr/bin/python3", "-I", "-c", `import os,sys
needle=("PRISM_EXECUTION_ID="+sys.argv[1]).encode()
for p in os.listdir('/proc'):
 if not p.isdigit() or int(p)==os.getpid(): continue
 try:
  if os.stat('/proc/'+p).st_uid != os.geteuid(): continue
  stat=open('/proc/'+p+'/stat').read().rsplit(')',1)[1].split()
  if stat[0] in ('Z','X'): continue
  env=open('/proc/'+p+'/environ','rb').read().split(b'\0')
  cmd=open('/proc/'+p+'/cmdline','rb').read().split(b'\0')
  if needle in env or sys.argv[1].encode() in cmd: sys.exit(1)
 except FileNotFoundError: pass
sys.exit(0)`, id}
}
