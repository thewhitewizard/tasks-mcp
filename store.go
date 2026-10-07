package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Store is how the tools read and write Projects and Tasks. Every method is one
// complete operation: it reads the current state, changes it and saves it.
type Store interface {
	ListProjects() ([]Project, error)
	// CreateProject assigns the ID.
	CreateProject(Project) (Project, error)
	ListTasks() ([]Task, error)
	GetTask(id string) (Task, error)
	// CreateTask assigns the ID.
	CreateTask(Task) (Task, error)
}

var (
	ErrNotFound       = errors.New("not found")
	ErrLimit          = errors.New("limit reached")
	ErrInvalidProject = errors.New("unknown project")
)

const (
	schemaVersion = 1
	maxFileSize   = 16 << 20
	maxProjects   = 200
	idAttempts    = 5
)

// document is the content of the Data file.
type document struct {
	SchemaVersion int       `json:"schema_version"`
	Projects      []Project `json:"projects"`
	Tasks         []Task    `json:"tasks"`
}

// jsonStore keeps everything in one JSON file, replaced atomically on each write.
type jsonStore struct {
	path     string
	maxTasks int
	newID    func(prefix string) string
	lock     fileLock
}

func newJSONStore(path string, maxTasks int, lockTimeout time.Duration) *jsonStore {
	return &jsonStore{path: path, maxTasks: maxTasks, newID: newID, lock: fileLock{path: path + ".lock", timeout: lockTimeout}}
}

// load reads the Data file. A missing file is an empty document; a file that
// is empty, corrupt, too large or of another version is an error, and is never
// touched.
func (s *jsonStore) load() (document, error) {
	empty := document{SchemaVersion: schemaVersion, Projects: []Project{}, Tasks: []Task{}}
	f, err := os.Open(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return document{}, fmt.Errorf("data file: %w", err)
	}
	defer f.Close() //nolint:errcheck // read-only file

	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return document{}, fmt.Errorf("data file: %w", err)
	}
	if len(data) > maxFileSize {
		return document{}, fmt.Errorf("data file is larger than %d MiB: left untouched", maxFileSize>>20)
	}

	var doc document
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		return document{}, errors.New("data file is empty or corrupt: left untouched, fix or remove it")
	}
	if doc.SchemaVersion != schemaVersion {
		return document{}, fmt.Errorf("data file has schema_version %d, this server reads %d: left untouched", doc.SchemaVersion, schemaVersion)
	}
	if doc.Projects == nil {
		doc.Projects = []Project{}
	}
	if doc.Tasks == nil {
		doc.Tasks = []Task{}
	}
	return doc, nil
}

// save replaces the Data file atomically: it writes a temporary file in the
// same directory, flushes it to disk, then renames it over the Data file.
func (s *jsonStore) save(doc document) (err error) {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("data file: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".tasks-*.tmp") // created 0600
	if err != nil {
		return fmt.Errorf("data file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("data file: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("data file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("data file: %w", err)
	}
	if err = os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("data file: %w", err)
	}
	return nil
}

// update takes the Lock, reads the document, applies fn and saves the result;
// nothing is written if fn fails. The document is read after the Lock is held,
// so that a write made by another process in the meantime is not overwritten.
func (s *jsonStore) update(fn func(*document) error) error {
	release, err := s.lock.acquire()
	if err != nil {
		return err
	}
	defer release()

	doc, err := s.load()
	if err != nil {
		return err
	}
	if err := fn(&doc); err != nil {
		return err
	}
	return s.save(doc)
}

// uniqueID draws IDs until taken reports one as free, giving up after idAttempts.
func (s *jsonStore) uniqueID(prefix string, taken func(string) bool) (string, error) {
	for range idAttempts {
		if id := s.newID(prefix); !taken(id) {
			return id, nil
		}
	}
	return "", fmt.Errorf("no free ID after %d attempts", idAttempts)
}

func (s *jsonStore) ListProjects() ([]Project, error) {
	doc, err := s.load()
	return doc.Projects, err
}

func (s *jsonStore) CreateProject(p Project) (Project, error) {
	err := s.update(func(doc *document) error {
		if len(doc.Projects) >= maxProjects {
			return fmt.Errorf("%w: at most %d projects", ErrLimit, maxProjects)
		}
		for _, existing := range doc.Projects {
			if strings.EqualFold(existing.Name, p.Name) {
				return fmt.Errorf("a project with this name already exists: %s", existing.ID)
			}
		}
		id, err := s.uniqueID("p_", func(id string) bool {
			for _, existing := range doc.Projects {
				if existing.ID == id {
					return true
				}
			}
			return false
		})
		if err != nil {
			return err
		}
		p.ID = id
		doc.Projects = append(doc.Projects, p)
		return nil
	})
	if err != nil {
		return Project{}, err
	}
	return p, nil
}

func (s *jsonStore) ListTasks() ([]Task, error) {
	doc, err := s.load()
	return doc.Tasks, err
}

// taskIndex returns the position of the Task with this ID, or ErrNotFound.
func (doc *document) taskIndex(id string) (int, error) {
	id = normalizeID(id)
	for i, task := range doc.Tasks {
		if task.ID == id {
			return i, nil
		}
	}
	return 0, ErrNotFound
}

// checkProject accepts an empty project (none) or the ID of an existing one.
func (doc *document) checkProject(id string) error {
	if id == "" {
		return nil
	}
	for _, p := range doc.Projects {
		if p.ID == id {
			return nil
		}
	}
	return ErrInvalidProject
}

func (s *jsonStore) GetTask(id string) (Task, error) {
	doc, err := s.load()
	if err != nil {
		return Task{}, err
	}
	i, err := doc.taskIndex(id)
	if err != nil {
		return Task{}, err
	}
	return doc.Tasks[i], nil
}

func (s *jsonStore) CreateTask(task Task) (Task, error) {
	err := s.update(func(doc *document) error {
		if len(doc.Tasks) >= s.maxTasks {
			return fmt.Errorf("%w: at most %d tasks", ErrLimit, s.maxTasks)
		}
		if err := doc.checkProject(task.Project); err != nil {
			return err
		}
		id, err := s.uniqueID("t_", func(id string) bool {
			_, err := doc.taskIndex(id)
			return err == nil
		})
		if err != nil {
			return err
		}
		task.ID = id
		doc.Tasks = append(doc.Tasks, task)
		return nil
	})
	if err != nil {
		return Task{}, err
	}
	return task, nil
}

var _ Store = (*jsonStore)(nil)
