package epub

// RepairOptions selects edits on a separate copy. Reader cleanup is opt-in.
type RepairOptions struct {
	FixLinks                    bool   `json:"fixLinks"`
	FixDuplicateIDs             bool   `json:"fixDuplicateIds"`
	RemoveScripts               bool   `json:"removeScripts"`
	FixMetadata                 bool   `json:"fixMetadata"`
	Language                    string `json:"language,omitempty"`
	GenerateIdentifier          bool   `json:"generateIdentifier"`
	FixDocumentMarkup           bool   `json:"fixDocumentMarkup"`
	NormalizeEntities           bool   `json:"normalizeEntities"`
	ReplaceMissingSceneDividers bool   `json:"replaceMissingSceneDividers"`
	ExpectedSHA256              string `json:"expectedSha256,omitempty"`
}

func DefaultRepairOptions() RepairOptions {
	return RepairOptions{FixLinks: true, FixDuplicateIDs: true, FixMetadata: true, FixDocumentMarkup: true}
}

type RepairAction struct {
	Kind          string `json:"kind"`
	File          string `json:"file,omitempty"`
	Description   string `json:"description"`
	Count         int    `json:"count"`
	RequiresOptIn bool   `json:"requiresOptIn"`
}

type RepairPlan struct {
	Path          string            `json:"path"`
	SourceSHA256  string            `json:"sourceSha256"`
	Actions       []RepairAction    `json:"actions"`
	Unresolved    []Issue           `json:"unresolved"`
	BlockedReason string            `json:"blockedReason,omitempty"`
	Options       RepairOptions     `json:"options"`
	Report        Report            `json:"report"`
	Suggestions   RepairSuggestions `json:"suggestions"`
}

type RepairSuggestions struct {
	Language        string   `json:"language,omitempty"`
	LanguageSources []string `json:"languageSources,omitempty"`
}
