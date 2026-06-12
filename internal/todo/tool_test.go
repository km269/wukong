package todo

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/km269/wukong/internal/util"
)

func TestNewStore_CreateAndClose(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	store, err := NewStore(dbPath, pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestStore_CreateTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	store, err := NewStore(dbPath, pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	task := &Task{
		ID:          uuid.New().String()[:8],
		Title:       "Test Task",
		Description: "Testing todo CRUD",
	}

	if err := store.Create(task); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if task.ID == "" {
		t.Error("expected non-empty task ID")
	}
	if task.Status != "pending" {
		t.Errorf(
			"expected 'pending', got %q", task.Status,
		)
	}
}

func TestStore_UpdateTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	store, err := NewStore(dbPath, pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	id := uuid.New().String()[:8]
	created := &Task{
		ID:          id,
		Title:       "Original",
		Description: "Original description",
	}
	if err := store.Create(created); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	updated := &Task{
		ID:          id,
		Title:       "Updated",
		Description: "Updated description",
		Status:      "in_progress",
	}
	if err := store.Update(updated); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify by listing
	tasks, err := store.List("")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	found := false
	for _, task := range tasks {
		if task.ID == id {
			found = true
			if task.Title != "Updated" {
				t.Errorf(
					"expected 'Updated', got %q",
					task.Title,
				)
			}
			if task.Status != "in_progress" {
				t.Errorf(
					"expected 'in_progress', got %q",
					task.Status,
				)
			}
		}
	}
	if !found {
		t.Error("updated task not found")
	}
}

func TestStore_ListTasks(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	store, err := NewStore(dbPath, pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	_ = store.Create(&Task{
		ID: uuid.New().String()[:8], Title: "Task 1",
	})
	_ = store.Create(&Task{
		ID: uuid.New().String()[:8], Title: "Task 2",
	})
	_ = store.Create(&Task{
		ID: uuid.New().String()[:8], Title: "Task 3",
	})

	tasks, err := store.List("")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(tasks) != 3 {
		t.Errorf("expected 3 tasks, got %d", len(tasks))
	}
}

func TestStore_ListTasks_ByStatus(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	store, err := NewStore(dbPath, pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	id1 := uuid.New().String()[:8]
	id2 := uuid.New().String()[:8]
	_ = store.Create(&Task{ID: id1, Title: "Pending Task"})
	_ = store.Create(&Task{ID: id2, Title: "Another Pending"})
	_ = store.Update(&Task{ID: id1, Status: "completed"})

	pending, err := store.List("pending")
	if err != nil {
		t.Fatalf("List pending failed: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf(
			"expected 1 pending task, got %d",
			len(pending),
		)
	}

	completed, err := store.List("completed")
	if err != nil {
		t.Fatalf("List completed failed: %v", err)
	}
	if len(completed) != 1 {
		t.Errorf(
			"expected 1 completed task, got %d",
			len(completed),
		)
	}
}

func TestStore_DeleteTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	store, err := NewStore(dbPath, pool)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	id := uuid.New().String()[:8]
	_ = store.Create(&Task{ID: id, Title: "To Delete"})
	if err := store.Delete(id); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	tasks, _ := store.List("")
	for _, task := range tasks {
		if task.ID == id {
			t.Error("task should have been deleted")
		}
	}
}

func TestTodoManager_Tools(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	store, _ := NewStore(dbPath, pool)
	defer store.Close()

	mgr := NewTodoManager(store)
	tools := mgr.Tools()

	if len(tools) != 5 {
		t.Errorf("expected 5 tools, got %d", len(tools))
	}

	expectedNames := map[string]bool{
		"todo_create":   true,
		"todo_update":   true,
		"todo_list":     true,
		"todo_complete": true,
		"todo_delete":   true,
	}
	for _, tool := range tools {
		name := tool.Declaration().Name
		if !expectedNames[name] {
			t.Errorf(
				"unexpected tool name: %q", name,
			)
		}
	}
}

func TestTodoManager_CreateTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	store, _ := NewStore(dbPath, pool)
	defer store.Close()

	mgr := NewTodoManager(store)
	ctx := context.Background()

	rsp, err := mgr.createTask(ctx, CreateTaskReq{
		Title:       "Integration Test",
		Description: "Testing from manager",
	})
	if err != nil {
		t.Fatalf("createTask failed: %v", err)
	}
	if !rsp.Success {
		t.Errorf(
			"expected success, got message: %q",
			rsp.Message,
		)
	}
}

func TestTodoManager_ListTasks(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	store, _ := NewStore(dbPath, pool)
	defer store.Close()

	mgr := NewTodoManager(store)
	ctx := context.Background()

	_, _ = mgr.createTask(ctx, CreateTaskReq{Title: "A"})
	_, _ = mgr.createTask(ctx, CreateTaskReq{Title: "B"})

	rsp, err := mgr.listTasks(ctx, ListTasksReq{})
	if err != nil {
		t.Fatalf("listTasks failed: %v", err)
	}
	if !rsp.Success {
		t.Error("expected success for listTasks")
	}
	if rsp.Count != 2 {
		t.Errorf("expected 2 tasks, got %d", rsp.Count)
	}
}

func TestTodoManager_CompleteTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	store, _ := NewStore(dbPath, pool)
	defer store.Close()

	mgr := NewTodoManager(store)
	ctx := context.Background()

	rsp, err := mgr.createTask(
		ctx, CreateTaskReq{Title: "Complete Me"},
	)
	if err != nil {
		t.Fatalf("createTask failed: %v", err)
	}
	if !rsp.Success {
		t.Fatalf("createTask: %s", rsp.Message)
	}

	// List to get the task ID
	listRsp, _ := mgr.listTasks(ctx, ListTasksReq{})
	if listRsp.Count == 0 {
		t.Fatal("no tasks found after create")
	}
	taskID := listRsp.Tasks[0].ID

	completeRsp, err := mgr.completeTask(ctx, CompleteTaskReq{
		ID: taskID,
	})
	if err != nil {
		t.Fatalf("completeTask failed: %v", err)
	}
	if !completeRsp.Success {
		t.Errorf(
			"expected success, got message: %q",
			completeRsp.Message,
		)
	}
}

func TestTodoManager_DeleteTask(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	store, _ := NewStore(dbPath, pool)
	defer store.Close()

	mgr := NewTodoManager(store)
	ctx := context.Background()

	rsp, _ := mgr.createTask(
		ctx, CreateTaskReq{Title: "Delete Me"},
	)
	if !rsp.Success {
		t.Fatal("failed to create task for deletion")
	}

	listRsp, _ := mgr.listTasks(ctx, ListTasksReq{})
	taskID := listRsp.Tasks[0].ID

	deleteRsp, err := mgr.deleteTask(ctx, DeleteTaskReq{
		ID: taskID,
	})
	if err != nil {
		t.Fatalf("deleteTask failed: %v", err)
	}
	if !deleteRsp.Success {
		t.Errorf(
			"expected success, got message: %q",
			deleteRsp.Message,
		)
	}

	// Verify deletion
	listRsp, _ = mgr.listTasks(ctx, ListTasksReq{})
	if listRsp.Count != 0 {
		t.Errorf(
			"expected 0 tasks after delete, got %d",
			listRsp.Count,
		)
	}
}

func TestTodoManager_Close(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	pool := util.NewDatabasePool(dbPath)
	defer pool.Close()

	store, _ := NewStore(dbPath, pool)
	mgr := NewTodoManager(store)

	if err := mgr.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Ensure temp directory cleanup
	os.RemoveAll(tmpDir)
}
