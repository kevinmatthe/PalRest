package saveworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/kevinmatt/palworld-playtime-guard/internal/store"
)

type Runner struct {
	command string
	timeout time.Duration
}

func New(command string, timeout time.Duration) (*Runner, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("save worker command is empty")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("save worker timeout must be positive")
	}
	return &Runner{command: command, timeout: timeout}, nil
}

func (r *Runner) Extract(ctx context.Context, levelPath string) (store.SaveSnapshot, error) {
	levelPath = strings.TrimSpace(levelPath)
	if levelPath == "" {
		return store.SaveSnapshot{}, fmt.Errorf("save worker level path is empty")
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, r.command, "--level", levelPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return store.SaveSnapshot{}, fmt.Errorf("save worker timed out after %s", r.timeout)
	}
	if err != nil {
		return store.SaveSnapshot{}, fmt.Errorf("save worker failed: %w: %s", err, truncate(stderr.String(), 2048))
	}
	var snapshot store.SaveSnapshot
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return store.SaveSnapshot{}, fmt.Errorf("decode save worker output: %w", err)
	}
	if decoder.Decode(&struct{}{}) == nil {
		return store.SaveSnapshot{}, fmt.Errorf("decode save worker output: trailing JSON")
	}
	return snapshot, nil
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "...(truncated)"
}
