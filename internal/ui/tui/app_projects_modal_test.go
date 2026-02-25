package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestProjects_StartupProbeGitUnregisteredShowsConfirm(t *testing.T) {
	m := newAppModel(nil, "")
	updated, cmd := m.Update(startupProbeMsg{
		RepoRoot: "/tmp/demo",
		IsGit:    true,
		Err:      nil,
	})
	if cmd != nil {
		t.Fatalf("unexpected cmd for probe result")
	}
	got := updated.(appModel)
	if got.projectsMode != projectsModeRegisterConfirm {
		t.Fatalf("projectsMode=%v, want=%v", got.projectsMode, projectsModeRegisterConfirm)
	}
	if got.registerRepoRoot != "/tmp/demo" {
		t.Fatalf("registerRepoRoot=%q", got.registerRepoRoot)
	}
}

func TestProjects_StartupProbeNotGitShowsWarn(t *testing.T) {
	m := newAppModel(nil, "")
	updated, _ := m.Update(startupProbeMsg{
		IsGit:       false,
		Interactive: true,
		Err:         errors.New("当前目录不是 git 项目"),
	})
	got := updated.(appModel)
	if got.projectsMode != projectsModeRegisterWarn {
		t.Fatalf("projectsMode=%v, want=%v", got.projectsMode, projectsModeRegisterWarn)
	}
	if got.registerWarn == "" {
		t.Fatalf("registerWarn should not be empty")
	}
}

func TestProjects_StartupProbeNotGitPassive_NoWarn(t *testing.T) {
	m := newAppModel(nil, "")
	updated, _ := m.Update(startupProbeMsg{
		IsGit:       false,
		Interactive: false,
		Err:         errors.New("当前目录不是 git 项目"),
	})
	got := updated.(appModel)
	if got.projectsMode != projectsModeList {
		t.Fatalf("projectsMode=%v, want=%v", got.projectsMode, projectsModeList)
	}
}

func TestProjects_RegisterWarnEnterReturnsList(t *testing.T) {
	m := newAppModel(nil, "")
	m.projectsMode = projectsModeRegisterWarn
	m.registerWarn = "warn"
	updated, cmd := m.updateProjectsKeys(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("warn enter should not spawn cmd")
	}
	got := updated.(appModel)
	if got.projectsMode != projectsModeList {
		t.Fatalf("projectsMode=%v, want=%v", got.projectsMode, projectsModeList)
	}
	if got.registerWarn != "" {
		t.Fatalf("registerWarn should be cleared")
	}
}
