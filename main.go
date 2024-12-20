package main

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	_ "gorm.io/gorm"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// 定义 Messages 表的结构体
type Messages struct {
	ID               uint      `gorm:"primaryKey"`
	UserID           string    `gorm:"size:255;not null"`
	IPAddress        string    `gorm:"size:45"`
	InputMessage     string    `gorm:"type:text;not null"`
	OutputMessage    string    `gorm:"type:text"`
	Timestamp        time.Time `gorm:"autoCreateTime"` // 将类型改为 time.Time
	MessageSeq       int       `gorm:"not null"`
	ModelUsed        string    `gorm:"size:255"`
	Keywords         string    `gorm:"type:text"`
	RequiresInternet bool      `gorm:"not null;default:false"`
	IsVoiceInput     bool      `gorm:"not null;default:false"`
	DeviceModel      string    `gorm:"size:255"`
	DeviceType       string    `gorm:"size:50"`
	AccessMethod     string    `gorm:"size:50"`
	ErrorMessage     string    `gorm:"type:text"`
}

// 创建消息
func CreateMessages(db *gorm.DB, messages *Messages) error {
	result := db.Create(messages)
	return result.Error
}

// 消息结构体
type Message struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"` // 将 images 嵌套到消息结构中，可选字段
}

// 请求结构体
type OllamaRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

// 将图片转换为 Base64
func convertImageToBase64(filePath string) (string, error) {
	fileBytes, err := ioutil.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("无法读取文件: %v", err)
	}
	return base64.StdEncoding.EncodeToString(fileBytes), nil
}

// 文件名清理函数，移除特殊字符
func sanitizeFileName(fileName string) string {
	reg := regexp.MustCompile(`[^\w\-.]`) // 保留字母、数字、下划线、连字符、点
	return reg.ReplaceAllString(fileName, "_")
}

