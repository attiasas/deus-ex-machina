package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/attiasas/deus-ex-machina/agent"
)

// localDefaultModel is Qwen2.5-7B-Instruct — best tool-calling F1 (0.933) among 7B local models,
// strong instruction/format compliance, fits in 8 GB RAM as Q4_K_M (~4.7 GB), Apache-2.0 licensed.
// The non-Coder variant follows text-format instructions more reliably than the Coder variant.
const localDefaultModel = "Qwen/Qwen2.5-7B-Instruct-GGUF"
const localDefaultPort = 8765
const localCacheDir = ".cache/deus-ex-machina/models"
const localServerStartTimeout = 5 * time.Minute

// localProvider runs GGUF models via a llama-server subprocess.
// Accepts either a local .gguf file path or a HuggingFace repo ID (org/name).
type localProvider struct {
	modelSpec string // path to .gguf OR HuggingFace repo ID
	filename  string // optional: specific filename pattern within HF repo
	hfToken   string
	port      int
	nGPU      int // GPU layers (0 = CPU-only, -1 = all on GPU)
	nCtx      int // context size
	server    *exec.Cmd
	inner     *openAICompat
}

// NewLocal creates a local provider.
// modelSpec is either a .gguf file path or a HuggingFace repo ID (org/model).
// hfFilename optionally narrows which GGUF to pick from a HF repo (glob pattern).
func NewLocal(modelSpec, hfFilename, hfToken string, port, nGPU, nCtx int) Provider {
	if modelSpec == "" {
		modelSpec = localDefaultModel
	}
	if port == 0 {
		port = localDefaultPort
	}
	// nGPU == 0 means "user didn't set it" → default to CPU-only (safe for all hardware)
	// Pass -1 explicitly to offload all layers to GPU
	if nCtx == 0 {
		nCtx = 8192
	}
	if hfToken == "" {
		hfToken = os.Getenv("HF_TOKEN")
	}
	return &localProvider{
		modelSpec: modelSpec,
		filename:  hfFilename,
		hfToken:   hfToken,
		port:      port,
		nGPU:      nGPU,
		nCtx:      nCtx,
	}
}

func (l *localProvider) Complete(ctx context.Context, messages []agent.Message, out io.Writer) (*agent.Response, error) {
	if l.inner == nil {
		modelPath, err := l.resolveModel(ctx, out)
		if err != nil {
			return nil, err
		}
		if err := l.startServer(ctx, modelPath, out); err != nil {
			return nil, err
		}
	}
	return l.inner.Complete(ctx, messages, out)
}

// resolveModel returns an absolute path to the first (or only) .gguf shard,
// downloading from HuggingFace if necessary.
func (l *localProvider) resolveModel(ctx context.Context, out io.Writer) (string, error) {
	spec := l.modelSpec

	// Local file path
	if strings.HasSuffix(spec, ".gguf") || isLocalPath(spec) {
		abs, err := filepath.Abs(spec)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("model file not found: %s", abs)
		}
		return abs, nil
	}

	// HuggingFace repo ID (org/model)
	if strings.Count(spec, "/") == 1 {
		return l.downloadFromHF(ctx, spec, out)
	}

	return "", fmt.Errorf("deus: cannot resolve model %q — provide a .gguf path or a HuggingFace repo ID (org/model)", spec)
}

func isLocalPath(s string) bool {
	return strings.HasPrefix(s, "/") || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../")
}

// downloadFromHF ensures all shards of the selected GGUF are cached locally and
// returns the path to the first (or only) shard.
func (l *localProvider) downloadFromHF(ctx context.Context, repoID string, out io.Writer) (string, error) {
	cacheDir := filepath.Join(homeDir(), localCacheDir, strings.ReplaceAll(repoID, "/", "--"))
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}

	allGGUFs, err := l.listHFGGUFs(ctx, repoID)
	if err != nil {
		return "", err
	}

	// Pick the target file then find all shards belonging to it
	selected, err := l.pickGGUF(allGGUFs)
	if err != nil {
		return "", err
	}
	shards := collectShards(selected, allGGUFs)

	// Check if all shards are already cached
	allCached := true
	for _, shard := range shards {
		if _, err := os.Stat(filepath.Join(cacheDir, shard)); err != nil {
			allCached = false
			break
		}
	}
	firstShard := filepath.Join(cacheDir, shards[0])
	if allCached {
		fmt.Fprintf(out, "using cached model: %s\n", firstShard)
		return firstShard, nil
	}

	// Download all missing shards
	for i, shard := range shards {
		dest := filepath.Join(cacheDir, shard)
		if _, err := os.Stat(dest); err == nil {
			fmt.Fprintf(out, "shard %d/%d already cached\n", i+1, len(shards))
			continue
		}
		label := shard
		if len(shards) > 1 {
			label = fmt.Sprintf("%s (shard %d/%d)", shard, i+1, len(shards))
		}
		fmt.Fprintf(out, "downloading %s ...\n", label)
		if err := l.hfDownloadFile(ctx, repoID, shard, dest); err != nil {
			return "", fmt.Errorf("download failed for %s: %w", shard, err)
		}
	}
	return firstShard, nil
}

