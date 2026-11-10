// Package library coordinates local scans, preferences and report history.
package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fryguy503/SpineGuard/internal/epub"
	"github.com/fryguy503/SpineGuard/internal/epubcheck"
)

const MaxBooks = 2000

type Settings struct {
	WordsPerPage int    `json:"wordsPerPage"`
	ReadingWPM   int    `json:"readingWPM"`
	Recursive    bool   `json:"recursive"`
	EPUBCheckJar string `json:"epubcheckJar"`
	JavaPath     string `json:"javaPath"`
	RunEPUBCheck bool   `json:"runEpubcheck"`
	Theme        string `json:"theme"`
}

func DefaultSettings() Settings {
	return Settings{WordsPerPage: 250, ReadingWPM: 250, Recursive: true, JavaPath: "java", Theme: "dark"}
}

func (s Settings) Validate() error {
	if s.WordsPerPage < 100 || s.WordsPerPage > 1000 {
		return errors.New("words per page must be between 100 and 1000")
	}
	if s.ReadingWPM < 80 || s.ReadingWPM > 1000 {
		return errors.New("reading speed must be between 80 and 1000 words per minute")
	}
	if s.Theme != "dark" && s.Theme != "light" && s.Theme != "system" {
		return errors.New("choose a valid appearance")
	}
	if s.RunEPUBCheck && strings.TrimSpace(s.EPUBCheckJar) == "" {
		return errors.New("select epubcheck.jar before enabling standards validation")
	}
	if strings.TrimSpace(s.JavaPath) == "" {
		return errors.New("enter java or the path to a Java executable")
	}
	return nil
}

type Validation struct {
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

type Report struct {
	epub.Report
	EPUBCheck *Validation `json:"epubcheck,omitempty"`
}

type RepairResult struct {
	OutputPath string   `json:"outputPath"`
	Changes    []string `json:"changes"`
	Report     Report   `json:"report"`
}

type State struct {
	Version  string   `json:"version"`
	Settings Settings `json:"settings"`
	Reports  []Report `json:"reports"`
	Warning  string   `json:"warning,omitempty"`
}

type Progress struct {
	Path   string  `json:"path"`
	Index  int     `json:"index"`
	Total  int     `json:"total"`
	Report *Report `json:"report,omitempty"`
}

type Service struct {
	mu       sync.Mutex
	dir      string
	settings Settings
	reports  []Report
	warning  string
	busy     bool
	cancel   context.CancelFunc
}

func New(dir string) *Service {
	s := &Service{dir: dir, settings: DefaultSettings(), reports: []Report{}}
	if dir == "" {
		s.warning = "Report history is unavailable because the settings directory could not be found."
		return s
	}
	if b, err := readLimited(filepath.Join(dir, "settings.json"), 1<<20); err == nil {
		settings := DefaultSettings()
		if json.Unmarshal(b, &settings) == nil && settings.Validate() == nil {
			s.settings = settings
		} else {
			s.warning = "Saved preferences could not be read. Defaults are in use."
		}
	} else if !os.IsNotExist(err) {
		s.warning = "Saved preferences could not be read: " + err.Error()
	}
	if b, err := readLimited(filepath.Join(dir, "library.json"), 64<<20); err == nil {
		var reports []Report
		if json.Unmarshal(b, &reports) == nil && len(reports) <= MaxBooks {
			s.reports = reports
		} else {
			s.warning = "Saved report history could not be read. Your EPUB files are unaffected."
		}
	} else if !os.IsNotExist(err) {
		s.warning = "Saved report history could not be read: " + err.Error()
	}
	return s
}

func readLimited(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("saved path is not a regular file")
	}
	if info.Size() > max {
		return nil, errors.New("saved file is too large")
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errors.New("saved file is too large")
	}
	return b, nil
}

func (s *Service) State(version string) State {
	s.mu.Lock()
	defer s.mu.Unlock()
	reports:=append([]Report{},s.reports...)
	for i:=range reports{updateEstimates(&reports[i],s.settings)}
	return State{Version: version, Settings: s.settings, Reports: reports, Warning: s.warning}
}

func (s *Service) SaveSettings(settings Settings) (Settings, error) {
	settings.JavaPath = strings.TrimSpace(settings.JavaPath)
	settings.EPUBCheckJar = strings.TrimSpace(settings.EPUBCheckJar)
	if err := settings.Validate(); err != nil {
		return settings, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.write("settings.json", settings); err != nil {
		return settings, fmt.Errorf("save preferences: %w", err)
	}
	s.settings = settings
	for i := range s.reports {
		updateEstimates(&s.reports[i], settings)
	}
	return settings, nil
}

func (s *Service) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return errors.New("wait for the current operation to finish before clearing reports")
	}
	if err := s.write("library.json", []Report{}); err != nil {
		return err
	}
	s.reports = []Report{}
	return nil
}

func (s *Service) write(name string, value any) error {
	if s.dir == "" {
		return errors.New("the settings directory is unavailable")
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".spineguard-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(s.dir, name))
}

func (s *Service) store(report Report) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.reports {
		if samePath(r.Path, report.Path) {
			s.reports = append(s.reports[:i], s.reports[i+1:]...)
			break
		}
	}
	s.reports = append([]Report{report}, s.reports...)
	if len(s.reports) > MaxBooks {
		s.reports = s.reports[:MaxBooks]
	}
}

