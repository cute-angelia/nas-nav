package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const backupFormatVersion = 1

const (
	maxBackupUpload     = 8 << 20
	maxBackupEntries    = 3
	maxBackupNavigation = 512 << 10
	maxBackupBackground = 5 << 20
)

var errRevisionConflict = errors.New("backup revision conflict")

type backupManifest struct {
	Format     string    `json:"format"`
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exportedAt"`
}

type importedBackup struct {
	Navigation Navigation
	Background []byte
	Extension  string
}

func (a *App) buildBackup(now time.Time) ([]byte, error) {
	a.mu.Lock()
	data, _, err := a.load()
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	var background []byte
	var backgroundName string
	if data.BackgroundImage != "" {
		path, _, fileErr := a.backgroundFile()
		if fileErr != nil {
			a.mu.Unlock()
			return nil, fileErr
		}
		background, err = os.ReadFile(path)
		backgroundName = "background" + filepath.Ext(path)
	}
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}

	data.BackgroundImage = ""
	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	manifest := backupManifest{Format: "nas-nav-backup", Version: backupFormatVersion, ExportedAt: now.UTC()}
	if err := writeZipJSON(zw, "manifest.json", manifest); err != nil {
		return nil, err
	}
	if err := writeZipJSON(zw, "navigation.json", data); err != nil {
		return nil, err
	}
	if len(background) > 0 {
		entry, err := zw.Create(backgroundName)
		if err != nil {
			return nil, err
		}
		if _, err := entry.Write(background); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func writeZipJSON(zw *zip.Writer, name string, value any) error {
	entry, err := zw.Create(name)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(entry)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	return nil
}

func parseBackup(raw []byte) (importedBackup, error) {
	if len(raw) == 0 || len(raw) > maxBackupUpload {
		return importedBackup{}, fmt.Errorf("备份文件最大 8MB")
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return importedBackup{}, fmt.Errorf("备份 ZIP 格式无效")
	}
	if len(zr.File) < 2 || len(zr.File) > maxBackupEntries {
		return importedBackup{}, fmt.Errorf("备份文件内容不完整")
	}
	entries := make(map[string][]byte, len(zr.File))
	backgroundName := ""
	for _, file := range zr.File {
		if file.FileInfo().IsDir() || strings.Contains(file.Name, "/") || strings.Contains(file.Name, `\`) {
			return importedBackup{}, fmt.Errorf("备份包含无效路径")
		}
		if _, exists := entries[file.Name]; exists {
			return importedBackup{}, fmt.Errorf("备份包含重复文件")
		}
		limit := int64(0)
		switch file.Name {
		case "manifest.json":
			limit = 16 << 10
		case "navigation.json":
			limit = maxBackupNavigation
		case "background.jpg", "background.png", "background.gif", "background.webp":
			if backgroundName != "" {
				return importedBackup{}, fmt.Errorf("备份包含多个背景图片")
			}
			backgroundName = file.Name
			limit = maxBackupBackground
		default:
			return importedBackup{}, fmt.Errorf("备份包含未知文件")
		}
		if file.UncompressedSize64 > uint64(limit) {
			return importedBackup{}, fmt.Errorf("备份内容超过大小限制")
		}
		content, err := readZipEntry(file, limit)
		if err != nil {
			return importedBackup{}, err
		}
		entries[file.Name] = content
	}

	manifestJSON, ok := entries["manifest.json"]
	if !ok {
		return importedBackup{}, fmt.Errorf("备份缺少 manifest.json")
	}
	var manifest backupManifest
	if err := decodeBackupJSON(manifestJSON, &manifest); err != nil || manifest.Format != "nas-nav-backup" || manifest.Version != backupFormatVersion || manifest.ExportedAt.IsZero() {
		return importedBackup{}, fmt.Errorf("备份版本或清单无效")
	}
	navigationJSON, ok := entries["navigation.json"]
	if !ok {
		return importedBackup{}, fmt.Errorf("备份缺少 navigation.json")
	}
	var navigation Navigation
	if err := decodeBackupJSON(navigationJSON, &navigation); err != nil {
		return importedBackup{}, fmt.Errorf("导航数据格式无效")
	}
	navigation.BackgroundImage = ""
	if navigation.Version != 1 {
		return importedBackup{}, fmt.Errorf("导航数据版本无效")
	}
	if err := validate(navigation); err != nil {
		return importedBackup{}, err
	}

	result := importedBackup{Navigation: navigation}
	if backgroundName != "" {
		result.Background = entries[backgroundName]
		result.Extension, err = backgroundExtension(result.Background)
		if err != nil {
			return importedBackup{}, err
		}
		if backgroundName != "background"+result.Extension {
			return importedBackup{}, fmt.Errorf("背景图片扩展名与内容不一致")
		}
	}
	return result, nil
}

func decodeBackupJSON(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var tail any
	if err := decoder.Decode(&tail); err != io.EOF {
		return fmt.Errorf("JSON 包含多余内容")
	}
	return nil
}

func readZipEntry(file *zip.File, limit int64) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("备份文件读取失败")
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("备份文件读取失败")
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("备份内容超过大小限制")
	}
	return data, nil
}

func backgroundExtension(data []byte) (string, error) {
	extension := map[string]string{
		"image/jpeg": ".jpg",
		"image/png":  ".png",
		"image/gif":  ".gif",
		"image/webp": ".webp",
	}[http.DetectContentType(data)]
	if extension == "" {
		return "", fmt.Errorf("背景图片格式无效")
	}
	return extension, nil
}

func (a *App) restoreBackup(imported importedBackup, expectedRevision string) (Navigation, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, currentRevision, err := a.load()
	if err != nil {
		return Navigation{}, "", err
	}
	if currentRevision != expectedRevision {
		return Navigation{}, "", errRevisionConflict
	}

	data := imported.Navigation
	data.BackgroundImage = ""
	stagedPath := ""
	stagedCreated := false
	if len(imported.Background) > 0 {
		hash := digest(string(imported.Background))[:12]
		stagedPath = filepath.Join(filepath.Dir(a.file), "background-"+hash+imported.Extension)
		if _, statErr := os.Stat(stagedPath); errors.Is(statErr, os.ErrNotExist) {
			if err := os.WriteFile(stagedPath, imported.Background, 0600); err != nil {
				return Navigation{}, "", err
			}
			stagedCreated = true
		} else if statErr != nil {
			return Navigation{}, "", statErr
		}
		data.BackgroundImage = "/media/background?v=" + hash
	}
	if err := a.writeData(data); err != nil {
		if stagedCreated {
			_ = os.Remove(stagedPath)
		}
		return Navigation{}, "", err
	}
	// The data switch is already committed. Stale files are harmless and can be
	// cleaned by the next background change, so cleanup must not turn success
	// into an apparent failed restore.
	_ = a.removeBackgroundFilesExcept(stagedPath)
	data, revision, err := a.load()
	return data, revision, err
}
