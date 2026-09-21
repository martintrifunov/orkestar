// Package workflow holds tasks, their dependencies, and resource leases:
// units of desired work that may outlive any individual agent session.
package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Status is a task's place in its lifecycle.
type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
	StatusCancelled  Status = "cancelled"
)

func (s Status) valid() bool {
	switch s {
	case StatusPending, StatusInProgress, StatusDone, StatusCancelled:
		return true
	default:
		return false
	}
}

// Task is a unit of desired work with dependencies and an optional
// assignee. Dependencies are other task IDs that must reach StatusDone
// before this task may move to StatusInProgress.
type Task struct {
	ID              string   `json:"id"`
	WorkspaceID     string   `json:"workspace_id"`
	Title           string   `json:"title"`
	Description     string   `json:"description,omitempty"`
	DependsOn       []string `json:"depends_on,omitempty"`
	AssigneeAgentID string   `json:"assignee_agent_id,omitempty"`
	WorktreePath    string   `json:"worktree_path,omitempty"`
	WorktreeBranch  string   `json:"worktree_branch,omitempty"`
	// AutoReview, when true, requires a reviewer-agent verdict before the
	// task may move to StatusDone. Callers that don't want that gate must
	// opt out explicitly when creating the task.
	AutoReview bool `json:"auto_review"`
	// AutoStart asks the daemon to launch AutoAgent on this task as soon as
	// every dependency is done, so a declared chain runs without a person or
	// an orchestrator calling task.wait for each link. The assignment is what
	// marks it as started: once AssigneeAgentID is set the daemon never
	// launches again, including across a restart.
	AutoStart bool `json:"auto_start,omitempty"`
	// AutoAgent names the adapter an automatic start launches. Empty means
	// there is nothing to start and AutoStart is inert.
	AutoAgent string `json:"auto_agent,omitempty"`
	// AutoPrompt is what that agent is told. Empty falls back to the task's
	// title and description.
	AutoPrompt string `json:"auto_prompt,omitempty"`
	// AutoStartError records why an automatic launch failed. A task carrying
	// one is not retried: a launch that failed once for a missing adapter or
	// a bad directory would fail again on every board change, so the reason
	// is reported and the decision to try again stays with a person.
	AutoStartError string `json:"auto_start_error,omitempty"`
	// TokenBudget, when positive, is the number of tokens the agents working
	// this task may report before BudgetAction applies. Zero means the task
	// has no token budget.
	TokenBudget int64 `json:"token_budget,omitempty"`
	// TimeBudgetSeconds, when positive, bounds how long one agent session
	// launched for this task may run before BudgetAction applies.
	TimeBudgetSeconds int64 `json:"time_budget_seconds,omitempty"`
	// BudgetAction is what crossing either budget does: BudgetActionWarn
	// raises attention, BudgetActionStop also interrupts the agent. Empty
	// behaves as warn.
	BudgetAction string    `json:"budget_action,omitempty"`
	Status       Status    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// The two ways a task's budget can be enforced when it is crossed.
const (
	BudgetActionWarn = "warn"
	BudgetActionStop = "stop"
)

// Board tracks tasks across workspaces and enforces dependency and
// transition rules. It has no knowledge of workspaces, agents, or IPC; the
// daemon translates between those and Board calls.
type Board struct {
	mu       sync.Mutex
	tasks    map[string]Task
	watchers map[chan struct{}]struct{}
	// order records the sequence tasks were created in. Creation timestamps
	// alone do not order them: two tasks created in the same clock tick
	// compare equal, and ranging a map to build the list means the sidebar can
	// show them in a different order on each refresh.
	order map[string]uint64
	next  uint64
}

// NewBoard returns an empty Board.
func NewBoard() *Board {
	return &Board{
		tasks:    make(map[string]Task),
		watchers: make(map[chan struct{}]struct{}),
		order:    make(map[string]uint64),
	}
}

// Watch returns a channel that carries a value whenever any task changes, and
// a function that stops watching.
//
// It signals that something moved, not what: the channel holds one pending
// wake and further changes leave it alone. A waiter is expected to re-read the
// board and judge for itself, which is what makes coalescing safe — a caller
// that misses three intermediate changes still sees the state they left
// behind. Delivering the tasks themselves would mean either blocking a
// mutation on a slow reader or dropping the one event that mattered.
func (b *Board) Watch() (<-chan struct{}, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	changes := make(chan struct{}, 1)
	b.watchers[changes] = struct{}{}
	return changes, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.watchers[changes]; ok {
			delete(b.watchers, changes)
			close(changes)
		}
	}
}

