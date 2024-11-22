package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	_ "strconv"
	"time"
	"todo/auth"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/lib/pq" // Import pq driver
)

type Task struct {
	Id   int    `json:"Id"`
	Desc string `json:"Desc"`
}

var db *sql.DB

// middleware
func caller(next http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		//session check
		cookie, err := r.Cookie("session_id")
		if err != nil || cookie.Value == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		sessionID := cookie.Value

		//fetching data
		var (
			username   string
			created_at time.Time
		)
		err = db.QueryRow("SELECT username, created_at FROM session WHERE session_id = $1", sessionID).Scan(&username, &created_at)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "User not found", http.StatusNotFound)
				return
			}
			http.Error(w, "Error fetching tasks", http.StatusInternalServerError)
			return
		}

		duration := time.Now().UTC().Sub(created_at) //time.Since(created_at)
		if duration >= 5*time.Minute {
			auth.Logout(w, r)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return

		}

		next.ServeHTTP(w, r)
	})
}

// Initialize Database
func initDB() {
	//db string
	connStr := "host=localhost port=5432 user=postgres password=rx dbname=todo-multi sslmode=disable"

	var err error
	// Open a connection
	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatal(err)
	}

	// Test the connection
	err = db.Ping()
	if err != nil {
		log.Fatal("Failed to connect to the database:", err)
	}
	fmt.Println("Connected to the database successfully!")

}

func main() {

	//initializing database
	initDB()
	//closing database onces the server is closed
	defer db.Close()

	// share the DB to auth package
	auth.SetDB(db)

	r := chi.NewRouter()

	//middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	// r.Use(caller)

	//routes
	r.Get("/tasks", caller(List))
	r.Post("/tasks", caller(Add))
	r.Put("/tasks", caller(Update))
	r.Delete("/tasks", caller(Delete))

	//auth routes
	r.Post("/login", auth.Login)
	r.Post("/register", auth.Register)
	r.Post("/logout", auth.Logout)

	//server start
	fmt.Println("Server running on http://localhost:8000")
	log.Fatal(http.ListenAndServe(":8000", r))

}

func Add(w http.ResponseWriter, r *http.Request) {

	//request
	var newTask Task
	err := json.NewDecoder(r.Body).Decode(&newTask)

	if err != nil || newTask.Desc == "" {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	//extracting session
	cookie, _ := r.Cookie("session_id")

	// extract user from db using cookie
	username := ""
	err = db.QueryRow("select username from session where session_id=$1", cookie.Value).Scan(&username)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// id selection
	err = db.QueryRow(`SELECT 
    CASE
        WHEN (SELECT id FROM tasks WHERE id = 1 AND username = $1) IS NULL THEN 1
        ELSE
            (
                SELECT COALESCE(MIN(t1.id + 1), 1)
                FROM tasks t1
                LEFT JOIN tasks t2 ON t1.id + 1 = t2.id AND t1.username = t2.username
                WHERE t2.id IS NULL
                AND t1.username = $1
            )
    END `, username).Scan(&newTask.Id)
	if err != nil {
		http.Error(w, "Error generating ID", http.StatusInternalServerError)
		return
	}

	//insertion
	_, err = db.Exec("INSERT INTO tasks (id,description,username) VALUES ($1, $2, $3)", newTask.Id, newTask.Desc, username)
	if err != nil {
		http.Error(w, "Error inserting task", http.StatusInternalServerError)
		return
	}

	//response
	w.Header().Set("Content-Type", "application/json")
	response := map[string]interface{}{
		"message": "Task added successfully!",
		"task":    newTask,
	}
	json.NewEncoder(w).Encode(response)

}

func List(w http.ResponseWriter, r *http.Request) {

	// extracting session id
	cookie, _ := r.Cookie("session_id")

	//fetching data
	rows, err := db.Query("select t.id, t.description FROM tasks t join session s on t.username=s.username where s.session_id = $1", cookie.Value)
	if err != nil {
		http.Error(w, "Error fetching tasks", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		var task Task
		if err := rows.Scan(&task.Id, &task.Desc); err != nil {
			log.Fatal(err)
		}
		tasks = append(tasks, task)
	}

	//response
	w.Header().Set("Content-Type", "application/json")
	if len(tasks) == 0 {
		json.NewEncoder(w).Encode(map[string]string{"message": "No Task Found"})
		return
	}
	json.NewEncoder(w).Encode(tasks)
}

func Update(w http.ResponseWriter, r *http.Request) {

	//extracting id from body
	var newTask Task
	err := json.NewDecoder(r.Body).Decode(&newTask)
	id := newTask.Id
	if err != nil || id <= 0 || newTask.Desc == "" {
		http.Error(w, "Invalid task ID or description", http.StatusBadRequest)
		return
	}

	//extracting session
	cookie, _ := r.Cookie("session_id")

	// extract user from db using cookie
	username := ""
	err = db.QueryRow("select username from session where session_id=$1", cookie.Value).Scan(&username)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	//updating the task
	var result sql.Result
	result, err = db.Exec("UPDATE tasks SET description = $2 WHERE id = $1 and username = $3", newTask.Id, newTask.Desc, username)
	if err != nil {
		http.Error(w, "Error updating task", http.StatusInternalServerError)
		return
	}

	//get the number of rows affected
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		http.Error(w, "Error getting rows affected", http.StatusInternalServerError)
		return
	}
	if rowsAffected == 0 {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}

	//response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Task updated successfully!",
		"task":    newTask,
	})

}

func Delete(w http.ResponseWriter, r *http.Request) {

	//extracting id from body
	var newTask Task
	err := json.NewDecoder(r.Body).Decode(&newTask)
	id := newTask.Id

	if err != nil || id <= 0 {
		http.Error(w, "Invalid task ID", http.StatusBadRequest)
		return
	}

	//extracting session
	cookie, _ := r.Cookie("session_id")

	// extract user from db using cookie
	username := ""
	err = db.QueryRow("select username from session where session_id=$1", cookie.Value).Scan(&username)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	//removing the task

	var result sql.Result
	result, err = db.Exec("DELETE FROM tasks WHERE id = $1 and username = $2", id, username)
	if err != nil {
		http.Error(w, "Error deleting task", http.StatusInternalServerError)
		return
	}

	//get the number of rows affected
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		http.Error(w, "Error getting rows affected", http.StatusInternalServerError)
		return
	}
	if rowsAffected == 0 {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}

	//response

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Task deleted successfully"})

}
