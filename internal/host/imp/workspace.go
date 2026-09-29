package imp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// workspaceSchemaVersion is the overall schema version of the import workspace.
// On mismatch it explicitly demands a matching version to continue or a fresh import; no migration is guessed (RFC §6.1).
const workspaceSchemaVersion = 1

// Digest computes a content digest, following the repo's existing "sha256:"+hex convention (see store/checkpoints.go).
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Artifact is the uniform identity of every semantic artifact in the workspace: schema version + input digest + payload.
// It may be reused only if the same InputDigest can be rebuilt from the current real semantic inputs (RFC §6.3 / invariant 1).
// No dependency graph is implemented: LoadState walks a fixed linear pipeline, comparing InputDigest step by step to decide reuse vs. invalidation, and NextAction derives the next step from that.
type Artifact[T any] struct {
	SchemaVersion int    `json:"schema_version"`
	InputDigest   string `json:"input_digest"`
	Payload       T      `json:"payload"`
}

// Manifest describes the single normalized source snapshot; it is the workspace's identity, not a derived artifact (RFC §6.1).
// It stores no absolute source path, which avoids leaking machine directories and removes recovery problems caused by moving the file.
type Manifest struct {
	Version          int    `json:"version"`
	SourceName       string `json:"source_name"`
	RawSHA256        string `json:"raw_sha256"`
	NormalizedSHA256 string `json:"normalized_sha256"`
	Encoding         string `json:"encoding"`
	SizeBytes        int64  `json:"size_bytes"`
	CreatedAt        string `json:"created_at"`
}

// Intent stores the explicit user authorizations given at import start; they must still be honored after recovery, are never guessed from artifacts, and the Runner never silently rewrites them (RFC §6.1).
type Intent struct {
	Version             int    `json:"version"`
	AutoConfirm         bool   `json:"auto_confirm,omitempty"`
	StoryResolution     string `json:"story_resolution,omitempty"` // open / closed
	ContinueAfterImport bool   `json:"continue_after_import,omitempty"`
}

// Standard workspace artifact relative paths.
const (
	fileManifest     = "manifest.json"
	fileIntent       = "intent.json"
	fileSource       = "source.txt"
	fileGuidance     = "guidance.txt"
	fileSegmentation = "segmentation.json"
	fileConfirmation = "confirmation.json"
	fileSynthesis    = "synthesis.json"
	fileStoryResolve = "story-resolution.json"
	dirAnalyses      = "analyses"
	dirRangeDigests  = "range-digests"
	dirSegmentChunks = "segment-chunks"
	dirFailures      = "failures"
)

// Workspace is an atomic artifact read/write handle for the <book root>/meta/import/ directory.
type Workspace struct {
	dir string
}

// OpenWorkspace returns a handle to meta/import/ under the book root; it does not guarantee the directory exists, use Active() to check.
func OpenWorkspace(bookDir string) *Workspace {
	return &Workspace{dir: filepath.Join(bookDir, "meta", "import")}
}

func (w *Workspace) path(rel string) string { return filepath.Join(w.dir, rel) }

// Active reports whether a published active workspace exists. Without meta/import/ it is not active,
// and half-initialized directories exist as meta/import.init-*, so they are never mistaken for active (RFC §6.1).
func (w *Workspace) Active() bool {
	fi, err := os.Stat(w.dir)
	return err == nil && fi.IsDir()
}

func (w *Workspace) has(rel string) bool {
	_, err := os.Stat(w.path(rel))
	return err == nil
}

// writeAtomic atomically writes rel (relative to the workspace) via "temp file + fsync + rename".
func (w *Workspace) writeAtomic(rel string, data []byte) error {
	full := w.path(rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), filepath.Base(full)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, full); err != nil {
		return err
	}
	syncDir(filepath.Dir(full))
	return nil
}

// syncDir best-effort fsyncs the directory entry so a just-completed rename survives power loss.
// Platforms such as Windows may not support syncing a directory; its error is ignored -- crash safety does not depend on it, it only covers power loss (RFC §12.3).
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

func (w *Workspace) writeJSON(rel string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return w.writeAtomic(rel, append(data, '\n'))
}

