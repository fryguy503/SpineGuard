package reader

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIndependentReadersMergeSavedBooksAndRefreshResume(t *testing.T) {
	dir := t.TempDir()
	one, two := New(dir), New(dir)
	defer one.Shutdown()
	defer two.Shutdown()
	first, err := one.Open(context.Background(), writeReaderFixture(t, readerFixture()), nil)
	if err != nil {
		t.Fatal(err)
	}
	secondPath := writeReaderFixture(t, readerFixture())
	second, err := two.Open(context.Background(), secondPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		var pending sync.WaitGroup
		pending.Add(2)
		errors := make(chan error, 2)
		go func() {
			defer pending.Done()
			errors <- one.SaveState(first.SessionID, ReadingState{Location: fmt.Sprintf("one-%d", i)})
		}()
		go func() {
			defer pending.Done()
			errors <- two.SaveState(second.SessionID, ReadingState{Location: fmt.Sprintf("two-%d", i)})
		}()
		pending.Wait()
		close(errors)
		for err := range errors {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	// This manager was created before the other process-like manager saved. Its
	// Open must pick up the newest disk records rather than its construction cache.
	secondInOne, err := one.Open(context.Background(), secondPath, nil)
	if err != nil || secondInOne.State.Location != "two-7" {
		t.Fatalf("fresh resume: %+v %v", secondInOne.State, err)
	}
	loaded, err := readStates(dir)
	if err != nil || len(loaded) != 2 || loaded[first.BookKey].State.Location != "one-7" || loaded[second.BookKey].State.Location != "two-7" {
		t.Fatalf("merged books: %+v %v", loaded, err)
	}
}

func TestReaderStateProcessLockHelper(t *testing.T) {
	dir := os.Getenv("SPINEGUARD_TEST_READER_STATE_LOCK")
	if dir == "" {
		t.Skip("subprocess helper")
	}
	unlock, err := lockState(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	fmt.Println("locked")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestReaderStateLockWaitsForAnotherProcessAndHonorsCancellation(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestReaderStateProcessLockHelper$")
	command.Env = append(os.Environ(), "SPINEGUARD_TEST_READER_STATE_LOCK="+dir)
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close(); _ = command.Process.Kill(); _ = command.Wait() }()
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		t.Fatalf("helper lock: %q %v", line, err)
	}
	wait, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	if unlock, err := lockState(wait, dir); !errors.Is(err, context.DeadlineExceeded) {
		if unlock != nil {
			unlock()
		}
		t.Fatalf("other process did not own lock: %v", err)
	}
	if err = input.Close(); err != nil {
		t.Fatal(err)
	}
	if err = command.Wait(); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockState(context.Background(), dir)
	if err != nil {
		t.Fatalf("lock not released on process exit: %v", err)
	}
	unlock()
	canceled, stopCanceled := context.WithCancel(context.Background())
	stopCanceled()
	if unlock, err := lockState(canceled, dir); !errors.Is(err, context.Canceled) {
		if unlock != nil {
			unlock()
		}
		t.Fatalf("canceled lock wait: %v", err)
	}
}

func TestReaderPreservesUnreadableExistingStateFile(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "reader-state.json")
	if err := os.WriteFile(filename, []byte("incomplete original state"), 0600); err != nil {
		t.Fatal(err)
	}
	m := New(dir)
	defer m.Shutdown()
	book, err := m.Open(context.Background(), writeReaderFixture(t, readerFixture()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.SaveState(book.SessionID, ReadingState{Location: "new"}); err == nil {
		t.Fatal("unreadable state was overwritten")
	}
	data, err := os.ReadFile(filename)
	if err != nil || string(data) != "incomplete original state" {
		t.Fatal("original unreadable state was changed")
	}
}
