package main

import "testing"

func TestWorktreeName(t *testing.T) {
	tests := []struct {
		name      string
		gitDir    string
		commonDir string
		want      string
	}{
		{
			name:      "main worktree",
			gitDir:    "/repo/.git",
			commonDir: "/repo/.git",
			want:      "",
		},
		{
			name:      "linked worktree",
			gitDir:    "/repo/.git/worktrees/feature-x",
			commonDir: "/repo/.git",
			want:      "feature-x",
		},
		{
			name:      "trailing slashes normalized",
			gitDir:    "/repo/.git/worktrees/bugfix/",
			commonDir: "/repo/.git/",
			want:      "bugfix",
		},
		{
			name:      "same path different formatting",
			gitDir:    "/repo/.git/",
			commonDir: "/repo/.git",
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := worktreeName(tt.gitDir, tt.commonDir)
			if got != tt.want {
				t.Errorf("worktreeName(%q, %q) = %q, want %q", tt.gitDir, tt.commonDir, got, tt.want)
			}
		})
	}
}
