package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Template is a piece of work declared once and applied many times: the tasks
// it breaks into, what depends on what, and which agent does each.
//
// It lives in a file under the workspace so it can be committed beside the
// code it describes. A pipeline that only exists in someone's shell history is
// not a pipeline the next person can run.
type Template struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Tasks       []TemplateTask `json:"tasks"`
}

// TemplateTask is one task in a template. Key names it for the others to
// depend on and never leaves the file: applying a template creates real tasks
// with real IDs, and the keys are resolved to those.
type TemplateTask struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	// Worktree gives the task its own git branch, which work that changes
	// files needs in order to be reviewable and to run beside other tasks.
	Worktree bool `json:"worktree,omitempty"`
	// AutoReview defaults to true, matching every other way a task is made.
	AutoReview *bool `json:"auto_review,omitempty"`
	// Agent names the adapter to launch when the template is applied with
	// starting enabled. Empty means the task is created and left for someone
	// to pick up.
	Agent string `json:"agent,omitempty"`
	// Prompt is what that agent is told. Empty means the task itself.
	Prompt string `json:"prompt,omitempty"`
}

// ParseTemplate reads and validates a template. It rejects anything that would
// only fail halfway through applying, because a half-applied template leaves a
// board someone has to clean up by hand.
func ParseTemplate(data []byte) (Template, error) {
	var template Template
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&template); err != nil {
		return Template{}, fmt.Errorf("parse template: %w", err)
	}
	if err := template.validate(); err != nil {
		return Template{}, err
	}
	return template, nil
}

func (t Template) validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("template name is required")
	}
	if len(t.Tasks) == 0 {
		return fmt.Errorf("template %q has no tasks", t.Name)
	}
	keys := make(map[string]bool, len(t.Tasks))
	for _, task := range t.Tasks {
		switch {
		case strings.TrimSpace(task.Key) == "":
			return fmt.Errorf("every task needs a key to be referred to by")
		case keys[task.Key]:
			return fmt.Errorf("two tasks share the key %q", task.Key)
		case strings.TrimSpace(task.Title) == "":
			return fmt.Errorf("task %q has no title", task.Key)
		}
		keys[task.Key] = true
	}
	for _, task := range t.Tasks {
		for _, dependency := range task.DependsOn {
			if dependency == task.Key {
				return fmt.Errorf("task %q depends on itself", task.Key)
			}
			if !keys[dependency] {
				return fmt.Errorf("task %q depends on %q, which the template does not define", task.Key, dependency)
			}
		}
	}
	_, err := t.Ordered()
	return err
}

// Ordered returns the tasks in an order where every task follows the ones it
// depends on, which is the order they have to be created in: a task can only
// depend on tasks that already exist.
//
// It is also where a cycle is caught. The board cannot catch this one for us —
// it refuses a dependency on a task that does not exist yet, so a cycle in a
// template shows up as a confusing missing-dependency error partway through
// creating things, long after the point where it could have been rejected.
func (t Template) Ordered() ([]TemplateTask, error) {
	byKey := make(map[string]TemplateTask, len(t.Tasks))
	for _, task := range t.Tasks {
		byKey[task.Key] = task
	}

	const (
		unvisited = iota
		inProgress
		done
	)
	state := make(map[string]int, len(t.Tasks))
	ordered := make([]TemplateTask, 0, len(t.Tasks))

	var visit func(key string, path []string) error
	visit = func(key string, path []string) error {
		switch state[key] {
		case done:
			return nil
		case inProgress:
			return fmt.Errorf("tasks depend on each other in a loop: %s", strings.Join(append(path, key), " → "))
		}
		state[key] = inProgress
		task := byKey[key]
		for _, dependency := range task.DependsOn {
			if err := visit(dependency, append(path, key)); err != nil {
				return err
			}
		}
		state[key] = done
		ordered = append(ordered, task)
		return nil
	}

	// Walked in declaration order so the result is the same every time, which
	// matters because it decides the order tasks appear on the board.
	for _, task := range t.Tasks {
		if err := visit(task.Key, nil); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

// ReviewRequired reports whether a template task wants a reviewer verdict,
// defaulting to true the way every other way of making a task does.
func (t TemplateTask) ReviewRequired() bool { return t.AutoReview == nil || *t.AutoReview }
