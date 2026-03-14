package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nexus-chat/nexus/internal/config"
	"github.com/nexus-chat/nexus/internal/server"

	_ "github.com/mattn/go-sqlite3"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version":
			fmt.Printf("nexus %s\n", version)
			return
		case "serve":
			// continue below
		case "db":
			if len(os.Args) < 4 {
				fmt.Fprintln(os.Stderr, "Usage: nexus db <slug> <sql>")
				os.Exit(1)
			}
			runDB(os.Args[2], strings.Join(os.Args[3:], " "))
			return
		case "help", "-h", "--help":
			printUsage()
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
			printUsage()
			os.Exit(1)
		}
	}

	// Default action or "serve" command
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	if err := server.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

func runDB(slug, query string) {
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	var dbPath string
	if slug == "global" {
		dbPath = filepath.Join(dataDir, "nexus.db")
	} else {
		dbPath = filepath.Join(dataDir, "workspaces", slug, "workspace.db")
	}
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		fmt.Fprintf(os.Stderr, "open error: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	upper := strings.TrimSpace(strings.ToUpper(query))
	if strings.HasPrefix(upper, "SELECT") {
		rows, err := db.Query(query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "query error: %v\n", err)
			os.Exit(1)
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		fmt.Println(strings.Join(cols, "|"))
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rows.Next() {
			rows.Scan(ptrs...)
			parts := make([]string, len(cols))
			for i, v := range vals {
				if v.Valid {
					parts[i] = v.String
				}
			}
			fmt.Println(strings.Join(parts, "|"))
		}
	} else {
		res, err := db.Exec(query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "exec error: %v\n", err)
			os.Exit(1)
		}
		n, _ := res.RowsAffected()
		fmt.Printf("OK, %d rows affected\n", n)
	}
}

func printUsage() {
	fmt.Println(`Usage: nexus <command>

Commands:
  serve     Start the Nexus server (default)
  db        Run SQL against a workspace DB
  version   Print version information
  help      Show this help message`)
}
