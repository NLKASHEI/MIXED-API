package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// Speech reuses the selected Google transport and its account/cancellation scope.
// No encoder process or provider switch is involved; supported containers are PCM/WAV.
func (s *Server) serveSpeech(w http.ResponseWriter, r *http.Request, b Backend, publicModel, model string, payload map[string]json.RawMessage) {
	if b.ID != "vertex" && b.ID != "aps" && b.ID != "aistudio" {
		jsonError(w, 501, "此来源未提供内置语音生成")
		return
	}
	input := payloadObject(payload)
	if err := protocolFields(input, "model", "input", "voice", "response_format", "instructions", "speed", "stream_format"); err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	text, ok := input["input"].(string)
	if !ok || strings.TrimSpace(text) == "" {
		jsonError(w, 400, "语音生成需要非空 input 文本")
		return
	}
	format := "wav"
	if value, ok := input["response_format"]; ok {
		format, _ = value.(string)
	}
	if format != "wav" && format != "pcm" {
		jsonError(w, 400, "内置语音当前支持 wav 或 pcm")
		return
	}
	if speed := input["speed"]; speed != nil && speed != float64(1) {
		jsonError(w, 400, "当前来源不支持语速数值；可通过 instructions 描述朗读方式")
		return
	}
	if mode := input["stream_format"]; mode != nil && mode != "audio" {
		jsonError(w, 400, "此接口返回二进制音频")
		return
	}
	voice := "Zephyr"
	if value, ok := input["voice"]; ok {
		voice, _ = value.(string)
	}
	if voice == "" {
		jsonError(w, 400, "voice 需要来源支持的声音名称")
		return
	}
	if value, ok := input["instructions"]; ok {
		instructions, valid := value.(string)
		if !valid {
			jsonError(w, 400, "instructions 需要文本")
			return
		}
		if instructions != "" {
			text = instructions + "\n" + text
		}
	}
	chat := map[string]any{"model": publicModel, "messages": []any{map[string]any{"role": "user", "content": text}}, "audio": map[string]any{"voice": voice, "format": "pcm16"}, "stream": false}
	raw, _ := json.Marshal(chat)
	var converted map[string]json.RawMessage
	json.Unmarshal(raw, &converted)
	request := r.Clone(r.Context())
	request.URL.Path = "/v1/chat/completions"
	buffer := &protocolBuffer{header: make(http.Header)}
	s.serveIntegrated(buffer, request, b, publicModel, model, converted)
	for _, key := range []string{"Retry-After", "X-Mixed-Backend"} {
		if value := buffer.header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	if buffer.overflow {
		jsonError(w, 502, "语音结果过大")
		return
	}
	if buffer.status != 200 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(buffer.status)
		w.Write(buffer.Bytes())
		return
	}
	var response struct {
		Choices []struct {
			Finish  string `json:"finish_reason"`
			Message struct {
				Media []struct {
					MIME string `json:"mime_type"`
					Data string `json:"data"`
				} `json:"media"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(buffer.Bytes(), &response) != nil || len(response.Choices) != 1 || response.Choices[0].Finish != "stop" {
		jsonError(w, 502, "来源未返回完整语音")
		return
	}
	pcm := []byte{}
	rate := 0
	for _, part := range response.Choices[0].Message.Media {
		data, err := base64.StdEncoding.DecodeString(part.Data)
		if err != nil {
			jsonError(w, 502, "来源语音数据无效")
			return
		}
		samples, hz, err := speechPCM(data, part.MIME)
		if err != nil || rate != 0 && rate != hz || len(pcm)+len(samples) > maxBody-44 {
			jsonError(w, 502, "来源语音格式无效或不一致")
			return
		}
		rate = hz
		pcm = append(pcm, samples...)
	}
	if len(pcm) == 0 {
		jsonError(w, 502, "来源未返回语音数据")
		return
	}
	if format == "pcm" && rate != 24000 {
		jsonError(w, 502, "PCM 接口需要 24 kHz 音频；请请求 wav 保留来源采样率")
		return
	}
	if format == "wav" {
		pcm = speechWave(pcm, rate)
		w.Header().Set("Content-Type", "audio/wav")
	} else {
		w.Header().Set("Content-Type", "audio/pcm")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(pcm)))
	w.Header().Set("Cache-Control", "no-store")
	w.Write(pcm)
}

func speechPCM(data []byte, contentType string) ([]byte, int, error) {
	invalid := errors.New("invalid audio")
	kind, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, 0, invalid
	}
	kind = strings.ToLower(kind)
	if kind == "audio/wav" || kind == "audio/x-wav" {
		if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" || uint64(binary.LittleEndian.Uint32(data[4:8]))+8 != uint64(len(data)) {
			return nil, 0, invalid
		}
		rate := 0
		var pcm []byte
		pos := 12
		for pos+8 <= len(data) {
			size := uint64(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
			end := uint64(pos) + 8 + size
			if end > uint64(len(data)) {
				return nil, 0, invalid
			}
			chunk := data[pos+8 : int(end)]
			switch string(data[pos : pos+4]) {
			case "fmt ":
				if len(chunk) < 16 || rate != 0 || binary.LittleEndian.Uint16(chunk[:2]) != 1 || binary.LittleEndian.Uint16(chunk[2:4]) != 1 || binary.LittleEndian.Uint16(chunk[14:16]) != 16 {
					return nil, 0, invalid
				}
				rate = int(binary.LittleEndian.Uint32(chunk[4:8]))
				if rate < 8000 || rate > 96000 || binary.LittleEndian.Uint16(chunk[12:14]) != 2 || binary.LittleEndian.Uint32(chunk[8:12]) != uint32(rate*2) {
					return nil, 0, invalid
				}
			case "data":
				if pcm != nil {
					return nil, 0, invalid
				}
				pcm = chunk
			}
			pos = int(end + size%2)
		}
		if pos != len(data) || rate == 0 || len(pcm) == 0 || len(pcm)%2 != 0 {
			return nil, 0, invalid
		}
		return pcm, rate, nil
	}
	if kind != "audio/l16" && kind != "audio/pcm" {
		return nil, 0, invalid
	}
	for key := range params {
		if key != "rate" && key != "codec" {
			return nil, 0, invalid
		}
	}
	if codec := params["codec"]; codec != "" && codec != "pcm" {
		return nil, 0, invalid
	}
	rate := 24000
	if value := params["rate"]; value != "" {
		rate, err = strconv.Atoi(value)
	}
	if err != nil || rate < 8000 || rate > 96000 || len(data) == 0 || len(data)%2 != 0 {
		return nil, 0, invalid
	}
	return data, rate, nil
}

func speechWave(pcm []byte, rate int) []byte {
	var out bytes.Buffer
	out.WriteString("RIFF")
	binary.Write(&out, binary.LittleEndian, uint32(len(pcm)+36))
	out.WriteString("WAVEfmt ")
	binary.Write(&out, binary.LittleEndian, uint32(16))
	binary.Write(&out, binary.LittleEndian, uint16(1))
	binary.Write(&out, binary.LittleEndian, uint16(1))
	binary.Write(&out, binary.LittleEndian, uint32(rate))
	binary.Write(&out, binary.LittleEndian, uint32(rate*2))
	binary.Write(&out, binary.LittleEndian, uint16(2))
	binary.Write(&out, binary.LittleEndian, uint16(16))
	out.WriteString("data")
	binary.Write(&out, binary.LittleEndian, uint32(len(pcm)))
	out.Write(pcm)
	return out.Bytes()
}
