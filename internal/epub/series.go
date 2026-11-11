package epub

import "strings"

// packageSeries uses only declared publication metadata. Collection metadata
// without an explicit series type is not interpreted as a book series.
func packageSeries(pkg packageDoc) (string, string) {
	indices := map[string][]string{}
	types := map[string][]string{}
	idCounts := map[string]int{}
	for _, meta := range pkg.Metadata.Meta {
		if meta.XMLName.Space != opfNS {
			continue
		}
		if meta.ID != "" {
			idCounts[meta.ID]++
		}
		if !strings.HasPrefix(meta.Refines, "#") || len(meta.Refines) < 2 {
			continue
		}
		id := strings.TrimPrefix(meta.Refines, "#")
		switch meta.Property {
		case "collection-type":
			types[id] = append(types[id], strings.TrimSpace(meta.Value))
		case "group-position":
			indices[id] = append(indices[id], strings.TrimSpace(meta.Value))
		}
	}
	series := map[string][]string{}
	for _, meta := range pkg.Metadata.Meta {
		if meta.XMLName.Space != opfNS || meta.Property != "belongs-to-collection" || meta.Refines != "" || meta.ID == "" || idCounts[meta.ID] != 1 {
			continue
		}
		kind, ok := oneMetadataValue(types[meta.ID])
		name := strings.TrimSpace(meta.Value)
		if !ok || kind != "series" || name == "" {
			continue
		}
		position, _ := oneMetadataValue(indices[meta.ID])
		series[name] = append(series[name], position)
	}
	if len(series) == 1 {
		for name, positions := range series {
			position, _ := oneMetadataValue(positions)
			return name, position
		}
	}
	if len(series) > 1 {
		// A single grouping must not silently choose one of several declared series.
		return "", ""
	}
	var names, positions []string
	for _, meta := range pkg.Metadata.Meta {
		if meta.XMLName.Space != opfNS || meta.Refines != "" {
			continue
		}
		switch meta.Name {
		case "calibre:series":
			names = append(names, strings.TrimSpace(meta.Content))
		case "calibre:series_index":
			positions = append(positions, strings.TrimSpace(meta.Content))
		}
	}
	name, ok := oneMetadataValue(names)
	if !ok {
		return "", ""
	}
	position, _ := oneMetadataValue(positions)
	return name, position
}

func oneMetadataValue(values []string) (string, bool) {
	if len(values) == 0 || values[0] == "" {
		return "", false
	}
	first := values[0]
	for _, value := range values[1:] {
		if value != first {
			return "", false
		}
	}
	return first, true
}
