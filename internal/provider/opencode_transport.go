package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"zzz/internal/logx"
)

const opencodeHealthTimeout = 30 * time.Second

type openCodeServer struct {
	cmd *exec.Cmd
}

func (s *openCodeServer) shutdown() {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return
	}
	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_ = s.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = s.cmd.Process.Kill()
		<-done
	}
}

func startOpenCodeServer(ctx context.Context, command, cwd string, port int) (*openCodeServer, error) {
	cmd := exec.CommandContext(ctx, command, "serve", "--hostname", "127.0.0.1", "--port", fmt.Sprintf("%d", port), "--print-logs")
	cmd.Dir = cwd
	cmd.Env = cleanOpenCodeEnv(os.Environ())
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &openCodeServer{cmd: cmd}, nil
}

func cleanOpenCodeEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, item := range env {
		if strings.HasPrefix(item, "OPENCODE_SERVER_USERNAME=") || strings.HasPrefix(item, "OPENCODE_SERVER_PASSWORD=") {
			continue
		}
		out = append(out, item)
	}
	return out
}

func allocatePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("failed to resolve tcp port")
	}
	return addr.Port, nil
}

func waitForHealth(ctx context.Context, client *http.Client, baseURL string) error {
	deadline := time.Now().Add(opencodeHealthTimeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/global/health", nil)
		resp, err := client.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return errors.New("timed out waiting for opencode server health")
}

func createSession(ctx context.Context, client *http.Client, baseURL, cwd string) (string, error) {
	body, err := doJSON(ctx, client, http.MethodPost, baseURL+"/session", map[string]any{
		"directory":  filepath.Clean(cwd),
		"permission": []map[string]string{{"permission": "*", "pattern": "*", "action": "allow"}},
	})
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("opencode session id missing")
	}
	return out.ID, nil
}

func postMessage(ctx context.Context, client *http.Client, baseURL, sessionID, prompt, model string) ([]byte, error) {
	url := fmt.Sprintf("%s/session/%s/message", baseURL, sessionID)
	return doJSON(ctx, client, http.MethodPost, url, buildMessagePayload(prompt, model))
}

func doJSON(ctx context.Context, client *http.Client, method, url string, payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s failed with %d: %s", method, url, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func bestEffortDeleteSession(client *http.Client, baseURL, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	url := fmt.Sprintf("%s/session/%s", baseURL, sessionID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	resp, err := client.Do(req)
	if err != nil {
		logx.Debugf("opencode: delete session %s skipped: %v", sessionID, err)
	}
	if err == nil && resp != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		logx.Debugf("opencode: deleted session %s", sessionID)
	}
}

func bestEffortAbortSession(client *http.Client, baseURL, sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	url := fmt.Sprintf("%s/session/%s/abort", baseURL, sessionID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		logx.Debugf("opencode: abort session %s skipped: %v", sessionID, err)
	}
	if err == nil && resp != nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		logx.Debugf("opencode: aborted session %s", sessionID)
	}
}

func mapOpenCodeError(err error, phase string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return &Error{Code: ErrAbortedByUser, Message: fmt.Sprintf("%s canceled", phase)}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &Error{Code: ErrProviderTime, Message: fmt.Sprintf("%s timed out", phase)}
	}
	return &Error{Code: ErrProtocol, Message: fmt.Sprintf("%s failed: %v", phase, err)}
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
