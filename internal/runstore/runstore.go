package runstore

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const schemaVersion = 1

type Store struct {
	repoRoot string
	now      func() time.Time
	hostname string
	pid      int
}

type RunMeta struct {
	SchemaVersion int    `json:"schema_version"`
	RunID         string `json:"run_id"`
	Prompt        string `json:"prompt"`
	RepoRoot      string `json:"repo_root"`
	BranchName    string `json:"branch_name"`
	CreatedAt     string `json:"created_at"`
}

type RunState struct {
	SchemaVersion       int    `json:"schema_version"`
	Status              string `json:"status"`
	CurrentIteration    int    `json:"current_iteration"`
	LastCompleted       int    `json:"last_completed_iteration"`
	SuccessCount        int    `json:"success_count"`
	FailCount           int    `json:"fail_count"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	TotalInputTokens    int    `json:"total_input_tokens"`
	TotalOutputTokens   int    `json:"total_output_tokens"`
	TotalCacheRead      int    `json:"total_cache_read_tokens"`
	TotalCacheWrite     int    `json:"total_cache_write_tokens"`
	TotalTokens         int    `json:"total_tokens"`
	CurrentActivity     string `json:"current_activity"`
	StartedAt           string `json:"started_at"`
	UpdatedAt           string `json:"updated_at"`
	WaitingUntil        string `json:"waiting_until"`
	BranchName          string `json:"branch_name"`
}

type Lock struct {
	PID        int    `json:"pid"`
	Hostname   string `json:"hostname"`
	AcquiredAt string `json:"acquired_at"`
}

type ChatEntry struct {
	SchemaVersion int    `json:"schema_version"`
	Time          string `json:"time"`
	RunID         string `json:"run_id"`
	Iteration     int    `json:"iteration"`
	Role          string `json:"role"`
	Text          string `json:"text"`
	TotalTokens   int    `json:"total_tokens"`
}

type JournalEvent struct {
	SchemaVersion int    `json:"schema_version"`
	Time          string `json:"time"`
	RunID         string `json:"run_id"`
	Kind          string `json:"kind"`
	Iteration     int    `json:"iteration,omitempty"`
	Title         string `json:"title,omitempty"`
	Body          string `json:"body,omitempty"`
}

func New(repoRoot string) (*Store, error) {
	h, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	return &Store{repoRoot: repoRoot, now: time.Now, hostname: h, pid: os.Getpid()}, nil
}

func (s *Store) RepoRoot() string { return s.repoRoot }

func (s *Store) EnsureIgnore() error {
	exclude := filepath.Join(s.repoRoot, ".git", "info", "exclude")
	b, _ := os.ReadFile(exclude)
	if strings.Contains(string(b), ".zzz/") {
		return nil
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("\n.zzz/\n")
	return err
}

func (s *Store) CreateRun(runID string, prompt string, branch string) (string, error) {
	if strings.TrimSpace(runID) == "" {
		runID = s.defaultRunID(prompt)
	}
	rd := s.runDir(runID)
	if err := os.MkdirAll(rd, 0o755); err != nil {
		return "", err
	}
	meta := RunMeta{SchemaVersion: schemaVersion, RunID: runID, Prompt: prompt, RepoRoot: s.repoRoot, BranchName: branch, CreatedAt: s.now().UTC().Format(time.RFC3339)}
	if err := writeJSONAtomic(filepath.Join(rd, "meta.json"), meta); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(rd, "notes.md"), []byte(""), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(rd, "chat.md"), []byte("# Chat\n\n"), 0o644); err != nil {
		return "", err
	}
	_ = s.AppendJournalEvent(runID, JournalEvent{
		Kind:  "mission",
		Title: "Run objective",
		Body:  strings.TrimSpace(prompt),
	})
	return runID, nil
}

func (s *Store) RunExists(runID string) bool {
	_, err := os.Stat(s.runDir(runID))
	return err == nil
}

func (s *Store) ResolveLatestRunForRepo() (string, error) {
	runs := filepath.Join(s.repoRoot, ".zzz", "runs")
	entries, err := os.ReadDir(runs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("no runs found")
		}
		return "", err
	}
	var latestID string
	var latestTime time.Time
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		meta, err := s.LoadMeta(e.Name())
		if err != nil || meta.RepoRoot != s.repoRoot {
			continue
		}
		tm, err := time.Parse(time.RFC3339, meta.CreatedAt)
		if err != nil {
			continue
		}
		if latestID == "" || tm.After(latestTime) {
			latestID = e.Name()
			latestTime = tm
		}
	}
	if latestID == "" {
		return "", errors.New("no runs found for current repository")
	}
	return latestID, nil
}

func (s *Store) LoadMeta(runID string) (RunMeta, error) {
	var m RunMeta
	b, err := os.ReadFile(filepath.Join(s.runDir(runID), "meta.json"))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	return m, nil
}

func (s *Store) LoadState(runID string) (RunState, error) {
	var st RunState
	b, err := os.ReadFile(filepath.Join(s.runDir(runID), "state.json"))
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, err
	}
	return st, nil
}

func (s *Store) WriteState(runID string, state RunState) error {
	state.SchemaVersion = schemaVersion
	state.UpdatedAt = s.now().UTC().Format(time.RFC3339)
	return writeJSONAtomic(filepath.Join(s.runDir(runID), "state.json"), state)
}

func (s *Store) AppendNote(runID string, note string) error {
	f, err := os.OpenFile(filepath.Join(s.runDir(runID), "notes.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	stamp := s.now().UTC().Format(time.RFC3339)
	_, err = f.WriteString(fmt.Sprintf("\n## %s\n%s\n", stamp, note))
	if err != nil {
		return err
	}
	_ = s.AppendJournalEvent(runID, JournalEvent{
		Kind:  "note",
		Title: "Operator note",
		Body:  strings.TrimSpace(note),
	})
	return nil
}

func (s *Store) ReadNotes(runID string) ([]string, error) {
	pack, err := s.readContextPackFromJournal(runID)
	if err == nil && len(pack) > 0 {
		return pack, nil
	}

	b, err := os.ReadFile(filepath.Join(s.runDir(runID), "notes.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	chunks := strings.Split(string(b), "## ")
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *Store) AppendJournalEvent(runID string, event JournalEvent) error {
	path := filepath.Join(s.runDir(runID), "journal.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	event.SchemaVersion = schemaVersion
	event.RunID = runID
	if strings.TrimSpace(event.Time) == "" {
		event.Time = s.now().UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func (s *Store) readContextPackFromJournal(runID string) ([]string, error) {
	path := filepath.Join(s.runDir(runID), "journal.jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) == 0 {
		return nil, nil
	}

	max := 80
	if len(lines) > max {
		lines = lines[len(lines)-max:]
	}

	mission := ""
	requests := make([]string, 0, 8)
	cards := make([]string, 0, 8)
	decisions := make([]string, 0, 8)
	nextBrief := ""

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e JournalEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		body := strings.TrimSpace(e.Body)
		title := strings.TrimSpace(e.Title)
		switch e.Kind {
		case "mission":
			if mission == "" {
				mission = body
			}
		case "request", "chat-user":
			if body != "" {
				requests = append(requests, fmt.Sprintf("- %s", oneLine(body)))
			}
		case "checkpoint", "summary":
			if body != "" {
				label := title
				if label == "" {
					label = "Loop card"
				}
				cards = append(cards, fmt.Sprintf("- %s: %s", label, oneLine(body)))
			}
		case "next-brief":
			if body != "" {
				nextBrief = body
			}
		case "handoff":
			if body != "" {
				nextBrief = body
			}
		case "decision", "learning":
			if body != "" {
				decisions = append(decisions, fmt.Sprintf("- %s", oneLine(body)))
			}
		case "note":
			if strings.Contains(strings.ToLower(body), "user asked") {
				requests = append(requests, fmt.Sprintf("- %s", oneLine(body)))
			}
		}
	}

	if mission == "" {
		mission = "(no mission recorded)"
	}
	if len(requests) == 0 {
		requests = []string{"- (none yet)"}
	}
	if len(cards) == 0 {
		cards = []string{"- (no loop cards yet)"}
	}
	if len(decisions) == 0 {
		decisions = []string{"- (no decisions yet)"}
	}

	keepLast := func(items []string, n int) []string {
		if len(items) <= n {
			return items
		}
		return items[len(items)-n:]
	}
	requests = keepLast(requests, 6)
	cards = keepLast(cards, 6)
	decisions = keepLast(decisions, 6)
	if strings.TrimSpace(nextBrief) == "" {
		nextBrief = "Immediate focus:\nContinue with the next smallest safe change that advances the mission.\n\nVerification requirement:\nRun one targeted command and report exact output."
	}

	pack := []string{
		"Mission brief:\n" + mission,
		"Next-loop instructions:\n" + nextBrief,
		"Request inbox:\n" + strings.Join(requests, "\n"),
		"Recent loop cards:\n" + strings.Join(cards, "\n"),
		"Decision log:\n" + strings.Join(decisions, "\n"),
	}
	return pack, nil
}

func oneLine(s string) string {
	if s == "" {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.Join(strings.Fields(s), " ")
}

func (s *Store) AppendIterationEvent(runID string, iteration int, payload any) error {
	path := filepath.Join(s.runDir(runID), fmt.Sprintf("loop-%d.trace.jsonl", iteration))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func (s *Store) AppendChatEntry(runID string, entry ChatEntry) error {
	historyPath := filepath.Join(s.runDir(runID), "chat-history.jsonl")
	f, err := os.OpenFile(historyPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	entry.SchemaVersion = schemaVersion
	entry.RunID = runID
	if strings.TrimSpace(entry.Time) == "" {
		entry.Time = s.now().UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}

	chatPath := filepath.Join(s.runDir(runID), "chat.md")
	mf, err := os.OpenFile(chatPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer mf.Close()

	label := "Assistant"
	switch strings.ToLower(strings.TrimSpace(entry.Role)) {
	case "user":
		label = "User"
	case "assistant":
		label = "Assistant"
	case "system":
		label = "System"
	case "next-loop":
		label = "Next Loop Context"
	}
	line := strings.TrimSpace(entry.Text)
	if line == "" {
		line = "(empty)"
	}
	line = strings.ReplaceAll(line, "\n", "\n  ")
	_, err = mf.WriteString(fmt.Sprintf("## %s · %s\n\n- **%s:** %s\n\n", entry.Time, runID, label, line))
	return err
}

func (s *Store) WriteArtifact(runID string, name string, content string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("artifact name is required")
	}
	path := filepath.Join(s.runDir(runID), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func (s *Store) WriteProjectArtifact(name string, content string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("artifact name is required")
	}
	path := filepath.Join(s.repoRoot, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func (s *Store) AcquireLock(runID string, staleAfter time.Duration) error {
	lockPath := filepath.Join(s.runDir(runID), ".lock")
	lock := Lock{PID: s.pid, Hostname: s.hostname, AcquiredAt: s.now().UTC().Format(time.RFC3339)}
	b, _ := json.Marshal(lock)

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		defer f.Close()
		_, err = f.Write(b)
		return err
	}

	current, readErr := s.readLock(lockPath)
	if readErr != nil {
		return err
	}
	if !s.isStale(current, staleAfter) {
		return errors.New("run lock is held by another active process")
	}

	if err := s.appendLockHistory(runID, current); err != nil {
		return err
	}
	if rmErr := os.Remove(lockPath); rmErr != nil {
		return rmErr
	}
	f, err = os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(b)
	return err
}

func (s *Store) ReleaseLock(runID string) error {
	err := os.Remove(filepath.Join(s.runDir(runID), ".lock"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *Store) runDir(runID string) string {
	return filepath.Join(s.repoRoot, ".zzz", "runs", runID)
}

func (s *Store) defaultRunID(prompt string) string {
	base := strings.ToLower(strings.TrimSpace(prompt))
	base = strings.ReplaceAll(base, " ", "-")
	base = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, base)
	if base == "" {
		base = "run"
	}
	if len(base) > 32 {
		base = base[:32]
	}
	return fmt.Sprintf("%s-%d", base, s.now().Unix())
}

func (s *Store) readLock(path string) (Lock, error) {
	var l Lock
	b, err := os.ReadFile(path)
	if err != nil {
		return l, err
	}
	if err := json.Unmarshal(b, &l); err != nil {
		return l, err
	}
	return l, nil
}

func (s *Store) isStale(lock Lock, staleAfter time.Duration) bool {
	at, err := time.Parse(time.RFC3339, lock.AcquiredAt)
	if err != nil {
		return true
	}
	if s.now().After(at.Add(staleAfter)) {
		return true
	}
	if lock.Hostname == s.hostname {
		if err := syscall.Kill(lock.PID, 0); err != nil {
			return true
		}
	}
	return false
}

func (s *Store) appendLockHistory(runID string, lock Lock) error {
	path := filepath.Join(s.runDir(runID), "lock-history.log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(lock)
	_, err = f.Write(append(b, '\n'))
	return err
}

func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		_ = f.Close()
		return err
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	parent, err := os.Open(filepath.Dir(path))
	if err == nil {
		_ = parent.Sync()
		_ = parent.Close()
	}
	return nil
}

func parseRunIDFromName(name string) int64 {
	parts := strings.Split(name, "-")
	if len(parts) == 0 {
		return 0
	}
	v, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	return v
}
