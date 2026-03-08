package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	stTitle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	stSelected = lipgloss.NewStyle().Bold(true).Background(lipgloss.AdaptiveColor{Light: "254", Dark: "236"})
	stDim      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "245", Dark: "240"})
	stTime     = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	stProject  = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	stUser     = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	stAsst     = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
	stTokens   = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow
	stKey      = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Bold(true)
)

type summaryLine struct {
	turnNum int
	role    string
	text    string
	tools   int
}

type summaryLoadedMsg struct {
	index        int
	lines        []summaryLine
	inputTokens  int
	outputTokens int
}

type copiedMsg struct{}

type tuiModel struct {
	allSessions   []SessionInfo // unfiltered
	sessions      []SessionInfo // filtered view
	cursor        int
	offset        int
	width         int
	height        int
	summaryLines  []summaryLine
	summaryFor    int
	inputTokens   int
	outputTokens  int
	showProject   bool
	projectOnly   bool   // true when filtering to current project
	projectFilter string // encoded cwd project dir
	chosen       string
	chosenAction string // "", "summary", "continue", "fork"
	copied       bool
	filtering     bool   // true when typing in the filter input
	filter        string // current filter text
}

func sessionUUID(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".jsonl")
}

func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("Jan 2")
	}
}

func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

type usageData struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

func scanTokenUsage(path string) (inputTokens, outputTokens int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	seenIDs := make(map[string]bool)

	for scanner.Scan() {
		var rec struct {
			Message *struct {
				ID    string     `json:"id"`
				Usage *usageData `json:"usage"`
			} `json:"message,omitempty"`
		}
		if json.Unmarshal(scanner.Bytes(), &rec) != nil || rec.Message == nil || rec.Message.Usage == nil || rec.Message.ID == "" {
			continue
		}
		if seenIDs[rec.Message.ID] {
			continue
		}
		seenIDs[rec.Message.ID] = true
		u := rec.Message.Usage
		inputTokens += u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
		outputTokens += u.OutputTokens
	}
	return
}

func loadSummaryCmd(index int, path string) tea.Cmd {
	return func() tea.Msg {
		f, err := os.Open(path)
		if err != nil {
			return summaryLoadedMsg{index: index}
		}
		defer f.Close()

		records := parseRecords(f)
		entries := buildConversation(records, path, false)

		var lines []summaryLine
		for i, entry := range entries {
			text := strings.Join(entry.Texts, " ")
			text = strings.ReplaceAll(text, "\n", " ")
			for strings.Contains(text, "  ") {
				text = strings.ReplaceAll(text, "  ", " ")
			}
			text = strings.TrimSpace(text)
			lines = append(lines, summaryLine{
				turnNum: i + 1,
				role:    entry.Role,
				text:    text,
				tools:   len(entry.Tools),
			})
		}

		// Sum token usage - re-scan file with usage-aware struct
		totalInput, totalOutput := scanTokenUsage(path)

		return summaryLoadedMsg{
			index:        index,
			lines:        lines,
			inputTokens:  totalInput,
			outputTokens: totalOutput,
		}
	}
}

func copyToClipboard(text string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		cmd.Run()
		return copiedMsg{}
	}
}

func (m tuiModel) Init() tea.Cmd {
	if len(m.sessions) > 0 {
		return loadSummaryCmd(0, m.sessions[0].Path)
	}
	return nil
}

