package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/download", downloadHandler)

	log.Printf("SpotiFLAC server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

// GET /health
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "1.0"})
}

// POST /download
// Body: {"url": "https://open.spotify.com/track/..."}
func downloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		http.Error(w, `{"error":"need {\"url\":\"spotify_url\"}"}`, http.StatusBadRequest)
		return
	}

	if !strings.Contains(req.URL, "open.spotify.com") {
		http.Error(w, `{"error":"invalid Spotify URL"}`, http.StatusBadRequest)
		return
	}

	// 임시 다운로드 디렉토리 생성
	tmpDir, err := os.MkdirTemp("", "spotiflac-*")
	if err != nil {
		http.Error(w, `{"error":"server error"}`, http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tmpDir)

	log.Printf("Downloading: %s", req.URL)

	// SpotiFLAC CLI 실행 (타임아웃 5분)
	spotiflacPath := spotiflacBinaryPath()
	cmd := exec.Command(spotiflacPath, "-o", tmpDir, req.URL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()

	select {
	case err := <-done:
		if err != nil {
			log.Printf("SpotiFLAC error: %v", err)
			http.Error(w, fmt.Sprintf(`{"error":"download failed: %v"}`, err), http.StatusInternalServerError)
			return
		}
	case <-time.After(5 * time.Minute):
		cmd.Process.Kill()
		http.Error(w, `{"error":"download timeout"}`, http.StatusGatewayTimeout)
		return
	}

	// 다운로드된 오디오 파일 탐색
	filePath, err := findAudioFile(tmpDir)
	if err != nil || filePath == "" {
		http.Error(w, `{"error":"no audio file found"}`, http.StatusInternalServerError)
		return
	}

	file, err := os.Open(filePath)
	if err != nil {
		http.Error(w, `{"error":"failed to read file"}`, http.StatusInternalServerError)
		return
	}
	defer file.Close()

	stat, _ := file.Stat()
	filename := filepath.Base(filePath)
	ext := strings.ToLower(filepath.Ext(filename))

	contentType := map[string]string{
		".flac": "audio/flac",
		".mp3":  "audio/mpeg",
		".m4a":  "audio/mp4",
		".ogg":  "audio/ogg",
	}[ext]
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("X-Filename", filename)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))

	io.Copy(w, file)
	log.Printf("Served: %s (%d bytes)", filename, stat.Size())
}

func findAudioFile(dir string) (string, error) {
	audioExts := map[string]bool{".flac": true, ".mp3": true, ".m4a": true, ".ogg": true}
	var found string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || found != "" {
			return err
		}
		if audioExts[strings.ToLower(filepath.Ext(path))] {
			found = path
		}
		return nil
	})
	return found, err
}

func spotiflacBinaryPath() string {
	// Railway 환경 또는 로컬 실행 모두 지원
	paths := []string{"./spotiflac", "/app/spotiflac", "spotiflac"}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "./spotiflac"
}
