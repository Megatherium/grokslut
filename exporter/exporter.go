// Package exporter writes Grok threads as Markdown, JSON, or a ZIP archive.
package exporter

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Megatherium/grokslut/grok"
)

type Format string

const (
	Markdown Format = "markdown"
	JSON     Format = "json"
	ZIP      Format = "zip"
)

type Result struct {
	Paths    []string `json:"paths"`
	Warnings []string `json:"warnings"`
}
type Progress struct {
	Phase   string `json:"phase"`
	Current int    `json:"current"`
	Total   int    `json:"total"`
	ID      string `json:"id"`
}
type ProgressFunc func(Progress)
type Exporter struct{ Client *grok.Client }

func (e Exporter) Export(ids []string, format Format, outDir string, progress ProgressFunc) (Result, error) {
	if len(ids) == 0 {
		return Result{}, fmt.Errorf("select at least one conversation")
	}
	if format != Markdown && format != JSON && format != ZIP {
		return Result{}, fmt.Errorf("format must be markdown, json, or zip")
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return Result{}, err
	}
	threads := make([]grok.Thread, 0, len(ids))
	for index, id := range ids {
		notify(progress, Progress{Phase: "loading", Current: index + 1, Total: len(ids), ID: id})
		thread, err := e.Client.LoadConversationProgress(id, func(loaded, total int) {
			notify(progress, Progress{Phase: "responses", Current: loaded, Total: total, ID: id})
		})
		if err != nil {
			return Result{}, err
		}
		threads = append(threads, thread)
	}
	if format == ZIP {
		return e.writeZIP(threads, outDir, progress)
	}
	result := Result{}
	for index, thread := range threads {
		directory, err := uniqueDir(outDir, SafeName(thread.Conversation.Title))
		if err != nil {
			return Result{}, err
		}
		files, warnings, err := e.writeDirectory(thread, directory, format == Markdown, format == JSON)
		if err != nil {
			return Result{}, err
		}
		result.Paths = append(result.Paths, files...)
		result.Warnings = append(result.Warnings, warnings...)
		notify(progress, Progress{Phase: "writing", Current: index + 1, Total: len(threads), ID: thread.Conversation.ID})
	}
	return result, nil
}

func (e Exporter) writeZIP(threads []grok.Thread, outDir string, progress ProgressFunc) (Result, error) {
	name := filepath.Join(outDir, "grokslut-export-"+time.Now().UTC().Format("20060102T150405Z")+".zip")
	file, err := os.Create(name)
	if err != nil {
		return Result{}, err
	}
	zipWriter := zip.NewWriter(file)
	result := Result{Paths: []string{name}}
	defer func() { zipWriter.Close(); file.Close() }()
	for index, thread := range threads {
		prefix := SafeName(thread.Conversation.Title) + "/"
		media, warnings := e.downloadMedia(thread.Responses)
		result.Warnings = append(result.Warnings, warnings...)
		if err := zipText(zipWriter, prefix+SafeName(thread.Conversation.Title)+".md", RenderMarkdown(thread, media.replacements)); err != nil {
			return Result{}, err
		}
		if err := zipJSON(zipWriter, prefix+SafeName(thread.Conversation.Title)+".json", thread); err != nil {
			return Result{}, err
		}
		for _, item := range media.items {
			if err := zipBytes(zipWriter, prefix+"media/"+item.Name, item.Data); err != nil {
				return Result{}, err
			}
		}
		notify(progress, Progress{Phase: "writing", Current: index + 1, Total: len(threads), ID: thread.Conversation.ID})
	}
	if err := zipWriter.Close(); err != nil {
		file.Close()
		return Result{}, err
	}
	if err := file.Close(); err != nil {
		return Result{}, err
	}
	return result, nil
}