func (m *tuiModel) applyFilter() tea.Cmd {
	m.sessions = m.allSessions

	// Project filter
	if m.projectOnly && m.projectFilter != "" {
		var filtered []SessionInfo
		for _, s := range m.sessions {
			projDir := filepath.Base(filepath.Dir(s.Path))
			if projDir == m.projectFilter {
				filtered = append(filtered, s)
			}
		}
		m.sessions = filtered
	}

	// Text filter
	if m.filter != "" {
		query := strings.ToLower(m.filter)
		var filtered []SessionInfo
		for _, s := range m.sessions {
			projDir := filepath.Base(filepath.Dir(s.Path))
			projPath := strings.ReplaceAll(projDir, "-", "/")
			text := strings.ToLower(s.Preview + " " + s.Project + " " + sessionUUID(s.Path) + " " + projDir + " " + projPath)
			if strings.Contains(text, query) {
				filtered = append(filtered, s)
			}
		}
		m.sessions = filtered
	}

	m.showProject = !m.projectOnly
	m.cursor = 0
	m.offset = 0
	m.summaryFor = -1
	m.summaryLines = nil
	if len(m.sessions) > 0 {
		return loadSummaryCmd(0, m.sessions[0].Path)
	}
	return nil
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case summaryLoadedMsg:
		if msg.index == m.cursor {
			m.summaryLines = msg.lines
			m.summaryFor = msg.index
			m.inputTokens = msg.inputTokens
			m.outputTokens = msg.outputTokens
		}

	case copiedMsg:
		m.copied = true

	case tea.KeyMsg:
		m.copied = false

		// Filter input mode
		if m.filtering {
			switch msg.String() {
			case "enter", "esc":
				m.filtering = false
				if msg.String() == "esc" {
					m.filter = ""
					cmd := m.applyFilter()
					return m, cmd
				}
			case "backspace":
				if len(m.filter) > 0 {
					m.filter = m.filter[:len(m.filter)-1]
					cmd := m.applyFilter()
					return m, cmd
				}
			case "ctrl+u":
				m.filter = ""
				cmd := m.applyFilter()
				return m, cmd
			default:
				if msg.Type == tea.KeyRunes {
					m.filter += string(msg.Runes)
					cmd := m.applyFilter()
					return m, cmd
				}
			}
			return m, nil
		}

		// Normal mode
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "esc":
			if m.filter != "" {
				m.filter = ""
				cmd := m.applyFilter()
				return m, cmd
			}
			return m, tea.Quit
		case "/":
			m.filtering = true
			return m, nil
		case "j", "down":
			if len(m.sessions) > 0 && m.cursor < len(m.sessions)-1 {
				m.cursor++
				m.fixScroll()
				return m, loadSummaryCmd(m.cursor, m.sessions[m.cursor].Path)
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
				m.fixScroll()
				return m, loadSummaryCmd(m.cursor, m.sessions[m.cursor].Path)
			}
		case "pgdown", "ctrl+d":
			if len(m.sessions) > 0 {
				lh := m.listHeight()
				m.cursor = min(m.cursor+lh, len(m.sessions)-1)
				m.fixScroll()
				return m, loadSummaryCmd(m.cursor, m.sessions[m.cursor].Path)
			}
		case "pgup", "ctrl+u":
			if len(m.sessions) > 0 {
				lh := m.listHeight()
				m.cursor = max(m.cursor-lh, 0)
				m.fixScroll()
				return m, loadSummaryCmd(m.cursor, m.sessions[m.cursor].Path)
			}
		case "home":
			if len(m.sessions) > 0 {
				m.cursor = 0
				m.fixScroll()
				return m, loadSummaryCmd(0, m.sessions[0].Path)
			}
		case "end":
			if len(m.sessions) > 0 {
				m.cursor = len(m.sessions) - 1
				m.fixScroll()
				return m, loadSummaryCmd(m.cursor, m.sessions[m.cursor].Path)
			}
		case "enter":
			if len(m.sessions) > 0 {
				m.chosen = m.sessions[m.cursor].Path
				return m, tea.Quit
			}
		case "s":
			if len(m.sessions) > 0 {
				m.chosen = m.sessions[m.cursor].Path
				m.chosenAction = "summary"
				return m, tea.Quit
			}
		case "c":
			if len(m.sessions) > 0 {
				m.chosen = m.sessions[m.cursor].Path
				m.chosenAction = "continue"
				return m, tea.Quit
			}
		case "f":
			if len(m.sessions) > 0 {
				m.chosen = m.sessions[m.cursor].Path
				m.chosenAction = "fork"
				return m, tea.Quit
			}
		case "y":
			if len(m.sessions) > 0 {
				uuid := sessionUUID(m.sessions[m.cursor].Path)
				return m, copyToClipboard(uuid)
			}
		case "p":
			m.projectOnly = !m.projectOnly
			cmd := m.applyFilter()
			return m, cmd
		}
	}
	return m, nil
}

