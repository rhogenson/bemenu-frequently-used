package main

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"io/fs"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// For build-time overriding
var bemenu = "bemenu"

func rumenuPath() ([]string, error) {
	var out []string
	for _, d := range strings.Split(os.Getenv("PATH"), ":") {
		files, _ := os.ReadDir(d)
		for _, f := range files {
			out = append(out, f.Name())
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no files")
	}
	return out, nil
}

func openDB(ctx context.Context, path string) (*sql.DB, error) {
	_, err := os.Stat(path)
	needInit := false
	if errors.Is(err, fs.ErrNotExist) {
		needInit = true
	} else if err != nil {
		return nil, fmt.Errorf("find DB file: %s", err)
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("open DB: %s", err)
	}
	if needInit {
		if _, err := db.ExecContext(ctx, `
			CREATE TABLE RecentlyUsed (
				Entry TEXT NOT NULL PRIMARY KEY,
				Count INTEGER NOT NULL
			) STRICT`,
		); err != nil {
			return nil, fmt.Errorf("init DB: %s", err)
		}
	}
	return db, nil
}

func readFreq(ctx context.Context, db *sql.DB) (map[string]int, error) {
	rows, err := db.QueryContext(ctx, "SELECT Entry, Count FROM RecentlyUsed")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	freq := make(map[string]int)
	for rows.Next() {
		var (
			entry string
			count int
		)
		if err := rows.Scan(&entry, &count); err != nil {
			return nil, err
		}
		freq[entry] = count
	}
	return freq, rows.Err()
}

func run(ctx context.Context) error {
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		dataDir = os.Getenv("HOME") + "/.local/share"
	}

	_, err := os.Stat(dataDir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return fmt.Errorf("make data dir: %s", err)
		}
	} else if err != nil {
		return fmt.Errorf("find data dir: %s", err)
	}

	db, err := openDB(ctx, dataDir+"/rumenu.sqlite")
	if err != nil {
		return err
	}
	defer db.Close()

	freq, err := readFreq(ctx, db)
	if err != nil {
		return fmt.Errorf("read recently used counts: %s", err)
	}

	progs, err := rumenuPath()
	if err != nil {
		return err
	}
	slices.SortFunc(progs, func(x, y string) int {
		if n := cmp.Compare(freq[x], freq[y]); n != 0 {
			return -n
		}
		return strings.Compare(x, y)
	})

	bemenu := exec.CommandContext(ctx, bemenu)
	bemenu.Stdin = strings.NewReader(strings.Join(progs, "\n") + "\n")
	choiceBytes, err := bemenu.Output()
	if err != nil {
		return fmt.Errorf("bemenu: %w", err)
	}
	choice := strings.TrimSuffix(string(choiceBytes), "\n")
	if choice == "" {
		return nil
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO RecentlyUsed (Entry, Count) VALUES (?, 1)
		ON CONFLICT DO UPDATE SET Count = Count + 1`,
		choice,
	); err != nil {
		return fmt.Errorf("update DB: %s", err)
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	sh := exec.CommandContext(ctx, shell)
	sh.Stdin = strings.NewReader(choice)
	if err := sh.Run(); err != nil {
		return fmt.Errorf("start program %q: %s", choice, err)
	}
	return nil
}

func main() {
	err := run(context.Background())
	var bemenuErr *exec.ExitError
	if errors.As(err, &bemenuErr) {
		os.Exit(bemenuErr.ExitCode())
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", err)
		os.Exit(1)
	}
}
