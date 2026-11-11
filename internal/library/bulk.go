package library

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fryguy503/SpineGuard/internal/epub"
)

// Bulk review uses the same conservative defaults for every book. Choices such
// as a language or script removal belong in the individual book's review.
type BulkPreviewItem struct {
	Path  string     `json:"path"`
	Title string     `json:"title"`
	Plan  RepairPlan `json:"plan"`
	Error string     `json:"error,omitempty"`
}

type BulkRepairPreview struct {
	Items     []BulkPreviewItem `json:"items"`
	Cancelled bool              `json:"cancelled"`
}

type BulkRepairRequest struct {
	Path    string             `json:"path"`
	Options epub.RepairOptions `json:"options"`
}

type BulkRepairItem struct {
	Path       string   `json:"path"`
	OutputPath string   `json:"outputPath,omitempty"`
	Report     *Report  `json:"report,omitempty"`
	Changes    []string `json:"changes,omitempty"`
	Error      string   `json:"error,omitempty"`
	Status     string   `json:"status"`
}

type BulkRepairResult struct {
	Directory string           `json:"directory"`
	Items     []BulkRepairItem `json:"items"`
	Cancelled bool             `json:"cancelled"`
}

// Index is the completed-book count, matching scan progress. A start event has
// no item; its corresponding completion event includes the review or result.
type BulkProgress struct {
	Phase       string           `json:"phase"`
	Path        string           `json:"path"`
	Index       int              `json:"index"`
	Total       int              `json:"total"`
	PreviewItem *BulkPreviewItem `json:"previewItem,omitempty"`
	Item        *BulkRepairItem  `json:"item,omitempty"`
	Report      *Report          `json:"report,omitempty"`
}

func bulkPaths(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 || len(paths) > MaxBooks {
		return nil, fmt.Errorf("choose between 1 and %d books for bulk repair", MaxBooks)
	}
	files := make([]string, len(paths))
	seen := map[string]bool{}
	infos := make([]os.FileInfo, 0, len(paths))
	for i, input := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.TrimSpace(input) == "" || strings.ContainsRune(input, 0) || !supported(input) {
			return nil, errors.New("bulk repair requires EPUB book paths")
		}
		absolute, err := filepath.Abs(input)
		if err != nil {
			return nil, fmt.Errorf("invalid book path: %w", err)
		}
		if runtime.GOOS == "windows" && strings.Contains(filepath.Base(absolute), ":") {
			return nil, errors.New("choose ordinary EPUB files, not alternate data streams")
		}
		// A moved, unreadable, oversized, or no-longer-regular source is a
		// per-book failure. Resolve identity when possible without making one
		// inaccessible book prevent review or repair of the rest of the batch.
		resolved, resolveErr := filepath.EvalSymlinks(absolute)
		if resolveErr != nil {
			resolved = absolute
		}
		info, statErr := os.Stat(resolved)
		key := filepath.Clean(resolved)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		duplicate := seen[key]
		if statErr == nil {
			for _, previous := range infos {
				duplicate = duplicate || os.SameFile(previous, info)
			}
		}
		if duplicate {
			return nil, fmt.Errorf("%s was selected more than once", filepath.Base(input))
		}
		seen[key] = true
		if statErr == nil {
			infos = append(infos, info)
		}
		files[i] = absolute
	}
	return files, nil
}

// ValidateBulkRepairRequests is also called by the native bridge before opening
// its single destination dialog. Nothing is created while validating a request.
func ValidateBulkRepairRequests(items []BulkRepairRequest) ([]BulkRepairRequest, error) {
	return validateBulkRepairRequests(context.Background(), items)
}

func validateBulkRepairRequests(ctx context.Context, items []BulkRepairRequest) ([]BulkRepairRequest, error) {
	if len(items) == 0 || len(items) > MaxBooks {
		return nil, fmt.Errorf("choose between 1 and %d books for bulk repair", MaxBooks)
	}
	paths := make([]string, len(items))
	for i, item := range items {
		choices := item.Options
		sha := choices.ExpectedSHA256
		choices.ExpectedSHA256 = ""
		if choices != epub.DefaultRepairOptions() {
			return nil, errors.New("bulk repair uses recommended fixes only; review language and optional changes one book at a time")
		}
		if decoded, err := hex.DecodeString(sha); err != nil || len(decoded) != 32 {
			return nil, errors.New("review every selected book before saving bulk repairs")
		}
		paths[i] = item.Path
	}
	files, err := bulkPaths(ctx, paths)
	if err != nil {
		return nil, err
	}
	validated := make([]BulkRepairRequest, len(items))
	for i, item := range items {
		validated[i] = item
		validated[i].Path = files[i]
		validated[i].Options.ExpectedSHA256 = strings.ToLower(item.Options.ExpectedSHA256)
	}
	return validated, nil
}

