package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fryguy503/SpineGuard/internal/epub"
	"github.com/fryguy503/SpineGuard/internal/reader"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const readerWindowFlag = "--spineguard-reader-window"
const maxReaderMessage = 4 << 20

type ReaderLaunch struct {
	Path       string `json:"path"`
	IssueIndex int    `json:"issueIndex"`
	Theme      string `json:"theme"`
}

// The private pipe carries an authorized snapshot. It is never exposed as a
// listening socket or a book-controlled command line.
type readerWindowMessage struct {
	Launch    ReaderLaunch `json:"launch"`
	ConfigDir string       `json:"configDir"`
	Title     string       `json:"title"`
	Issues    []epub.Issue `json:"issues"`
}

type readerWindowProcess struct {
	command *exec.Cmd
	input   io.WriteCloser
}

type readerWindows struct {
	mu      sync.Mutex
	dir     string
	windows map[string]*readerWindowProcess
}

func newReaderWindows(dir string) *readerWindows {
	if dir != "" {
		if absolute, err := filepath.Abs(dir); err == nil {
			dir = absolute
		}
	}
	return &readerWindows{dir: dir, windows: map[string]*readerWindowProcess{}}
}

func canonicalReaderPath(filename string) (string, error) {
	if filename == "" || strings.ContainsRune(filename, 0) {
		return "", errors.New("a book filename is required")
	}
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	if runtime.GOOS == "windows" {
		absolute = strings.ToLower(absolute)
	}
	return absolute, nil
}

func isReaderWindow(args []string) bool {
	return len(args) == 1 && args[0] == readerWindowFlag
}

func encodeReaderMessage(message readerWindowMessage) ([]byte, error) {
	data, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	if len(data)+1 > maxReaderMessage {
		return nil, errors.New("this book's inspection details are too large for a reader window")
	}
	return append(data, '\n'), nil
}

// OpenReaderWindow starts one independent native reader for each book. Further
// requests for that book reuse its pipe, focus the window, and jump to a finding.
func (a *App) OpenReaderWindow(filename string, issueIndex int) error {
	requested, err := canonicalReaderPath(filename)
	if err != nil {
		return err
	}
	state := a.GetState()
	message := readerWindowMessage{Launch: ReaderLaunch{IssueIndex: issueIndex, Theme: state.Settings.Theme}}
	found := false
	for _, report := range state.Reports {
		known, err := canonicalReaderPath(report.Path)
		if err == nil && known == requested {
			message.Launch.Path, err = filepath.Abs(report.Path)
			if err != nil {
				return err
			}
			message.Title = report.Title
			message.Issues = report.Issues
			found = true
			break
		}
	}
	if !found {
		return errors.New("scan this book before opening it in the reader")
	}
	if issueIndex < -1 || issueIndex >= len(message.Issues) {
		return errors.New("this finding is no longer available; reopen the book's inspector")
	}
	if a.readerWindows == nil {
		return errors.New("the reader window service is unavailable")
	}
	return a.readerWindows.open(requested, message)
}

func (w *readerWindows) open(key string, message readerWindowMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	message.ConfigDir = w.dir
	data, err := encodeReaderMessage(message)
	if err != nil {
		return err
	}
	if existing := w.windows[key]; existing != nil {
		if _, err := existing.input.Write(data); err == nil {
			return nil
		}
		_ = existing.input.Close()
		delete(w.windows, key)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(executable, readerWindowFlag)
	configureReaderProcess(command)
	input, err := command.StdinPipe()
	if err != nil {
		return err
	}
	if err = command.Start(); err != nil {
		input.Close()
		return fmt.Errorf("the reader window could not be opened: %w", err)
	}
	process := &readerWindowProcess{command: command, input: input}
	w.windows[key] = process
	go func() {
		_ = command.Wait()
		_ = input.Close()
		w.mu.Lock()
		if w.windows[key] == process {
			delete(w.windows, key)
		}
		w.mu.Unlock()
	}()
	if _, err = input.Write(data); err != nil {
		delete(w.windows, key)
		input.Close()
		_ = command.Process.Kill() // Only the just-created child, never a user app.
		return fmt.Errorf("the reader window could not receive this book: %w", err)
	}
	return nil
}

func readReaderMessage(input *bufio.Reader) (readerWindowMessage, error) {
	data := []byte{}
	for {
		part, err := input.ReadSlice('\n')
		if len(data)+len(part) > maxReaderMessage {
			return readerWindowMessage{}, errors.New("reader window message exceeds its size limit")
		}
		data = append(data, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return readerWindowMessage{}, err
		}
		break
	}
	var message readerWindowMessage
	if err := json.Unmarshal(data, &message); err != nil {
		return message, errors.New("reader window received invalid book details")
	}
	if !filepath.IsAbs(message.Launch.Path) || strings.ContainsRune(message.Launch.Path, 0) || message.Launch.IssueIndex < -1 || message.Launch.IssueIndex >= len(message.Issues) {
		return message, errors.New("reader window received an invalid book location")
	}
	if message.ConfigDir != "" && !filepath.IsAbs(message.ConfigDir) {
		return message, errors.New("reader configuration directory must be absolute")
	}
	if message.Launch.Theme != "dark" && message.Launch.Theme != "light" && message.Launch.Theme != "system" {
		message.Launch.Theme = "dark"
	}
	return message, nil
}

// ReaderWindow is the only object bound inside a detached reader. It has no
// library mutation, repair, export, shell, or arbitrary-file-opening methods.
type ReaderWindow struct {
	mu           sync.Mutex
	ctx          context.Context
	message      readerWindowMessage
	input        *bufio.Reader
	reader       *reader.Manager
	closeAllowed atomic.Bool
	closeOnce    sync.Once
}

func newReaderWindow(input io.Reader) (*ReaderWindow, error) {
	buffer := bufio.NewReaderSize(input, 64<<10)
	message, err := readReaderMessage(buffer)
	if err != nil {
		return nil, fmt.Errorf("the reader window could not receive its book: %w", err)
	}
	return &ReaderWindow{ctx: context.Background(), message: message, input: buffer, reader: reader.New(message.ConfigDir)}, nil
}

func (w *ReaderWindow) startup(ctx context.Context) { w.ctx = ctx }
func (w *ReaderWindow) shutdown(context.Context)    { w.reader.Shutdown() }
func (w *ReaderWindow) title() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	title := strings.TrimSpace(w.message.Title)
	if len(title) > 240 {
		title = string([]rune(title)[:min(len([]rune(title)), 120)])
	}
	if title == "" {
		title = filepath.Base(w.message.Launch.Path)
	}
	return title + " — SpineGuard Reader"
}

