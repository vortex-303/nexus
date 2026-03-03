package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "github.com/mattn/go-sqlite3"

	"github.com/nexus-chat/nexus/internal/db/migrations"
)

// Global holds the global nexus.db connection.
type Global struct {
	DB *sql.DB
}

// OpenGlobal opens (or creates) the global nexus.db in dataDir.
func OpenGlobal(dataDir string) (*Global, error) {
	path := filepath.Join(dataDir, "nexus.db")
	db, err := openSQLite(path)
	if err != nil {
		return nil, fmt.Errorf("opening global db: %w", err)
	}
	if err := migrations.RunGlobal(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating global db: %w", err)
	}
	return &Global{DB: db}, nil
}

func (g *Global) Close() error {
	return g.DB.Close()
}

// WorkspaceDB holds a per-workspace database connection.
type WorkspaceDB struct {
	DB *sql.DB
}

// WorkspaceManager manages per-workspace database connections.
type WorkspaceManager struct {
	dataDir string
	mu      sync.Mutex
	dbs     map[string]*WorkspaceDB
}

func NewWorkspaceManager(dataDir string) *WorkspaceManager {
	return &WorkspaceManager{
		dataDir: dataDir,
		dbs:     make(map[string]*WorkspaceDB),
	}
}

// Open returns (or creates) the workspace database for the given slug.
func (wm *WorkspaceManager) Open(slug string) (*WorkspaceDB, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if wdb, ok := wm.dbs[slug]; ok {
		return wdb, nil
	}

	wsDir := filepath.Join(wm.dataDir, "workspaces", slug)
	if err := os.MkdirAll(wsDir, 0700); err != nil {
		return nil, fmt.Errorf("creating workspace dir: %w", err)
	}

	// Create brain directory for definition files
	brainDir := filepath.Join(wsDir, "brain")
	if err := os.MkdirAll(brainDir, 0700); err != nil {
		return nil, fmt.Errorf("creating brain dir: %w", err)
	}
	skillsDir := filepath.Join(brainDir, "skills")
	if err := os.MkdirAll(skillsDir, 0700); err != nil {
		return nil, fmt.Errorf("creating skills dir: %w", err)
	}

	// Create blobs directory for file storage
	blobsDir := filepath.Join(wsDir, "blobs")
	if err := os.MkdirAll(blobsDir, 0700); err != nil {
		return nil, fmt.Errorf("creating blobs dir: %w", err)
	}

	path := filepath.Join(wsDir, "workspace.db")
	db, err := openSQLite(path)
	if err != nil {
		return nil, fmt.Errorf("opening workspace db %s: %w", slug, err)
	}
	if err := migrations.RunWorkspace(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating workspace db %s: %w", slug, err)
	}

	wdb := &WorkspaceDB{DB: db}
	wm.dbs[slug] = wdb
	return wdb, nil
}

// BlobsDir returns the blobs directory path for a workspace.
func (wm *WorkspaceManager) BlobsDir(slug string) string {
	return filepath.Join(wm.dataDir, "workspaces", slug, "blobs")
}

// CloseAll closes all workspace database connections.
func (wm *WorkspaceManager) CloseAll() {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	for _, wdb := range wm.dbs {
		wdb.DB.Close()
	}
}

func openSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	// Verify connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