// notify wakes every watcher. Callers must hold b.mu, which is what lets a
// waiter register before its first read and never miss the change in between.
func (b *Board) notify() {
	for changes := range b.watchers {
		select {
		case changes <- struct{}{}:
		default:
		}
	}
}

// Create adds a new task. DependsOn entries must reference existing tasks;
// since a task can only depend on tasks that already exist, the dependency
// graph is a DAG by construction. autoReview sets whether the task requires
// a reviewer-agent verdict before it can move to StatusDone.
func (b *Board) Create(workspaceID, title, description string, dependsOn []string, autoReview bool) (Task, error) {
	if title == "" {
		return Task{}, errors.New("task title is required")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// The task has no ID yet, so it cannot be its own dependency and no edge
	// can point back to it: the cycle and self checks are no-ops here, and
	// what is left is the existence check Create has always done, plus the
	// same deduplication an edit gets.
	dependsOn, err := b.validDependencies("", dependsOn)
	if err != nil {
		return Task{}, err
	}

	id, err := newID("task")
	if err != nil {
		return Task{}, err
	}
	now := time.Now().UTC()
	task := Task{
		ID:          id,
		WorkspaceID: workspaceID,
		Title:       title,
		Description: description,
		DependsOn:   append([]string(nil), dependsOn...),
		AutoReview:  autoReview,
		Status:      StatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	b.tasks[id] = task
	b.order[id] = b.next
	b.next++
	b.notify()
	return task, nil
}

// TaskEdit is a partial change to a task. A nil field is left as it was, so a
// caller changing a title cannot blank a description it never sent.
type TaskEdit struct {
	Title       *string
	Description *string
	DependsOn   *[]string
}

// Update applies an edit to a task.
//
// Dependencies are the part that needs care. Create could not build a cycle:
// a task may only depend on tasks that already exist, so edges always point
// backwards in time. An edit has no such guarantee, and a cycle would make
// every task in it permanently unstartable, each waiting on the next. Reject
// one rather than let the board reach that state.
func (b *Board) Update(taskID string, edit TaskEdit) (Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	task, ok := b.tasks[taskID]
	if !ok {
		return Task{}, fmt.Errorf("task %q does not exist", taskID)
	}

	if edit.Title != nil {
		if *edit.Title == "" {
			return Task{}, errors.New("task title is required")
		}
		task.Title = *edit.Title
	}
	if edit.Description != nil {
		task.Description = *edit.Description
	}
	if edit.DependsOn != nil {
		dependsOn, err := b.validDependencies(taskID, *edit.DependsOn)
		if err != nil {
			return Task{}, err
		}
		task.DependsOn = dependsOn
	}

	task.UpdatedAt = time.Now().UTC()
	b.tasks[taskID] = task
	b.notify()
	return task, nil
}

// validDependencies checks a proposed dependency list and returns it
// deduplicated. Callers must hold b.mu.
func (b *Board) validDependencies(taskID string, dependsOn []string) ([]string, error) {
	seen := make(map[string]bool, len(dependsOn))
	unique := make([]string, 0, len(dependsOn))
	for _, dependencyID := range dependsOn {
		if dependencyID == taskID {
			return nil, fmt.Errorf("task %q cannot depend on itself", taskID)
		}
		if _, ok := b.tasks[dependencyID]; !ok {
			return nil, fmt.Errorf("dependency %q does not exist", dependencyID)
		}
		if seen[dependencyID] {
			continue
		}
		seen[dependencyID] = true
		unique = append(unique, dependencyID)
	}
	if path := b.cycleThrough(taskID, unique); path != "" {
		return nil, fmt.Errorf("task %q cannot depend on %s: it would never be startable", taskID, path)
	}
	return unique, nil
}

// cycleThrough reports the path back to taskID that the proposed dependencies
// would create, or empty if there is none. Callers must hold b.mu.
func (b *Board) cycleThrough(taskID string, dependsOn []string) string {
	visited := make(map[string]bool, len(b.tasks))
	var walk func(id string, path []string) string
	walk = func(id string, path []string) string {
		if id == taskID {
			return strings.Join(path, " → ")
		}
		if visited[id] {
			return ""
		}
		visited[id] = true
		for _, next := range b.tasks[id].DependsOn {
			if found := walk(next, append(path, next)); found != "" {
				return found
			}
		}
		return ""
	}
	for _, dependencyID := range dependsOn {
		if found := walk(dependencyID, []string{dependencyID}); found != "" {
			return found
		}
	}
	return ""
}

// SetStatus transitions a task to the given status. Moving to
// StatusInProgress requires every dependency to already be StatusDone.
func (b *Board) SetStatus(taskID string, status Status) (Task, error) {
	if !status.valid() {
		return Task{}, fmt.Errorf("invalid task status %q", status)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	task, ok := b.tasks[taskID]
	if !ok {
		return Task{}, fmt.Errorf("task %q does not exist", taskID)
	}

	if status == StatusInProgress {
		for _, dependencyID := range task.DependsOn {
			dependency, ok := b.tasks[dependencyID]
			if !ok || dependency.Status != StatusDone {
				return Task{}, fmt.Errorf("task %q is blocked on incomplete dependency %q", taskID, dependencyID)
			}
		}
	}

	task.Status = status
	task.UpdatedAt = time.Now().UTC()
	b.tasks[taskID] = task
	b.notify()
	return task, nil
}

// Assign sets a task's assignee. An empty agentID clears the assignment.
func (b *Board) Assign(taskID, agentID string) (Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	task, ok := b.tasks[taskID]
	if !ok {
		return Task{}, fmt.Errorf("task %q does not exist", taskID)
	}
	task.AssigneeAgentID = agentID
	task.UpdatedAt = time.Now().UTC()
	b.tasks[taskID] = task
	b.notify()
	return task, nil
}

// SetAutoStart records the agent an automatic start should launch once the
// task's dependencies are done. An empty agent clears the request, which is
// also how a person cancels one before it fires.
func (b *Board) SetAutoStart(taskID, agent, prompt string) (Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	task, ok := b.tasks[taskID]
	if !ok {
		return Task{}, fmt.Errorf("task %q does not exist", taskID)
	}
	task.AutoStart = agent != ""
	task.AutoAgent = agent
	task.AutoPrompt = prompt
	task.AutoStartError = ""
	task.UpdatedAt = time.Now().UTC()
	b.tasks[taskID] = task
	b.notify()
	return task, nil
}

// SetAutoStartError records why an automatic start could not happen. An empty
// reason clears it. A task with an error is skipped by the auto-start scan
// rather than retried on every board change.
func (b *Board) SetAutoStartError(taskID, reason string) (Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	task, ok := b.tasks[taskID]
	if !ok {
		return Task{}, fmt.Errorf("task %q does not exist", taskID)
	}
	task.AutoStartError = reason
	task.UpdatedAt = time.Now().UTC()
	b.tasks[taskID] = task
	b.notify()
	return task, nil
}

// SetBudget records how much an agent working this task may spend, and what
// crossing the limit does. Zero for both limits clears the budget. Tokens are
// the primary unit because that is what agents report; time is the fallback
// for an agent that reports nothing.
func (b *Board) SetBudget(taskID string, tokens, seconds int64, action string) (Task, error) {
	if tokens < 0 || seconds < 0 {
		return Task{}, errors.New("budgets cannot be negative")
	}
	switch action {
	case "", BudgetActionWarn, BudgetActionStop:
	default:
		return Task{}, fmt.Errorf("invalid budget action %q", action)
	}
	if action == "" {
		action = BudgetActionWarn
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	task, ok := b.tasks[taskID]
	if !ok {
		return Task{}, fmt.Errorf("task %q does not exist", taskID)
	}
	task.TokenBudget = tokens
	task.TimeBudgetSeconds = seconds
	task.BudgetAction = action
	task.UpdatedAt = time.Now().UTC()
	b.tasks[taskID] = task
	b.notify()
	return task, nil
}

// SetWorktree records the git worktree path and branch associated with a
// task. It only tracks metadata; creating or removing the worktree on disk
// is the caller's responsibility (see internal/git).
func (b *Board) SetWorktree(taskID, path, branch string) (Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	task, ok := b.tasks[taskID]
	if !ok {
		return Task{}, fmt.Errorf("task %q does not exist", taskID)
	}
	task.WorktreePath = path
	task.WorktreeBranch = branch
	task.UpdatedAt = time.Now().UTC()
	b.tasks[taskID] = task
	b.notify()
	return task, nil
}

// Get returns a single task by ID.
func (b *Board) Get(taskID string) (Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	task, ok := b.tasks[taskID]
	if !ok {
		return Task{}, fmt.Errorf("task %q does not exist", taskID)
	}
	return task, nil
}

// List returns every task, ordered by creation time.
func (b *Board) List() []Task {
	b.mu.Lock()
	defer b.mu.Unlock()
	tasks := make([]Task, 0, len(b.tasks))
	for _, task := range b.tasks {
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(left, right int) bool {
		if !tasks[left].CreatedAt.Equal(tasks[right].CreatedAt) {
			return tasks[left].CreatedAt.Before(tasks[right].CreatedAt)
		}
		return b.order[tasks[left].ID] < b.order[tasks[right].ID]
	})
	return tasks
}

func newID(prefix string) (string, error) {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate ID: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(raw), nil
}

// Condition is a state a waiter is waiting for a task to reach.
type Condition string

const (
	// ConditionDone waits for the task to be completed. A task that is
	// cancelled instead fails the wait rather than hanging until the deadline:
	// waiting for a completion that can no longer happen is a caller's bug and
	// they should hear about it.
	ConditionDone Condition = "done"
	// ConditionFinished waits for the task to stop being open, either way.
	ConditionFinished Condition = "finished"
	// ConditionStartable waits until nothing blocks the task, which is what an
	// orchestrator holding a dependency actually wants to know.
	ConditionStartable Condition = "startable"
)

func (c Condition) valid() bool {
	switch c {
	case ConditionDone, ConditionFinished, ConditionStartable:
		return true
	default:
		return false
	}
}

// satisfiedBy reports whether the task meets the condition, and whether
// waiting any longer is pointless. Callers must hold b.mu.
func (b *Board) satisfiedBy(task Task, until Condition) (met bool, hopeless bool) {
	switch until {
	case ConditionDone:
		return task.Status == StatusDone, task.Status == StatusCancelled
	case ConditionFinished:
		return task.Status == StatusDone || task.Status == StatusCancelled, false
	case ConditionStartable:
		if task.Status == StatusCancelled {
			return false, true
		}
		for _, dependencyID := range task.DependsOn {
			dependency, ok := b.tasks[dependencyID]
			if !ok || dependency.Status != StatusDone {
				return false, false
			}
		}
		return true, false
	}
	return false, true
}

// WaitFor blocks until a task meets a condition, the wait becomes pointless,
// or ctx ends. It is the primitive an orchestrating agent has no substitute
// for: without it, watching another agent's work means asking again and again,
// and for an agent every ask is a turn.
//
// The watcher is registered before the first read, so a change landing between
// the two wakes the wait rather than being missed.
func (b *Board) WaitFor(ctx context.Context, taskID string, until Condition) (Task, error) {
	if !until.valid() {
		return Task{}, fmt.Errorf("invalid wait condition %q", until)
	}
	changes, stop := b.Watch()
	defer stop()

	for {
		b.mu.Lock()
		task, ok := b.tasks[taskID]
		var met, hopeless bool
		if ok {
			met, hopeless = b.satisfiedBy(task, until)
		}
		b.mu.Unlock()

		switch {
		case !ok:
			return Task{}, fmt.Errorf("task %q does not exist", taskID)
		case met:
			return task, nil
		case hopeless:
			return task, fmt.Errorf("task %q is %s and will never be %s", taskID, task.Status, until)
		}

		select {
		case <-changes:
		case <-ctx.Done():
			return task, fmt.Errorf("waiting for task %q to be %s: %w", taskID, until, ctx.Err())
		}
	}
}

// Watchers reports how many watchers are registered. It exists so a test can
// prove waits clean up after themselves: a daemon that leaks one per
// orchestration call would wake an ever-growing list on every task change.
func (b *Board) Watchers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.watchers)
}

// Clear removes tasks in place and wakes pending waits during a daemon reset.
func (b *Board) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tasks = make(map[string]Task)
	b.order = make(map[string]uint64)
	b.next = 0
	b.notify()
}
