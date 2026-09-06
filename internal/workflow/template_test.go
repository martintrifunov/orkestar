package workflow_test

import (
	"strings"
	"testing"

	"github.com/martintrifunov/orkestar/internal/workflow"
)

const releaseTemplate = `{
  "name": "release-check",
  "description": "what has to happen before a release",
  "tasks": [
    {"key": "changelog", "title": "Update the changelog", "depends_on": ["tests"]},
    {"key": "tests", "title": "Run the suite", "worktree": true, "auto_review": false,
     "agent": "claude-code", "prompt": "run go test ./..."}
  ]
}`

func TestParseTemplate(t *testing.T) {
	template, err := workflow.ParseTemplate([]byte(releaseTemplate))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if template.Name != "release-check" || len(template.Tasks) != 2 {
		t.Fatalf("unexpected template: %+v", template)
	}

	// Declared second but depended on by the first, so it has to be created
	// first: a task can only depend on tasks that already exist.
	ordered, err := template.Ordered()
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if ordered[0].Key != "tests" || ordered[1].Key != "changelog" {
		t.Fatalf("order is %s then %s", ordered[0].Key, ordered[1].Key)
	}
	if !ordered[0].Worktree || ordered[0].Agent != "claude-code" {
		t.Fatalf("task details were lost: %+v", ordered[0])
	}
	if ordered[0].ReviewRequired() {
		t.Fatal("auto_review false was ignored")
	}
	// Absent means on, matching every other way a task is made.
	if !ordered[1].ReviewRequired() {
		t.Fatal("auto_review defaulted to off")
	}
}

// A template is rejected whole. Anything caught only partway through applying
// leaves a board someone has to clean up by hand.
func TestParseTemplateRejectsWhatWouldFailHalfway(t *testing.T) {
	for _, test := range []struct{ name, document, wants string }{
		{"no name", `{"tasks":[{"key":"a","title":"A"}]}`, "name is required"},
		{"no tasks", `{"name":"empty","tasks":[]}`, "no tasks"},
		{"no key", `{"name":"x","tasks":[{"title":"A"}]}`, "key"},
		{"no title", `{"name":"x","tasks":[{"key":"a"}]}`, "title"},
		{
			"duplicate keys",
			`{"name":"x","tasks":[{"key":"a","title":"A"},{"key":"a","title":"B"}]}`,
			"share the key",
		},
		{
			"dependency that is not defined",
			`{"name":"x","tasks":[{"key":"a","title":"A","depends_on":["ghost"]}]}`,
			"does not define",
		},
		{
			"depends on itself",
			`{"name":"x","tasks":[{"key":"a","title":"A","depends_on":["a"]}]}`,
			"itself",
		},
		{
			"a loop",
			`{"name":"x","tasks":[
			   {"key":"a","title":"A","depends_on":["b"]},
			   {"key":"b","title":"B","depends_on":["a"]}]}`,
			"loop",
		},
		{
			"a longer loop",
			`{"name":"x","tasks":[
			   {"key":"a","title":"A","depends_on":["c"]},
			   {"key":"b","title":"B","depends_on":["a"]},
			   {"key":"c","title":"C","depends_on":["b"]}]}`,
			"loop",
		},
		{"not json", `{`, "parse template"},
		{
			"a misspelled field",
			`{"name":"x","tasks":[{"key":"a","title":"A","dependson":["b"]}]}`,
			"unknown field",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := workflow.ParseTemplate([]byte(test.document))
			if err == nil {
				t.Fatal("accepted")
			}
			if !strings.Contains(err.Error(), test.wants) {
				t.Fatalf("error was %q, wanted something about %q", err, test.wants)
			}
		})
	}
}

// A diamond is not a loop: two tasks depending on one, and a fourth on both.
func TestTemplateAllowsSharedDependencies(t *testing.T) {
	document := `{"name":"x","tasks":[
	  {"key":"top","title":"Top","depends_on":["left","right"]},
	  {"key":"left","title":"Left","depends_on":["base"]},
	  {"key":"right","title":"Right","depends_on":["base"]},
	  {"key":"base","title":"Base"}]}`

	template, err := workflow.ParseTemplate([]byte(document))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ordered, err := template.Ordered()
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if len(ordered) != 4 {
		t.Fatalf("ordered %d of 4", len(ordered))
	}
	// Whatever the order, nothing may appear before something it depends on.
	position := map[string]int{}
	for index, task := range ordered {
		position[task.Key] = index
	}
	for _, task := range ordered {
		for _, dependency := range task.DependsOn {
			if position[dependency] > position[task.Key] {
				t.Fatalf("%s comes before its dependency %s", task.Key, dependency)
			}
		}
	}
}

// The order decides how tasks appear on the board, so it must not shift
// between runs the way ranging a map would make it.
func TestTemplateOrderIsStable(t *testing.T) {
	template, err := workflow.ParseTemplate([]byte(releaseTemplate))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	first, err := template.Ordered()
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	for range 20 {
		again, err := template.Ordered()
		if err != nil {
			t.Fatalf("order: %v", err)
		}
		for i := range first {
			if again[i].Key != first[i].Key {
				t.Fatalf("order changed between calls: %s then %s", first[i].Key, again[i].Key)
			}
		}
	}
}
