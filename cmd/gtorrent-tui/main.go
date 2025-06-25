package main

import (
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/filepicker"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/danferreira/gtorrent/internal/torrent"
)

var (
	tableStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.NormalBorder())
	infoBoxStyle = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).Padding(0, 1).Width(77)

	helpStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render
)

type mode int

const (
	modeMain     mode = iota // default
	modePickFile             // show file-picker in a dialog
)

type model struct {
	table      table.Model
	help       help.Model
	keyMap     keyMap
	filepicker filepicker.Model
	uiMode     mode
	quitting   bool

	err    string
	client *torrent.Client

	torrents []*torrent.TorrentInfo
}

type keyMap struct {
	open  key.Binding
	start key.Binding
	stop  key.Binding
	quit  key.Binding
}

type newTorrentMsg struct {
	t *torrent.TorrentInfo
}

type torrentStoppedMsg struct{}

type errMsg struct {
	err error
}

type tickMsg time.Time

func (m model) Init() tea.Cmd {
	return tickCmd()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		m.help.Width = wm.Width
		m.filepicker, _ = m.filepicker.Update(msg)
	}

	switch m.uiMode {
	case modePickFile:
		return m.updatePicker(msg)
	default:
		return m.updateMain(msg)
	}
}

func (m model) updateMain(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keyMap.quit):
			m.quitting = true
			return m, tea.Quit

		case key.Matches(msg, m.keyMap.start):
			selectedTorrent := m.selectedTorrent()
			if selectedTorrent != nil {
				return m, m.startTorrent(selectedTorrent)
			}

			return m, nil

		case key.Matches(msg, m.keyMap.stop):
			selectedTorrent := m.selectedTorrent()
			if selectedTorrent != nil {
				return m, m.stopTorrent(selectedTorrent)
			}

			return m, nil

		case key.Matches(msg, m.keyMap.open):
			m.uiMode = modePickFile
			return m, m.filepicker.Init()
		}
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		return m, cmd
	case newTorrentMsg:
		m.torrents = append(m.torrents, msg.t)
		m.updateRows()
		m.table.SetCursor(len(m.table.Rows()) - 1)
		return m, nil
	case tickMsg:
		m.updateRows()
		return m, tickCmd()
	}

	return m, nil
}

func (m model) updatePicker(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		if key.Matches(msg, m.keyMap.quit) {
			m.uiMode = modeMain
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.filepicker, cmd = m.filepicker.Update(msg)

	if didSelect, path := m.filepicker.DidSelectFile(msg); didSelect {
		m.uiMode = modeMain

		return m, tea.Batch(cmd, m.addTorrent(path), tickCmd())
	}

	return m, cmd
}

func (m model) addTorrent(path string) tea.Cmd {
	return func() tea.Msg {
		t, err := m.client.AddFile(path)
		if err != nil {
			return errMsg{err}
		}

		err = m.client.StartTorrent(t.Metadata.Info.InfoHash)
		if err != nil {
			return errMsg{err}
		}

		return newTorrentMsg{t}
	}
}

func (m model) View() string {
	if m.quitting {
		return ""
	}

	if m.uiMode == modePickFile {
		return "Open torrent:" + " " + m.filepicker.CurrentDirectory + "\n\n" + m.filepicker.View() + "\n"
	}

	helpView := m.help.ShortHelpView([]key.Binding{
		m.keyMap.open,
		m.keyMap.start,
		m.keyMap.stop,
		m.keyMap.quit,
	})

	infoBox := m.updateInfoBox()

	return lipgloss.JoinVertical(lipgloss.Left, tableStyle.Render(m.table.View()), infoBox, helpView)
}

func (m model) startTorrent(t *torrent.TorrentInfo) tea.Cmd {
	return func() tea.Msg {
		m.client.StartTorrent(t.Metadata.Info.InfoHash)

		return torrentStoppedMsg{}
	}
}

func (m model) stopTorrent(t *torrent.TorrentInfo) tea.Cmd {
	return func() tea.Msg {
		m.client.StopTorrent(t.Metadata.Info.InfoHash)

		return torrentStoppedMsg{}
	}
}

func (m *model) updateRows() {
	rows := make([]table.Row, 0, len(m.torrents))
	for i, ti := range m.torrents {
		s := ti.State.Snapshot()
		pct := float64(s.Downloaded) / float64(ti.Metadata.Info.TotalLength()) * 100
		rows = append(rows, table.Row{
			fmt.Sprint(i + 1),
			ti.Metadata.Info.Name,
			fmt.Sprintf("%5.1f%%", pct),
			ti.State.Status().String(),
			fmt.Sprint(ti.State.Peers()),
		})
	}
	m.table.SetRows(rows)
}

func (m *model) updateInfoBox() string {
	if len(m.torrents) == 0 {
		return infoBoxStyle.Render("No torrent")
	}

	i := m.table.Cursor()
	if i < 0 {
		return infoBoxStyle.Render("Select a torrent")
	}
	info := strings.Builder{}

	t := m.torrents[i]

	const bytesInMB = 1024 * 1024

	megabytes := float64(t.Metadata.Info.TotalLength()) / bytesInMB

	info.WriteString("\n")
	info.WriteString(fmt.Sprintf("Hash: %s", hex.EncodeToString(t.Metadata.Info.InfoHash[:])))
	info.WriteString("\n")
	info.WriteString(fmt.Sprintf("Size: %.2fMB", megabytes))

	return infoBoxStyle.Render(info.String())
}

func (m model) selectedTorrent() *torrent.TorrentInfo {
	cursor := m.table.Cursor()
	if cursor < 0 || cursor >= len(m.table.Rows()) {
		return nil
	}

	return m.torrents[cursor]
}

func configurePicker() filepicker.Model {
	fp := filepicker.New()
	fp.AllowedTypes = []string{".torrent"}

	return fp
}

func configureTable() table.Model {
	columns := []table.Column{
		{Title: "#", Width: 2},
		{Title: "File", Width: 30},
		{Title: "Progress", Width: 10},
		{Title: "Status", Width: 15},
		{Title: "Peers", Width: 10},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(7),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(false)
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(false)
	t.SetStyles(s)

	return t
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	fp := configurePicker()
	t := configureTable()

	c := torrent.NewClient(torrent.Config{
		ListenPort: 6881,
	})

	m := model{filepicker: fp, table: t, client: c,
		keyMap: keyMap{
			open: key.NewBinding(
				key.WithKeys("o"),
				key.WithHelp("o", "open"),
			),
			start: key.NewBinding(
				key.WithKeys("r"),
				key.WithHelp("r", "resume"),
			),
			stop: key.NewBinding(
				key.WithKeys("s"),
				key.WithHelp("s", "stop"),
			),
			quit: key.NewBinding(
				key.WithKeys("q", "ctrl+c"),
				key.WithHelp("q", "quit"),
			),
		},
		help: help.New(),
	}

	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}