func (m *tuiModel) fixScroll() {
	lh := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+lh {
		m.offset = m.cursor - lh + 1
	}
}

func (m tuiModel) listHeight() int {
	// Reserve: title(2) + scroll info(1) + separator(1) + summary(min) + separator(1) + footer(2) = 7
	avail := m.height - 7
	if avail < 4 {
		avail = 4
	}
	lh := avail / 2
	if lh > 15 {
		lh = 15
	}
	if lh > len(m.sessions) {
		lh = len(m.sessions)
	}
	if lh < 1 {
		lh = 1
	}
	return lh
}

func (m tuiModel) summaryHeight() int {
	lh := m.listHeight()
	// title(2) + list(lh) + scroll(1) + sep(1) + sep(1) + footer(2) = lh + 7
	sh := m.height - lh - 7
	if sh < 1 {
		sh = 1
	}
	return sh
}

func (m tuiModel) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	var b strings.Builder
	listH := m.listHeight()
	sumH := m.summaryHeight()

	// Title
	if m.filter != "" {
		b.WriteString(" " + stTitle.Render(fmt.Sprintf("Sessions (%d/%d)", len(m.sessions), len(m.allSessions))) + "\n\n")
	} else {
		b.WriteString(" " + stTitle.Render(fmt.Sprintf("Sessions (%d)", len(m.sessions))) + "\n\n")
	}

	// Session list
	for i := m.offset; i < m.offset+listH && i < len(m.sessions); i++ {
		b.WriteString(m.renderRow(i) + "\n")
	}

	// Scroll indicator
	if len(m.sessions) > listH {
		info := fmt.Sprintf(" %d-%d of %d",
			m.offset+1,
			min(m.offset+listH, len(m.sessions)),
			len(m.sessions))
		b.WriteString(stDim.Render(info) + "\n")
	} else {
		b.WriteString("\n")
	}

	// Separator
	b.WriteString(stDim.Render(strings.Repeat("─", m.width)) + "\n")

	// Summary
	linesWritten := 0
	if len(m.sessions) == 0 {
		b.WriteString(stDim.Render("  No matching sessions") + "\n")
		linesWritten++
	} else if m.summaryFor == m.cursor && len(m.summaryLines) > 0 {
		// Show the last few messages
		start := 0
		if len(m.summaryLines) > sumH {
			start = len(m.summaryLines) - sumH
		}
		for _, sl := range m.summaryLines[start:] {
			b.WriteString(m.renderSummaryLine(sl) + "\n")
			linesWritten++
		}
	} else if len(m.sessions) > 0 {
		b.WriteString(stDim.Render("  ...") + "\n")
		linesWritten++
	}
	for linesWritten < sumH {
		b.WriteString("\n")
		linesWritten++
	}

	// Bottom separator
	b.WriteString(stDim.Render(strings.Repeat("─", m.width)) + "\n")

	// Footer
	if m.filtering {
		cursor := stTitle.Render("_")
		b.WriteString(" /" + m.filter + cursor + "\n")
	} else if m.copied {
		b.WriteString(" " + stUser.Render("Copied!") + "\n")
	} else if len(m.sessions) > 0 {
		s := m.sessions[m.cursor]
		uuid := sessionUUID(s.Path)
		ts := s.Timestamp.Format("2006-01-02 15:04")
		tokInfo := ""
		if m.summaryFor == m.cursor && (m.inputTokens > 0 || m.outputTokens > 0) {
			tokInfo = fmt.Sprintf("  %s in / %s out", formatTokens(m.inputTokens), formatTokens(m.outputTokens))
		}
		if m.showProject {
			b.WriteString(stDim.Render(fmt.Sprintf(" %s  %s  %s", uuid, ts, s.Project)) + stTokens.Render(tokInfo) + "\n")
		} else {
			b.WriteString(stDim.Render(fmt.Sprintf(" %s  %s", uuid, ts)) + stTokens.Render(tokInfo) + "\n")
		}
	} else {
		b.WriteString("\n")
	}

	projLabel := " this project"
	if m.projectOnly {
		projLabel = " all projects"
	}
	help := " " +
		stKey.Render("↑↓") + stDim.Render(" navigate  ") +
		stKey.Render("enter") + stDim.Render(" read  ") +
		stKey.Render("c") + stDim.Render(" continue  ") +
		stKey.Render("f") + stDim.Render(" fork  ") +
		stKey.Render("s") + stDim.Render(" summary  ") +
		stKey.Render("/") + stDim.Render(" filter  ") +
		stKey.Render("p") + stDim.Render(projLabel+"  ") +
		stKey.Render("y") + stDim.Render(" yank uuid  ") +
		stKey.Render("q") + stDim.Render(" quit")
	if m.filter != "" && !m.filtering {
		help = " " +
			stKey.Render("↑↓") + stDim.Render(" navigate  ") +
			stKey.Render("enter") + stDim.Render(" read  ") +
			stKey.Render("c") + stDim.Render(" continue  ") +
			stKey.Render("f") + stDim.Render(" fork  ") +
			stKey.Render("s") + stDim.Render(" summary  ") +
			stKey.Render("/") + stDim.Render(" filter  ") +
			stKey.Render("esc") + stDim.Render(" clear  ") +
			stKey.Render("p") + stDim.Render(projLabel+"  ") +
			stKey.Render("y") + stDim.Render(" yank uuid  ") +
			stKey.Render("q") + stDim.Render(" quit")
	}
	b.WriteString(help + "\n")

	return b.String()
}

