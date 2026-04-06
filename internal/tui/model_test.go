package tui

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"zzz/internal/orchestrator"
	"zzz/internal/runstore"
)

func TestModelUpdatesFromEvent(t *testing.T) {
	m := New("do thing", 5, nil, nil)
	state := runstore.RunState{Status: "running", LastCompleted: 2, ConsecutiveFailures: 1, TotalTokens: 300, CurrentActivity: "planning"}
	updated, _ := m.Update(EventMsg(orchestrator.Event{State: state}))
	m2 := updated.(Model)
	if m2.iteration != 2 || m2.totalTokens != 300 || m2.failureStreak != 1 {
		t.Fatalf("bad model update: %+v", m2)
	}
}

func TestViewShowsNoProgressAndFailureStreak(t *testing.T) {
	m := New("prompt", 4, nil, nil)
	m.iteration = 2
	m.failureStreak = 2
	out := m.View()
	if strings.Contains(out, "Progress") {
		t.Fatalf("expected progress to be removed: %s", out)
	}
	if !strings.Contains(out, "Failure Streak") {
		t.Fatalf("missing failure streak: %s", out)
	}
}

func TestFallbackActivityWhenMissing(t *testing.T) {
	m := New("prompt", 0, nil, nil)
	updated, _ := m.Update(EventMsg(orchestrator.Event{State: runstore.RunState{Status: "running"}}))
	m2 := updated.(Model)
	if m2.activity == "" {
		t.Fatal("expected fallback activity")
	}
}

func TestViewUsesDashboardSections(t *testing.T) {
	m := New("ship polished ui", 8, nil, nil)
	m.iteration = 3
	m.totalTokens = 1200
	out := m.View()
	if !strings.Contains(out, "Overview") {
		t.Fatalf("missing overview panel: %s", out)
	}
	if !strings.Contains(out, "Run Metrics") {
		t.Fatalf("missing run metrics panel: %s", out)
	}
	if !strings.Contains(out, "Git Stats") {
		t.Fatalf("missing git stats panel: %s", out)
	}
	if !strings.Contains(out, "Activity") {
		t.Fatalf("missing activity panel: %s", out)
	}
	if !strings.Contains(out, "Ask zzz") {
		t.Fatalf("missing ask panel: %s", out)
	}
	if !strings.Contains(out, "Controls") {
		t.Fatalf("missing controls footer: %s", out)
	}
}

func TestViewUsesAvailableTerminalWidth(t *testing.T) {
	m := New("full width dashboard", 10, nil, nil)
	m.width = 180
	m.height = 50
	out := m.View()
	max := maxVisibleLineLen(out)
	if max < 164 {
		t.Fatalf("expected view to scale near full width; got max visible line len %d", max)
	}
}

func TestViewAddsSidePadding(t *testing.T) {
	m := New("padded view", 8, nil, nil)
	m.width = 120
	m.height = 40
	out := stripANSI(m.View())
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "███████╗") {
			if !strings.HasPrefix(line, "  ") {
				t.Fatalf("expected two-space left padding on content lines: %q", line)
			}
			return
		}
	}
	t.Fatalf("did not find content line to validate padding: %s", out)
}

func TestViewRendersLargeCenteredASCIIHeader(t *testing.T) {
	m := New("ascii header", 3, nil, nil)
	m.width = 140
	m.height = 40
	out := m.View()
	if !strings.Contains(out, "███████╗") {
		t.Fatalf("missing ascii title in header: %s", out)
	}
	lines := strings.Split(out, "\n")
	foundCentered := false
	for _, line := range lines {
		clean := stripANSI(line)
		idx := strings.Index(clean, "███████╗")
		if idx > 0 {
			foundCentered = true
			break
		}
	}
	if !foundCentered {
		t.Fatalf("expected centered ascii title, none found: %s", out)
	}
}

