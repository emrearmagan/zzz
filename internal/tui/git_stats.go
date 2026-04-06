package tui

import (
	"zzz/internal/gitstate"
)

type liveGitStatsSource struct{}

func NewLiveGitStatsSource() GitStatsSource {
	return &liveGitStatsSource{}
}

func (s *liveGitStatsSource) Snapshot() GitStats {
	inspector := gitstate.New()
	diff, err := inspector.DiffNumstatFromHEAD()
	if err != nil {
		return GitStats{}
	}
	return GitStats{
		FilesChanged: diff.FilesChanged,
		Insertions:   diff.Insertions,
		Deletions:    diff.Deletions,
	}
}

type mockGitStatsSource struct {
	step int
}

func NewMockGitStatsSource() GitStatsSource {
	return &mockGitStatsSource{}
}

func (s *mockGitStatsSource) Snapshot() GitStats {
	s.step++
	phase := s.step % 24
	return GitStats{
		FilesChanged: 2 + (phase % 7),
		Insertions:   18 + ((phase * 9) % 84),
		Deletions:    6 + ((phase * 5) % 46),
	}
}
