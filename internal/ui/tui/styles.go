package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
)

var (
	// 基础语义色：尽量在浅色/深色主题都能保持可读。
	cText  = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#F9FAFB"} // gray-900 / gray-50
	cMuted = lipgloss.AdaptiveColor{Light: "#374151", Dark: "#D1D5DB"} // gray-700 / gray-300
	cFaint = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"} // gray-500 / gray-400

	cBorder = lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#374151"} // gray-400 / gray-700
	cTitle  = cText

	// bar 背景：浅色用浅灰，深色用深灰，避免“灰字灰底”。
	cBarBG = lipgloss.AdaptiveColor{Light: "#E5E7EB", Dark: "#111827"} // gray-200 / gray-900

	// 选中行：浅色用淡蓝底 + 深字；深色用深蓝底 + 浅字。
	cSelBG = lipgloss.AdaptiveColor{Light: "#DBEAFE", Dark: "#1E3A8A"} // blue-100 / blue-900
	cSelFG = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#F9FAFB"}

	// 状态色（foreground/background 复用）：浅色用偏深，深色用偏亮。
	cInfo    = lipgloss.AdaptiveColor{Light: "#1D4ED8", Dark: "#60A5FA"} // blue-700 / blue-400
	cOk      = lipgloss.AdaptiveColor{Light: "#047857", Dark: "#34D399"} // emerald-700 / emerald-400
	cWarn    = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"} // amber-700 / amber-400
	cDanger  = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"} // red-700 / red-400
	cNeutral = lipgloss.AdaptiveColor{Light: "#4B5563", Dark: "#9CA3AF"} // gray-600 / gray-400

	cBadgeFG = lipgloss.AdaptiveColor{Light: "#F9FAFB", Dark: "#111827"}
)

func tableStyles() table.Styles {
	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(cBorder).
		Foreground(cText).
		BorderBottom(true).
		Bold(true)
	s.Selected = s.Selected.
		Foreground(cSelFG).
		Background(cSelBG).
		Bold(false)
	return s
}

func appStyle() lipgloss.Style {
	return lipgloss.NewStyle().Padding(1, 2)
}

func headerStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(cText).
		Background(cBarBG).
		Padding(0, 1)
}

func footerStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(cText).
		Background(cBarBG).
		Padding(0, 1)
}

func panelStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cBorder).
		Padding(0, 1)
}

func panelTitle(text string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(cTitle).Render(text)
}

func faint(text string) string {
	return lipgloss.NewStyle().Foreground(cFaint).Render(text)
}

func kv(k, v string) string {
	k = lipgloss.NewStyle().Foreground(cMuted).Render(k)
	v = lipgloss.NewStyle().Foreground(cText).Render(v)
	return fmt.Sprintf("%s %s", k, v)
}

func badge(text string, color lipgloss.TerminalColor) string {
	return lipgloss.NewStyle().
		Foreground(cBadgeFG).
		Background(color).
		Bold(true).
		Padding(0, 1).
		Render(text)
}