// listHFGGUFs returns all .gguf filenames in a HuggingFace repo.
func (l *localProvider) listHFGGUFs(ctx context.Context, repoID string) ([]string, error) {
	url := fmt.Sprintf("https://huggingface.co/api/models/%s", repoID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if l.hfToken != "" {
		req.Header.Set("Authorization", "Bearer "+l.hfToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HuggingFace API error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HuggingFace API %d: %s", resp.StatusCode, string(body))
	}

	var info struct {
		Siblings []struct {
			Rfilename string `json:"rfilename"`
		} `json:"siblings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}

	var files []string
	for _, s := range info.Siblings {
		if strings.HasSuffix(s.Rfilename, ".gguf") {
			files = append(files, s.Rfilename)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .gguf files found in repo %s", repoID)
	}
	return files, nil
}

// pickGGUF selects the best single file (or first shard of a group) from a list.
func (l *localProvider) pickGGUF(ggufFiles []string) (string, error) {
	if l.filename != "" {
		for _, f := range ggufFiles {
			matched, _ := filepath.Match(l.filename, f)
			if matched || strings.Contains(f, l.filename) {
				return f, nil
			}
		}
		return "", fmt.Errorf("no GGUF file matching %q", l.filename)
	}

	// Prefer single-file (non-split) variants first, then split.
	// Pick preference order: Q4_K_M > Q5_K_M > Q4_K_S > Q4_0 > first file.
	prefs := []string{"q4_k_m", "q5_k_m", "q4_k_s", "q4_0"}

	// First pass: single-file only
	for _, pref := range prefs {
		for _, f := range ggufFiles {
			if strings.Contains(strings.ToLower(f), pref) && !isSplitShard(f) {
				return f, nil
			}
		}
	}
	// Second pass: allow split files
	for _, pref := range prefs {
		for _, f := range ggufFiles {
			if strings.Contains(strings.ToLower(f), pref) {
				return firstShard(f, ggufFiles), nil
			}
		}
	}
	// Fall back to first file
	return firstShard(ggufFiles[0], ggufFiles), nil
}

// isSplitShard reports whether filename is part of a split GGUF set.
// Split files look like: name-00001-of-00002.gguf
func isSplitShard(filename string) bool {
	_, _, total := parseSplitSuffix(filename)
	return total > 1
}

// parseSplitSuffix extracts (base, shardNum, totalShards) from a split GGUF filename.
// Returns ("", 0, 0) if not a split file.
func parseSplitSuffix(filename string) (base string, num, total int) {
	name := strings.TrimSuffix(filename, ".gguf")
	ofIdx := strings.LastIndex(name, "-of-")
	if ofIdx < 0 {
		return
	}
	dashIdx := strings.LastIndex(name[:ofIdx], "-")
	if dashIdx < 0 {
		return
	}
	n, err1 := strconv.Atoi(name[dashIdx+1 : ofIdx])
	t, err2 := strconv.Atoi(name[ofIdx+4:])
	if err1 != nil || err2 != nil {
		return
	}
	return name[:dashIdx], n, t
}

// firstShard returns the first shard filename for a split GGUF, or the filename itself
// if it is not split.
func firstShard(filename string, all []string) string {
	base, _, total := parseSplitSuffix(filename)
	if total <= 1 {
		return filename
	}
	// Reconstruct the first shard name from the base
	totalStr := fmt.Sprintf("%05d", total)
	for _, f := range all {
		b, n, t := parseSplitSuffix(f)
		if b == base && n == 1 && fmt.Sprintf("%05d", t) == totalStr {
			return f
		}
	}
	// Fallback: the file itself might already be shard 1
	return filename
}

// collectShards returns all shard filenames for the group containing filename.
// For a non-split file, returns [filename].
func collectShards(filename string, all []string) []string {
	base, _, total := parseSplitSuffix(filename)
	if total <= 1 {
		return []string{filename}
	}
	shards := make([]string, 0, total)
	for i := 1; i <= total; i++ {
		want := fmt.Sprintf("%s-%05d-of-%05d.gguf", base, i, total)
		for _, f := range all {
			if f == want {
				shards = append(shards, f)
				break
			}
		}
	}
	if len(shards) != total {
		// Fallback: return just what we found
		return shards
	}
	return shards
}

func (l *localProvider) hfDownloadFile(ctx context.Context, repoID, filename, dest string) error {
	url := fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repoID, filename)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if l.hfToken != "" {
		req.Header.Set("Authorization", "Bearer "+l.hfToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)

	total := resp.ContentLength
	var written int64
	buf := make([]byte, 32*1024)
	lastPrint := time.Now()
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return werr
			}
			written += int64(n)
			if time.Since(lastPrint) > 2*time.Second && total > 0 {
				pct := float64(written) / float64(total) * 100
				fmt.Printf("\r  %.1f%% (%s / %s)", pct, humanBytes(written), humanBytes(total))
				lastPrint = time.Now()
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			f.Close()
			return readErr
		}
	}
	if total > 0 {
		fmt.Println()
	}
	f.Close()
	return os.Rename(tmp, dest)
}

func (l *localProvider) startServer(ctx context.Context, modelPath string, out io.Writer) error {
	serverBin := l.findServerBinary()
	if serverBin == "" {
		return fmt.Errorf(`deus: llama-server not found in PATH
  Install llama.cpp: https://github.com/ggerganov/llama.cpp
  Or use -provider ollama for a managed local runner`)
	}

	portStr := strconv.Itoa(l.port)
	args := []string{
		"--model", modelPath,
		"--host", "127.0.0.1",
		"--port", portStr,
		"--ctx-size", strconv.Itoa(l.nCtx),
		"--n-gpu-layers", strconv.Itoa(l.nGPU),
		"--log-disable",
	}

	fmt.Fprintf(out, "starting llama-server on port %d (may take a moment to load model) ...\n", l.port)
	var stderrBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, serverBin, args...)
	cmd.Stderr = &stderrBuf
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start llama-server: %w", err)
	}
	l.server = cmd

	// Wait until the server reports ready. Loading a large model can take several minutes.
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", l.port)
	deadline := time.Now().Add(localServerStartTimeout)
	lastDot := time.Now()
	for time.Now().Before(deadline) {
		// Check if process already exited (crash)
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return fmt.Errorf("llama-server exited unexpectedly:\n%s", stderrBuf.String())
		}

		resp, err := http.Get(baseURL + "/health")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				fmt.Fprintln(out)
				break
			}
			// 503 = still loading; any other non-200 is unexpected
			if resp.StatusCode != http.StatusServiceUnavailable {
				return fmt.Errorf("llama-server health check returned %d: %s", resp.StatusCode, string(body))
			}
		}

		if time.Since(lastDot) >= 3*time.Second {
			fmt.Fprint(out, ".")
			lastDot = time.Now()
		}
		time.Sleep(500 * time.Millisecond)
	}

	if time.Now().After(deadline) {
		cmd.Process.Kill()
		errMsg := stderrBuf.String()
		if errMsg != "" {
			return fmt.Errorf("llama-server did not start within %v:\n%s", localServerStartTimeout, errMsg)
		}
		return fmt.Errorf("llama-server did not start within %v", localServerStartTimeout)
	}

	fmt.Fprintf(out, "llama-server ready\n")
	l.inner = &openAICompat{baseURL: baseURL, apiKey: "local", model: "local"}
	return nil
}

func (l *localProvider) findServerBinary() string {
	names := []string{"llama-server", "llama.cpp-server"}
	if runtime.GOOS == "windows" {
		names = []string{"llama-server.exe", "llama.cpp-server.exe"}
	}
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