func (e Exporter) writeDirectory(thread grok.Thread, directory string, markdown, jsonOut bool) ([]string, []string, error) {
	media, warnings := e.downloadMedia(thread.Responses)
	var paths []string
	base := SafeName(thread.Conversation.Title)
	if markdown {
		target := filepath.Join(directory, base+".md")
		if err := os.WriteFile(target, []byte(RenderMarkdown(thread, media.replacements)), 0644); err != nil {
			return nil, nil, err
		}
		paths = append(paths, target)
	}
	if jsonOut {
		target := filepath.Join(directory, base+".json")
		if err := writeThreadJSON(target, thread); err != nil {
			return nil, nil, err
		}
		paths = append(paths, target)
	}
	for _, item := range media.items {
		target := filepath.Join(directory, "media", item.Name)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return nil, nil, err
		}
		if err := os.WriteFile(target, item.Data, 0644); err != nil {
			return nil, nil, err
		}
		paths = append(paths, target)
	}
	return paths, warnings, nil
}

type downloadedMedia struct {
	items        []mediaItem
	replacements map[string]string
}
type mediaItem struct {
	Name string
	Data []byte
}

func (e Exporter) downloadMedia(responses []json.RawMessage) (downloadedMedia, []string) {
	result := downloadedMedia{replacements: map[string]string{}}
	var warnings []string
	seen := map[string]bool{}
	for _, raw := range responses {
		for _, assetURL := range MediaURLs(raw) {
			if seen[assetURL] {
				continue
			}
			seen[assetURL] = true
			response, err := e.Client.GetMedia(assetURL)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("could not download media %s: %v", redactURL(assetURL), err))
				continue
			}
			data, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<20))
			response.Body.Close()
			if readErr != nil {
				warnings = append(warnings, fmt.Sprintf("could not download media %s: %v", redactURL(assetURL), readErr))
				continue
			}
			filename := fmt.Sprintf("%03d-%s%s", len(result.items)+1, SafeName(fileStem(assetURL)), extension(response.Header.Get("Content-Type"), assetURL))
			result.items = append(result.items, mediaItem{filename, data})
			result.replacements[assetURL] = "media/" + filename
		}
	}
	return result, warnings
}

func RenderMarkdown(thread grok.Thread, replacements map[string]string) string {
	conversation := thread.Conversation
	lines := []string{"---", "id: " + quote(conversation.ID), "title: " + quote(conversation.Title), "created_at: " + quote(conversation.CreatedAt), "updated_at: " + quote(conversation.UpdatedAt), "---", "", "# " + conversation.Title, ""}
	responses := append([]json.RawMessage(nil), thread.Responses...)
	sort.SliceStable(responses, func(i, j int) bool { return responseTime(responses[i]) < responseTime(responses[j]) })
	for _, raw := range responses {
		author, id, parent, body := responseFields(raw)
		for remote, local := range replacements {
			body = strings.ReplaceAll(body, remote, local)
		}
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		heading := "## " + author
		if id != "" {
			heading += " (" + id + ")"
		}
		lines = append(lines, heading, "", body, "")
		if parent != "" {
			lines = append(lines, "_Parent response: "+parent+"_", "")
		}
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}

func MediaURLs(raw json.RawMessage) []string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	var result []string
	var collect func(any, bool)
	collect = func(current any, eligible bool) {
		switch item := current.(type) {
		case string:
			if eligible && strings.HasPrefix(item, "http") {
				result = append(result, item)
			}
		case []any:
			for _, entry := range item {
				collect(entry, eligible)
			}
		case map[string]any:
			for key, entry := range item {
				collect(entry, eligible || mediaKey.MatchString(key))
			}
		}
	}
	collect(value, false)
	return result
}

var mediaKey = regexp.MustCompile(`(?i)(generatedImageUrls|imageUrls|assetUrls|attachments?|media)`)

func responseFields(raw json.RawMessage) (author, id, parent, body string) {
	var object map[string]any
	_ = json.Unmarshal(raw, &object)
	author = stringAt(object, "author", "role", "sender")
	if nested, ok := object["author"].(map[string]any); ok {
		author = stringAt(nested, "role", "name")
	}
	author = strings.Title(defaultString(author, "unknown"))
	id = stringAt(object, "id", "responseId")
	parent = stringAt(object, "parentResponseId", "parentId", "parent_id")
	body = contentAt(object)
	return
}
func contentAt(object map[string]any) string {
	for _, key := range []string{"message", "content", "text"} {
		if result := contentString(object[key]); result != "" {
			return result
		}
	}
	for _, key := range []string{"response"} {
		if nested, ok := object[key].(map[string]any); ok {
			if result := contentAt(nested); result != "" {
				return result
			}
		}
	}
	return ""
}
func contentString(value any) string {
	switch item := value.(type) {
	case string:
		return item
	case map[string]any:
		return defaultString(contentString(item["text"]), contentString(item["content"]))
	case []any:
		var values []string
		for _, entry := range item {
			values = append(values, contentString(entry))
		}
		return strings.Join(values, "\n")
	}
	return ""
}
func stringAt(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}
func responseTime(raw json.RawMessage) string {
	var object map[string]any
	_ = json.Unmarshal(raw, &object)
	return stringAt(object, "createTime", "createdAt", "created_at", "timestamp")
}
func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
func quote(value string) string { encoded, _ := json.Marshal(value); return string(encoded) }

