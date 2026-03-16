package tui

import tea "github.com/charmbracelet/bubbletea"

func openRunsCmd() tea.Cmd {
	return func() tea.Msg {
		return gotoRunsMsg{}
	}
}
