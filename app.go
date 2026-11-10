package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/fryguy503/SpineGuard/internal/epub"
	"github.com/fryguy503/SpineGuard/internal/library"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const version = "1.2.0"

type App struct {
	ctx       context.Context
	service   *library.Service
	operation sync.Mutex
}

func NewApp() *App {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = ""
	} else {
		dir = filepath.Join(dir, "SpineGuard")
	}
	if portable := os.Getenv("SPINEGUARD_CONFIG_DIR"); portable != "" {
		dir = portable
	}
	return &App{ctx: context.Background(), service: library.New(dir)}
}

func (a *App) startup(ctx context.Context)  { a.ctx = ctx }
func (a *App) domReady(ctx context.Context) {}
func (a *App) shutdown(ctx context.Context) { a.service.Cancel() }
func (a *App) GetState() library.State      { return a.service.State(version) }
func (a *App) SaveSettings(settings library.Settings) (library.Settings, error) {
	return a.service.SaveSettings(settings)
}
func (a *App) ClearReports() error { return a.service.Clear() }

func (a *App) SelectFiles() ([]string, error) {
	return wruntime.OpenMultipleFilesDialog(a.ctx, wruntime.OpenDialogOptions{Title: "Choose EPUB books", Filters: []wruntime.FileFilter{{DisplayName: "EPUB books", Pattern: "*.epub;*.kepub"}}})
}
func (a *App) SelectFolder() (string, error) {
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: "Choose a folder of EPUB books"})
}
func (a *App) SelectEPUBCheck() (string, error) {
	return wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{Title: "Select epubcheck.jar (keep its lib folder alongside it)", Filters: []wruntime.FileFilter{{DisplayName: "EPUBCheck Java archive", Pattern: "*.jar"}}})
}

func (a *App) ScanPaths(paths []string) ([]library.Report, error) {
	if len(paths) == 0 {
		return []library.Report{}, nil
	}
	if !a.operation.TryLock() {
		return nil, errors.New("a scan or repair is already running")
	}
	defer a.operation.Unlock()
	wruntime.EventsEmit(a.ctx, "scan:state", map[string]bool{"running": true})
	defer wruntime.EventsEmit(a.ctx, "scan:state", map[string]bool{"running": false})
	reports, err := a.service.Scan(a.ctx, paths, func(p library.Progress) { wruntime.EventsEmit(a.ctx, "scan:progress", p) })
	if warning := a.GetState().Warning; warning != "" {
		wruntime.EventsEmit(a.ctx, "app:warning", warning)
	}
	return reports, err
}
func (a *App) CancelScan() { a.service.Cancel() }

func (a *App) RepairBook(input string) (library.RepairResult, error) {
	return a.repairBook(input, epub.DefaultRepairOptions())
}

func (a *App) PreviewRepair(input string, choices epub.RepairOptions) (library.RepairPlan, error) {
	if !a.operation.TryLock() {
		return library.RepairPlan{}, errors.New("a scan or repair is already running")
	}
	defer a.operation.Unlock()
	plan, err := a.service.PreviewRepair(a.ctx, input, choices)
	if warning := a.GetState().Warning; warning != "" {
		wruntime.EventsEmit(a.ctx, "app:warning", warning)
	}
	return plan, err
}

func (a *App) RepairBookWithOptions(input string, choices epub.RepairOptions) (library.RepairResult, error) {
	if choices.ExpectedSHA256 == "" {
		return library.RepairResult{}, errors.New("review repairs before saving a repaired copy")
	}
	return a.repairBook(input, choices)
}

func (a *App) repairBook(input string, choices epub.RepairOptions) (library.RepairResult, error) {
	if !a.operation.TryLock() {
		return library.RepairResult{}, errors.New("a scan or repair is already running")
	}
	defer a.operation.Unlock()
	output, err := wruntime.SaveFileDialog(a.ctx, wruntime.SaveDialogOptions{Title: "Save a repaired copy", DefaultDirectory: filepath.Dir(input), DefaultFilename: strings.TrimSuffix(filepath.Base(input), filepath.Ext(input)) + ".repaired.epub", Filters: []wruntime.FileFilter{{DisplayName: "EPUB book", Pattern: "*.epub"}}})
	if err != nil || output == "" {
		return library.RepairResult{}, err
	}
	if !strings.EqualFold(filepath.Ext(output), ".epub") {
		return library.RepairResult{}, errors.New("save the repaired copy with an .epub extension")
	}
	// Repairs never replace an existing file, even if the native dialog approved an overwrite.
	if _, err := os.Lstat(output); err == nil {
		return library.RepairResult{}, errors.New("that file already exists; choose a new filename to preserve both copies")
	} else if !os.IsNotExist(err) {
		return library.RepairResult{}, err
	}
	wruntime.EventsEmit(a.ctx, "scan:state", map[string]bool{"running": true})
	defer wruntime.EventsEmit(a.ctx, "scan:state", map[string]bool{"running": false})
	r, err := a.service.RepairWithOptions(a.ctx, input, output, choices)
	if warning := a.GetState().Warning; warning != "" {
		wruntime.EventsEmit(a.ctx, "app:warning", warning)
	}
	return r, err
}

func (a *App) ExportReports(format string) (string, error) {
	if format != "json" && format != "csv" {
		return "", errors.New("choose JSON or CSV")
	}
	reports := a.GetState().Reports
	if len(reports) == 0 {
		return "", errors.New("scan some books before exporting a report")
	}
	path, err := wruntime.SaveFileDialog(a.ctx, wruntime.SaveDialogOptions{Title: "Export SpineGuard reports", DefaultFilename: "SpineGuard-report." + format, Filters: []wruntime.FileFilter{{DisplayName: strings.ToUpper(format) + " report", Pattern: "*." + format}}})
	if err != nil || path == "" {
		return "", err
	}
	if !strings.EqualFold(filepath.Ext(path), "."+format) {
		return "", fmt.Errorf("save this report with a .%s extension", format)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".spineguard-export-*.tmp")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := library.Export(f, format, reports); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

func (a *App) RevealFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", filepath.Dir(path))
	case "darwin":
		cmd = exec.Command("open", "-R", path)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(path))
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
