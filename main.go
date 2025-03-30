package main

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// For build-time overriding
var bemenu = "bemenu"

var (
	dataDir        = findDataDir()
	countsFileName = dataDir + "/counts"
)

func findDataDir() string {
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		dataDir = os.Getenv("HOME") + "/.local/share"
	}
	dataDir += "/rumenu"
	return dataDir
}

func rumenuPath() ([]string, error) {
	wg := new(sync.WaitGroup)
	dirs := strings.Split(os.Getenv("PATH"), ":")
	dirContents := make([][]string, len(dirs))
	for i, d := range dirs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			files, _ := os.ReadDir(d)
			names := make([]string, len(files))
			for j, f := range files {
				names[j] = f.Name()
			}
			dirContents[i] = names
		}()
	}
	wg.Wait()
	out := slices.Concat(dirContents...)
	if len(out) == 0 {
		return nil, errors.New("no files")
	}
	return out, nil
}

func readFreq() (map[string]int, error) {
	countsFile, err := os.Open(countsFileName)
	if err != nil {
		return nil, fmt.Errorf("read counts: %s", err)
	}
	defer countsFile.Close()
	counts := make(map[string]int)
	lineNum := 0
	scanner := bufio.NewScanner(countsFile)
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		i := strings.LastIndex(line, "\t")
		if i < 0 {
			return counts, fmt.Errorf("error parsing counts file %q line %d", countsFileName, lineNum)
		}
		name, countStr := line[:i], line[i+1:]
		count, err := strconv.Atoi(countStr)
		if err != nil {
			return counts, fmt.Errorf("error parsing counts file %q line %d", countsFileName, lineNum)
		}
		counts[name] = count
	}
	return counts, scanner.Err()
}

func writeFreq(freq map[string]int) error {
	tempFile, err := os.CreateTemp(dataDir, "")
	if err != nil {
		return fmt.Errorf("write counts: %s", err)
	}
	keys := make([]string, 0, len(freq))
	for k := range freq {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(x, y string) int {
		if n := cmp.Compare(freq[y], freq[x]); n != 0 {
			return n
		}
		return strings.Compare(x, y)
	})
	w := bufio.NewWriter(tempFile)
	for _, k := range keys {
		if _, err := fmt.Fprintf(w, "%s\t%d\n", k, freq[k]); err != nil {
			tempFile.Close()
			os.Remove(tempFile.Name())
			return fmt.Errorf("write counts: %s", err)
		}
	}
	if err := w.Flush(); err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		return fmt.Errorf("write counts: %s", err)
	}
	if err := tempFile.Close(); err != nil {
		os.Remove(tempFile.Name())
		return fmt.Errorf("write counts: %s", err)
	}
	if err := os.Rename(tempFile.Name(), countsFileName); err != nil {
		os.Remove(tempFile.Name())
		return fmt.Errorf("write counts: %s", err)
	}
	return nil
}

func run(ctx context.Context) error {
	wg := new(sync.WaitGroup)

	var freq map[string]int
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		if freq, err = readFreq(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %s\n", err)
		}
	}()

	var progs []string
	var err error
	wg.Add(1)
	go func() {
		defer wg.Done()
		progs, err = rumenuPath()
	}()

	wg.Wait()
	if err != nil {
		return err
	}

	compareProgs := func(x, y string) int {
		if n := cmp.Compare(freq[y], freq[x]); n != 0 {
			return n
		}
		return strings.Compare(x, y)
	}
	slices.SortFunc(progs, compareProgs)

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

	var progErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		sh := exec.CommandContext(ctx, shell)
		sh.Stdin = strings.NewReader(choice + "\n")
		if err := sh.Run(); err != nil {
			progErr = fmt.Errorf("%s: %w", choice, err)
		}
	}()

	var writeFreqErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		if writeFreqErr = os.MkdirAll(dataDir, 0755); writeFreqErr != nil {
			return
		}

		if _, ok := slices.BinarySearchFunc(progs, choice, compareProgs); !ok {
			return
		}
		if freq == nil {
			freq = make(map[string]int)
		}
		freq[choice]++
		writeFreqErr = writeFreq(freq)
	}()

	wg.Wait()
	if progErr != nil {
		return progErr
	}
	return writeFreqErr
}

func main() {
	err := run(context.Background())
	var bemenuErr *exec.ExitError
	if errors.As(err, &bemenuErr) {
		os.Exit(bemenuErr.ExitCode())
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", err)
		os.Exit(255)
	}
}
