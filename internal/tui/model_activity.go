package tui

import "strings"

func (m *Model) pushActivity(activity string) {
	a := normalizeActivity(activity)
	if a == "" {
		return
	}
	if len(m.activityLog) > 0 && m.activityLog[len(m.activityLog)-1] == a {
		return
	}
	m.activityLog = append(m.activityLog, a)
}

func normalizeActivity(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimLeft(line, "-•*✓☑☒▶▷→ ")
		line = strings.Join(strings.Fields(line), " ")
		if isActivityNoiseLine(line) {
			continue
		}
		line = truncateRunes(line, 110)
		if line != "" {
			return line
		}
	}
	return ""
}

func normalizeAnswerLine(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	lines := make([]string, 0, 8)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimLeft(line, "-•*✓☑☒▶▷→ ")
		line = strings.Join(strings.Fields(line), " ")
		if isActivityNoiseLine(line) {
			continue
		}
		lines = append(lines, truncateRunes(line, 220))
		if len(lines) >= 12 {
			break
		}
	}
	if len(lines) == 0 {
		return truncateRunes(strings.Join(strings.Fields(raw), " "), 220)
	}
	return strings.Join(lines, "\n")
}

func isActivityNoiseLine(line string) bool {
	l := strings.ToLower(strings.TrimSpace(line))
	if l == "" {
		return true
	}
	if strings.HasSuffix(l, ":") {
		switch l {
		case "objective:", "question:", "current objective:", "current run notes:":
			return true
		}
	}
	if strings.Contains(l, "answer directly and concretely in plain text") {
		return true
	}
	return false
}

func (m Model) activityHistoryLines(limit int) []string {
	if limit < 1 {
		limit = 1
	}
	entries := m.activityLog
	current := normalizeActivity(m.activity)
	if len(entries) > 0 && current != "" && entries[len(entries)-1] == current {
		entries = entries[:len(entries)-1]
	}
	if len(entries) == 0 {
		return []string{"✓ warming up..."}
	}
	start := 0
	if len(entries) > limit {
		start = len(entries) - limit
	}
	rows := make([]string, 0, limit+1)
	if start > 0 {
		rows = append(rows, "...")
	}
	for _, entry := range entries[start:] {
		rows = append(rows, "✓ "+entry)
	}
	return rows
}
