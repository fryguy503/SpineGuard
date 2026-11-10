package epub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func sourceDigest(ctx context.Context, filename string) (string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<30 {
		return "", errors.New("repair requires a regular EPUB file no larger than 2 GiB")
	}
	h := sha256.New()
	n, err := io.Copy(h, contextReader{ctx, io.LimitReader(f, (2<<30)+1)})
	if err != nil {
		return "", err
	}
	if n > 2<<30 {
		return "", errors.New("EPUB changed or exceeded the repair size limit")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func repairBlockedReason(a *archive) string {
	if a.unsafe {
		return "Archive integrity or resource-limit checks failed; repair cannot recover corrupt payloads or safely process this archive."
	}
	if a.protected {
		return "Repairs are disabled for encrypted or digitally signed books to preserve protected resources."
	}
	if _, ok := a.files["META-INF/container.xml"]; !ok {
		return "Required container.xml is missing; its contents cannot be safely invented."
	}
	return ""
}

// Load declared text by media type, including resources without a familiar suffix.
func loadDeclaredRepairText(a *archive) {
	var container struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if decoder(a.text["META-INF/container.xml"]).Decode(&container) != nil {
		return
	}
	for _, root := range container.Rootfiles {
		if !safeName(root.FullPath) {
			continue
		}
		b, err := a.cacheText(root.FullPath)
		if err != nil {
			continue
		}
		var pkg packageDoc
		if decoder(b).Decode(&pkg) != nil {
			continue
		}
		for _, item := range pkg.Manifest {
			if item.MediaType != "application/xhtml+xml" && item.MediaType != "image/svg+xml" && item.MediaType != "application/x-dtbncx+xml" && item.MediaType != "text/css" {
				continue
			}
			name, _, external, err := resolveReference(root.FullPath, item.Href)
			if err == nil && !external {
				_, _ = a.cacheText(name)
			}
		}
	}
}

func repairPackageNames(text map[string][]byte) map[string]bool {
	names := map[string]bool{}
	var container struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if decoder(text["META-INF/container.xml"]).Decode(&container) == nil {
		for _, root := range container.Rootfiles {
			names[root.FullPath] = true
		}
	}
	return names
}

func collectRepairEdits(ctx context.Context, a *archive, choices RepairOptions) (map[string][]byte, []RepairAction, []Issue) {
	modified := map[string][]byte{}
	actions := []RepairAction{}
	notes := []Issue{}
	working := map[string][]byte{}
	packages := repairPackageNames(a.text)
	for name, b := range a.text {
		working[name] = b
	}
	editable := func(name string) bool {
		return !a.obfuscated || (!packages[name] && !strings.HasSuffix(strings.ToLower(name), ".opf") && name != "META-INF/encryption.xml")
	}
	for _, name := range sortedTextNames(working) {
		if ctx.Err() != nil {
			break
		}
		b := working[name]
		if name == "mimetype" || strings.HasSuffix(strings.ToLower(name), ".css") || !editable(name) {
			continue
		}
		if fixed, ok := sanitizeXML(b); ok {
			working[name] = fixed
			modified[name] = fixed
			actions = append(actions, RepairAction{Kind: "xml", File: name, Description: "Normalize malformed XML entities or invalid control characters.", Count: 1})
		}
	}
	files := map[string]bool{}
	for name, f := range a.files {
		if !f.FileInfo().IsDir() {
			files[name] = true
		}
	}
	linkModified, linkActions, linkNotes := planLinkDocuments(ctx, working, files, choices)
	for name, b := range linkModified {
		if editable(name) {
			working[name] = b
			modified[name] = b
		}
	}
	for _, action := range linkActions {
		if editable(action.File) {
			actions = append(actions, action)
		}
	}
	notes = append(notes, linkNotes...)
	cleanupModified, cleanupActions, cleanupNotes := planCleanupDocuments(ctx, working, choices)
	for name, b := range cleanupModified {
		if editable(name) {
			working[name] = b
			modified[name] = b
		}
	}
	for _, action := range cleanupActions {
		if editable(action.File) {
			actions = append(actions, action)
		}
	}
	notes = append(notes, cleanupNotes...)
	return modified, actions, notes
}

func issueRepairKey(i Issue) string { return i.Code + "\x00" + i.File + "\x00" + i.Detail }
func annotateRepairIssues(a *archive, report *Report) {
	if len(report.Issues) == 0 {
		return
	}
	packages := repairPackageNames(a.text)
	for i := range report.Issues {
		issue := &report.Issues[i]
		if issue.Repairable {
			issue.RepairKind = "xml"
			if strings.HasPrefix(issue.Code, "MIMETYPE_") || issue.Code == "ZIP_PREFIX" {
				issue.RepairKind = "packaging"
			}
		}
		if issue.RepairReason == "" {
			issue.RepairReason = defaultRepairReason(issue.Code)
		}
		if a.obfuscated && issue.Repairable && (packages[issue.File] || strings.HasSuffix(strings.ToLower(issue.File), ".opf") || issue.File == "META-INF/encryption.xml") {
			issue.Repairable = false
			issue.RepairReason = "Package and font-obfuscation metadata are preserved to protect the font key. Obtain a corrected source edition or review this metadata with its fonts."
		}
	}
	if reason := repairBlockedReason(a); reason != "" {
		for i := range report.Issues {
			report.Issues[i].Repairable = false
			report.Issues[i].RepairReason = reason
		}
		return
	}
	wanted := false
	for _, i := range report.Issues {
		if i.Code == "LINK_MISSING" || i.Code == "LINK_FRAGMENT" || i.Code == "CONTENT_SCRIPT" || i.Code == "CONTENT_DUPLICATE_ID" || i.Code == "CONTENT_EVENT_HANDLER" || i.Code == "CONTENT_JAVASCRIPT_URL" {
			wanted = true
			break
		}
	}
	if !wanted {
		return
	}
	_, _, notes := collectRepairEdits(a.ctx, a, DefaultRepairOptions())
	byExact := map[string]Issue{}
	byFile := map[string]Issue{}
	for _, note := range notes {
		if note.Detail != "" {
			byExact[issueRepairKey(note)] = note
		} else {
			byFile[note.Code+"\x00"+note.File] = note
		}
	}
	for i := range report.Issues {
		issue := &report.Issues[i]
		note, ok := byExact[issueRepairKey(*issue)]
		if !ok {
			note, ok = byFile[issue.Code+"\x00"+issue.File]
		}
		if ok {
			issue.Repairable = note.Repairable
			issue.RepairKind = note.RepairKind
			issue.RepairReason = note.RepairReason
			issue.OptionalRepair = note.OptionalRepair
		}
	}
}

func defaultRepairReason(code string) string {
	switch {
	case strings.HasPrefix(code, "MIMETYPE_") || code == "ZIP_PREFIX":
		return "EPUB identification and ZIP packaging can be normalized in a new copy."
	case code == "XML_INVALID":
		return "Only XML damage that can be normalized and strictly reparsed is repaired automatically."
	case code == "XML_DTD_CHECK_INCOMPLETE":
		return "Run EPUBCheck to assess DTD-dependent XML. Custom entities are preserved."
	case code == "METADATA_LANGUAGE":
		return "Set the book's correct language in a metadata editor; SpineGuard does not guess it."
	case code == "METADATA_IDENTIFIER":
		return "Restore the publication's identifier and package reference. A guessed identifier could break obfuscated fonts."
	case strings.HasPrefix(code, "NAV_") || strings.HasPrefix(code, "NCX_"):
		return "Navigation needs a verified table of contents; SpineGuard does not invent chapter destinations."
	case code == "CONTENT_DUPLICATE_ID":
		return "Duplicate identifiers can be renamed only when no link, CSS, script, or ID-reference dependency needs them."
	case code == "CONTENT_SCRIPT" || code == "CONTENT_EVENT_HANDLER" || code == "CONTENT_JAVASCRIPT_URL":
		return "Optional reader cleanup can remove scripting after review; interactive behavior may be lost."
	case code == "FONT_OBFUSCATION":
		return "Recognized font obfuscation is informational and is preserved."
	case code == "LINK_MISSING" || code == "LINK_FRAGMENT":
		return "A link is repaired only when its destination can be proven uniquely from the book."
	case strings.HasPrefix(code, "ZIP_"):
		return "Re-download a damaged archive; missing or corrupt compressed content cannot be recreated."
	default:
		return "No automatic transformation is implemented for this finding. Review it or obtain a corrected edition."
	}
}

func PlanRepair(ctx context.Context, input string, opts Options, choices RepairOptions) (plan RepairPlan, err error) {
	plan = RepairPlan{Path: input, Options: choices, Actions: []RepairAction{}, Unresolved: []Issue{}}
	if abs, e := filepath.Abs(input); e == nil {
		plan.Path = abs
	}
	before, err := sourceDigest(ctx, input)
	if err != nil {
		return plan, err
	}
	plan.SourceSHA256 = before
	if choices.ExpectedSHA256 != "" && !strings.EqualFold(choices.ExpectedSHA256, before) {
		return plan, errors.New("the EPUB changed after the repair review; review the current file again")
	}
	report, err := Scan(ctx, input, opts)
	if err != nil {
		return plan, err
	}
	plan.Report = report
	archiveReport := Report{Issues: []Issue{}}
	a, err := openArchive(ctx, input, &archiveReport)
	if err != nil {
		plan.BlockedReason = "The EPUB archive could not be opened: " + err.Error()
		plan.Unresolved = append(plan.Unresolved, report.Issues...)
		return plan, nil
	}
	defer a.z.Close()
	loadDeclaredRepairText(a)
	a.inspectProtection()
	if plan.BlockedReason = repairBlockedReason(a); plan.BlockedReason != "" {
		plan.Unresolved = append(plan.Unresolved, report.Issues...)
		return plan, nil
	}
	modified, actions, _ := collectRepairEdits(ctx, a, choices)
	if err := ctx.Err(); err != nil {
		return plan, err
	}
	packaging := false
	for _, issue := range report.Issues {
		if strings.HasPrefix(issue.Code, "MIMETYPE_") || issue.Code == "ZIP_PREFIX" {
			packaging = true
			break
		}
	}
	if packaging {
		actions = append([]RepairAction{{Kind: "packaging", Description: "Normalize EPUB ZIP packaging and exact mimetype identification.", Count: 1}}, actions...)
	} else if len(actions) > 0 {
		actions = append([]RepairAction{{Kind: "packaging", Description: "Write the selected edits into a new EPUB archive, preserving unchanged resource payloads.", Count: 1}}, actions...)
	}
	plan.Actions = actions
	if len(actions) > 0 {
		tmp, e := os.CreateTemp("", "spineguard-preview-*.epub")
		if e != nil {
			return plan, e
		}
		tmpName := tmp.Name()
		defer os.Remove(tmpName)
		if e = writeRepairArchive(ctx, a, tmp, modified); e != nil {
			tmp.Close()
			return plan, e
		}
		if e = tmp.Close(); e != nil {
			return plan, e
		}
		preview, e := Scan(ctx, tmpName, opts)
		if e != nil {
			return plan, fmt.Errorf("verify proposed repairs: %w", e)
		}
		for _, issue := range preview.Issues {
			if issue.Severity != "info" {
				plan.Unresolved = append(plan.Unresolved, issue)
			}
		}
	} else {
		for _, issue := range report.Issues {
			if issue.Severity != "info" {
				plan.Unresolved = append(plan.Unresolved, issue)
			}
		}
	}
	after, err := sourceDigest(ctx, input)
	if err != nil {
		return plan, err
	}
	if before != after {
		return plan, errors.New("the EPUB changed while preparing the repair review; review it again")
	}
	plan.Options.ExpectedSHA256 = before
	return plan, nil
}
