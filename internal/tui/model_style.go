package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type theme struct {
	ink       lipgloss.Color
	muted     lipgloss.Color
	subtle    lipgloss.Color
	border    lipgloss.Color
	panel     lipgloss.Color
	accent    lipgloss.Color
	warn      lipgloss.Color
	danger    lipgloss.Color
	success   lipgloss.Color
	altAccent lipgloss.Color
}

func dashboardTheme() theme {
	return theme{
		ink:       lipgloss.Color("#CDD6F4"),
		muted:     lipgloss.Color("#8B93AA"),
		subtle:    lipgloss.Color("#646B80"),
		border:    lipgloss.Color("#313244"),
		panel:     lipgloss.Color("#1E1E2E"),
		accent:    lipgloss.Color("#89B4FA"),
		altAccent: lipgloss.Color("#CBA6F7"),
		warn:      lipgloss.Color("#F9E2AF"),
		danger:    lipgloss.Color("#F38BA8"),
		success:   lipgloss.Color("#A6E3A1"),
	}
}

func panel(t theme, title string, body string, width int) string {
	border := lipgloss.RoundedBorder()
	return lipgloss.NewStyle().
		Width(width).
		Border(border).
		BorderForeground(t.border).
		Background(t.panel).
		Padding(0, 1).
		Render(
			lipgloss.NewStyle().Bold(true).Foreground(t.accent).Render(title) + "\n" +
				lipgloss.NewStyle().Foreground(t.ink).Render(body),
		)
}

type metric struct {
	label string
	value string
}

func metricKV(label string, value string) metric {
	return metric{label: label, value: value}
}

func alignedMetricRows(t theme, rows ...metric) string {
	maxLabel := 0
	for _, row := range rows {
		if len(row.label) > maxLabel {
			maxLabel = len(row.label)
		}
	}
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		label := lipgloss.NewStyle().Foreground(t.muted).Render(fmt.Sprintf("%-*s", maxLabel, row.label))
		value := lipgloss.NewStyle().Foreground(t.ink).Bold(true).Render(row.value)
		lines = append(lines, fmt.Sprintf("%s : %s", label, value))
	}
	return strings.Join(lines, "\n")
}

func renderHeader(width int, t theme, frame int) string {
	orbit := []string{"◐", "◓", "◑", "◒"}
	art := []string{
		"███████╗███████╗███████╗",
		"╚══███╔╝╚══███╔╝╚══███╔╝",
		"  ███╔╝   ███╔╝   ███╔╝ ",
		" ███╔╝   ███╔╝   ███╔╝  ",
		"███████╗███████╗███████╗",
		"quiet loop, loud results " + orbit[frame%len(orbit)],
	}
	rows := make([]string, 0, len(art))
	for _, line := range art {
		styled := lipgloss.NewStyle().Bold(true).Foreground(t.accent).Render(line)
		rows = append(rows, lipgloss.PlaceHorizontal(width, lipgloss.Center, styled))
	}
	return strings.Join(rows, "\n")
}

func iterationValue(iteration int, max int) string {
	if max <= 0 {
		return fmt.Sprintf("%d / ∞", iteration)
	}
	return fmt.Sprintf("%d / %d", iteration, max)
}

func clamp(v int, min int, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func truncateRunes(s string, max int) string {
	if max < 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

func wrapText(s string, width int) string {
	if width < 12 {
		return s
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	lines := make([]string, 0, 4)
	line := words[0]
	for _, word := range words[1:] {
		candidate := line + " " + word
		if len([]rune(candidate)) <= width {
			line = candidate
			continue
		}
		lines = append(lines, line)
		line = word
	}
	lines = append(lines, line)
	return strings.Join(lines, "\n")
}

func wrapMultiline(s string, width int) string {
	if width < 8 {
		return s
	}
	parts := strings.Split(s, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimRight(part, " \t")
		if strings.TrimSpace(trimmed) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, wrapText(trimmed, width))
	}
	return strings.Join(out, "\n")
}

func humanStatus(status string) string {
	s := strings.TrimSpace(strings.ToLower(status))
	if s == "" {
		return "Idle"
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
