package cmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"aor/afl/db"
	"aor/afl/model"

	"github.com/google/uuid"
)

// Capture routes capture subcommands.
func Capture(d *db.DB, args []string) error {
	if len(args) == 0 {
		return captureUsage()
	}

	switch args[0] {
	case "upload":
		return captureUpload(d, args[1:])
	case "batch":
		return captureBatch(d, args[1:])
	case "status":
		return captureStatus(d, args[1:])
	case "get":
		return captureGet(d, args[1:])
	default:
		return captureUsage()
	}
}

func captureUpload(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("capture upload", flag.ContinueOnError)
	pathName := fs.String("path", "", "Path name (defaults to happy path)")
	source := fs.String("source", model.SourceManual, "Capture source: playwright, xcodebuildmcp, droidmind, manual")
	workspace := fs.String("workspace", "", "Workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagsWithValue := map[string]bool{"path": true, "source": true, "workspace": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 4 {
		return fmt.Errorf("usage: afl capture upload <FLOW-ID> <step-order> <platform> <image-path> [--path <path-name>] [--source <tool>] [--workspace <ws>] [--json]")
	}

	flowID := positional[0]
	stepOrder, err := strconv.Atoi(positional[1])
	if err != nil {
		return fmt.Errorf("invalid step-order %q: must be an integer", positional[1])
	}
	platform := positional[2]
	imagePath := positional[3]

	if !model.IsValidPlatform(platform) {
		return fmt.Errorf("invalid platform %q: must be one of %s", platform, strings.Join(model.ValidPlatforms, ", "))
	}
	if !model.IsValidSource(*source) {
		return fmt.Errorf("invalid source %q: must be one of playwright, xcodebuildmcp, droidmind, manual", *source)
	}

	ws := resolveOrDetectWorkspace(d, *workspace)

	flow, path, step, err := resolveFlowPathStep(d, ws, flowID, *pathName, stepOrder)
	if err != nil {
		return err
	}

	screenshot, err := uploadScreenshot(d, flow, step, platform, imagePath, *source)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(screenshot)
	}

	fmt.Printf("uploaded: step %d %q [%s] %s (%s)\n", step.SortOrder, step.Name, platform, path.Name, db.FormatBytes(screenshot.SizeBytes))
	return nil
}

func captureBatch(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("capture batch", flag.ContinueOnError)
	pathName := fs.String("path", "", "Path name (defaults to happy path)")
	source := fs.String("source", model.SourceManual, "Capture source: playwright, xcodebuildmcp, droidmind, manual")
	workspace := fs.String("workspace", "", "Workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagsWithValue := map[string]bool{"path": true, "source": true, "workspace": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 3 {
		return fmt.Errorf("usage: afl capture batch <FLOW-ID> <platform> <dir> [--path <path-name>] [--source <tool>] [--workspace <ws>] [--json]")
	}

	flowID := positional[0]
	platform := positional[1]
	dir := positional[2]

	if !model.IsValidPlatform(platform) {
		return fmt.Errorf("invalid platform %q: must be one of %s", platform, strings.Join(model.ValidPlatforms, ", "))
	}
	if !model.IsValidSource(*source) {
		return fmt.Errorf("invalid source %q: must be one of playwright, xcodebuildmcp, droidmind, manual", *source)
	}

	// Verify directory exists.
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("directory %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", dir)
	}

	ws := resolveOrDetectWorkspace(d, *workspace)

	flow, err := d.ResolveFlow(ws, flowID)
	if err != nil {
		return err
	}
	if flow == nil {
		return fmt.Errorf("flow %q not found in workspace %s", flowID, ws)
	}

	path, err := resolvePathForFlow(d, flow.ID, *pathName)
	if err != nil {
		return err
	}

	// Find numbered image files in the directory.
	type numberedFile struct {
		order    int
		path     string
		filename string
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read directory %q: %w", dir, err)
	}

	var files []numberedFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := filepath.Ext(name)
		if !isImageExtension(ext) {
			continue
		}
		base := strings.TrimSuffix(name, ext)
		n, err := strconv.Atoi(base)
		if err != nil {
			continue
		}
		files = append(files, numberedFile{
			order:    n,
			path:     filepath.Join(dir, name),
			filename: name,
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].order < files[j].order
	})

	if len(files) == 0 {
		return fmt.Errorf("no numbered image files found in %q (expected 1.png, 2.png, etc.)", dir)
	}

	var uploaded []model.Screenshot
	for _, f := range files {
		step, err := d.GetStepByOrder(path.ID, f.order)
		if err != nil {
			return fmt.Errorf("looking up step %d: %w", f.order, err)
		}
		if step == nil {
			return fmt.Errorf("no step with order %d in path %q", f.order, path.Name)
		}

		screenshot, err := uploadScreenshot(d, flow, step, platform, f.path, *source)
		if err != nil {
			return fmt.Errorf("uploading %s: %w", f.filename, err)
		}
		uploaded = append(uploaded, *screenshot)
	}

	if *jsonOut {
		return outputJSON(uploaded)
	}

	fmt.Printf("uploaded %d screenshots for %s [%s] path %q:\n", len(uploaded), flow.FlowID, platform, path.Name)
	for _, s := range uploaded {
		fmt.Printf("  step %s: %s (%s)\n", s.StepID, s.Filename, db.FormatBytes(s.SizeBytes))
	}
	return nil
}

