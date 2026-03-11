package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
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
	chosenCWD    string
	chosenAction string // "", "summary", "continue", "fork"
	copied       bool
	filtering     bool   // true when typing in the filter input
	filter        string // current filter text
	formatFilter  int    // 0=all, 1=claude only, 2=codex only
}

func sessionUUID(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	// For Codex rollout files (rollout-<date>-<uuid>), extract the UUID
	if strings.HasPrefix(base, "rollout-") && len(base) >= 36 {
		candidate := base[len(base)-36:]
		if isUUID(candidate) {
			return candidate
		}
	}
	return base
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

func loadSummaryCmd(index int, si SessionInfo) tea.Cmd {
	return func() tea.Msg {
		f, err := os.Open(si.Path)
		if err != nil {
			return summaryLoadedMsg{index: index}
		}
		defer f.Close()

		ps := parseSessionFile(f, si.Path, "")

		var lines []summaryLine
		turnNum := 0
		for _, entry := range ps.Entries {
			if entry.Role != "system" {
				turnNum++
			}
			text := strings.Join(entry.Texts, " ")
			text = strings.ReplaceAll(text, "\n", " ")
			for strings.Contains(text, "  ") {
				text = strings.ReplaceAll(text, "  ", " ")
			}
			text = strings.TrimSpace(text)
			lines = append(lines, summaryLine{
				turnNum: turnNum,
				role:    entry.Role,
				text:    text,
				tools:   len(entry.Tools),
			})
		}

		// Sum token usage
		var totalInput, totalOutput int
		switch si.Format {
		case FormatCodex:
			totalInput, totalOutput = scanCodexTokenUsage(si.Path)
		default:
			totalInput, totalOutput = scanTokenUsage(si.Path)
		}

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
		return loadSummaryCmd(0, m.sessions[0])
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

	// Format filter
	if m.formatFilter != 0 {
		var filtered []SessionInfo
		for _, s := range m.sessions {
			if m.formatFilter == 1 && s.Format == FormatClaudeCode {
				filtered = append(filtered, s)
			} else if m.formatFilter == 2 && s.Format == FormatCodex {
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
			projPath := s.CWD
			if projPath == "" {
				projPath = strings.ReplaceAll(projDir, "-", "/")
			}
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
		return loadSummaryCmd(0, m.sessions[0])
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
				return m, loadSummaryCmd(m.cursor, m.sessions[m.cursor])
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
				m.fixScroll()
				return m, loadSummaryCmd(m.cursor, m.sessions[m.cursor])
			}
		case "pgdown", "ctrl+d":
			if len(m.sessions) > 0 {
				lh := m.listHeight()
				m.cursor = min(m.cursor+lh, len(m.sessions)-1)
				m.fixScroll()
				return m, loadSummaryCmd(m.cursor, m.sessions[m.cursor])
			}
		case "pgup", "ctrl+u":
			if len(m.sessions) > 0 {
				lh := m.listHeight()
				m.cursor = max(m.cursor-lh, 0)
				m.fixScroll()
				return m, loadSummaryCmd(m.cursor, m.sessions[m.cursor])
			}
		case "home":
			if len(m.sessions) > 0 {
				m.cursor = 0
				m.fixScroll()
				return m, loadSummaryCmd(0, m.sessions[0])
			}
		case "end":
			if len(m.sessions) > 0 {
				m.cursor = len(m.sessions) - 1
				m.fixScroll()
				return m, loadSummaryCmd(m.cursor, m.sessions[m.cursor])
			}
		case "enter":
			if len(m.sessions) > 0 {
				m.chosen = m.sessions[m.cursor].Path
				m.chosenCWD = m.sessions[m.cursor].CWD
				return m, tea.Quit
			}
		case "s":
			if len(m.sessions) > 0 {
				m.chosen = m.sessions[m.cursor].Path
				m.chosenCWD = m.sessions[m.cursor].CWD
				m.chosenAction = "summary"
				return m, tea.Quit
			}
		case "c":
			if len(m.sessions) > 0 {
				m.chosen = m.sessions[m.cursor].Path
				m.chosenCWD = m.sessions[m.cursor].CWD
				m.chosenAction = "continue"
				return m, tea.Quit
			}
		case "f":
			if len(m.sessions) > 0 {
				m.chosen = m.sessions[m.cursor].Path
				m.chosenCWD = m.sessions[m.cursor].CWD
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
		case "t":
			m.formatFilter = (m.formatFilter + 1) % 3
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
	var fmtLabel string
	switch m.formatFilter {
	case 0:
		fmtLabel = " claude"
	case 1:
		fmtLabel = " codex"
	case 2:
		fmtLabel = " all"
	}
	help := " " +
		stKey.Render("↑↓") + stDim.Render(" navigate  ") +
		stKey.Render("enter") + stDim.Render(" read  ") +
		stKey.Render("c") + stDim.Render(" continue  ") +
		stKey.Render("f") + stDim.Render(" fork  ") +
		stKey.Render("s") + stDim.Render(" summary  ") +
		stKey.Render("/") + stDim.Render(" filter  ") +
		stKey.Render("p") + stDim.Render(projLabel+"  ") +
		stKey.Render("t") + stDim.Render(fmtLabel+"  ") +
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
			stKey.Render("t") + stDim.Render(fmtLabel+"  ") +
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
	turns := fmt.Sprintf("%3dt", s.Turns)
	preview := strings.ReplaceAll(s.Preview, "\n", " ")
	for strings.Contains(preview, "  ") {
		preview = strings.ReplaceAll(preview, "  ", " ")
	}

	// Column widths: cursor(2) + idx(2) + 2 + when(10) + 2 + turns(4) + 2 + [proj(16) + 2] + preview
	const fixedWith = 2 + 2 + 2 + 10 + 2 + 4 + 2    // 24
	const fixedWithProj = fixedWith + 16 + 2           // 42

	if selected {
		cur := "▸ "
		var row string
		if m.showProject {
			proj := fmt.Sprintf("%-16s", truncate(s.Project, 16))
			pw := max(m.width-fixedWithProj, 10)
			row = fmt.Sprintf("%s%s  %s  %s  %s  %s", cur, idx, when, turns, proj, truncate(preview, pw))
		} else {
			pw := max(m.width-fixedWith, 10)
			row = fmt.Sprintf("%s%s  %s  %s  %s", cur, idx, when, turns, truncate(preview, pw))
		}
		for len(row) < m.width {
			row += " "
		}
		return stSelected.Render(row)
	}

	idxStr := stDim.Render(idx)
	whenStr := stTime.Render(when)
	turnsStr := stDim.Render(turns)

	if m.showProject {
		proj := truncate(s.Project, 16)
		projStr := stProject.Render(fmt.Sprintf("%-16s", proj))
		pw := max(m.width-fixedWithProj, 10)
		return "  " + idxStr + "  " + whenStr + "  " + turnsStr + "  " + projStr + "  " + truncate(preview, pw)
	}

	pw := max(m.width-fixedWith, 10)
	return "  " + idxStr + "  " + whenStr + "  " + turnsStr + "  " + truncate(preview, pw)
}

func (m tuiModel) renderSummaryLine(sl summaryLine) string {
	maxW := max(m.width-12, 20)

	if sl.role == "system" {
		return stDim.Render(fmt.Sprintf("     %s", truncate(sl.text, maxW)))
	}

	turnStr := stDim.Render(fmt.Sprintf(" %2d ", sl.turnNum))

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

	projectFilter := cwdProjectDir()

	m := tuiModel{
		allSessions:   sessions,
		showProject:   false,
		projectOnly:   true,
		projectFilter: projectFilter,
		formatFilter:  1, // claude only by default
		summaryFor:    -1,
	}
	m.applyFilter()

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
		realDir := final.chosenCWD
		if realDir == "" {
			// Fallback: reverse-map the project directory name
			projDir := filepath.Base(filepath.Dir(final.chosen))
			realDir = strings.ReplaceAll(projDir, "-", "/")
			if len(projDir) > 0 && projDir[0] == '-' {
				realDir = "/" + strings.ReplaceAll(projDir[1:], "-", "/")
			}
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
		renderSession(final.chosen, "", "", showThinking, true, fromTurn, toTurn, 0)
	default:
		renderSession(final.chosen, "", "", showThinking, false, fromTurn, toTurn, 0)
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
	stBannerBarDone = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2")).
			Bold(true)
)

const bannerTotalFrames = 50

var (
	waveChars    = []rune{' ', ' ', '·', '░', '▒', '▓', '█', '▓', '▒', '░', '·', ' ', ' '}
	waveColors   = []lipgloss.Style{
		lipgloss.NewStyle().Foreground(lipgloss.Color("87")), // bright cyan (center)
		lipgloss.NewStyle().Foreground(lipgloss.Color("81")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("75")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("69")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("63")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("57")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("54")), // deep blue (edges)
		lipgloss.NewStyle().Foreground(lipgloss.Color("53")),
	}
	spinnerChars = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	barGradient  = []lipgloss.Style{
		lipgloss.NewStyle().Foreground(lipgloss.Color("17")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("18")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("19")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("20")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("21")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("27")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("33")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("39")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("45")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("51")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("87")),
	}
)

type bannerTickMsg time.Time

type bannerModel struct {
	path   string
	frame  int
	width  int
	height int
	done   bool
}

func bannerTick() tea.Cmd {
	return tea.Tick(30*time.Millisecond, func(t time.Time) tea.Msg {
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
		m.frame++
		if m.frame >= bannerTotalFrames {
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
	if m.width == 0 || m.height == 0 {
		return ""
	}

	t := float64(m.frame) / float64(bannerTotalFrames)
	cy := m.height / 2
	halfH := math.Max(float64(m.height/2), 1)

	var b strings.Builder
	for y := 0; y < m.height; y++ {
		dist := math.Abs(float64(y - cy))
		relDist := dist / halfH
		if relDist > 1 {
			relDist = 1
		}

		// Central content: 7 lines (cy-3 to cy+3)
		if y >= cy-3 && y <= cy+3 {
			b.WriteString(m.bannerCenterLine(y-cy, t))
		} else {
			// Wave background — intensity fades with progress and distance from center
			intensity := (1.0 - t*0.9) * (0.2 + 0.8*(1.0-relDist))
			waveLine := m.bannerWaveLine(dist, t, intensity)

			colorIdx := int(relDist * float64(len(waveColors)-1))
			if colorIdx >= len(waveColors) {
				colorIdx = len(waveColors) - 1
			}
			b.WriteString(waveColors[colorIdx].Render(waveLine))
		}

		if y < m.height-1 {
			b.WriteByte('\n')
		}
	}

	return b.String()
}

func (m bannerModel) bannerWaveLine(dist, t, intensity float64) string {
	line := make([]rune, m.width)
	for x := 0; x < m.width; x++ {
		// Dual-frequency sine wave flowing inward toward center
		phase := float64(x)*0.12 - dist*0.4 + t*10.0
		val := (math.Sin(phase) + math.Sin(phase*0.618+2.1)) / 2
		val = (val + 1) / 2 * intensity
		idx := int(val * float64(len(waveChars)-1))
		if idx >= len(waveChars) {
			idx = len(waveChars) - 1
		}
		if idx < 0 {
			idx = 0
		}
		line[x] = waveChars[idx]
	}
	return string(line)
}

func (m bannerModel) bannerCenterLine(offset int, t float64) string {
	switch offset {
	case -3, -1, 1:
		return ""
	case -2:
		return m.bannerCenter(stBannerTitle.Render(" CONTEXT LIMIT REACHED "))
	case 0:
		return m.bannerProgressLine(t)
	case 2:
		spinner := spinnerChars[m.frame%len(spinnerChars)]
		if t < 0.4 {
			return m.bannerCenter(stBannerBody.Render(spinner + " Rendering session transcript..."))
		}
		return m.bannerCenter(stBannerBarDone.Render(spinner + " Restarting with full context..."))
	case 3:
		return m.bannerCenter(stBannerPath.Render(truncate(m.path, m.width-4)))
	}
	return ""
}

func (m bannerModel) bannerProgressLine(t float64) string {
	barWidth := 40
	if barWidth > m.width-8 {
		barWidth = m.width - 8
	}
	filled := int(t * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}

	var bar strings.Builder
	prevColor := -1
	count := 0
	for i := 0; i < filled; i++ {
		colorIdx := i * (len(barGradient) - 1) / max(barWidth-1, 1)
		if colorIdx != prevColor {
			if count > 0 {
				bar.WriteString(barGradient[prevColor].Render(strings.Repeat("█", count)))
			}
			prevColor = colorIdx
			count = 1
		} else {
			count++
		}
	}
	if count > 0 && prevColor >= 0 {
		bar.WriteString(barGradient[prevColor].Render(strings.Repeat("█", count)))
	}
	empty := barWidth - filled
	if empty > 0 {
		bar.WriteString(stDim.Render(strings.Repeat("░", empty)))
	}

	pad := (m.width - barWidth) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + bar.String()
}

func (m bannerModel) bannerCenter(s string) string {
	w := lipgloss.Width(s)
	pad := (m.width - w) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + s
}

func showRestartBanner(transcriptPath string) {
	m := bannerModel{path: transcriptPath}
	p := tea.NewProgram(m, tea.WithAltScreen())
	p.Run()
}

