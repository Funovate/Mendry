package preparation

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Toolchain is a platform-approved build environment. Project configuration
// can select a compatible entry, but can never provide an image reference.
type Toolchain struct {
	ID          string
	Runtime     string
	Version     string
	Profile     string
	ImageDigest string
	Default     bool
}

// ToolchainCatalog is the platform-owned allowlist used by repository detection.
type ToolchainCatalog struct {
	entries []Toolchain
}

func NewToolchainCatalog(entries []Toolchain) (ToolchainCatalog, error) {
	seen := make(map[string]struct{}, len(entries)*2)
	defaults := make(map[string]bool)
	copyEntries := append([]Toolchain(nil), entries...)
	for i := range copyEntries {
		entry := &copyEntries[i]
		entry.ID = strings.TrimSpace(entry.ID)
		entry.Runtime = strings.TrimSpace(entry.Runtime)
		entry.Version = strings.TrimSpace(entry.Version)
		entry.Profile = strings.TrimSpace(entry.Profile)
		entry.ImageDigest = strings.TrimSpace(entry.ImageDigest)
		if entry.Profile == "" {
			entry.Profile = "default"
		}
		selectionKey := entry.Runtime + "\x00" + entry.Version + "\x00" + entry.Profile
		if entry.ID == "" || (entry.Runtime != "go" && entry.Runtime != "node") || entry.Version == "" ||
			!imageBaseAllowed(entry.ImageDigest, entry.Runtime) {
			return ToolchainCatalog{}, fmt.Errorf("platform toolchain entry is invalid")
		}
		if _, exists := seen[entry.ID]; exists {
			return ToolchainCatalog{}, fmt.Errorf("platform toolchain ID is duplicated")
		}
		if _, exists := seen[selectionKey]; exists {
			return ToolchainCatalog{}, fmt.Errorf("platform toolchain selection is duplicated")
		}
		defaultKey := entry.Runtime + "\x00" + entry.Profile
		if entry.Default && defaults[defaultKey] {
			return ToolchainCatalog{}, fmt.Errorf("platform toolchain default is duplicated")
		}
		seen[entry.ID], seen[selectionKey] = struct{}{}, struct{}{}
		if entry.Default {
			defaults[defaultKey] = true
		}
	}
	sort.Slice(copyEntries, func(i, j int) bool { return copyEntries[i].ID < copyEntries[j].ID })
	return ToolchainCatalog{entries: copyEntries}, nil
}

func legacyToolchainCatalog(goImage, nodeImage string) (ToolchainCatalog, error) {
	entries := []Toolchain{}
	if strings.TrimSpace(goImage) != "" {
		entries = append(entries, Toolchain{ID: "go-1.23-default", Runtime: "go", Version: "1.23", Profile: "default", ImageDigest: goImage, Default: true})
	}
	if strings.TrimSpace(nodeImage) != "" {
		entries = append(entries, Toolchain{ID: "node-22-default", Runtime: "node", Version: "22", Profile: "default", ImageDigest: nodeImage, Default: true})
	}
	return NewToolchainCatalog(entries)
}

func (c ToolchainCatalog) resolve(runtime, requestedVersion, profile string) (Toolchain, bool) {
	if profile == "" {
		profile = "default"
	}
	if requestedVersion != "" {
		for _, entry := range c.entries {
			if entry.Runtime == runtime && entry.Profile == profile && versionCompatible(runtime, requestedVersion, entry.Version) {
				return entry, true
			}
		}
		return Toolchain{}, false
	}
	for _, entry := range c.entries {
		if entry.Runtime == runtime && entry.Profile == profile && entry.Default {
			return entry, true
		}
	}
	return Toolchain{}, false
}

var (
	nodeMajorPattern      = regexp.MustCompile(`(?:^|[^0-9])(\d{1,3})(?:\.[0-9xX*]+)?`)
	nodeComparatorPattern = regexp.MustCompile(`(>=|>|<=|<)\s*(\d{1,3})`)
)

func versionCompatible(runtime, requested, available string) bool {
	requested = strings.TrimSpace(requested)
	switch runtime {
	case "go":
		requestedParts := strings.Split(strings.TrimSuffix(requested, ".0"), ".")
		availableParts := strings.Split(strings.TrimSuffix(available, ".0"), ".")
		if len(requestedParts) != 2 || len(availableParts) != 2 || requestedParts[0] != availableParts[0] {
			return false
		}
		requestedMinor, requestedErr := strconv.Atoi(requestedParts[1])
		availableMinor, availableErr := strconv.Atoi(availableParts[1])
		return requestedErr == nil && availableErr == nil && availableMinor >= requestedMinor
	case "node":
		availableMajor, err := strconv.Atoi(strings.SplitN(available, ".", 2)[0])
		if err != nil {
			return false
		}
		comparators := nodeComparatorPattern.FindAllStringSubmatch(requested, -1)
		if len(comparators) > 0 {
			for _, comparator := range comparators {
				bound, _ := strconv.Atoi(comparator[2])
				satisfied := map[string]bool{
					">=": availableMajor >= bound, ">": availableMajor > bound,
					"<=": availableMajor <= bound, "<": availableMajor < bound,
				}[comparator[1]]
				if !satisfied {
					return false
				}
			}
			return true
		}
		match := nodeMajorPattern.FindStringSubmatch(requested)
		if len(match) != 2 {
			return false
		}
		major, _ := strconv.Atoi(match[1])
		return major == availableMajor
	default:
		return false
	}
}