func (s *Service) persist() {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Cover thumbnails remain in memory only, keeping history small and private.
	reports := make([]Report, len(s.reports))
	copy(reports, s.reports)
	for i := range reports {
		reports[i].CoverData = ""
	}
	if err := s.write("library.json", reports); err != nil {
		s.warning = "Reports could not be saved: " + err.Error()
	}
}

func (s *Service) begin(parent context.Context) (context.Context, Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return nil, Settings{}, errors.New("a scan or repair is already running")
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.busy = true
	return ctx, s.settings, nil
}

func (s *Service) finish() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.cancel = nil
	s.busy = false
	s.mu.Unlock()
	s.persist()
}
func (s *Service) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func updateEstimates(report *Report, settings Settings) {
	if report.Stats.Words < 0 {
		return
	}
	report.Stats.EstimatedPages = (report.Stats.Words + settings.WordsPerPage - 1) / settings.WordsPerPage
	report.Stats.ReadingMinutes = (report.Stats.Words + settings.ReadingWPM - 1) / settings.ReadingWPM
}

func Discover(ctx context.Context, paths []string, recursive bool) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	add := func(path string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !supported(path) {
			return nil
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return err
		}
		if !seen[resolved] {
			seen[resolved] = true
			files = append(files, absolute)
		}
		if len(files) > MaxBooks {
			return fmt.Errorf("choose at most %d EPUBs per batch", MaxBooks)
		}
		return nil
	}
	for _, input := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := os.Stat(input)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", input, err)
		}
		if !info.IsDir() {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("%s is not a regular file", input)
			}
			if !supported(input) {
				return nil, fmt.Errorf("%s is not an EPUB file", filepath.Base(input))
			}
			if err := add(input); err != nil {
				return nil, err
			}
			continue
		}
		err = filepath.WalkDir(input, func(path string, d os.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if path != input && (!recursive || strings.HasPrefix(d.Name(), ".")) {
					return filepath.SkipDir
				}
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if d.Type().IsRegular() {
				return add(path)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scan folder %s: %w", input, err)
		}
	}
	if len(files) == 0 {
		return nil, errors.New("no EPUB files were found in the selected location")
	}
	sort.Strings(files)
	return files, nil
}

func supported(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".epub" || ext == ".kepub"
}

// CheckFile is shared by the desktop app and command-line utility.
func CheckFile(ctx context.Context, path string, settings Settings) Report {
	r, err := epub.Scan(ctx, path, epub.Options{WordsPerPage: settings.WordsPerPage, ReadingWPM: settings.ReadingWPM})
	if err != nil && ctx.Err() == nil {
		if r.Path == "" {
			r.Path = path
			r.Filename = filepath.Base(path)
			r.Title = r.Filename
			r.ScannedAt = time.Now().UTC()
		}
		r.Issues = append(r.Issues, epub.Issue{Code: "SCAN_FAILED", Severity: "error", Title: "Book could not be completely scanned", Detail: err.Error()})
		epub.Recount(&r)
	}
	report := Report{Report: r}
	if settings.RunEPUBCheck && ctx.Err() == nil && r.Status != "unreadable" {
		result, err := epubcheck.Check(ctx, path, settings.EPUBCheckJar, settings.JavaPath)
		report.EPUBCheck = &Validation{Status: result.Status, Version: result.Version, Detail: result.Detail}
		report.Issues = append(report.Issues, result.Issues...)
		if err != nil && ctx.Err() == nil {
			report.EPUBCheck.Status = "unavailable"
			report.EPUBCheck.Detail = err.Error()
			report.Issues = append(report.Issues, epub.Issue{Code: "EPUBCHECK_UNAVAILABLE", Severity: "warning", Title: "Standards validation did not complete", Detail: err.Error()})
		}
		epub.Recount(&report.Report)
	}
	return report
}

func (s *Service) Scan(parent context.Context, paths []string, progress func(Progress)) ([]Report, error) {
	ctx, settings, err := s.begin(parent)
	if err != nil {
		return nil, err
	}
	defer s.finish()
	files, err := Discover(ctx, paths, settings.Recursive)
	if errors.Is(err, context.Canceled) {
		return []Report{}, nil
	}
	if err != nil {
		return nil, err
	}
	reports := make([]Report, 0, len(files))
	for i, path := range files {
		if ctx.Err() != nil {
			break
		}
		if progress != nil {
			progress(Progress{Path: path, Index: i, Total: len(files)})
		}
		r := CheckFile(ctx, path, settings)
		if ctx.Err() != nil {
			break
		}
		s.store(r)
		reports = append(reports, r)
		if progress != nil {
			progress(Progress{Path: path, Index: i + 1, Total: len(files), Report: &r})
		}
	}
	return reports, nil
}

func (s *Service) Repair(parent context.Context, input, output string) (RepairResult, error) {
	ctx, settings, err := s.begin(parent)
	if err != nil {
		return RepairResult{}, err
	}
	defer s.finish()
	r, err := epub.Repair(ctx, input, output, epub.Options{WordsPerPage: settings.WordsPerPage, ReadingWPM: settings.ReadingWPM})
	if err != nil {
		return RepairResult{}, err
	}
	report := Report{Report: r.Report}
	if settings.RunEPUBCheck {
		report = CheckFile(ctx, output, settings)
	}
	s.store(report)
	return RepairResult{OutputPath: r.OutputPath, Changes: r.Changes, Report: report}, nil
}