func (m tuiModel) renderRow(i int) string {
	s := m.sessions[i]
	selected := i == m.cursor

	idx := fmt.Sprintf("%2d", i+1)
	when := fmt.Sprintf("%-10s", relativeTime(s.Timestamp))
	preview := strings.ReplaceAll(s.Preview, "\n", " ")
	for strings.Contains(preview, "  ") {
		preview = strings.ReplaceAll(preview, "  ", " ")
	}

	if selected {
		cur := "▸ "
		var row string
		if m.showProject {
			proj := fmt.Sprintf("%-16s", truncate(s.Project, 16))
			pw := max(m.width-36, 10)
			row = fmt.Sprintf("%s%s  %s  %s  %s", cur, idx, when, proj, truncate(preview, pw))
		} else {
			pw := max(m.width-18, 10)
			row = fmt.Sprintf("%s%s  %s  %s", cur, idx, when, truncate(preview, pw))
		}
		for len(row) < m.width {
			row += " "
		}
		return stSelected.Render(row)
	}

	idxStr := stDim.Render(idx)
	whenStr := stTime.Render(when)

	if m.showProject {
		proj := truncate(s.Project, 16)
		projStr := stProject.Render(fmt.Sprintf("%-16s", proj))
		pw := max(m.width-36, 10)
		return "  " + idxStr + "  " + whenStr + "  " + projStr + "  " + truncate(preview, pw)
	}

	pw := max(m.width-18, 10)
	return "  " + idxStr + "  " + whenStr + "  " + truncate(preview, pw)
}

