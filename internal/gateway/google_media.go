package gateway

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

type geminiBlob struct {
	MIME string `json:"mimeType"`
	Data string `json:"data"`
}

func inlineMedia(mime, data string) (*geminiBlob, error) {
	if !(strings.HasPrefix(mime, "image/") || strings.HasPrefix(mime, "audio/") || strings.HasPrefix(mime, "video/") || mime == "application/pdf") {
		return nil, errors.New("媒体类型不支持")
	}
	if bytes, err := base64.StdEncoding.DecodeString(data); err != nil || len(bytes) == 0 {
		return nil, errors.New("媒体需要非空 base64 数据")
	}
	return &geminiBlob{mime, data}, nil
}

func mediaDataURL(value string) (*geminiBlob, error) {
	meta, data, ok := strings.Cut(value, ",")
	if !ok || !strings.HasPrefix(meta, "data:") || !strings.HasSuffix(meta, ";base64") {
		return nil, errors.New("文件请使用 base64 data URL")
	}
	return inlineMedia(strings.TrimSuffix(strings.TrimPrefix(meta, "data:"), ";base64"), data)
}

func googleMediaConfig(payload map[string]json.RawMessage, config map[string]any) error {
	if raw, ok := payload["media_resolution"]; ok {
		var name string
		if json.Unmarshal(raw, &name) != nil {
			return errors.New("媒体分辨率无效")
		}
		value := map[string]string{"low": "MEDIA_RESOLUTION_LOW", "medium": "MEDIA_RESOLUTION_MEDIUM", "high": "MEDIA_RESOLUTION_HIGH"}[name]
		if value == "" {
			return errors.New("媒体分辨率需为 low、medium 或 high")
		}
		config["mediaResolution"] = value
	}
	if raw, ok := payload["modalities"]; ok {
		var values []string
		if json.Unmarshal(raw, &values) != nil || len(values) == 0 {
			return errors.New("输出类型无效")
		}
		modes := []string{}
		seen := map[string]bool{}
		for _, value := range values {
			if value != "text" && value != "image" && value != "audio" || seen[value] {
				return errors.New("输出类型无效")
			}
			seen[value] = true
			modes = append(modes, strings.ToUpper(value))
		}
		config["responseModalities"] = modes
	}
	if raw, ok := payload["audio"]; ok {
		var audio map[string]any
		if json.Unmarshal(raw, &audio) != nil || audio == nil {
			return errors.New("语音参数无效")
		}
		if err := protocolFields(audio, "voice", "format"); err != nil {
			return err
		}
		voice, ok := audio["voice"].(string)
		if !ok || strings.TrimSpace(voice) == "" {
			return errors.New("请选择语音声音")
		}
		if format := audio["format"]; format != nil && format != "pcm16" {
			return errors.New("Google 原始语音使用 pcm16；WAV 请用 audio/speech 接口")
		}
		if modes, ok := config["responseModalities"].([]string); ok {
			if !slices.Contains(modes, "AUDIO") {
				return errors.New("语音参数需要 audio 输出类型")
			}
		} else {
			config["responseModalities"] = []string{"AUDIO"}
		}
		config["speechConfig"] = map[string]any{"voiceConfig": map[string]any{"prebuiltVoiceConfig": map[string]any{"voiceName": voice}}}
	}
	return nil
}

func playgroundSpeech(value any) (any, error) {
	config, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("语音参数无效")
	}
	voice, ok := config["voiceConfig"].(map[string]any)
	if !ok {
		return nil, errors.New("语音声音无效")
	}
	built, ok := voice["prebuiltVoiceConfig"].(map[string]any)
	if !ok {
		return nil, errors.New("语音声音无效")
	}
	name, ok := built["voiceName"].(string)
	if !ok || name == "" {
		return nil, errors.New("语音声音无效")
	}
	return []any{[]any{[]any{name}}}, nil
}
