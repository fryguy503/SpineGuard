package main

import (
	"errors"

	"github.com/fryguy503/SpineGuard/internal/library"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) PreviewBulkRepair(paths []string) (library.BulkRepairPreview, error) {
	if !a.operation.TryLock() {
		return library.BulkRepairPreview{}, errors.New("a scan or repair is already running")
	}
	defer a.operation.Unlock()
	preview, err := a.service.PreviewBulkRepair(a.ctx, paths, func(p library.BulkProgress) {
		wruntime.EventsEmit(a.ctx, "bulk:progress", p)
	})
	if warning := a.GetState().Warning; warning != "" {
		wruntime.EventsEmit(a.ctx, "app:warning", warning)
	}
	return preview, err
}

func (a *App) RepairBooks(items []library.BulkRepairRequest) (library.BulkRepairResult, error) {
	if !a.operation.TryLock() {
		return library.BulkRepairResult{}, errors.New("a scan or repair is already running")
	}
	defer a.operation.Unlock()
	validated, err := library.ValidateBulkRepairRequests(items)
	if err != nil {
		return library.BulkRepairResult{}, err
	}
	directory, err := wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: "Choose a folder for the repaired copies"})
	if err != nil || directory == "" {
		return library.BulkRepairResult{Items: []library.BulkRepairItem{}, Cancelled: directory == "" && err == nil}, err
	}
	result, err := a.service.RepairBooks(a.ctx, directory, validated, func(p library.BulkProgress) {
		wruntime.EventsEmit(a.ctx, "bulk:progress", p)
	})
	if warning := a.GetState().Warning; warning != "" {
		wruntime.EventsEmit(a.ctx, "app:warning", warning)
	}
	return result, err
}