func (m tuiModel) renderSummaryLine(sl summaryLine) string {
	turnStr := stDim.Render(fmt.Sprintf(" %2d ", sl.turnNum))
	maxW := max(m.width-12, 20)

	switch sl.role {
	case "user":
		return turnStr + stUser.Render("User: ") + truncate(sl.text, maxW)
	case "assistant":
		if sl.tools > 0 {
			suffix := stDim.Render(fmt.Sprintf(" (%d tools)", sl.tools))
			return turnStr + stAsst.Render("Asst: ") + truncate(sl.text, maxW-15) + suffix
		}
		return turnStr + stAsst.Render("Asst: ") + truncate(sl.text, maxW)
	}
	return ""
}

func runTUI(n int, showThinking bool, fromTurn, toTurn int) {
	sessions := findSessions(n, "")
	if len(sessions) == 0 {
		fmt.Println("No sessions found.")
		return
	}

	m := tuiModel{
		allSessions:   sessions,
		sessions:      sessions,
		showProject:   true,
		projectFilter: cwdProjectDir(),
		summaryFor:    -1,
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	final := result.(tuiModel)
	if final.chosen == "" {
		return
	}

	switch final.chosenAction {
	case "continue", "fork":
		// cd to the session's project directory
		projDir := filepath.Base(filepath.Dir(final.chosen))
		realDir := strings.ReplaceAll(projDir, "-", "/")
		if len(projDir) > 0 && projDir[0] == '-' {
			realDir = "/" + strings.ReplaceAll(projDir[1:], "-", "/")
		}
		if err := os.Chdir(realDir); err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot cd to %s: %v\n", realDir, err)
			os.Exit(1)
		}

		uuid := sessionUUID(final.chosen)
		args := []string{"--resume", uuid}
		if final.chosenAction == "fork" {
			args = append(args, "--fork-session")
		}
		runClaude(args)

	case "summary":
		renderSession(final.chosen, "", showThinking, true, fromTurn, toTurn)
	default:
		renderSession(final.chosen, "", showThinking, false, fromTurn, toTurn)
	}
}

// --- Restart banner TUI ---

var (
	stBannerTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("4")).
			Padding(0, 2)
	stBannerBody = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6"))
	stBannerPath = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "245", Dark: "240"})
	stBannerBar = lipgloss.NewStyle().
			Foreground(lipgloss.Color("4"))
	stBannerBarDone = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2")).
			Bold(true)
)

type bannerTickMsg time.Time

type bannerModel struct {
	path     string
	progress int // 0-30
	width    int
	height   int
	done     bool
}

func bannerTick() tea.Cmd {
	return tea.Tick(40*time.Millisecond, func(t time.Time) tea.Msg {
		return bannerTickMsg(t)
	})
}

func (m bannerModel) Init() tea.Cmd {
	return bannerTick()
}

func (m bannerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case bannerTickMsg:
		_ = msg
		m.progress++
		if m.progress >= 30 {
			m.done = true
			return m, tea.Quit
		}
		return m, bannerTick()
	case tea.KeyMsg:
		m.done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m bannerModel) View() string {
	if m.width == 0 {
		return ""
	}

	var b strings.Builder

	// Center vertically
	pad := (m.height - 8) / 2
	for i := 0; i < pad; i++ {
		b.WriteString("\n")
	}

	// Title
	title := stBannerTitle.Render(" CONTEXT LIMIT REACHED ")
	titleW := lipgloss.Width(title)
	leftPad := (m.width - titleW) / 2
	if leftPad < 0 {
		leftPad = 0
	}
	b.WriteString(strings.Repeat(" ", leftPad) + title + "\n\n")

	// Progress bar
	barWidth := 40
	if barWidth > m.width-8 {
		barWidth = m.width - 8
	}
	filled := m.progress * barWidth / 30
	empty := barWidth - filled
	bar := stBannerBar.Render(strings.Repeat("█", filled)) + stDim.Render(strings.Repeat("░", empty))
	barLeft := (m.width - barWidth) / 2
	if barLeft < 0 {
		barLeft = 0
	}
	b.WriteString(strings.Repeat(" ", barLeft) + bar + "\n\n")

	// Status text
	var status string
	if m.progress < 15 {
		status = stBannerBody.Render("Rendering session transcript...")
	} else {
		status = stBannerBarDone.Render("Restarting claude with full context...")
	}
	statusW := lipgloss.Width(status)
	statusPad := (m.width - statusW) / 2
	if statusPad < 0 {
		statusPad = 0
	}
	b.WriteString(strings.Repeat(" ", statusPad) + status + "\n\n")

	// Path
	pathStr := stBannerPath.Render(truncate(m.path, m.width-4))
	pathW := lipgloss.Width(pathStr)
	pathPad := (m.width - pathW) / 2
	if pathPad < 0 {
		pathPad = 0
	}
	b.WriteString(strings.Repeat(" ", pathPad) + pathStr + "\n")

	return b.String()
}

