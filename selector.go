package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// taskItem wraps an AtaTask for the bubbletea list model.
type taskItem struct {
	task AtaTask
}

func (i taskItem) Title() string {
	prefix := i.task.ID
	if i.task.IsEpic {
		prefix += " [epic]"
	}
	return prefix + "  " + i.task.Title
}

func (i taskItem) Description() string {
	var parts []string
	if i.task.EpicID != "" {
		parts = append(parts, "epic:"+i.task.EpicID)
	}
	if len(i.task.Tags) > 0 {
		parts = append(parts, strings.Join(i.task.Tags, ", "))
	}
	return strings.Join(parts, "  ")
}

func (i taskItem) FilterValue() string {
	return i.task.ID + " " + i.task.Title + " " + strings.Join(i.task.Tags, " ")
}

// taskDelegate renders each list item.
type taskDelegate struct{}

func (d taskDelegate) Height() int                             { return 1 }
func (d taskDelegate) Spacing() int                            { return 0 }
func (d taskDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d taskDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	ti, ok := item.(taskItem)
	if !ok {
		return
	}

	cursor := "  "
	if index == m.Index() {
		cursor = "> "
	}

	id := ti.task.ID
	title := ti.task.Title

	var meta []string
	if ti.task.IsEpic {
		meta = append(meta, "[epic]")
	}
	if ti.task.EpicID != "" {
		meta = append(meta, "epic:"+ti.task.EpicID)
	}
	if len(ti.task.Tags) > 0 {
		meta = append(meta, strings.Join(ti.task.Tags, ","))
	}

	metaStr := ""
	if len(meta) > 0 {
		metaStr = "  " + strings.Join(meta, " ")
	}

	if index == m.Index() {
		style := lipgloss.NewStyle().Bold(true)
		dimStyle := lipgloss.NewStyle().Faint(true).Bold(true)
		fmt.Fprintf(w, "%s%s  %s%s", cursor, style.Render(id), style.Render(title), dimStyle.Render(metaStr))
	} else {
		dimStyle := lipgloss.NewStyle().Faint(true)
		fmt.Fprintf(w, "%s%s  %s%s", cursor, id, title, dimStyle.Render(metaStr))
	}
}

type selectorModel struct {
	list     list.Model
	selected *AtaTask
	quit     bool
}

func (m selectorModel) Init() tea.Cmd {
	return nil
}

func (m selectorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Don't intercept keys when filtering.
		if m.list.FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "enter":
			if item, ok := m.list.SelectedItem().(taskItem); ok {
				task := item.task
				m.selected = &task
			}
			return m, tea.Quit
		case "q", "esc", "ctrl+c":
			m.quit = true
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.list.SetWidth(msg.Width)
		m.list.SetHeight(msg.Height)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m selectorModel) View() string {
	return m.list.View()
}

// selectTask presents an interactive fuzzy-searchable list of tasks and returns
// the user's selection. Returns nil if the user cancels.
func selectTask(tasks []AtaTask) (*AtaTask, error) {
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no ready tasks")
	}

	items := make([]list.Item, len(tasks))
	for i, t := range tasks {
		items[i] = taskItem{task: t}
	}

	l := list.New(items, taskDelegate{}, 80, min(len(tasks)+4, 20))
	l.Title = "Select a task"
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(true)

	m := selectorModel{list: l}
	p := tea.NewProgram(m)

	result, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("selector: %w", err)
	}

	final := result.(selectorModel)
	if final.quit {
		return nil, nil
	}
	return final.selected, nil
}