func (w *ReaderWindow) webviewDataPath() string {
	// A shared reader profile preserves reading preferences across readers while
	// keeping library webview state separate. Reader records use their own file.
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.message.ConfigDir == "" {
		return ""
	}
	return filepath.Join(w.message.ConfigDir, "reader-webview")
}

func readerWindowSize(screenWidth, screenHeight int) (width, height, minWidth, minHeight int) {
	width, height, minWidth, minHeight = 760, 980, 560, 680
	if screenWidth > 0 {
		width = min(width, max(320, screenWidth-64))
		minWidth = min(minWidth, width)
	}
	if screenHeight > 0 {
		height = min(height, max(360, screenHeight-96))
		minHeight = min(minHeight, height)
	}
	width = min(width, max(320, height*4/5))
	minWidth = min(minWidth, width)
	return
}

func (w *ReaderWindow) domReady(ctx context.Context) {
	if screens, err := wruntime.ScreenGetAll(ctx); err == nil {
		for _, screen := range screens {
			if screen.IsCurrent || screen.IsPrimary {
				width, height, minWidth, minHeight := readerWindowSize(screen.Size.Width, screen.Size.Height)
				wruntime.WindowSetMinSize(ctx, minWidth, minHeight)
				wruntime.WindowSetSize(ctx, width, height)
				wruntime.WindowCenter(ctx)
				break
			}
		}
	}
	wruntime.WindowShow(ctx)
	go w.readControls()
}

func (w *ReaderWindow) readControls() {
	for {
		message, err := readReaderMessage(w.input)
		if err != nil {
			return // Closing the library closes the pipe, not this reader window.
		}
		w.mu.Lock()
		current, _ := canonicalReaderPath(w.message.Launch.Path)
		next, _ := canonicalReaderPath(message.Launch.Path)
		if current != next || message.ConfigDir != w.message.ConfigDir {
			w.mu.Unlock()
			continue
		}
		w.message = message
		w.mu.Unlock()
		wruntime.WindowSetTitle(w.ctx, w.title())
		wruntime.WindowUnminimise(w.ctx)
		wruntime.WindowShow(w.ctx)
		wruntime.EventsEmit(w.ctx, "reader:open", message.Launch)
	}
}

func (w *ReaderWindow) beforeClose(ctx context.Context) bool {
	if w.closeAllowed.Load() {
		return false
	}
	w.closeOnce.Do(func() {
		wruntime.EventsEmit(ctx, "reader:before-close")
		go func() {
			time.Sleep(6 * time.Second)
			w.CloseWindow() // The window must still close if its renderer failed.
		}()
	})
	return true
}

func (w *ReaderWindow) GetReaderLaunch() ReaderLaunch {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.message.Launch
}

func (w *ReaderWindow) OpenReader(filename string) (reader.Book, error) {
	requested, err := canonicalReaderPath(filename)
	if err != nil {
		return reader.Book{}, err
	}
	w.mu.Lock()
	known, _ := canonicalReaderPath(w.message.Launch.Path)
	path := w.message.Launch.Path
	issues := append([]epub.Issue{}, w.message.Issues...)
	w.mu.Unlock()
	if requested != known {
		return reader.Book{}, errors.New("this reader window can open only its selected book")
	}
	return w.reader.Open(w.ctx, path, issues)
}

func (w *ReaderWindow) CancelReaderOpen()                 { w.reader.CancelOpen() }
func (w *ReaderWindow) CancelReaderSearch(session string) { w.reader.CancelSearch(session) }
func (w *ReaderWindow) CloseReader(session string) error  { return w.reader.Close(session) }
func (w *ReaderWindow) SaveReaderState(session string, state reader.ReadingState) error {
	return w.reader.SaveState(session, state)
}
func (w *ReaderWindow) SearchReader(session, query string) ([]reader.SearchResult, error) {
	return w.reader.Search(w.ctx, session, query, 100)
}
func (w *ReaderWindow) LocateReaderIssue(session string, index int) (reader.Location, error) {
	return w.reader.LocateIssue(w.ctx, session, index)
}
func (w *ReaderWindow) ReadReaderSource(session, id string, line, column int) (reader.Source, error) {
	return w.reader.Source(w.ctx, session, id, line, column, 200)
}
func (w *ReaderWindow) RequestClose() { wruntime.Quit(w.ctx) }
func (w *ReaderWindow) CloseWindow() {
	if !w.closeAllowed.Swap(true) {
		wruntime.Quit(w.ctx)
	}
}
func (w *ReaderWindow) serveHTTP(response http.ResponseWriter, request *http.Request) {
	w.reader.ServeHTTP(response, request)
}