func showRestartBanner(transcriptPath string) {
	m := bannerModel{path: transcriptPath}
	p := tea.NewProgram(m, tea.WithAltScreen())
	p.Run()
}

// --- Fastcompact confirmation TUI ---

type confirmModel struct {
	selected bool // true = yes (fastcompact), false = no (normal compact)
	decided  bool
	width    int
	height   int
}

func (m confirmModel) Init() tea.Cmd { return nil }

func (m confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y", "enter":
			m.decided = true
			return m, tea.Quit
		case "n", "N", "esc", "q":
			m.selected = false
			m.decided = true
			return m, tea.Quit
		case "left", "right", "h", "l", "tab":
			m.selected = !m.selected
		}
	}
	return m, nil
}

func (m confirmModel) View() string {
	if m.width == 0 {
		return ""
	}

	var b strings.Builder

	pad := (m.height - 10) / 2
	for i := 0; i < pad; i++ {
		b.WriteString("\n")
	}

	title := stBannerTitle.Render(" CONTEXT LIMIT REACHED ")
	titleW := lipgloss.Width(title)
	b.WriteString(strings.Repeat(" ", max((m.width-titleW)/2, 0)) + title + "\n\n")

	desc := stBannerBody.Render("Use fastcompact to restart with full session context?")
	descW := lipgloss.Width(desc)
	b.WriteString(strings.Repeat(" ", max((m.width-descW)/2, 0)) + desc + "\n")

	subdesc := stBannerPath.Render("Otherwise, Claude's built-in compaction will run.")
	subdescW := lipgloss.Width(subdesc)
	b.WriteString(strings.Repeat(" ", max((m.width-subdescW)/2, 0)) + subdesc + "\n\n")

	activeBtn := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("0")).
		Background(lipgloss.Color("4")).
		Padding(0, 3)
	inactiveBtn := lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "245", Dark: "240"}).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.AdaptiveColor{Light: "245", Dark: "240"}).
		Padding(0, 2)

	var yesBtn, noBtn string
	if m.selected {
		yesBtn = activeBtn.Render("Yes, fastcompact")
		noBtn = inactiveBtn.Render("No, normal compact")
	} else {
		yesBtn = inactiveBtn.Render("Yes, fastcompact")
		noBtn = activeBtn.Render("No, normal compact")
	}

	buttons := yesBtn + "   " + noBtn
	buttonsW := lipgloss.Width(buttons)
	b.WriteString(strings.Repeat(" ", max((m.width-buttonsW)/2, 0)) + buttons + "\n\n")

	help := stDim.Render("←→ switch   enter confirm   esc cancel")
	helpW := lipgloss.Width(help)
	b.WriteString(strings.Repeat(" ", max((m.width-helpW)/2, 0)) + help + "\n")

	return b.String()
}

func askFastcompactConfirm() bool {
	m := confirmModel{selected: true}
	p := tea.NewProgram(m, tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		return false
	}
	final := result.(confirmModel)
	return final.decided && final.selected
}

