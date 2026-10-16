package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type engineSettings struct {
	MaxRetries            int  `json:"max_retries"`
	RequestTimeoutSeconds int  `json:"request_timeout_seconds"`
	AggregateStream       bool `json:"aggregate_stream"`
}

func (s *Server) engineConfigPath(id string) (string, error) {
	if id == localEngineID && localEnginePresent() {
		dir, err := localEngineDir()
		return filepath.Join(dir, "config", "config.json"), err
	}
	if id == "aps" {
		dir, err := s.binaryDir(id)
		if err != nil {
			return "", err
		}
		exe, err := findManagedExe(dir, binaryUpstreams[id].executable)
		return filepath.Join(filepath.Dir(exe), "config", "config.json"), err
	}
	return "", errors.New("此引擎尚无统一参数适配器，请使用连接配置")
}

func (s *Server) handleEngineSettings(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/engine-settings/")
	path, err := s.engineConfigPath(id)
	if err != nil {
		jsonError(w, 400, "引擎未安装或不支持统一参数管理")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		jsonError(w, 405, "Use GET or PUT")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	input, err := os.Open(path)
	if err != nil {
		jsonError(w, 400, "尚未导入或安装该引擎，请先到设置接入")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(input, 1<<20+1))
	input.Close()
	if err != nil || len(raw) > 1<<20 {
		jsonError(w, 400, "引擎配置不可读取或过大")
		return
	}
	var original map[string]json.RawMessage
	if json.Unmarshal(raw, &original) != nil || original == nil {
		jsonError(w, 400, "引擎配置不是有效 JSON 对象")
		return
	}
	settings := engineSettings{MaxRetries: 2, RequestTimeoutSeconds: 180}
	if json.Unmarshal(raw, &settings) != nil {
		jsonError(w, 400, "引擎参数格式无效")
		return
	}
	if r.Method == http.MethodGet {
		jsonResult(w, 200, settings)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var next engineSettings
	if decoder.Decode(&next) != nil || next.MaxRetries < 0 || next.MaxRetries > 10 || next.RequestTimeoutSeconds < 10 || next.RequestTimeoutSeconds > 600 {
		jsonError(w, 400, "重试需为 0–10，超时需为 10–600 秒；只允许已支持的参数")
		return
	}
	values, _ := json.Marshal(next)
	var fields map[string]json.RawMessage
	json.Unmarshal(values, &fields)
	for name, value := range fields {
		original[name] = value
	}
	updated, _ := json.MarshalIndent(original, "", "  ")
	if err = os.WriteFile(filepath.Join(filepath.Dir(path), ".mixed-settings.previous.json"), raw, 0600); err != nil {
		jsonError(w, 500, "无法备份原配置，未修改")
		return
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".mixed-settings-")
	if err != nil {
		jsonError(w, 500, "无法创建配置文件")
		return
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(updated); err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil || os.Rename(temp.Name(), path) != nil {
		jsonError(w, 500, "配置保存失败，请检查目录权限")
		return
	}
	jsonResult(w, 200, map[string]bool{"saved": true})
}
