package binary

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/v3/cli/version"
)

const (
	// SDKVersionPrefix distinguishes SDK components from standalone installations.
	SDKVersionPrefix = "sdk-"
	// SDKManifestName records the products owned by an installed SDK.
	SDKManifestName         = "sdk.json"
	sdkFilePermissions      = 0o644
	tarantoolUnifiedGCMajor = 3
)

// ErrInvalidSDK identifies an unusable ownership record.
var ErrInvalidSDK = errors.New("invalid SDK installation")

type sdkManifest struct {
	Version  string   `json:"version"`
	Products []string `json:"products"`
}

// SDKProducts selects the supported SDK executables.
func SDKProducts() []Program {
	return []Program{ProgramEe, ProgramTcm, ProgramTtEe}
}

// SDKInstallation records component ownership independently of active selections.
type SDKInstallation struct {
	Version  string
	Products []Program
}

// Selections expands the recorded SDK products into ordinary version selections.
func (sdk SDKInstallation) Selections() []Selection {
	selections := make([]Selection, 0, len(sdk.Products))
	for _, product := range sdk.Products {
		selections = append(selections,
			Selection{Program: product, ID: SDKVersionPrefix + sdk.Version})
	}

	return selections
}

// SDKComponentVersion decodes a component's SDK identity without storing another
// copy of the bundle version alongside its CLI identifier.
func SDKComponentVersion(program Program, selected string) (string, bool) {
	if !program.CanBeSDKComponent() {
		return "", false
	}

	return strings.CutPrefix(selected, SDKVersionPrefix)
}

// BundleInstallationID separates standalone EE versions from SDK archive revisions.
// SDKs and other archive products retain the complete source version.
func BundleInstallationID(program Program, source version.Version) string {
	if program != ProgramEe {
		return source.Str
	}

	build := source.BuildName
	if source.Major >= tarantoolUnifiedGCMajor {
		parts := make([]string, 0)

		for part := range strings.SplitSeq(build, "-") {
			if part != "gc64" && part != "nogc64" {
				parts = append(parts, part)
			}
		}

		build = strings.Join(parts, "-")
	}

	selected := fmt.Sprintf("%d.%d.%d", source.Major, source.Minor, source.Patch)
	if release := source.Release.String(); release != "" {
		selected += "-" + release
	}

	if build != "" {
		selected = build + "-" + selected
	}

	return selected
}

// MatchInstalledVersion keeps SDK component requests in their own namespace.
func MatchInstalledVersion(program Program, requested string, installed []string) (string, error) {
	candidates := installed
	fromSDK := false

	if program == ProgramSDK {
		requested = strings.TrimPrefix(requested, SDKVersionPrefix)
	} else {
		requested, fromSDK = strings.CutPrefix(requested, SDKVersionPrefix)
		candidates = make([]string, 0, len(installed))

		for _, candidate := range installed {
			plain, isSDK := strings.CutPrefix(candidate, SDKVersionPrefix)
			if isSDK == fromSDK {
				candidates = append(candidates, plain)
			}
		}
	}

	if program == ProgramEe && !fromSDK {
		if slices.Contains(candidates, requested) {
			return requested, nil
		}

		parsed, err := version.Parse(requested)
		if err == nil {
			selected := BundleInstallationID(program, parsed)
			if slices.Contains(candidates, selected) {
				return selected, nil
			}
		}
	}

	selected, err := version.MatchInstalledVersion(requested, candidates)
	if err != nil {
		return "", err
	}

	if fromSDK {
		selected = SDKVersionPrefix + selected
	}

	return selected, nil
}

// ValidateSDKManifest checks ownership identities against the selected directory
// before they are used as paths or executables. It does not inspect product files.
func ValidateSDKManifest(sdk SDKInstallation, selected string) error {
	if sdk.Version == "" || sdk.Version == "." || sdk.Version == ".." ||
		strings.ContainsAny(sdk.Version, "/\\\x00") || len(sdk.Products) == 0 {
		return fmt.Errorf("%w: %q", ErrInvalidSDK, sdk.Version)
	}

	for _, product := range sdk.Products {
		if !product.CanBeSDKComponent() {
			return fmt.Errorf("%w: invalid product %s", ErrInvalidSDK, product)
		}
	}

	if sdk.Version != selected {
		return fmt.Errorf("%w: version %q does not match %q",
			ErrInvalidSDK, sdk.Version, selected)
	}

	return nil
}

// ReadSDK reads and decodes an SDK ownership record. Call ValidateSDKManifest
// before using its identities, and ValidateSDKFiles when complete files are required.
func ReadSDK(root, selected string) (SDKInstallation, error) {
	var sdk SDKInstallation

	path := filepath.Join(InstallationPath(root, ProgramSDK, selected), SDKManifestName)

	data, err := os.ReadFile(path)
	if err != nil {
		return sdk, fmt.Errorf("failed to read SDK installation: %w", err)
	}

	var manifest sdkManifest

	err = json.Unmarshal(data, &manifest)
	if err != nil {
		return sdk, fmt.Errorf("%w: failed to parse ownership record: %w", ErrInvalidSDK, err)
	}

	sdk.Version = manifest.Version
	for _, name := range manifest.Products {
		product, err := ParseProgram(name)
		if err != nil {
			return sdk, fmt.Errorf("%w: %w", ErrInvalidSDK, err)
		}

		sdk.Products = append(sdk.Products, product)
	}

	return sdk, nil
}

// WriteSDK saves ownership in a prepared SDK directory.
func WriteSDK(directory string, sdk SDKInstallation) error {
	err := ValidateSDKManifest(sdk, sdk.Version)
	if err != nil {
		return err
	}

	manifest := sdkManifest{Version: sdk.Version, Products: make([]string, 0, len(sdk.Products))}
	for _, product := range sdk.Products {
		manifest.Products = append(manifest.Products, product.String())
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode SDK installation: %w", err)
	}

	// The ownership record is readable in the system scope as well.
	err = os.WriteFile(filepath.Join(directory, SDKManifestName),
		append(data, '\n'), sdkFilePermissions)
	if err != nil {
		return fmt.Errorf("failed to write SDK installation: %w", err)
	}

	return nil
}

// ValidateSDKFiles checks the files of an SDK whose manifest has already passed
// ValidateSDKManifest. It does not repeat manifest validation.
func ValidateSDKFiles(root string, sdk SDKInstallation) error {
	for _, selection := range sdk.Selections() {
		err := ValidateInstallation(root, selection)
		if err != nil {
			return err
		}
	}

	return nil
}

// ActivateSDK selects SDK products and headers together. The caller must
// first call ValidateSDKManifest and ValidateSDKFiles.
func ActivateSDK(root string, sdk SDKInstallation) error {
	err := ActivateSelections(root, sdk.Selections())
	if err != nil {
		return err
	}

	return nil
}

// SDKActive checks the active links of an SDK whose manifest and files have
// already been validated by the caller.
func SDKActive(root string, sdk SDKInstallation) bool {
	for _, selection := range sdk.Selections() {
		links := installationLinks(root, selection)

		for _, link := range links {
			active, err := ResolveSymlink(link.path)
			if err != nil {
				return false
			}

			target, err := ResolveSymlink(link.target)
			if err != nil {
				return false
			}

			if active != target {
				return false
			}
		}
	}

	return true
}
