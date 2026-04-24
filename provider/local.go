package provider

import (
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

// localDefaultModel is Qwen2.5-Coder-7B — top-ranked open-source coding model (HumanEval 88.4%),
// fits in 8 GB RAM as Q4_K_M (~4.7 GB), Apache-2.0 licensed.
const localDefaultModel = "Qwen/Qwen2.5-Coder-7B-Instruct-GGUF"
const localDefaultPort = 8765
const localCacheDir = ".cache/deus-ex-machina/models"

// localProvider runs GGUF models via a llama-server subprocess.
// Accepts either a local .gguf file path or a HuggingFace repo ID (org/name).
type localProvider struct {
	modelSpec string // path to .gguf OR HuggingFace repo ID
	filename  string // optional: specific filename within HF repo (e.g. "*Q4_K_M.gguf")
	hfToken   string
	port      int
	nGPU      int    // GPU layers (-1 = auto)
	nCtx      int    // context size (0 = default 2048)
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
	if nGPU == 0 {
		nGPU = -1
	}
	if nCtx == 0 {
		nCtx = 2048
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

func (l *localProvider) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolDef, out io.Writer) (*agent.Response, error) {
	if l.inner == nil {
		modelPath, err := l.resolveModel(ctx, out)
		if err != nil {
			return nil, err
		}
		if err := l.startServer(ctx, modelPath, out); err != nil {
			return nil, err
		}
	}
	return l.inner.Complete(ctx, messages, tools, out)
}

// resolveModel returns an absolute path to a .gguf file,
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

// downloadFromHF downloads a GGUF model from HuggingFace, caching it locally.
func (l *localProvider) downloadFromHF(ctx context.Context, repoID string, out io.Writer) (string, error) {
	cacheDir := filepath.Join(homeDir(), localCacheDir, strings.ReplaceAll(repoID, "/", "--"))
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}

	filename, err := l.resolveHFFilename(ctx, repoID)
	if err != nil {
		return "", err
	}

	dest := filepath.Join(cacheDir, filename)
	if _, err := os.Stat(dest); err == nil {
		fmt.Fprintf(out, "using cached model: %s\n", dest)
		return dest, nil
	}

	fmt.Fprintf(out, "downloading %s/%s ...\n", repoID, filename)
	if err := l.hfDownloadFile(ctx, repoID, filename, dest); err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}
	fmt.Fprintf(out, "saved to %s\n", dest)
	return dest, nil
}

// resolveHFFilename picks the GGUF file to download from a HF repo.
// Prefers files matching l.filename pattern, otherwise picks the smallest Q4_K_M variant.
func (l *localProvider) resolveHFFilename(ctx context.Context, repoID string) (string, error) {
	url := fmt.Sprintf("https://huggingface.co/api/models/%s", repoID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if l.hfToken != "" {
		req.Header.Set("Authorization", "Bearer "+l.hfToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("HuggingFace API error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("HuggingFace API %d: %s", resp.StatusCode, string(body))
	}

	var info struct {
		Siblings []struct {
			Rfilename string `json:"rfilename"`
		} `json:"siblings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", err
	}

	var ggufFiles []string
	for _, s := range info.Siblings {
		if strings.HasSuffix(s.Rfilename, ".gguf") {
			ggufFiles = append(ggufFiles, s.Rfilename)
		}
	}
	if len(ggufFiles) == 0 {
		return "", fmt.Errorf("no .gguf files found in repo %s", repoID)
	}

	// If user specified a pattern, use it
	if l.filename != "" {
		for _, f := range ggufFiles {
			matched, _ := filepath.Match(l.filename, f)
			if matched || strings.Contains(f, l.filename) {
				return f, nil
			}
		}
		return "", fmt.Errorf("no GGUF file matching %q in repo %s", l.filename, repoID)
	}

	// Pick preference order: Q4_K_M > Q5_K_M > Q4_K_S > Q4_0 > first file
	// Match case-insensitively since repos differ (Q4_K_M vs q4_k_m).
	for _, pref := range []string{"Q4_K_M", "q4_k_m", "Q5_K_M", "q5_k_m", "Q4_K_S", "q4_k_s", "Q4_0", "q4_0"} {
		for _, f := range ggufFiles {
			if strings.Contains(f, pref) {
				return f, nil
			}
		}
	}
	return ggufFiles[0], nil
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
		n, err := resp.Body.Read(buf)
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
		if err == io.EOF {
			break
		}
		if err != nil {
			f.Close()
			return err
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

	fmt.Fprintf(out, "starting llama-server on port %d ...\n", l.port)
	cmd := exec.CommandContext(ctx, serverBin, args...)
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start llama-server: %w", err)
	}
	l.server = cmd

	// Wait until the server is accepting requests
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", l.port)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if time.Now().After(deadline) {
		cmd.Process.Kill()
		return fmt.Errorf("llama-server did not start within 30s")
	}

	fmt.Fprintf(out, "llama-server ready\n")
	l.inner = &openAICompat{baseURL: baseURL, apiKey: "local", model: "local"}
	return nil
}

func (l *localProvider) findServerBinary() string {
	// Check common names in PATH
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
