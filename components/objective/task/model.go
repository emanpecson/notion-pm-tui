package task

import (
	"fmt"
	"notion-project-tui/notion"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

type Model struct {
	list    list.Model
	notion  *notion.Client
	loading bool

	typeOptions []notion.SelectItem

	// id of the milestone backing the current task list; new tasks hang off this
	// via the @milestone relation. set on every milestone switch (MilestoneTasksMsg).
	milestoneID string

	// working copy of the current milestone's tasks, used for local mutations
	// (add, delete, status change) and list rendering. rebuilt on every
	// milestone switch via MilestoneTasksMsg — not the source of truth for persistence.
	// note: local mutations here do not sync back to the milestone's TaskGroups.
	groups map[notion.TaskStatus][]Item

	ActiveKeyMap help.KeyMap // for help focus view
	normalKeyMap NormalKeyMap
	selectKeyMap SelectKeyMap
	editKeyMap   EditKeyMap

	Mode      *Mode // ptr so the delegates see mode switches live
	SelectCtx *SelectModeCtx
	EditCtx   *EditModeCtx
	DeleteCtx *DeleteModeCtx

	// todo: this might go into the editModeCtx
	tempIDCounter int // for generating temp IDs for new tasks
}

func New(client *notion.Client) Model {
	// f := FocusState{}
	mode := NormalMode
	sel := SelectModeCtx{}
	edit := EditModeCtx{}
	del := DeleteModeCtx{}

	l := list.New([]list.Item{}, NewItemDelegate(false, &f), 0, 0)
	l.Title = "Tasks"
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetShowTitle(false)
	l.SetShowFilter(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()

	return Model{
		list:    l,
		notion:  client,
		loading: true,

		groups: map[notion.TaskStatus][]Item{},

		ActiveKeyMap: NormalKeyMapper, // default map view
		normalKeyMap: NormalKeyMapper,
		selectKeyMap: SelectKeyMapper,
		editKeyMap:   EditKeyMapper,

		Mode:      &mode,
		SelectCtx: &sel,
		EditCtx:   &edit,
		DeleteCtx: &del,
	}
}

func (m Model) Init() tea.Cmd {
	return m.notion.FetchTaskTypeOptions()
}

// -- helpers --

func (m Model) changeTaskStatus(task Item, delta int) (Model, tea.Cmd) {
	prevStatus := task.Status
	newStatus := cycleStatus(task.Status, delta)
	if newStatus == prevStatus {
		return m, nil // no change
	}

	// remove from old group
	oldGroup := m.groups[prevStatus]
	for i, t := range oldGroup {
		if t.ID == task.ID {
			m.groups[prevStatus] = append(oldGroup[:i], oldGroup[i+1:]...)
			break
		}
	}

	// update task status and add to new group
	task.Status = newStatus
	m.groups[newStatus] = append(m.groups[newStatus], task)

	// rebuild list to show change
	m.list.SetItems(m.buildTaskList(notion.TaskGroups{}))

	// a temp task isn't on notion yet; the create will carry its status
	if strings.HasPrefix(task.ID, "temp") {
		return m, nil
	}

	// persist the status change; reverted by onUpdateStatus on failure
	taskID := task.ID
	return m, func() tea.Msg {
		err := m.notion.UpdatePageProperties(taskID, map[string]any{
			"status": map[string]any{"name": newStatus},
		})
		return UpdateStatusMsg{TaskID: taskID, PrevStatus: prevStatus, Err: err}
	}
}

func (m Model) updateTaskInGroups(updated Item) Model {
	group := m.groups[updated.Status]

	// overwrite task in m.groups
	for i, t := range group {
		if t.ID == updated.ID {
			m.groups[updated.Status][i] = updated
			break
		}
	}

	// then rebuild item list
	m.list.SetItems(m.buildTaskList(notion.TaskGroups{}))
	return m
}

func (m Model) addTask() Model {
	defaultStatus := notion.TaskIdle

	m.tempIDCounter++
	tempID := fmt.Sprintf("temp-%d", m.tempIDCounter)

	// new task with defaults
	newTask := Item{
		ID:       tempID,
		Task:     "",
		Status:   notion.TaskIdle,
		Priority: 0, // p0
		Type:     "feat",
	}

	// Add to group
	m.groups[defaultStatus] = append(m.groups[defaultStatus], newTask)

	// Rebuild list
	items := m.buildTaskList(notion.TaskGroups{})
	m.list.SetItems(items)

	// Find the new task's index in the rebuilt list
	for i, item := range items {
		if task, ok := item.(Item); ok && task.ID == tempID {
			// Select the new task
			m.list.Select(i)

			// Initialize focus state
			m.Focus.taskID = tempID
			m.Focus.taskIdx = i
			m.SelectCtx.field = TaskTitle

			// Initialize text input and enter writing mode
			m.Focus.tempTitle = initTempTitle(newTask)
			m.Focus.Mode = EditMode
			m.ActiveKeyMap = m.editKeyMap

			break
		}
	}

	return m
}

func (m Model) deleteTask(task Item) (Model, tea.Cmd) {
	// capture the prior list index so a failed trash can restore selection
	idx := m.Focus.taskIdx

	// optimistically remove from group
	group := m.groups[task.Status]
	for i, t := range group {
		if t.ID == task.ID {
			m.groups[task.Status] = append(group[:i], group[i+1:]...)
			break
		}
	}

	// Rebuild list
	m.list.SetItems(m.buildTaskList(notion.TaskGroups{}))

	// Clear pending delete state
	m.Focus.pendingDelete = false

	// a temp task was never persisted; nothing to trash on notion
	if strings.HasPrefix(task.ID, "temp") {
		return m, nil
	}

	// trash the page on notion; reverted by onDeleteTask on failure
	taskID := task.ID
	return m, func() tea.Msg {
		err := m.notion.TrashPage(taskID)
		return DeleteTaskMsg{Task: task, Idx: idx, Err: err}
	}
}

// todo: this should be a value receiver, return model
func (m *Model) SetItemDelegate(d list.ItemDelegate) {
	m.list.SetDelegate(d)
}

func (m Model) ClearTasks() Model {
	m.groups = map[notion.TaskStatus][]Item{}
	m.list.SetItems([]list.Item{})
	m.loading = true
	return m
}

func (m Model) buildTaskList(groups notion.TaskGroups) []list.Item {
	var items []list.Item
	for _, status := range notion.TaskStatusOrder() {
		group, ok := m.groups[status]
		if !ok || len(group) == 0 {
			continue
		}
		hasMore := groups[status].NextCursor != nil
		items = append(items, GroupHeader{
			Status:  status,
			Hidden:  groups[status].Hide,
			Count:   len(group),
			HasMore: hasMore,
		})
		if !groups[status].Hide {
			for _, item := range group {
				items = append(items, item)
			}
			if groups[status].NextCursor != nil {
				items = append(items, LoadMoreItem{Status: status, Loading: groups[status].Loading})
			}
		}
	}
	return items
}