// 计算文件的 MD5 值
func calculateFileMD5(file io.Reader) (string, error) {
	hash := md5.New()
	_, err := io.Copy(hash, file)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// 上传文件并返回路径
func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	r.ParseMultipartForm(10 << 20) // 最大文件大小 10 MB

	// 获取上传的文件
	file, handler, err := r.FormFile("file")
	if err != nil {
		log.Printf("Error retrieving file: %v", err)
		http.Error(w, "Failed to retrieve file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// 检查文件 MIME 类型
	buffer := make([]byte, 512) // 读取前 512 字节判断 MIME 类型
	_, err = file.Read(buffer)
	if err != nil {
		log.Printf("Error reading file: %v", err)
		http.Error(w, "Failed to read file", http.StatusInternalServerError)
		return
	}
	file.Seek(0, io.SeekStart) // 重置文件指针

	mimeType := http.DetectContentType(buffer)
	log.Printf("Detected MIME type: %s", mimeType)

	// 保存文件
	uploadDir := "./uploads"
	if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
		os.Mkdir(uploadDir, 0755)
	}
	uploadPath := filepath.Join(uploadDir, sanitizeFileName(handler.Filename))

	out, err := os.Create(uploadPath)
	if err != nil {
		log.Printf("Error saving file: %v", err)
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}
	defer out.Close()
	io.Copy(out, file)

	// 判断是否是音频文件
	if isAudioFile(mimeType, handler.Filename) {
		// 调用预处理
		processedPath := uploadPath + "_processed.wav" // 处理后的文件路径
		err = preprocessAudio(uploadPath, processedPath)
		if err != nil {
			log.Printf("Error preprocessing audio: %v", err)
			http.Error(w, "Failed to preprocess audio", http.StatusInternalServerError)
			return
		}

		// 使用处理后的文件调用 Whisper
		transcription, err := transcribeAudio(processedPath)
		if err != nil {
			log.Printf("Error transcribing audio: %v", err)
			http.Error(w, "Failed to transcribe audio", http.StatusInternalServerError)
			return
		}

		// 返回文件路径和转录结果
		response := map[string]string{
			"filePath":      "/uploads/" + sanitizeFileName(handler.Filename),
			"transcription": transcription,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	} else {
		// 如果不是音频，只返回文件路径
		response := map[string]string{
			"filePath": "/uploads/" + sanitizeFileName(handler.Filename),
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

// 判断文件是否为音频
func isAudioFile(mimeType, fileName string) bool {
	// 检查 MIME 类型
	if mimeType == "audio/mpeg" || mimeType == "audio/wav" || mimeType == "audio/ogg" || mimeType == "audio/x-wav" {
		return true
	}

	// 检查文件扩展名
	ext := filepath.Ext(fileName)
	audioExtensions := map[string]bool{
		".mp3":  true,
		".wav":  true,
		".ogg":  true,
		".flac": true,
	}
	if audioExtensions[ext] {
		return true
	}

	return false
}

// 调用 Ollama API，支持 Base64 图片
func callOllamaAPI(messages []Message, model string, w http.ResponseWriter) error {
	apiURL := "http://localhost:11434/api/chat"

	// 遍历消息，找到需要处理的图片路径
	for i, message := range messages {
		for j, imagePath := range message.Images {
			// 将图片路径转换为 Base64
			base64Str, err := convertImageToBase64("." + imagePath) // 假设图片存储在当前目录下的 uploads 目录中
			if err != nil {
				return fmt.Errorf("failed to convert image to base64: %v", err)
			}
			// 替换为 Base64 字符串
			messages[i].Images[j] = base64Str
		}
	}

	requestData := OllamaRequest{
		Model:    model,
		Messages: messages,
	}

	// 序列化请求数据
	requestBody, err := json.Marshal(requestData)
	if err != nil {
		return fmt.Errorf("failed to serialize request: %v", err)
	}

	//log.Printf("Request Body: %s", string(requestBody)) // 调试用日志

	resp, err := http.Post(apiURL, "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		return fmt.Errorf("failed to call Ollama API: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		log.Printf("Error Response: %s", string(body))
		return fmt.Errorf("unexpected response status: %v", resp.Status)
	}

	// 设置响应头支持流式传输
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Transfer-Encoding", "chunked")

	decoder := json.NewDecoder(resp.Body)
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming unsupported")
	}

	// 逐块读取并传输数据
	for {
		var chunk map[string]interface{}
		if err := decoder.Decode(&chunk); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("failed to decode response chunk: %v", err)
		}
		chunkJSON, _ := json.Marshal(chunk)
		w.Write(chunkJSON)
		w.Write([]byte("\n"))
		flusher.Flush()
	}
	return nil
}

// GetClientIP 获取客户端 IP 地址
func GetClientIP(r *http.Request) string {
	// 1. 优先从 X-Forwarded-For 获取（处理反向代理的情况）
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		// 如果有多个 IP，用逗号分隔，取第一个
		ips := strings.Split(forwarded, ",")
		clientIP := strings.TrimSpace(ips[0])
		if isValidIP(clientIP) {
			return clientIP
		}
	}

	// 2. 检查 X-Real-Ip（常见于 Nginx 配置）
	realIP := r.Header.Get("X-Real-Ip")
	if realIP != "" && isValidIP(realIP) {
		return realIP
	}

	// 3. 从 RemoteAddr 中获取（直接连接的情况）
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && isValidIP(host) {
		return host
	}

	// 4. 无法获取 IP 时返回空字符串
	return ""
}

// isValidIP 检查是否为有效的 IP 地址
func isValidIP(ip string) bool {
	return net.ParseIP(ip) != nil
}

// /chat 路由处理逻辑
func chatHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	// 解析 JSON 请求体
	var request OllamaRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil {
		log.Printf("Error decoding JSON: %v", err)
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	// 调用 Ollama API
	err := callOllamaAPI(request.Messages, request.Model, w)
	if err != nil {
		log.Printf("Error calling Ollama API: %v", err)
		http.Error(w, fmt.Sprintf("Failed to process request: %v", err), http.StatusInternalServerError)
	}
}

// 静态文件服务
func serveStaticFiles() {
	// 提供静态文件服务
	staticFiles := http.FileServer(http.Dir("./static"))
	http.Handle("/", staticFiles)

	// 提供 uploads 目录文件的访问
	uploadsFiles := http.FileServer(http.Dir("./uploads"))
	http.Handle("/uploads/", http.StripPrefix("/uploads/", uploadsFiles))
}

// transcribeAudio 使用 Whisper.cpp 转换语音为文字
// 入参 audioPath: 音频文件路径
// 返回转录的文字和可能的错误
func transcribeAudio(audioPath string) (string, error) {
	// Whisper 可执行文件路径
	whisperCmd := "./static/whisper" // 如果可执行文件是 `whisper`
	// 模型路径
	modelPath := "./static/ggml-base.bin" // 确保模型路径正确

	// 创建命令行调用
	cmd := exec.Command(whisperCmd, "-m", modelPath, audioPath)

	// 捕获命令输出
	var outputBuffer bytes.Buffer
	var errorBuffer bytes.Buffer
	cmd.Stdout = &outputBuffer
	cmd.Stderr = &errorBuffer

	// 执行命令
	err := cmd.Run()
	if err != nil {
		log.Printf("Whisper command failed: %s\nError: %s", err, errorBuffer.String())
		return "", fmt.Errorf("error running whisper: %v", err)
	}

	// 处理转录结果
	transcription := outputBuffer.String()
	if transcription == "" {
		log.Printf("Whisper returned empty transcription. Stderr: %s", errorBuffer.String())
		return "", fmt.Errorf("whisper returned empty transcription")
	}

	log.Printf("Transcription successful: %s", transcription)
	return transcription, nil
}
func preprocessAudio(inputPath string, outputPath string) error {
	// 使用 ffmpeg 将文件转换为 16kHz 单声道 PCM WAV 格式
	cmd := exec.Command("ffmpeg", "-i", inputPath, "-ar", "16000", "-ac", "1", outputPath)
	var errorBuffer bytes.Buffer
	cmd.Stderr = &errorBuffer

	err := cmd.Run()
	if err != nil {
		log.Printf("Error preprocessing audio: %s", errorBuffer.String())
		return fmt.Errorf("error preprocessing audio: %v", err)
	}
	return nil
}

// OllamaModel represents a single model in the response
type OllamaModel struct {
	Name       string `json:"name"`
	ModifiedAt string `json:"modified_at"`
	Size       int64  `json:"size"`
	Digest     string `json:"digest"`
	Details    struct {
		Format            string   `json:"format"`
		Family            string   `json:"family"`
		Families          []string `json:"families"` // 修改为 []string
		ParameterSize     string   `json:"parameter_size"`
		QuantizationLevel string   `json:"quantization_level"`
	} `json:"details"`
}

// Response represents the response from the API
type Response struct {
	Models []OllamaModel `json:"models"`
}

// ListLocalModels handles the /get-list route and returns the list of local models
func ListLocalModels(w http.ResponseWriter, r *http.Request) {
	// Define the API endpoint
	apiURL := "http://localhost:11434/api/tags"

	// Fetch the list of models
	models, err := getLocalModels(apiURL)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error fetching local models: %v", err), http.StatusInternalServerError)
		return
	}

	// Set response header
	w.Header().Set("Content-Type", "application/json")

	// Respond with the models as JSON
	if err := json.NewEncoder(w).Encode(models); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
	}
}

// getLocalModels fetches the list of local models from the API
func getLocalModels(apiURL string) ([]OllamaModel, error) {
	// Make the GET request
	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to make GET request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response body
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check if response status is not 200 OK
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected response status: %d, body: %s", resp.StatusCode, string(body))
	}

	// Parse the JSON response
	var result Response
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return result.Models, nil
}

// 主函数
func main() {
	// 创建 uploads 目录
	if _, err := os.Stat("./uploads"); os.IsNotExist(err) {
		err := os.Mkdir("./uploads", 0755)
		if err != nil {
			log.Fatalf("Failed to create uploads directory: %v", err)
		}
	}

	// 静态文件服务
	serveStaticFiles()

	// 注册 /upload 路由
	http.HandleFunc("/upload", uploadHandler)

	// 注册 /chat 路由
	http.HandleFunc("/chat", chatHandler)

	// 注册 /chat 路由
	http.HandleFunc("/get-list", ListLocalModels)

	// 启动服务器
	log.Println("Server running at http://localhost:8080...")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
