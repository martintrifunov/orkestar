package workflow_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/workflow"
)

// waitFor runs a wait in the background and hands back a way to collect it, so
// a test can prove the wait was still blocked before the change that releases
// it.
func waitFor(board *workflow.Board, taskID string, until workflow.Condition) (func(*testing.T) (workflow.Task, error), func(*testing.T)) {
	type outcome struct {
		task workflow.Task
		err  error
	}
	done := make(chan outcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		task, err := board.WaitFor(ctx, taskID, until)
		done <- outcome{task, err}
	}()

	collect := func(t *testing.T) (workflow.Task, error) {
		t.Helper()
		select {
		case got := <-done:
			return got.task, got.err
		case <-time.After(5 * time.Second):
			t.Fatal("the wait never returned")
			return workflow.Task{}, nil
		}
	}
	stillWaiting := func(t *testing.T) {
		t.Helper()
		select {
		case got := <-done:
			t.Fatalf("the wait returned early: %+v %v", got.task, got.err)
		case <-time.After(150 * time.Millisecond):
		}
	}
	return collect, stillWaiting
}

func TestWaitForCompletion(t *testing.T) {
	board := workflow.NewBoard()
	task := mustCreate(t, board, "Ship it")

	collect, stillWaiting := waitFor(board, task.ID, workflow.ConditionDone)
	stillWaiting(t)

	if _, err := board.SetStatus(task.ID, workflow.StatusDone); err != nil {
		t.Fatalf("set status: %v", err)
	}
	finished, err := collect(t)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if finished.Status != workflow.StatusDone {
		t.Fatalf("returned %+v", finished)
	}
}

// A task that is already in the state being waited for must return at once,
// or an orchestrator that checks after the fact hangs until its deadline.
func TestWaitReturnsImmediatelyWhenAlreadyMet(t *testing.T) {
	board := workflow.NewBoard()
	task := mustCreate(t, board, "Already done")
	if _, err := board.SetStatus(task.ID, workflow.StatusDone); err != nil {
		t.Fatalf("set status: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := board.WaitFor(ctx, task.ID, workflow.ConditionDone); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

// Waiting for a completion that can no longer happen is a caller's bug, and
// they should hear about it rather than sit until the deadline.
func TestWaitingForDoneFailsOnCancellation(t *testing.T) {
	board := workflow.NewBoard()
	task := mustCreate(t, board, "Abandoned")

	collect, stillWaiting := waitFor(board, task.ID, workflow.ConditionDone)
	stillWaiting(t)

	if _, err := board.SetStatus(task.ID, workflow.StatusCancelled); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if _, err := collect(t); err == nil || !strings.Contains(err.Error(), "never") {
		t.Fatalf("cancellation reported as %v", err)
	}
}

// Waiting for a task to be finished either way accepts cancellation.
func TestWaitForFinishedAcceptsCancellation(t *testing.T) {
	board := workflow.NewBoard()
	task := mustCreate(t, board, "Abandoned")

	collect, _ := waitFor(board, task.ID, workflow.ConditionFinished)
	if _, err := board.SetStatus(task.ID, workflow.StatusCancelled); err != nil {
		t.Fatalf("set status: %v", err)
	}
	finished, err := collect(t)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if finished.Status != workflow.StatusCancelled {
		t.Fatalf("returned %+v", finished)
	}
}

// The condition an orchestrator holding a dependency actually wants: tell me
// when this can start.
func TestWaitForStartable(t *testing.T) {
	board := workflow.NewBoard()
	first := mustCreate(t, board, "First")
	second := mustCreate(t, board, "Second")
	dependent := mustCreate(t, board, "Dependent", first.ID, second.ID)

	collect, stillWaiting := waitFor(board, dependent.ID, workflow.ConditionStartable)
	stillWaiting(t)

	if _, err := board.SetStatus(first.ID, workflow.StatusDone); err != nil {
		t.Fatalf("set status: %v", err)
	}
	// One of two is not enough.
	stillWaiting(t)

	if _, err := board.SetStatus(second.ID, workflow.StatusDone); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if _, err := collect(t); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

// A task with no dependencies is startable now.
func TestWaitForStartableWithNoDependencies(t *testing.T) {
	board := workflow.NewBoard()
	task := mustCreate(t, board, "Nothing blocks this")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := board.WaitFor(ctx, task.ID, workflow.ConditionStartable); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

// A dependency edit is a change like any other, and a wait has to notice it.
// This is the case a mutation that forgot to notify would fail.
func TestWaitNoticesADependencyBeingCleared(t *testing.T) {
	board := workflow.NewBoard()
	blocker := mustCreate(t, board, "Blocker")
	dependent := mustCreate(t, board, "Dependent", blocker.ID)

	collect, stillWaiting := waitFor(board, dependent.ID, workflow.ConditionStartable)
	stillWaiting(t)

	if _, err := board.Update(dependent.ID, workflow.TaskEdit{DependsOn: ids()}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := collect(t); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

func TestWaitEndsWithItsContext(t *testing.T) {
	board := workflow.NewBoard()
	task := mustCreate(t, board, "Never finishes")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := board.WaitFor(ctx, task.ID, workflow.ConditionDone)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait ended with %v, want a deadline", err)
	}
}

func TestWaitRejectsWhatItCannotWaitFor(t *testing.T) {
	board := workflow.NewBoard()
	task := mustCreate(t, board, "Exists")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := board.WaitFor(ctx, task.ID, "whenever"); err == nil {
		t.Fatal("an unknown condition was accepted")
	}
	if _, err := board.WaitFor(ctx, "task_missing", workflow.ConditionDone); err == nil {
		t.Fatal("waiting on a task that does not exist should fail")
	}
	_ = task
}

// Several waiters on one task must all be released, and a coalesced wake must
// not lose any of them.
func TestManyWaitersAreAllReleased(t *testing.T) {
	board := workflow.NewBoard()
	task := mustCreate(t, board, "Everyone is waiting")

	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := board.WaitFor(ctx, task.ID, workflow.ConditionDone); err != nil {
				t.Errorf("wait: %v", err)
			}
		}()
	}
	// Churn the board so the coalescing path is exercised under real traffic.
	for i := range 20 {
		if _, err := board.Update(task.ID, workflow.TaskEdit{Description: text(string(rune('a' + i%26)))}); err != nil {
			t.Fatalf("update: %v", err)
		}
	}
	if _, err := board.SetStatus(task.ID, workflow.StatusDone); err != nil {
		t.Fatalf("set status: %v", err)
	}
	group.Wait()
}

// Watchers are cleaned up when their wait ends, or a long-lived daemon
// accumulates one per orchestration call.
func TestWaitersDoNotAccumulate(t *testing.T) {
	board := workflow.NewBoard()
	task := mustCreate(t, board, "Short waits")

	for range 50 {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		_, _ = board.WaitFor(ctx, task.ID, workflow.ConditionDone)
		cancel()
	}
	if got := board.Watchers(); got != 0 {
		t.Fatalf("%d watchers left behind", got)
	}
}
