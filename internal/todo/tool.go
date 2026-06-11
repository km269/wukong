// Package todo provides a task tracking system for the agent.
// It implements Function Tools for creating, updating, completing,
// and listing tasks that the agent can use to manage complex workflows.
package todo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// Task represents a todo task item.
type Task struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      string    `json:"status"` // pending, in_progress, completed
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Store provides persistent storage for todo tasks.
type Store struct {
	db *sql.DB
}

// NewStore creates a new todo store with the given database path.
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := initSchema(db); err != nil {
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// Create adds a new task to the store.
func (s *Store) Create(task *Task) error {
	now := time.Now()
	task.CreatedAt = now
	task.UpdatedAt = now
	if task.Status == "" {
		task.Status = "pending"
	}

	_, err := s.db.Exec(
		`INSERT INTO tasks (id, title, description, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		task.ID, task.Title, task.Description,
		task.Status, task.CreatedAt, task.UpdatedAt,
	)
	return err
}

// Update modifies an existing task.
func (s *Store) Update(task *Task) error {
	task.UpdatedAt = time.Now()
	result, err := s.db.Exec(
		`UPDATE tasks SET title=?, description=?, status=?, updated_at=?
		 WHERE id=?`,
		task.Title, task.Description, task.Status,
		task.UpdatedAt, task.ID,
	)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("task %s not found", task.ID)
	}
	return nil
}

// List returns all tasks, optionally filtered by status.
func (s *Store) List(status string) ([]Task, error) {
	var rows *sql.Rows
	var err error

	if status != "" {
		rows, err = s.db.Query(
			`SELECT id, title, description, status, created_at, updated_at
			 FROM tasks WHERE status=? ORDER BY created_at DESC`, status,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT id, title, description, status, created_at, updated_at
			 FROM tasks ORDER BY created_at DESC`,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(
			&t.ID, &t.Title, &t.Description,
			&t.Status, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// Delete removes a task by ID.
func (s *Store) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM tasks WHERE id=?`, id)
	return err
}

func initSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			description TEXT DEFAULT '',
			status TEXT DEFAULT 'pending',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

// Tool Types for Function Tools

// CreateTaskReq is the input for creating a task.
type CreateTaskReq struct {
	ID          string `json:"id" jsonschema:"description=Unique task identifier"`
	Title       string `json:"title" jsonschema:"description=Task title"`
	Description string `json:"description,omitempty" jsonschema:"description=Optional task description"`
}

// CreateTaskRsp is the output for creating a task.
type CreateTaskRsp struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// UpdateTaskReq is the input for updating a task.
type UpdateTaskReq struct {
	ID          string `json:"id" jsonschema:"description=Task ID to update"`
	Title       string `json:"title,omitempty" jsonschema:"description=New title"`
	Description string `json:"description,omitempty" jsonschema:"description=New description"`
	Status      string `json:"status,omitempty" jsonschema:"description=New status: pending, in_progress, completed"`
}

// UpdateTaskRsp is the output for updating a task.
type UpdateTaskRsp struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ListTasksReq is the input for listing tasks.
type ListTasksReq struct {
	Status string `json:"status,omitempty" jsonschema:"description=Filter by status: pending, in_progress, completed"`
}

// ListTasksRsp is the output for listing tasks.
type ListTasksRsp struct {
	Success bool   `json:"success"`
	Tasks   []Task `json:"tasks"`
	Count   int    `json:"count"`
}

// TodoManager wraps the store and provides tool definitions.
type TodoManager struct {
	store *Store
}

// NewTodoManager creates a new todo manager.
func NewTodoManager(store *Store) *TodoManager {
	return &TodoManager{store: store}
}

// Tools returns all todo-related function tools.
func (m *TodoManager) Tools() []tool.Tool {
	return []tool.Tool{
		function.NewFunctionTool(
			m.createTask,
			function.WithName("todo_create"),
			function.WithDescription(
				"Create a new todo task for tracking work items",
			),
		),
		function.NewFunctionTool(
			m.updateTask,
			function.WithName("todo_update"),
			function.WithDescription(
				"Update an existing todo task title, description, or status",
			),
		),
		function.NewFunctionTool(
			m.listTasks,
			function.WithName("todo_list"),
			function.WithDescription(
				"List all todo tasks, optionally filtered by status",
			),
		),
		function.NewFunctionTool(
			m.completeTask,
			function.WithName("todo_complete"),
			function.WithDescription(
				"Mark a todo task as completed",
			),
		),
	}
}

func (m *TodoManager) createTask(
	ctx context.Context, req CreateTaskReq,
) (CreateTaskRsp, error) {
	task := &Task{
		ID:          req.ID,
		Title:       req.Title,
		Description: req.Description,
	}
	if err := m.store.Create(task); err != nil {
		return CreateTaskRsp{
			Success: false,
			Message: fmt.Sprintf("Failed: %v", err),
		}, err
	}
	return CreateTaskRsp{
		Success: true,
		Message: fmt.Sprintf("Task %q created", req.Title),
	}, nil
}

func (m *TodoManager) updateTask(
	ctx context.Context, req UpdateTaskReq,
) (UpdateTaskRsp, error) {
	task := &Task{
		ID:          req.ID,
		Title:       req.Title,
		Description: req.Description,
		Status:      req.Status,
	}
	if err := m.store.Update(task); err != nil {
		return UpdateTaskRsp{
			Success: false,
			Message: fmt.Sprintf("Failed: %v", err),
		}, err
	}
	return UpdateTaskRsp{
		Success: true,
		Message: fmt.Sprintf("Task %q updated", req.ID),
	}, nil
}

func (m *TodoManager) listTasks(
	ctx context.Context, req ListTasksReq,
) (ListTasksRsp, error) {
	tasks, err := m.store.List(req.Status)
	if err != nil {
		return ListTasksRsp{
			Success: false,
		}, err
	}
	return ListTasksRsp{
		Success: true,
		Tasks:   tasks,
		Count:   len(tasks),
	}, nil
}

// CompleteTaskReq is the input for completing a task.
type CompleteTaskReq struct {
	ID string `json:"id" jsonschema:"description=Task ID to complete"`
}

// CompleteTaskRsp is the output for completing a task.
type CompleteTaskRsp struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (m *TodoManager) completeTask(
	ctx context.Context, req CompleteTaskReq,
) (CompleteTaskRsp, error) {
	task := &Task{
		ID:     req.ID,
		Status: "completed",
	}
	if err := m.store.Update(task); err != nil {
		return CompleteTaskRsp{
			Success: false,
			Message: fmt.Sprintf("Failed: %v", err),
		}, err
	}
	return CompleteTaskRsp{
		Success: true,
		Message: fmt.Sprintf("Task %q completed", req.ID),
	}, nil
}
