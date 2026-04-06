package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	rendered := m.renderStackLayout()
	rendered = lipgloss.NewStyle().Padding(0, 1).Render(rendered)
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, rendered)
	}
	return rendered
}

func (m Model) renderStackLayout() string {
	elapsed := m.elapsed.Round(time.Second)
	theme := dashboardTheme()
	totalWidth := m.width - 4
	if totalWidth < 52 {
		totalWidth = 52
	}

	header := renderHeader(totalWidth, theme, m.spinnerIdx)
	line := lipgloss.NewStyle().Foreground(theme.border).Width(totalWidth).Render(strings.Repeat("─", totalWidth))

	overview := panel(theme, "Overview", wrapText(strings.TrimSpace(m.prompt), totalWidth-8), totalWidth)

	compact := totalWidth < 96
	metricsWidth := (totalWidth - 2) / 2
	gitWidth := totalWidth - metricsWidth - 2
	metricsBody := alignedMetricRows(theme,
		metricKV("Status", humanStatus(m.status)),
		metricKV("Model", displayModel(m.model)),
		metricKV("Iteration", iterationValue(m.iteration, m.maxIterations)),
		metricKV("Elapsed", elapsed.String()),
		metricKV("Tokens (in/out)", fmt.Sprintf("%d / %d", m.tokensIn, m.tokensOut)),
		metricKV("Failure Streak", fmt.Sprintf("%d", m.failureStreak)),
	)

	gitBody := alignedMetricRows(theme,
		metricKV("Files Changed", fmt.Sprintf("%d", m.gitStats.FilesChanged)),
		metricKV("Lines Added", fmt.Sprintf("+%d", m.gitStats.Insertions)),
		metricKV("Lines Removed", fmt.Sprintf("-%d", m.gitStats.Deletions)),
	)
	metricsLeft := panel(theme, "Run Metrics", metricsBody, metricsWidth)
	metricsRight := panel(theme, "Git Stats", gitBody, gitWidth)
	metricsRow := lipgloss.JoinHorizontal(lipgloss.Top, metricsLeft, lipgloss.NewStyle().Width(2).Render(""), metricsRight)
	if compact {
		metricsRow = lipgloss.JoinVertical(lipgloss.Left,
			panel(theme, "Run Metrics", metricsBody, totalWidth),
			panel(theme, "Git Stats", gitBody, totalWidth),
		)
	}

	activityText := strings.TrimSpace(m.activity)
	if activityText == "" {
		activityText = m.nextFallback()
	}
	historyCount := 3
	historyLines := m.activityHistoryLines(historyCount)
	for i, row := range historyLines {
		if strings.HasPrefix(row, "✓ ") {
			row = "✓ " + playfulActivity(strings.TrimSpace(strings.TrimPrefix(row, "✓ ")))
		} else if row != "..." {
			row = playfulActivity(row)
		}
		historyLines[i] = lipgloss.NewStyle().Foreground(theme.subtle).Render(truncateRunes(row, totalWidth-8))
	}
	spin := m.spinner[m.spinnerIdx%len(m.spinner)]
	activityLine := playfulActivity(activityText)
	if strings.TrimSpace(activityLine) == "" {
		activityLine = activityText
	}
	activityRows := []string{
		lipgloss.NewStyle().Foreground(theme.muted).Render("Recent activity"),
	}
	activityRows = append(activityRows, historyLines...)
	if m.status == "stopped" || m.status == "aborted" {
		activityRows = append(activityRows, "", fmt.Sprintf("%s  %s", lipgloss.NewStyle().Foreground(theme.success).Render("✓"), truncateRunes(activityLine, totalWidth-8)))
	} else {
		activityRows = append(activityRows, "", fmt.Sprintf("%s  %s", lipgloss.NewStyle().Foreground(theme.accent).Render(spin), truncateRunes(activityLine, totalWidth-8)))
	}
	activityBody := clampLines(strings.Join(activityRows, "\n"), clamp(m.height/4, 6, 12))
	activity := panel(theme, "Activity", activityBody, totalWidth)

	chatMaxLines := clamp(m.height/3, 7, 14)
	chat := panel(theme, "Ask zzz", m.renderChatBody(totalWidth, theme, chatMaxLines), totalWidth)

	controls := lipgloss.NewStyle().
		Foreground(theme.muted).
		Width(totalWidth).
		Align(lipgloss.Center).
		Render("Controls: q quit  •  Ctrl+C stop  •  Enter send question")

	sections := []string{header, line, overview, metricsRow}
	sections = append(sections, activity, chat, controls)
	if strings.TrimSpace(m.lastError) != "" {
		errLine := lipgloss.NewStyle().Foreground(theme.warn).Width(totalWidth).Render("Last error: " + truncateRunes(m.lastError, totalWidth-12))
		sections = append(sections, errLine)
	}
	if m.debugEnabled {
		debugMaxLines := clamp(m.height/4, 4, 10)
		sections = append(sections, panel(theme, "Debug Logs", m.renderDebugBody(totalWidth, theme, debugMaxLines), totalWidth))
	}

	out := strings.Join(sections, "\n")
	if m.height > 0 {
		out = fitToHeight(out, m.height-1)
	}
	return out
}

func displayModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return "(default)"
	}
	return model
}

func (m Model) renderDebugBody(totalWidth int, t theme, maxLines int) string {
	if len(m.debugLogs) == 0 {
		return lipgloss.NewStyle().Foreground(t.subtle).Render("(no logs yet)")
	}
	if maxLines < 1 {
		maxLines = 1
	}
	logs := m.debugLogs
	if len(logs) > maxLines {
		logs = logs[len(logs)-maxLines:]
	}
	lines := make([]string, 0, len(m.debugLogs))
	for _, line := range logs {
		lines = append(lines, lipgloss.NewStyle().Foreground(t.subtle).Render(truncateRunes(line, totalWidth-8)))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderChatBody(totalWidth int, t theme, maxLines int) string {
	placeholder := "Ask me anything while I work for you..."
	text := strings.TrimSpace(string(m.input))
	cursor := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Bold(true).Render("|")
	if text == "" {
		text = cursor + " " + lipgloss.NewStyle().Foreground(t.subtle).Render(placeholder)
	} else {
		text = text + cursor
	}
	boxWidth := totalWidth - 8
	inputLine := lipgloss.NewStyle().
		Width(boxWidth).
		Border(lipgloss.NormalBorder()).
		BorderForeground(t.accent).
		Padding(0, 1).
		Render(text)

	rows := []string{inputLine}
	if m.askPending {
		spin := m.spinner[m.spinnerIdx%len(m.spinner)]
		think := m.thinkingText
		if strings.TrimSpace(think) == "" {
			think = "thinking..."
		}
		rows = append(rows, "", lipgloss.NewStyle().Foreground(t.subtle).Render(fmt.Sprintf("%s %s", spin, think)))
		if strings.TrimSpace(m.pendingAnswer) != "" {
			rows = append(rows, "", lipgloss.NewStyle().Foreground(t.ink).Render(wrapMultiline(m.pendingAnswer, totalWidth-8)))
		}
	}
	if response, ok := m.latestAssistantResponse(); ok {
		rows = append(rows, "", lipgloss.NewStyle().Foreground(t.ink).Render(wrapMultiline(response, totalWidth-8)))
	}
	return clampLines(strings.Join(rows, "\n"), maxLines)
}

func clampLines(s string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	trimmed := append([]string{}, lines[:maxLines-1]...)
	trimmed = append(trimmed, "…")
	return strings.Join(trimmed, "\n")
}

func fitToHeight(s string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= maxLines {
		return s
	}
	trimmed := append([]string{}, lines[:maxLines-1]...)
	trimmed = append(trimmed, "…")
	return strings.Join(trimmed, "\n")
}