func (s *Service) PreviewBulkRepair(parent context.Context, paths []string, progress func(BulkProgress)) (BulkRepairPreview, error) {
	result := BulkRepairPreview{Items: []BulkPreviewItem{}}
	ctx, settings, err := s.begin(parent)
	if err != nil {
		return result, err
	}
	defer s.finish()
	files, err := bulkPaths(ctx, paths)
	if errors.Is(err, context.Canceled) {
		result.Cancelled = true
		return result, nil
	}
	if err != nil {
		return result, err
	}
	for i, path := range files {
		if ctx.Err() != nil {
			result.Cancelled = true
			break
		}
		if progress != nil {
			progress(BulkProgress{Phase: "preview", Path: path, Index: i, Total: len(files)})
		}
		item := BulkPreviewItem{Path: path, Title: filepath.Base(path)}
		plan, previewErr := epub.PlanRepair(ctx, path, epub.Options{WordsPerPage: settings.WordsPerPage, ReadingWPM: settings.ReadingWPM}, epub.DefaultRepairOptions())
		if ctx.Err() != nil {
			result.Cancelled = true
			break
		}
		if previewErr != nil {
			item.Error = previewErr.Error()
		} else {
			report := Report{Report: plan.Report}
			if settings.RunEPUBCheck {
				report = CheckFile(ctx, path, settings)
			}
			if ctx.Err() != nil {
				result.Cancelled = true
				break
			}
			s.store(report)
			item.Plan = RepairPlan{RepairPlan: plan, Report: report}
			if report.Title != "" {
				item.Title = report.Title
			}
		}
		result.Items = append(result.Items, item)
		if progress != nil {
			p := BulkProgress{Phase: "preview", Path: path, Index: i + 1, Total: len(files), PreviewItem: &item}
			if previewErr == nil {
				p.Report = &item.Plan.Report
			}
			progress(p)
		}
	}
	result.Cancelled = result.Cancelled || ctx.Err() != nil
	return result, nil
}

func nextBulkOutput(directory, input string) (string, error) {
	stem := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	for n := 1; n <= 10000; n++ {
		name := stem + ".repaired.epub"
		if n > 1 {
			name = fmt.Sprintf("%s.repaired (%d).epub", stem, n)
		}
		output := filepath.Join(directory, name)
		if _, err := os.Lstat(output); errors.Is(err, os.ErrNotExist) {
			return output, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("too many copies with this filename already exist in the destination")
}

func (s *Service) RepairBooks(parent context.Context, directory string, requests []BulkRepairRequest, progress func(BulkProgress)) (BulkRepairResult, error) {
	result := BulkRepairResult{Items: []BulkRepairItem{}}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return result, err
	}
	if info, statErr := os.Stat(directory); statErr != nil || !info.IsDir() {
		return result, errors.New("choose an existing destination folder for the repaired copies")
	}
	result.Directory = directory
	ctx, settings, err := s.begin(parent)
	if err != nil {
		return result, err
	}
	defer s.finish()
	items, err := validateBulkRepairRequests(ctx, requests)
	if errors.Is(err, context.Canceled) {
		result.Cancelled = true
		for _, pending := range requests {
			result.Items = append(result.Items, BulkRepairItem{Path: pending.Path, Status: "cancelled"})
		}
		return result, nil
	}
	if err != nil {
		return result, err
	}
	for i, request := range items {
		if ctx.Err() != nil {
			result.Cancelled = true
			for _, pending := range items[i:] {
				result.Items = append(result.Items, BulkRepairItem{Path: pending.Path, Status: "cancelled"})
			}
			break
		}
		if progress != nil {
			progress(BulkProgress{Phase: "repair", Path: request.Path, Index: i, Total: len(items)})
		}
		item := s.repairBulkItem(ctx, directory, request, settings)
		if item.Status == "cancelled" {
			result.Cancelled = true
		}
		result.Items = append(result.Items, item)
		if progress != nil {
			progress(BulkProgress{Phase: "repair", Path: request.Path, Index: i + 1, Total: len(items), Item: &item, Report: item.Report})
		}
	}
	result.Cancelled = result.Cancelled || ctx.Err() != nil
	return result, nil
}

func (s *Service) repairBulkItem(ctx context.Context, directory string, request BulkRepairRequest, settings Settings) BulkRepairItem {
	item := BulkRepairItem{Path: request.Path, Status: "failed"}
	fail := func(err error) BulkRepairItem {
		if ctx.Err() != nil {
			item.Status = "cancelled"
		} else {
			item.Error = err.Error()
		}
		return item
	}
	options := epub.Options{WordsPerPage: settings.WordsPerPage, ReadingWPM: settings.ReadingWPM}
	plan, err := epub.PlanRepair(ctx, request.Path, options, request.Options)
	if err != nil {
		return fail(err)
	}
	if plan.BlockedReason != "" {
		return fail(errors.New(plan.BlockedReason))
	}
	if len(plan.Actions) == 0 {
		return fail(errors.New("no recommended automatic fixes are available; review this book individually"))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	output, err := nextBulkOutput(directory, request.Path)
	if err != nil {
		return fail(err)
	}
	repaired, err := epub.RepairWithOptions(ctx, request.Path, output, options, request.Options)
	if err != nil {
		return fail(err)
	}
	report := Report{Report: repaired.Report}
	if settings.RunEPUBCheck {
		// The copy has already been verified and published atomically. A cancelled
		// optional validator must not make a completed copy disappear from results.
		checked := CheckFile(ctx, output, settings)
		if ctx.Err() == nil {
			report = checked
		}
	}
	s.store(report)
	item.Status, item.OutputPath, item.Changes, item.Report = "saved", repaired.OutputPath, repaired.Changes, &report
	return item
}