func writeThreadJSON(target string, thread grok.Thread) error {
	var buffer bytes.Buffer
	if err := encodeThread(&buffer, thread); err != nil {
		return err
	}
	return os.WriteFile(target, buffer.Bytes(), 0644)
}
func zipText(writer *zip.Writer, name, value string) error {
	return zipBytes(writer, name, []byte(value))
}
func zipJSON(writer *zip.Writer, name string, thread grok.Thread) error {
	var buffer bytes.Buffer
	if err := encodeThread(&buffer, thread); err != nil {
		return err
	}
	return zipBytes(writer, name, buffer.Bytes())
}
func zipBytes(writer *zip.Writer, name string, data []byte) error {
	destination, err := writer.Create(name)
	if err != nil {
		return err
	}
	_, err = destination.Write(data)
	return err
}
func encodeThread(writer io.Writer, thread grok.Thread) error {
	conversation := thread.Conversation.Raw
	if len(conversation) == 0 {
		conversation, _ = json.Marshal(thread.Conversation)
	}
	_, err := writer.Write([]byte(`{"conversation":`))
	if err != nil {
		return err
	}
	if _, err = writer.Write(conversation); err != nil {
		return err
	}
	if _, err = writer.Write([]byte(`,"responses":`)); err != nil {
		return err
	}
	responses, err := json.Marshal(thread.Responses)
	if err != nil {
		return err
	}
	if _, err = writer.Write(responses); err != nil {
		return err
	}
	_, err = writer.Write([]byte("}\n"))
	return err
}
func SafeName(value string) string {
	value = strings.ReplaceAll(value, "/", " ")
	value = strings.ReplaceAll(value, "..", " ")
	var builder strings.Builder
	for _, char := range strings.TrimSpace(value) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || strings.ContainsRune("._ -", char) {
			builder.WriteRune(char)
		} else {
			builder.WriteRune('_')
		}
	}
	result := strings.Trim(strings.TrimSpace(builder.String()), ". ")
	if len(result) > 80 {
		result = result[:80]
	}
	return defaultString(result, "conversation")
}
func uniqueDir(parent, name string) (string, error) {
	for index := 1; ; index++ {
		suffix := ""
		if index > 1 {
			suffix = fmt.Sprintf("-%d", index)
		}
		target := filepath.Join(parent, name+suffix)
		if err := os.Mkdir(target, 0755); err == nil {
			return target, nil
		} else if !os.IsExist(err) {
			return "", err
		}
	}
}
func extension(contentType, source string) string {
	if ext, _, err := mime.ParseMediaType(contentType); err == nil {
		switch ext {
		case "image/jpeg":
			return ".jpg"
		case "image/png":
			return ".png"
		case "image/webp":
			return ".webp"
		case "image/gif":
			return ".gif"
		case "video/mp4":
			return ".mp4"
		}
	}
	ext := filepath.Ext(fileStem(source))
	if regexp.MustCompile(`^\.[A-Za-z0-9]{1,5}$`).MatchString(ext) {
		return ext
	}
	return ""
}
func fileStem(source string) string {
	parsed, err := url.Parse(source)
	if err != nil {
		return "asset"
	}
	name := filepath.Base(parsed.Path)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	return defaultString(name, "asset")
}
func redactURL(source string) string {
	parsed, err := url.Parse(source)
	if err != nil {
		return "<invalid media URL>"
	}
	return parsed.Scheme + "://" + parsed.Host + parsed.Path
}
func notify(progress ProgressFunc, event Progress) {
	if progress != nil {
		progress(event)
	}
}
