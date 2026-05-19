package app

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func fileDataURL(path string) (string, error) {
	path, err := prepareImageAttachment(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	typ := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if typ == "" {
		typ = http.DetectContentType(data)
	}
	if !strings.HasPrefix(typ, "image/") {
		return "", fmt.Errorf("%s is not an image", path)
	}
	return "data:" + typ + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func loadImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

func looksBase64Image(s string) bool {
	if len(s) < 64 || strings.Contains(s, " ") || strings.Contains(s, "\n") {
		return false
	}
	return strings.HasPrefix(s, "iVBOR") || strings.HasPrefix(s, "/9j/") || strings.HasPrefix(s, "UklGR")
}

func parseImagePaths(src string) ([]string, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return nil, nil
	}
	fields := strings.FieldsFunc(src, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ';'
	})
	var paths []string
	for _, field := range fields {
		path := strings.Trim(strings.TrimSpace(field), `"'`)
		if path == "" {
			continue
		}
		path = expandPath(path)
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("%s not found", path)
		}
		prepared, err := prepareImageAttachment(path)
		if err != nil {
			return nil, err
		}
		paths = append(paths, prepared)
	}
	return paths, nil
}

func validateImage(path string) error {
	_, err := prepareImageAttachment(path)
	return err
}

func prepareImageAttachment(path string) (string, error) {
	path = expandPath(path)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("%s not found", path)
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".svg" || ext == ".svgz" {
		converted, err := convertSVGToPNG(path)
		if err != nil {
			return "", err
		}
		return flattenImageToWhitePNG(converted)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	typ := mime.TypeByExtension(ext)
	if typ == "" {
		typ = http.DetectContentType(data)
	}
	if !strings.HasPrefix(typ, "image/") {
		return "", fmt.Errorf("%s is not an image", path)
	}
	if typ == "image/svg+xml" {
		converted, err := convertSVGToPNG(path)
		if err != nil {
			return "", err
		}
		return flattenImageToWhitePNG(converted)
	}
	return flattenImageToWhitePNG(path)
}

func convertSVGToPNG(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum(append([]byte(path), data...))
	outDir := filepath.Join("app_outputs", "converted")
	outPath := filepath.Join(outDir, "svg_"+hex.EncodeToString(sum[:8])+".png")
	if _, err := os.Stat(outPath); err == nil {
		return outPath, nil
	}
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("SVG attachments must be converted to PNG first on this platform")
	}
	tmp, err := os.MkdirTemp("", "cheng-svg-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	cmd := exec.Command("qlmanage", "-t", "-s", "1600", "-o", tmp, path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to convert SVG with qlmanage: %s", strings.TrimSpace(string(out)))
	}
	produced := filepath.Join(tmp, filepath.Base(path)+".png")
	if _, err := os.Stat(produced); err != nil {
		return "", fmt.Errorf("failed to convert SVG: PNG thumbnail was not produced")
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", err
	}
	data, err = os.ReadFile(produced)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return "", err
	}
	return outPath, nil
}

func flattenImageToWhitePNG(path string) (string, error) {
	path = expandPath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum(append([]byte(path), data...))
	outDir := filepath.Join("app_outputs", "prepared")
	outPath := filepath.Join(outDir, "image_"+hex.EncodeToString(sum[:8])+".png")
	if _, err := os.Stat(outPath); err == nil {
		return outPath, nil
	}
	src, err := loadImage(path)
	if err != nil {
		return "", err
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := png.Encode(f, dst); err != nil {
		return "", err
	}
	return outPath, nil
}

func pickImageFile() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("osascript", "-e", `POSIX path of (choose file of type {"public.image"} with prompt "Choose an image")`).Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	case "windows":
		script := `Add-Type -AssemblyName System.Windows.Forms; $d = New-Object System.Windows.Forms.OpenFileDialog; $d.Filter = 'Images|*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp|All files|*.*'; if ($d.ShowDialog() -eq 'OK') { $d.FileName }`
		out, err := exec.Command("powershell", "-NoProfile", "-Command", script).Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	default:
		out, err := exec.Command("zenity", "--file-selection", "--title=Choose an image", "--file-filter=Images | *.png *.jpg *.jpeg *.gif *.webp *.bmp").Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
}
