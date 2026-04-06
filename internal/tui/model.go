package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"zzz/internal/orchestrator"
)

type Model struct {
	prompt        string
	maxIterations int
	model         string
	startedAt     time.Time
	elapsed       time.Duration
	status        string
	iteration     int
	failureStreak int
	totalTokens   int
	tokensIn      int
	tokensOut     int
	tokensCache   int
	activity      string
	activityLog   []string
	fallbackIdx   int
	fallback      []string
	width         int
	height        int
	spinnerIdx    int
	spinner       []string
	input         []rune
	messages      []chatMessage
	askPending    bool
	pendingAskID  int
	thinkingText  string
	pendingAnswer string
	askUpdates    chan tea.Msg
	askCancel     context.CancelFunc
	asker         AskHandler
	gitStats      GitStats
	lastGitSync   time.Time
	gitSource     GitStatsSource
	debugEnabled  bool
	logStream     <-chan string
	debugLogs     []string
	lastError     string
}

type AskHandler interface {
	Ask(ctx context.Context, question string, iteration int, totalTokens int, prompt string) <-chan AskStreamEvent
}

type AskStreamEvent struct {
	Thinking    string
	AnswerDelta string
	Answer      string
	Err         error
}

type chatMessage struct {
	role string
	text string
}

type GitStats struct {
	FilesChanged int
	Insertions   int
	Deletions    int
}

type GitStatsSource interface {
	Snapshot() GitStats
}

type EventMsg orchestrator.Event
type TickMsg time.Time
type AskThinkingMsg struct {
	ID   int
	Text string
}
type AskAnswerDeltaMsg struct {
	ID    int
	Delta string
}
type AskReplyMsg struct {
	ID     int
	Answer string
}
type LogMsg struct{ Line string }

func New(prompt string, maxIterations int, gitSource GitStatsSource, asker AskHandler) Model {
	if gitSource == nil {
		gitSource = NewLiveGitStatsSource()
	}
	return Model{
		prompt:        prompt,
		maxIterations: maxIterations,
		startedAt:     time.Now(),
		status:        "running",
		elapsed:       0,
		iteration:     1,
		activity:      "warming up...",
		width:         120,
		height:        40,
		fallback: []string{
			"Compiling ideas while you sleep...",
			"Negotiating with edge cases...",
			"Calmly reducing tomorrow's surprises...",
		},
		spinner:      []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		thinkingText: "thinking...",
		messages:     []chatMessage{},
		gitSource:    gitSource,
		asker:        asker,
	}
}

func (m *Model) EnableDebugLogs(stream <-chan string) {
	if stream == nil {
		return
	}
	m.debugEnabled = true
	m.logStream = stream
}

func (m *Model) EnableErrorLogs(stream <-chan string) {
	if stream == nil {
		return
	}
	m.logStream = stream
}

func (m *Model) SetModel(model string) {
	m.model = strings.TrimSpace(model)
}

func (m Model) Init() tea.Cmd {
	if m.logStream != nil {
		return tea.Batch(tickCmd(), waitForLogMessage(m.logStream))
	}
	return tickCmd()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch x := msg.(type) {
	case tea.KeyMsg:
		if x.String() == "ctrl+c" || x.String() == "q" {
			if m.askCancel != nil {
				m.askCancel()
				m.askCancel = nil
			}
			return m, tea.Quit
		}
		switch x.Type {
		case tea.KeyEnter:
			cmd := m.submitQuestion()
			return m, cmd
		case tea.KeyBackspace, tea.KeyDelete:
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
		default:
			r := []rune(x.String())
			if len(r) == 1 && r[0] >= 32 && r[0] != 127 {
				m.input = append(m.input, r[0])
			}
		}
		return m, nil
	case AskThinkingMsg:
		if x.ID != m.pendingAskID {
			return m, nil
		}
		if strings.TrimSpace(x.Text) != "" {
			m.thinkingText = normalizeActivity(x.Text)
		}
		if m.askPending && m.askUpdates != nil {
			return m, waitForAskMessage(m.askUpdates)
		}
		return m, nil
	case AskAnswerDeltaMsg:
		if x.ID != m.pendingAskID {
			return m, nil
		}
		if x.Delta != "" {
			m.pendingAnswer = normalizeAnswerLine(m.pendingAnswer + x.Delta)
		}
		if m.askPending && m.askUpdates != nil {
			return m, waitForAskMessage(m.askUpdates)
		}
		return m, nil
	case AskReplyMsg:
		if x.ID != m.pendingAskID {
			return m, nil
		}
		m.askPending = false
		m.thinkingText = ""
		m.pendingAnswer = ""
		m.askUpdates = nil
		if m.askCancel != nil {
			m.askCancel()
			m.askCancel = nil
		}
		m.messages = append(m.messages, chatMessage{role: "assistant", text: normalizeAnswerLine(x.Answer)})
		return m, nil
	case LogMsg:
		line := strings.TrimSpace(x.Line)
		if line != "" {
			m.debugLogs = append(m.debugLogs, line)
			if strings.Contains(strings.ToLower(line), "error") {
				m.lastError = line
			}
			if len(m.debugLogs) > 15 {
				m.debugLogs = m.debugLogs[len(m.debugLogs)-15:]
			}
		}
		if m.logStream != nil {
			return m, waitForLogMessage(m.logStream)
		}
		return m, nil
	case EventMsg:
		e := orchestrator.Event(x)
		prevStatus := m.status
		m.status = e.State.Status
		if e.State.CurrentIteration > 0 {
			m.iteration = e.State.CurrentIteration
		} else if e.State.LastCompleted > 0 {
			m.iteration = e.State.LastCompleted
		} else {
			m.iteration = 1
		}
		m.failureStreak = e.State.ConsecutiveFailures
		m.totalTokens = e.State.TotalTokens
		m.tokensIn = e.State.TotalInputTokens
		m.tokensOut = e.State.TotalOutputTokens
		m.tokensCache = e.State.TotalCacheRead + e.State.TotalCacheWrite
		if strings.TrimSpace(e.State.CurrentActivity) != "" {
			clean := normalizeActivity(e.State.CurrentActivity)
			m.activity = clean
			m.pushActivity(clean)
		} else {
			m.activity = m.nextFallback()
		}
		if (m.status == "stopped" || m.status == "aborted") && prevStatus != m.status {
			m.elapsed = time.Since(m.startedAt)
		}
		return m, nil
	case TickMsg:
		if m.status != "stopped" && m.status != "aborted" {
			m.spinnerIdx = (m.spinnerIdx + 1) % len(m.spinner)
			m.elapsed = time.Since(m.startedAt)
		}
		if strings.TrimSpace(m.activity) == "" {
			m.activity = m.nextFallback()
		}
		if time.Since(m.lastGitSync) >= time.Second {
			m.gitStats = m.gitSource.Snapshot()
			m.lastGitSync = time.Now()
		}
		return m, tickCmd()
	case tea.WindowSizeMsg:
		m.width = x.Width
		m.height = x.Height
		return m, nil
	}
	return m, nil
}

func tickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg { return TickMsg(t) })
}

func (m *Model) nextFallback() string {
	v := m.fallback[m.fallbackIdx%len(m.fallback)]
	m.fallbackIdx++
	return v
}