func captureStatus(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("capture status", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagsWithValue := map[string]bool{"workspace": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 1 {
		return fmt.Errorf("usage: afl capture status <FLOW-ID> [--workspace <ws>] [--json]")
	}

	flowID := positional[0]
	ws := resolveOrDetectWorkspace(d, *workspace)

	flow, err := d.ResolveFlow(ws, flowID)
	if err != nil {
		return err
	}
	if flow == nil {
		return fmt.Errorf("flow %q not found in workspace %s", flowID, ws)
	}

	fc, err := d.FlowCoverage(flow.ID)
	if err != nil {
		return err
	}

	if *jsonOut {
		return outputJSON(fc)
	}

	fmt.Printf("%s: %s\n", fc.FlowID, fc.Name)
	fmt.Println()

	for _, pc := range fc.Paths {
		fmt.Printf("%s (%d steps):\n", pc.Path.Name, pc.TotalSteps)
		for _, platform := range model.ValidPlatforms {
			count := pc.Coverage[platform]
			fmt.Printf("  %-13s %d/%d\n", platform+":", count, pc.TotalSteps)
		}
		fmt.Println()
	}
	return nil
}

func captureGet(d *db.DB, args []string) error {
	fs := flag.NewFlagSet("capture get", flag.ContinueOnError)
	pathName := fs.String("path", "", "Path name (defaults to happy path)")
	output := fs.String("output", "", "Copy screenshot to this file path")
	workspace := fs.String("workspace", "", "Workspace")
	jsonOut := fs.Bool("json", false, "Output JSON")

	flagsWithValue := map[string]bool{"path": true, "output": true, "workspace": true}
	flagArgs, positional := splitFlagsAndPositional(args, flagsWithValue)
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}

	if len(positional) < 3 {
		return fmt.Errorf("usage: afl capture get <FLOW-ID> <step-order> <platform> [--path <path-name>] [--output <file>] [--workspace <ws>] [--json]")
	}

	flowID := positional[0]
	stepOrder, err := strconv.Atoi(positional[1])
	if err != nil {
		return fmt.Errorf("invalid step-order %q: must be an integer", positional[1])
	}
	platform := positional[2]

	if !model.IsValidPlatform(platform) {
		return fmt.Errorf("invalid platform %q: must be one of %s", platform, strings.Join(model.ValidPlatforms, ", "))
	}

	ws := resolveOrDetectWorkspace(d, *workspace)

	flow, _, step, err := resolveFlowPathStep(d, ws, flowID, *pathName, stepOrder)
	if err != nil {
		return err
	}

	screenshot, err := d.GetScreenshotForStep(step.ID, platform)
	if err != nil {
		return err
	}
	if screenshot == nil {
		return fmt.Errorf("no screenshot for step %d [%s] in flow %s", stepOrder, platform, flow.FlowID)
	}

	screenshotsDir, err := db.ScreenshotsDir()
	if err != nil {
		return err
	}
	filePath := filepath.Join(screenshotsDir, flow.ID, step.ID, platform, screenshot.StoredName)

	if *output != "" {
		if err := copyFile(filePath, *output); err != nil {
			return fmt.Errorf("copy screenshot: %w", err)
		}
		if !*jsonOut {
			fmt.Printf("copied to %s\n", *output)
		}
	}

	if *jsonOut {
		return outputJSON(screenshot)
	}

	if *output == "" {
		fmt.Println(filePath)
	}
	return nil
}

// resolveFlowPathStep resolves a flow, path, and step from user-supplied arguments.
func resolveFlowPathStep(d *db.DB, ws, flowID, pathName string, stepOrder int) (*model.Flow, *model.Path, *model.Step, error) {
	flow, err := d.ResolveFlow(ws, flowID)
	if err != nil {
		return nil, nil, nil, err
	}
	if flow == nil {
		return nil, nil, nil, fmt.Errorf("flow %q not found in workspace %s", flowID, ws)
	}

	path, err := resolvePathForFlow(d, flow.ID, pathName)
	if err != nil {
		return nil, nil, nil, err
	}

	step, err := d.GetStepByOrder(path.ID, stepOrder)
	if err != nil {
		return nil, nil, nil, err
	}
	if step == nil {
		return nil, nil, nil, fmt.Errorf("no step with order %d in path %q", stepOrder, path.Name)
	}

	return flow, path, step, nil
}

// resolvePathForFlow finds a path by name, defaulting to the first happy path.
func resolvePathForFlow(d *db.DB, flowID, pathName string) (*model.Path, error) {
	if pathName != "" {
		path, err := d.GetPathByName(flowID, pathName)
		if err != nil {
			return nil, err
		}
		if path == nil {
			return nil, fmt.Errorf("path %q not found in flow", pathName)
		}
		return path, nil
	}

	// Default to the first happy path.
	paths, err := d.ListPaths(flowID)
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		if p.PathType == model.PathTypeHappy {
			return &p, nil
		}
	}
	if len(paths) > 0 {
		return &paths[0], nil
	}
	return nil, fmt.Errorf("flow has no paths")
}