func (w *Workspace) readJSON(rel string, v any) error {
	data, err := os.ReadFile(w.path(rel))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// LoadManifest reads the identity of the workspace's source snapshot.
func (w *Workspace) LoadManifest() (*Manifest, error) {
	var m Manifest
	if err := w.readJSON(fileManifest, &m); err != nil {
		return nil, err
	}
	if m.Version != workspaceSchemaVersion {
		return nil, fmt.Errorf("manifest schema 版本 %d != %d，请用匹配版本继续或重新导入", m.Version, workspaceSchemaVersion)
	}
	return &m, nil
}

// LoadIntent reads the user's start-of-import authorizations.
func (w *Workspace) LoadIntent() (*Intent, error) {
	var in Intent
	if err := w.readJSON(fileIntent, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

// LoadSource reads the text of the normalized source snapshot.
func (w *Workspace) LoadSource() ([]byte, error) {
	return os.ReadFile(w.path(fileSource))
}

// LoadGuidance reads the user's segmentation guidance (RFC §18.3); a missing file means no guidance.
// Guidance, like source.txt, is a semantic input of segmentation rather than a derived artifact, and is updated by an explicit --guide,
// so a content change naturally invalidates segmentation and everything downstream via InputDigest.
func (w *Workspace) LoadGuidance() (string, error) {
	data, err := os.ReadFile(w.path(fileGuidance))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// readBytes reads an artifact's raw bytes, used for downstream InputDigest binding.
func (w *Workspace) readBytes(rel string) ([]byte, error) {
	return os.ReadFile(w.path(rel))
}

// writeArtifact writes a semantic artifact carrying the uniform identity.
func writeArtifact[T any](w *Workspace, rel, inputDigest string, payload T) error {
	return w.writeJSON(rel, Artifact[T]{
		SchemaVersion: workspaceSchemaVersion,
		InputDigest:   inputDigest,
		Payload:       payload,
	})
}

// readArtifact reads a semantic artifact and validates its schema version; whether the InputDigest matches is decided by the caller against the current inputs.
func readArtifact[T any](w *Workspace, rel string) (*Artifact[T], error) {
	var a Artifact[T]
	if err := w.readJSON(rel, &a); err != nil {
		return nil, err
	}
	if a.SchemaVersion != workspaceSchemaVersion {
		return nil, fmt.Errorf("%s schema 版本 %d != %d，请用匹配版本继续或重新导入", rel, a.SchemaVersion, workspaceSchemaVersion)
	}
	return &a, nil
}

// clearDir deletes one of the workspace's intermediate cache directories. The error must be handed
// to the caller: swallowing it makes the "cleared" message lie -- the next rerun reuses the bad cache anyway (Windows antivirus/handle-holding is a real scenario, Debug-First).
func (w *Workspace) clearDir(rel string) error {
	return os.RemoveAll(w.path(rel))
}

// FailureMeta is the diagnostic metadata of the most recent failure (RFC §14.2).
type FailureMeta struct {
	Stage         string `json:"stage"`
	Detail        string `json:"detail"`
	StopReason    string `json:"stop_reason,omitempty"`
	PrefixSalvage string `json:"prefix_salvage,omitempty"` // available:N / unavailable
}

// writeFailure best-effort saves the most recent failure's metadata and the untruncated raw model response to failures/ (RFC §14.2).
// The raw response may contain body text, so it only lands in the user's own book directory, never in ordinary logs or redacted diagnostic exports.
func (w *Workspace) writeFailure(meta FailureMeta, rawResponse string) {
	_ = w.writeJSON(filepath.Join(dirFailures, "last.json"), meta)
	_ = w.writeAtomic(filepath.Join(dirFailures, "last-response.txt"), []byte(rawResponse))
}

// createWorkspace writes manifest/intent/source into a temp directory, validates them, then atomically publishes by directory rename to meta/import/.
// This way the initial trio never reaches NextAction in a half-initialized form, and no stage=initializing is needed (RFC §6.1).
func createWorkspace(bookDir string, m Manifest, in Intent, normalized []byte) (*Workspace, error) {
	base := filepath.Join(bookDir, "meta")
	final := filepath.Join(base, "import")
	if fi, err := os.Stat(final); err == nil && fi.IsDir() {
		return nil, fmt.Errorf("导入工作区已存在：%s（无参数 /import 可从中恢复）", final)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(base, "import.init-*")
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(tmp)
		}
	}()

	tw := &Workspace{dir: tmp}
	if err := tw.writeAtomic(fileSource, normalized); err != nil {
		return nil, err
	}
	if err := tw.writeJSON(fileManifest, m); err != nil {
		return nil, err
	}
	if err := tw.writeJSON(fileIntent, in); err != nil {
		return nil, err
	}
	// Before publishing, verify the trio is readable and the source snapshot matches the manifest, eliminating half-written workspaces.
	got, err := tw.LoadManifest()
	if err != nil {
		return nil, fmt.Errorf("校验初始 manifest：%w", err)
	}
	src, err := tw.LoadSource()
	if err != nil {
		return nil, fmt.Errorf("校验初始源快照：%w", err)
	}
	if d := Digest(src); d != got.NormalizedSHA256 {
		return nil, fmt.Errorf("初始源快照摘要不一致：%s != %s", d, got.NormalizedSHA256)
	}
	if _, err := tw.LoadIntent(); err != nil {
		return nil, fmt.Errorf("校验初始 intent：%w", err)
	}

	if err := os.Rename(tmp, final); err != nil {
		return nil, err
	}
	syncDir(base)
	committed = true
	return &Workspace{dir: final}, nil
}
