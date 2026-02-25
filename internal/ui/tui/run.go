package tui

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"dalek/internal/app"
)

func Run(h *app.Home, initialProject string) error {
	m := newAppModel(h, initialProject)
	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithANSICompressor())
	_, err := prog.Run()
	if err != nil {
		return err
	}
	// 避免某些终端在退出 AltScreen 后残留光标状态
	_, _ = os.Stdout.WriteString("")
	return nil
}
