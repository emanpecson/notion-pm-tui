package task

import (
	"notion-project-tui/notion"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) onNormalKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	// todo: this is an interesting method; investigate if we keep or conform towards mstone
	// cancel pending delete if any key other than Delete is pressed
	if m.Focus.pendingDelete && !key.Matches(msg, m.normalKeyMap.Delete) {
		m.Focus.pendingDelete = false
		return m, nil
	}

	switch {
	case key.Matches(msg, m.normalKeyMap.Select):
		selected := m.list.SelectedItem()
		if header, ok := selected.(GroupHeader); ok {
			return m, func() tea.Msg {
				return notion.ToggleTaskGroupMsg{Status: header.Status}
			}
		} else if loadMore, ok := selected.(LoadMoreItem); ok && !loadMore.Loading {
			return m, func() tea.Msg {
				return notion.QueryMoreTaskPagesMsg{Status: loadMore.Status}
			}
		} else if task, ok := selected.(Item); ok {
			m.Focus.taskID = task.ID
			m.Focus.taskIdx = m.list.Index()
			m.SelectCtx.field = TaskTitle

			m.ActiveKeyMap = SelectKeyMapper
			m.Focus.Mode = SelectMode
		}
		return m, nil

	case key.Matches(msg, m.normalKeyMap.AddTask):
		m = m.addTask()
		return m, nil

	case key.Matches(msg, m.normalKeyMap.JumpUp):
		m.list.Select(max(0, m.list.Index()-5))
		return m, nil

	case key.Matches(msg, m.normalKeyMap.JumpDown):
		m.list.Select(min(len(m.list.Items())-1, m.list.Index()+5))
		return m, nil

	case key.Matches(msg, m.normalKeyMap.StatusPrev):
		if task, ok := m.list.SelectedItem().(Item); ok {
			return m.changeTaskStatus(task, -1)
		}
		return m, nil

	case key.Matches(msg, m.normalKeyMap.StatusNext):
		if task, ok := m.list.SelectedItem().(Item); ok {
			return m.changeTaskStatus(task, +1)
		}
		return m, nil

	// todo: instead, we should enter deleteMode and await key
	case key.Matches(msg, m.normalKeyMap.Delete):
		if task, ok := m.list.SelectedItem().(Item); ok {
			if m.Focus.pendingDelete && m.Focus.taskID == task.ID {
				return m.deleteTask(task)
			}
			m.Focus.pendingDelete = true
			m.Focus.taskID = task.ID
			m.Focus.taskIdx = m.list.Index()
		}
		return m, nil
	}

	// forward unmatched keys to list (e.g. up/down navigation)
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}