func TestActivityPanelShowsLatestFiveWithEllipsis(t *testing.T) {
	m := New("activity history", 10, nil, nil)
	activities := []string{
		"Scanning repository layout and dependency graph",
		"Reviewing prior notes and open constraints",
		"Designing minimal patch strategy",
		"Applying targeted code updates",
		"Running focused validation checks",
		"Writing iteration notes and rationale",
	}
	for i, activity := range activities {
		updated, _ := m.Update(EventMsg(orchestrator.Event{State: runstore.RunState{Status: "running", LastCompleted: i + 1, CurrentActivity: activity}}))
		m = updated.(Model)
	}

	out := m.View()
	if !strings.Contains(out, "...") {
		t.Fatalf("expected ellipsis when more than five activities exist: %s", out)
	}
	if strings.Contains(out, activities[0]) {
		t.Fatalf("expected oldest activity to be trimmed from view: %s", out)
	}
	if strings.Contains(out, "- ") {
		t.Fatalf("expected checkmark activity prefix, not dash bullets: %s", out)
	}
	if !strings.Contains(out, "✓") {
		t.Fatalf("expected checkmark marker for recent activities: %s", out)
	}
	if strings.Contains(out, "Objective:") || strings.Contains(out, "Current objective:") {
		t.Fatalf("expected normalized activity text, got prompt headers: %s", out)
	}
}

func TestQuestionInputAndResponse(t *testing.T) {
	m := New("chat", 5, nil, testAsker{response: "provider answer line one\nprovider answer line two"})
	m.width = 120
	m.height = 40
	updated, _ := m.Update(teaKeyMsg("h"))
	m = updated.(Model)
	updated, _ = m.Update(teaKeyMsg("i"))
	m = updated.(Model)
	updated, cmd := m.Update(teaEnterMsg())
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected async reply command after submit")
	}
	for i := 0; i < 3 && cmd != nil; i++ {
		msg := cmd()
		updated, next := m.Update(msg)
		m = updated.(Model)
		cmd = next
	}
	out := m.View()
	if strings.Contains(out, "Q: hi") {
		t.Fatalf("should only show latest response, not full chat history: %s", out)
	}
	if !strings.Contains(out, "provider answer line one") || !strings.Contains(out, "provider answer line two") {
		t.Fatalf("expected assistant response in chat output: %s", out)
	}
}

func TestAskShowsStreamedAnswerBeforeCompletion(t *testing.T) {
	m := New("chat", 5, nil, testAsker{answerDeltas: []string{"stream ", "in ", "chunks"}})
	m.width = 120
	m.height = 40
	updated, _ := m.Update(teaKeyMsg("h"))
	m = updated.(Model)
	updated, cmd := m.Update(teaEnterMsg())
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected async command")
	}

	msg := cmd() // first event from stream
	updated, next := m.Update(msg)
	m = updated.(Model)
	out := stripANSI(m.View())
	if !strings.Contains(out, "stream") {
		t.Fatalf("expected partial streamed answer to render while pending: %s", out)
	}

	for next != nil {
		msg = next()
		updated, next = m.Update(msg)
		m = updated.(Model)
	}
	if m.askPending {
		t.Fatal("expected ask to complete after stream closes")
	}
}

func TestCursorIsSteadyPipe(t *testing.T) {
	m := New("cursor steady", 5, nil, nil)
	m.width = 120
	m.height = 40
	if !strings.Contains(stripANSI(m.View()), "| Ask me") {
		t.Fatalf("expected visible straight cursor initially")
	}
	updated, _ := m.Update(TickMsg(time.Now()))
	m = updated.(Model)
	updated, _ = m.Update(TickMsg(time.Now()))
	m = updated.(Model)
	if !strings.Contains(stripANSI(m.View()), "| Ask me") {
		t.Fatalf("expected cursor to remain visible and non-blinking")
	}
	if strings.Contains(stripANSI(m.View()), "█ Ask me") {
		t.Fatalf("expected no block-style ask cursor glyph")
	}
}

func TestHeaderAnimatesOrbitInSingleLayout(t *testing.T) {
	theme := dashboardTheme()
	staticA := stripANSI(renderHeader(100, theme, 0))
	staticB := stripANSI(renderHeader(100, theme, 3))
	if staticA == staticB {
		t.Fatalf("header should animate orbit marker")
	}
	if !strings.Contains(staticA, "quiet loop, loud results") {
		t.Fatalf("header should include orbit dot next to tagline")
	}
}

func TestLayout1HasNoProgressInMetrics(t *testing.T) {
	m := New("layout one", 10, nil, nil)
	m.width = 130
	m.height = 44
	m.iteration = 4
	m.maxIterations = 10
	out := m.View()
	if strings.Contains(out, "Progress") {
		t.Fatalf("expected progress to be removed from layout: %s", out)
	}
}