// uploadScreenshot handles the core logic of uploading a single screenshot file.
func uploadScreenshot(d *db.DB, flow *model.Flow, step *model.Step, platform, imagePath, source string) (*model.Screenshot, error) {
	// Verify image file exists.
	info, err := os.Stat(imagePath)
	if err != nil {
		return nil, fmt.Errorf("image file %q: %w", imagePath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%q is a directory, not an image file", imagePath)
	}

	// Detect MIME type from extension.
	ext := strings.ToLower(filepath.Ext(imagePath))
	mimeType, ok := imageMimeTypes[ext]
	if !ok {
		return nil, fmt.Errorf("unsupported image extension %q: supported extensions are .png, .jpg, .jpeg, .webp", ext)
	}

	// Generate stored name.
	storedName := uuid.New().String() + ext

	// Build the target directory.
	screenshotsDir, err := db.ScreenshotsDir()
	if err != nil {
		return nil, err
	}
	targetDir := filepath.Join(screenshotsDir, flow.ID, step.ID, platform)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, fmt.Errorf("create screenshot dir: %w", err)
	}

	// Delete old file if there's an existing screenshot for this step+platform.
	existing, err := d.GetScreenshotForStep(step.ID, platform)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		oldPath := filepath.Join(targetDir, existing.StoredName)
		_ = os.Remove(oldPath) // best-effort; file may already be gone
	}

	// Copy file to target location.
	targetPath := filepath.Join(targetDir, storedName)
	if err := copyFile(imagePath, targetPath); err != nil {
		return nil, fmt.Errorf("copy image: %w", err)
	}

	filename := filepath.Base(imagePath)
	capturedAt := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	screenshot, err := d.UpsertScreenshot(step.ID, platform, filename, storedName, mimeType, info.Size(), source, capturedAt)
	if err != nil {
		// Clean up the copied file on DB error.
		_ = os.Remove(targetPath)
		return nil, err
	}

	return screenshot, nil
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// isImageExtension returns true if the file extension is a supported image type.
func isImageExtension(ext string) bool {
	_, ok := imageMimeTypes[strings.ToLower(ext)]
	return ok
}

// imageMimeTypes maps file extensions to MIME types.
var imageMimeTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".webp": "image/webp",
}

func captureUsage() error {
	return fmt.Errorf(`usage: afl capture <subcommand>

Subcommands:
  upload <FLOW-ID> <step-order> <platform> <image-path>   Upload a screenshot
  batch <FLOW-ID> <platform> <dir>                        Batch upload from numbered files
  status <FLOW-ID>                                        Show capture coverage
  get <FLOW-ID> <step-order> <platform>                   Get a screenshot

Flags:
  --path <name>        Path name (defaults to happy path)
  --source <tool>      Capture source: playwright, xcodebuildmcp, droidmind, manual
  --output <file>      Copy screenshot to file (for get)
  --workspace <ws>     Override workspace
  --json               Output JSON`)
}