func TestAskBoxShowsCursorWhenEmpty(t *testing.T) {
	m := New("cursor", 5, nil, nil)
	m.width = 120
	m.height = 40
	out := m.View()
	if !strings.Contains(out, "|") {
		t.Fatalf("expected visible cursor in ask box: %s", out)
	}
	if strings.Contains(out, "Latest responses") {
		t.Fatalf("should not render response block before any question: %s", out)
	}
}

func TestAskBoxCursorAppearsBeforePlaceholder(t *testing.T) {
	m := New("cursor placeholder", 5, nil, nil)
	m.width = 120
	m.height = 40
	out := stripANSI(m.View())
	if !strings.Contains(out, "| Ask me anything") {
		t.Fatalf("expected cursor before placeholder text, got: %s", out)
	}
	if strings.Contains(out, "Ask me anything while I work for you...|") {
		t.Fatalf("cursor should not render at end of placeholder: %s", out)
	}
}

func TestAskResponseSupportsMultiline(t *testing.T) {
	m := New("multiline", 5, nil, nil)
	m.width = 120
	m.height = 40
	m.messages = append(m.messages, chatMessage{role: "assistant", text: "line one\nline two\nline three"})
	out := stripANSI(m.View())
	if !strings.Contains(out, "line one") || !strings.Contains(out, "line two") || !strings.Contains(out, "line three") {
		t.Fatalf("expected multiline assistant response to render fully, got: %s", out)
	}
}

func TestMockGitStatsSourceIsUsed(t *testing.T) {
	src := &stubGitStatsSource{stats: GitStats{FilesChanged: 9, Insertions: 40, Deletions: 13}}
	m := New("mock git", 5, src, nil)
	m.width = 120
	m.height = 40
	updated, _ := m.Update(TickMsg(time.Now()))
	m = updated.(Model)
	out := m.View()
	if !strings.Contains(out, "9") || !strings.Contains(out, "+40") || !strings.Contains(out, "-13") {
		t.Fatalf("expected mocked git stats in view: %s", out)
	}
}

func TestDebugLogPanelRendersLatestEntries(t *testing.T) {
	m := New("debug", 5, nil, nil)
	stream := make(chan string, 20)
	m.EnableDebugLogs(stream)
	for i := 0; i < 18; i++ {
		stream <- "log line " + strconv.Itoa(i)
	}
	close(stream)
	for i := 0; i < 18; i++ {
		updated, cmd := m.Update(LogMsg{Line: "log line " + strconv.Itoa(i)})
		m = updated.(Model)
		_ = cmd
	}
	out := m.View()
	if !strings.Contains(out, "Debug Logs") {
		t.Fatalf("expected debug panel in view: %s", out)
	}
	if strings.Contains(out, "log line 0") || !strings.Contains(out, "log line 17") {
		t.Fatalf("expected only latest log lines in debug panel: %s", out)
	}
}

func maxVisibleLineLen(s string) int {
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	max := 0
	for _, line := range strings.Split(s, "\n") {
		clean := ansi.ReplaceAllString(line, "")
		w := utf8.RuneCountInString(clean)
		if w > max {
			max = w
		}
	}
	return max
}

func stripANSI(s string) string {
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	return ansi.ReplaceAllString(s, "")
}

func teaKeyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func teaEnterMsg() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyEnter}
}

type stubGitStatsSource struct {
	stats GitStats
}

func (s *stubGitStatsSource) Snapshot() GitStats {
	return s.stats
}

type testAsker struct {
	updates      []string
	answerDeltas []string
	response     string
}

func (a testAsker) Ask(_ context.Context, _ string, _ int, _ int, _ string) <-chan AskStreamEvent {
	out := make(chan AskStreamEvent, len(a.updates)+1)
	go func() {
		defer close(out)
		for _, line := range a.updates {
			out <- AskStreamEvent{Thinking: line}
		}
		for _, part := range a.answerDeltas {
			out <- AskStreamEvent{AnswerDelta: part}
		}
		if a.response != "" {
			out <- AskStreamEvent{Answer: a.response}
		}
	}()
	return out
}
